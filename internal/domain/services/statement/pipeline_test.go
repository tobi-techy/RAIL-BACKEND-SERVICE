package statement

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- Document Extractor Tests ---

func TestDetectContentType_PDF(t *testing.T) {
	data := []byte("%PDF-1.4 fake pdf content")
	result := detectMimeFromBytes(data)
	assert.Equal(t, "application/pdf", result)
}

func TestDetectContentType_JPEG(t *testing.T) {
	data := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	result := detectMimeFromBytes(data)
	assert.Equal(t, "image/jpeg", result)
}

func TestDetectContentType_PNG(t *testing.T) {
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	result := detectMimeFromBytes(data)
	assert.Equal(t, "image/png", result)
}

func TestDetectContentType_Unknown(t *testing.T) {
	data := []byte("hello world this is not a valid file")
	result := detectMimeFromBytes(data)
	assert.Equal(t, "", result)
}

// detectMimeFromBytes replicates the handler's logic for testing
func detectMimeFromBytes(data []byte) string {
	if len(data) >= 5 && string(data[:5]) == "%PDF-" {
		return "application/pdf"
	}
	magics := []struct {
		prefix []byte
		offset int
		mime   string
	}{
		{[]byte{0xFF, 0xD8, 0xFF}, 0, "image/jpeg"},
		{[]byte{0x89, 0x50, 0x4E, 0x47}, 0, "image/png"},
		{[]byte("ftyp"), 4, "image/heic"},
	}
	for _, m := range magics {
		end := m.offset + len(m.prefix)
		if len(data) >= end {
			match := true
			for i, b := range m.prefix {
				if data[m.offset+i] != b {
					match = false
					break
				}
			}
			if match {
				return m.mime
			}
		}
	}
	return ""
}

// --- Extractor Strategy Cascade ---

func TestDocumentExtractor_PDFWithText(t *testing.T) {
	// When pdfcpu fails on a fake PDF, should fall through to textract
	logger := zap.NewNop()
	mockTextract := &mockTextractClient{text: "Date Description Amount Balance\n15/01/2025 Salary Credit 500000.00 500000.00\n20/01/2025 Rent Debit 200000.00 300000.00"}
	extractor := NewDocumentExtractor(mockTextract, nil, logger)

	data := []byte("%PDF-1.4 not a real pdf content")
	result, err := extractor.Extract(context.Background(), data, "application/pdf")

	require.NoError(t, err)
	assert.Equal(t, StrategyTextract, result.Strategy)
	assert.Contains(t, result.Text, "Salary")
}

func TestDocumentExtractor_ImageUsesTextract(t *testing.T) {
	logger := zap.NewNop()
	mockTextract := &mockTextractClient{text: "extracted image text with enough content to pass threshold"}
	extractor := NewDocumentExtractor(mockTextract, nil, logger)

	data := []byte{0xFF, 0xD8, 0xFF, 0xE0} // JPEG header
	result, err := extractor.Extract(context.Background(), data, "image/jpeg")

	require.NoError(t, err)
	assert.Equal(t, StrategyTextract, result.Strategy)
}

func TestDocumentExtractor_NoServicesReturnsError(t *testing.T) {
	logger := zap.NewNop()
	extractor := NewDocumentExtractor(nil, nil, logger)

	data := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	_, err := extractor.Extract(context.Background(), data, "image/jpeg")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no OCR service available")
}

func TestDocumentExtractor_UnsupportedType(t *testing.T) {
	logger := zap.NewNop()
	extractor := NewDocumentExtractor(nil, nil, logger)

	_, err := extractor.Extract(context.Background(), []byte("data"), "video/mp4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported content type")
}

// --- Validation Tests ---

func TestValidateTransactions_ValidSet(t *testing.T) {
	txns := []*entities.BankStatementTransaction{
		{TransactionDate: time.Now(), Description: "Salary", Amount: decimal.NewFromInt(500000), Currency: "NGN", Type: "credit", Category: "salary"},
		{TransactionDate: time.Now(), Description: "Rent", Amount: decimal.NewFromInt(200000), Currency: "NGN", Type: "debit", Category: "rent"},
		{TransactionDate: time.Now(), Description: "Food", Amount: decimal.NewFromInt(15000), Currency: "NGN", Type: "debit", Category: "food"},
	}

	result, valid := ValidateTransactions(txns)
	require.True(t, result.Valid)
	assert.Equal(t, 3, result.ValidTransactions)
	assert.Equal(t, 0, result.DroppedCount)
	assert.Len(t, valid, 3)
	assert.Greater(t, result.Confidence, 0.5)
}

func TestValidateTransactions_DropsDuplicates(t *testing.T) {
	date := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	txns := []*entities.BankStatementTransaction{
		{TransactionDate: date, Description: "POS Purchase", Amount: decimal.NewFromInt(5000), Currency: "NGN", Type: "debit", Category: "shopping"},
		{TransactionDate: date, Description: "POS Purchase", Amount: decimal.NewFromInt(5000), Currency: "NGN", Type: "debit", Category: "shopping"}, // dup
	}

	result, valid := ValidateTransactions(txns)
	assert.Equal(t, 1, result.ValidTransactions)
	assert.Equal(t, 1, result.DroppedCount)
	assert.Len(t, valid, 1)
}

func TestValidateTransactions_DropsExcessiveAmounts(t *testing.T) {
	txns := []*entities.BankStatementTransaction{
		{TransactionDate: time.Now(), Description: "Normal", Amount: decimal.NewFromInt(50000), Currency: "NGN", Type: "debit", Category: "other"},
		{TransactionDate: time.Now(), Description: "Suspicious", Amount: decimal.NewFromInt(200_000_000), Currency: "NGN", Type: "debit", Category: "other"}, // > 100M NGN
	}

	result, valid := ValidateTransactions(txns)
	assert.Equal(t, 1, result.ValidTransactions)
	assert.Equal(t, 1, result.DroppedCount)
	assert.Len(t, valid, 1)
}

func TestValidateTransactions_EmptyReturnsInvalid(t *testing.T) {
	result, valid := ValidateTransactions(nil)
	assert.False(t, result.Valid)
	assert.Nil(t, valid)
}

func TestValidateTransactions_BalanceMismatches(t *testing.T) {
	bal1 := decimal.NewFromInt(100000)
	bal2 := decimal.NewFromInt(50000) // should be 80000 (100000-20000), so mismatch

	txns := []*entities.BankStatementTransaction{
		{TransactionDate: time.Now(), Description: "First", Amount: decimal.NewFromInt(20000), Currency: "NGN", Type: "debit", Category: "other", BalanceAfter: &bal1},
		{TransactionDate: time.Now().Add(time.Hour), Description: "Second", Amount: decimal.NewFromInt(20000), Currency: "NGN", Type: "debit", Category: "other", BalanceAfter: &bal2},
	}

	result, _ := ValidateTransactions(txns)
	assert.Greater(t, result.BalanceMismatches, 0)
}

// --- Pipeline Tests ---

func TestPipeline_ExtractsAndDelegates(t *testing.T) {
	// Test that the pipeline correctly cascades extraction strategies
	// We test the extraction cascade separately since Parse requires an LLM.
	logger := zap.NewNop()
	mockTextract := &mockTextractClient{text: "Enough text for the extractor to consider this valid output from textract service"}
	extractor := NewDocumentExtractor(mockTextract, nil, logger)

	// Verify extraction cascade works for a fake PDF (pdfcpu will fail → textract wins)
	result, err := extractor.Extract(context.Background(), []byte("%PDF-1.4 not real"), "application/pdf")
	require.NoError(t, err)
	assert.Equal(t, StrategyTextract, result.Strategy)
	assert.Contains(t, result.Text, "Enough text")
}

func TestPipeline_TextractFallsToVisionForImages(t *testing.T) {
	logger := zap.NewNop()
	mockTextract := &mockTextractClient{err: assert.AnError}
	mockVision := &mockVisionClient{text: "Vision extracted this bank statement text successfully"}
	extractor := NewDocumentExtractor(mockTextract, mockVision, logger)

	result, err := extractor.Extract(context.Background(), []byte{0xFF, 0xD8, 0xFF}, "image/jpeg")
	require.NoError(t, err)
	assert.Equal(t, StrategyVisionLLM, result.Strategy)
}

// --- ToEntities Tests ---

func TestTransactionParser_ToEntities(t *testing.T) {
	parser := &TransactionParser{}
	uploadID := uuid.New()
	userID := uuid.New()

	parsed := &ParseResult{
		BankName: "Access Bank",
		Currency: "NGN",
		PeriodStart: "2025-03-01",
		PeriodEnd:   "2025-03-31",
		Transactions: []ParsedTxn{
			{Date: "2025-03-05", Description: "POS Shoprite", Amount: 25000, Type: "debit", Category: "groceries"},
			{Date: "2025-03-10", Description: "Transfer from John", Amount: 100000, Type: "credit", Category: "transfer_in"},
			{Date: "invalid-date", Description: "Bad date", Amount: 5000, Type: "debit", Category: "other"},   // should be skipped
			{Date: "2025-03-15", Description: "Zero amount", Amount: 0, Type: "debit", Category: "other"},       // should be skipped
			{Date: "2025-03-15", Description: "Negative", Amount: -500, Type: "debit", Category: "other"},        // should be skipped
		},
	}

	txns, periodStart, periodEnd := parser.ToEntities(parsed, uploadID, userID)

	assert.Len(t, txns, 2) // only 2 valid
	assert.NotNil(t, periodStart)
	assert.NotNil(t, periodEnd)
	assert.Equal(t, "2025-03-01", periodStart.Format("2006-01-02"))
	assert.Equal(t, "2025-03-31", periodEnd.Format("2006-01-02"))

	assert.Equal(t, "NGN", txns[0].Currency)
	assert.Equal(t, uploadID, txns[0].UploadID)
	assert.Equal(t, userID, txns[0].UserID)
}

// --- IsGarbageText Tests ---

func TestIsGarbageText_Normal(t *testing.T) {
	assert.False(t, IsGarbageText("Transaction Date Amount Balance"))
}

func TestIsGarbageText_Garbage(t *testing.T) {
	// Mostly non-printable
	garbage := strings.Repeat("\x01\x02\x03\x04\x05", 20)
	assert.True(t, IsGarbageText(garbage))
}

func TestIsGarbageText_Empty(t *testing.T) {
	assert.True(t, IsGarbageText(""))
}

// --- Progress Reporter Tests ---

func TestNoOpReporter(t *testing.T) {
	reporter := &NoOpReporter{}
	// Should not panic
	reporter.Report(context.Background(), &StageProgress{
		UploadID: uuid.New(),
		Stage:    StageExtract,
		Progress: 0.5,
		Message:  "testing",
	})
}

// --- FileStore Tests ---

func TestLocalFileStore(t *testing.T) {
	store := NewLocalFileStore()
	ctx := context.Background()

	// Upload
	url, err := store.Upload(ctx, "test/file.pdf", []byte("pdf content"), "application/pdf")
	require.NoError(t, err)
	assert.Contains(t, url, "test/file.pdf")

	// Download
	data, err := store.Download(ctx, "test/file.pdf")
	require.NoError(t, err)
	assert.Equal(t, []byte("pdf content"), data)

	// Download missing
	_, err = store.Download(ctx, "nonexistent")
	require.Error(t, err)

	// Delete
	err = store.Delete(ctx, "test/file.pdf")
	require.NoError(t, err)

	_, err = store.Download(ctx, "test/file.pdf")
	require.Error(t, err)
}

// --- Mocks ---

type mockTextractClient struct {
	text string
	err  error
}

func (m *mockTextractClient) ExtractText(_ context.Context, _ []byte, _ string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.text, nil
}

type mockVisionClient struct {
	text string
	err  error
}

func (m *mockVisionClient) ExtractTextFromImage(_ context.Context, _ []byte, _ string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.text, nil
}
