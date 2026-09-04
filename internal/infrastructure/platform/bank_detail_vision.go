package platform

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/services/document"
)

// BankDetailExtraction holds bank account details extracted from an image
// (screenshot of a bank app, transfer confirmation page, or QR code).
type BankDetailExtraction struct {
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
	Amount        string `json:"amount,omitempty"`
	Currency      string `json:"currency,omitempty"`
	IsQRCode      bool   `json:"is_qr_code,omitempty"`
	RawText       string `json:"raw_text,omitempty"`
}

// BankDetailVision extracts bank account details from images so Miriam can
// pre-fill transfer details from a screenshot or QR code.
type BankDetailVision interface {
	Available() bool
	ExtractBankDetails(ctx context.Context, image []byte, mimeType string) (*BankDetailExtraction, error)
}

// ocrBankDetailVision uses the document pipeline's OCR to extract text, then
// parses bank details from the text using regex patterns.
type ocrBankDetailVision struct {
	pipeline *document.Pipeline
}

// NewOCRBankDetailVision builds a BankDetailVision on top of the document
// pipeline's OCR engine. Returns nil when pipeline is nil.
func NewOCRBankDetailVision(pipeline *document.Pipeline) BankDetailVision {
	if pipeline == nil {
		return nil
	}
	return &ocrBankDetailVision{pipeline: pipeline}
}

func (v *ocrBankDetailVision) Available() bool { return v != nil && v.pipeline != nil }

func (v *ocrBankDetailVision) ExtractBankDetails(ctx context.Context, image []byte, mimeType string) (*BankDetailExtraction, error) {
	if len(image) == 0 {
		return nil, fmt.Errorf("empty image")
	}
	if len(image) > maxReceiptImageBytes {
		return nil, fmt.Errorf("image too large (%d bytes)", len(image))
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	res, err := v.pipeline.Process(ctx, image, mimeType)
	if err != nil {
		return nil, fmt.Errorf("process image: %w", err)
	}
	if res == nil || res.OCR == nil {
		return nil, fmt.Errorf("no OCR result")
	}

	rawText := res.OCR.Text
	extraction := parseBankDetailsFromText(rawText)
	if extraction == nil {
		return nil, fmt.Errorf("no bank details found in image")
	}
	extraction.RawText = rawText
	return extraction, nil
}

// Regex patterns for extracting bank details from OCR text.
var (
	// Nigerian NUBAN account numbers are 10 digits.
	accountNumberRe = regexp.MustCompile(`(?i)(?:account(?:\s+no\.?|number)?|acct(?:\.|\s+no\.?)?)\s*:?\s*(\d{10})`)
	// Bare 10-digit number (fallback when labels are missing).
	bareAccountRe = regexp.MustCompile(`\b(\d{10})\b`)
	// Bank name patterns — match common Nigerian bank names.
	bankNameRe = regexp.MustCompile(`(?i)\b(guaranty\s+trust\s+bank|gtbank|gtb|access\s+bank|zenith\s+bank|uba|united\s+bank\s+for\s+africa|first\s+bank|kuda|kuda\s+bank|wema\s+bank|fcmb|first\s+city\s+monument\s+bank|fidelity\s+bank|union\s+bank|sterling\s+bank|polaris\s+bank|keystone\s+bank|stanbic\s+ibtc|ecobank|standard\s+chartered|citibank)\b`)
	// Account name patterns — use [ \t] instead of \s to avoid matching across newlines.
	accountNameRe = regexp.MustCompile(`(?i)(?:account[ \t]*name|name)\s*:?\s*([A-Z][a-z]+(?:[ \t]+[A-Z][a-z]+){1,3})`)
	// Amount patterns.
	amountRe = regexp.MustCompile(`(?i)(?:amount|amt)\s*:?\s*([₦$£€]?\s*[\d,]+\.?\d*)`)
)

// parseBankDetailsFromText extracts bank details from OCR text using regex.
// Returns nil if no account number is found.
func parseBankDetailsFromText(text string) *BankDetailExtraction {
	if text == "" {
		return nil
	}

	extraction := &BankDetailExtraction{}

	// Extract account number (try labeled first, then bare 10-digit).
	if m := accountNumberRe.FindStringSubmatch(text); m != nil {
		extraction.AccountNumber = m[1]
	} else if m := bareAccountRe.FindStringSubmatch(text); m != nil {
		extraction.AccountNumber = m[1]
	}

	// If no account number found, this probably isn't a bank detail image.
	if extraction.AccountNumber == "" {
		return nil
	}

	// Extract bank name.
	if m := bankNameRe.FindStringSubmatch(text); m != nil {
		extraction.BankName = strings.TrimSpace(m[1])
	}

	// Extract account name.
	if m := accountNameRe.FindStringSubmatch(text); m != nil {
		extraction.AccountName = strings.TrimSpace(m[1])
	}

	// Extract amount.
	if m := amountRe.FindStringSubmatch(text); m != nil {
		amount := strings.TrimSpace(m[1])
		// Normalize: remove currency symbols and commas.
		amount = strings.TrimPrefix(amount, "₦")
		amount = strings.TrimPrefix(amount, "$")
		amount = strings.TrimPrefix(amount, "£")
		amount = strings.TrimPrefix(amount, "€")
		amount = strings.ReplaceAll(amount, ",", "")
		amount = strings.TrimSpace(amount)
		if amount != "" {
			extraction.Amount = amount
		}
		if strings.Contains(m[1], "₦") || strings.Contains(m[1], "NGN") {
			extraction.Currency = "NGN"
		} else if strings.Contains(m[1], "$") || strings.Contains(m[1], "USD") {
			extraction.Currency = "USD"
		}
	}

	return extraction
}

// FormatBankDetailSummary renders the extraction the way Miriam talks.
func FormatBankDetailSummary(ext *BankDetailExtraction) string {
	var b strings.Builder
	b.WriteString("I can see bank details in that image")
	if ext.BankName != "" {
		fmt.Fprintf(&b, ": %s", ext.BankName)
	}
	if ext.AccountNumber != "" {
		fmt.Fprintf(&b, ", account %s", ext.AccountNumber)
	}
	if ext.AccountName != "" {
		fmt.Fprintf(&b, ", name: %s", ext.AccountName)
	}
	b.WriteString(".")
	if ext.Amount != "" {
		currency := ext.Currency
		if currency == "" {
			currency = "NGN"
		}
		fmt.Fprintf(&b, " I also see an amount: %s%s.", currencySymbolForBankDetail(currency), ext.Amount)
	}
	b.WriteString(" Want me to set up a transfer to this account?")
	return b.String()
}

func currencySymbolForBankDetail(currency string) string {
	switch strings.ToUpper(currency) {
	case "NGN":
		return "₦"
	case "USD":
		return "$"
	case "GBP":
		return "£"
	case "EUR":
		return "€"
	default:
		return ""
	}
}
