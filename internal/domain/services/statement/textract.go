package statement

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/textract"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"
	"go.uber.org/zap"
)

// TextractExtractor wraps AWS Textract for OCR on scanned PDFs and images.
type TextractExtractor struct {
	client *textract.Client
	logger *zap.Logger
}

type TextractConfig struct {
	Region string
}

func NewTextractExtractor(ctx context.Context, cfg TextractConfig, logger *zap.Logger) (*TextractExtractor, error) {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	client := textract.NewFromConfig(awsCfg)
	return &TextractExtractor{client: client, logger: logger}, nil
}

// ExtractText implements TextractClient. Uses DetectDocumentText (sync API).
// Limitations: max 5MB, single-page PDFs only. For multi-page PDFs >1 page,
// Textract may only return the first page. Images (JPEG/PNG) work fully.
// Consider StartDocumentTextDetection (async) for multi-page PDFs in the future.
func (t *TextractExtractor) ExtractText(ctx context.Context, data []byte, _ string) (string, error) {
	if len(data) > 5*1024*1024 {
		return "", fmt.Errorf("document exceeds 5MB sync limit — use async Textract")
	}

	input := &textract.DetectDocumentTextInput{
		Document: &types.Document{
			Bytes: data,
		},
	}

	result, err := t.client.DetectDocumentText(ctx, input)
	if err != nil {
		return "", fmt.Errorf("textract DetectDocumentText: %w", err)
	}

	// Reconstruct text from LINE blocks, preserving page structure
	var lines []string
	currentPage := int32(0)
	for _, block := range result.Blocks {
		if block.BlockType == types.BlockTypePage {
			if currentPage > 0 {
				lines = append(lines, "\n---PAGE BREAK---\n")
			}
			currentPage++
		}
		if block.BlockType == types.BlockTypeLine && block.Text != nil {
			lines = append(lines, aws.ToString(block.Text))
		}
	}

	text := strings.Join(lines, "\n")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("textract returned no text")
	}

	t.logger.Debug("textract extraction complete",
		zap.Int("blocks", len(result.Blocks)),
		zap.Int("text_length", len(text)),
	)
	return text, nil
}

// AnalyzeExpense uses Textract AnalyzeExpense for receipt/statement understanding.
// More expensive but understands tabular financial data better.
func (t *TextractExtractor) AnalyzeExpense(ctx context.Context, data []byte) (string, error) {
	if len(data) > 5*1024*1024 {
		return "", fmt.Errorf("document exceeds 5MB sync limit")
	}

	input := &textract.AnalyzeExpenseInput{
		Document: &types.Document{
			Bytes: data,
		},
	}

	result, err := t.client.AnalyzeExpense(ctx, input)
	if err != nil {
		return "", fmt.Errorf("textract AnalyzeExpense: %w", err)
	}

	// Extract line items from expense documents
	var lines []string
	for _, doc := range result.ExpenseDocuments {
		for _, group := range doc.LineItemGroups {
			for _, item := range group.LineItems {
				var parts []string
				for _, field := range item.LineItemExpenseFields {
					if field.ValueDetection != nil && field.ValueDetection.Text != nil {
						parts = append(parts, aws.ToString(field.ValueDetection.Text))
					}
				}
				if len(parts) > 0 {
					lines = append(lines, strings.Join(parts, "\t"))
				}
			}
		}
	}

	return strings.Join(lines, "\n"), nil
}
