package document

import (
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// currencySymbols maps symbols/codes to ISO currency codes.
var currencySymbols = map[string]string{
	"₦": "NGN", "ngn": "NGN", "naira": "NGN",
	"$": "USD", "usd": "USD", "dollar": "USD",
	"£": "GBP", "gbp": "GBP", "pound": "GBP",
	"€": "EUR", "eur": "EUR", "euro": "EUR",
}

// detectCurrency scans text for the first currency indicator.
func detectCurrency(text string) string {
	lower := strings.ToLower(text)
	// Symbols first (unambiguous), then codes/words.
	for _, sym := range []string{"₦", "£", "€", "$"} {
		if strings.Contains(text, sym) {
			return currencySymbols[sym]
		}
	}
	for _, code := range []string{"ngn", "gbp", "eur", "usd", "naira", "pound", "euro", "dollar"} {
		if strings.Contains(lower, code) {
			return currencySymbols[code]
		}
	}
	return ""
}

// amountRe matches money amounts with optional thousands separators and decimals.
// Examples: 1,234.56  18.90  1880  ₦2,000.00
var amountRe = regexp.MustCompile(`[-+]?\d{1,3}(?:,\d{3})*(?:\.\d{1,2})?|\d+(?:\.\d{1,2})?`)

// parseAmount converts a matched money string to a Decimal, stripping separators
// and currency glyphs. Returns false when no numeric value is present.
func parseAmount(s string) (decimal.Decimal, bool) {
	cleaned := s
	for sym := range currencySymbols {
		cleaned = strings.ReplaceAll(cleaned, sym, "")
	}
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	cleaned = strings.TrimSpace(cleaned)
	m := regexp.MustCompile(`[-+]?\d+(?:\.\d+)?`).FindString(cleaned)
	if m == "" {
		return decimal.Zero, false
	}
	d, err := decimal.NewFromString(m)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

// lastAmountOnLine returns the right-most money value on a line (totals/balances
// live in the rightmost column on receipts and statements).
func lastAmountOnLine(line string) (decimal.Decimal, bool) {
	matches := amountRe.FindAllString(line, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if d, ok := parseAmount(matches[i]); ok {
			return d, true
		}
	}
	return decimal.Zero, false
}

// dateLayouts are tried in order; unambiguous ISO first.
var dateLayouts = []string{
	"2006-01-02", "2006/01/02", "02/01/2006", "02-01-2006",
	"02 Jan 2006", "2 Jan 2006", "Jan 2, 2006", "January 2, 2006",
	"02-Jan-2006", "02 January 2006",
}

var dateTokenRe = regexp.MustCompile(`\d{1,4}[-/ ][A-Za-z0-9]{1,9}[-/ ]\d{1,4}|\d{4}-\d{2}-\d{2}`)

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// findFirstDate scans text for the first parseable date token.
func findFirstDate(text string) (time.Time, bool) {
	for _, tok := range dateTokenRe.FindAllString(text, -1) {
		if t, ok := parseDate(tok); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// RuleFieldExtractor extracts fields deterministically from OCR text.
type RuleFieldExtractor struct{}

// NewRuleFieldExtractor builds the default deterministic extractor.
func NewRuleFieldExtractor() *RuleFieldExtractor { return &RuleFieldExtractor{} }

// Extract implements FieldExtractor.
func (e *RuleFieldExtractor) Extract(doc DocType, text string, lines []Line) (*Fields, error) {
	f := &Fields{Type: doc, Currency: detectCurrency(text)}
	switch doc {
	case DocReceipt, DocInvoice:
		e.extractReceipt(f, text)
	case DocBankStatement:
		e.extractStatement(f, text)
	default:
		// Unknown: still try receipt-style extraction as a best effort.
		e.extractReceipt(f, text)
	}
	return f, nil
}

func decPtr(d decimal.Decimal) *decimal.Decimal { return &d }
func timePtr(t time.Time) *time.Time            { return &t }

func (e *RuleFieldExtractor) extractReceipt(f *Fields, text string) {
	textLines := strings.Split(text, "\n")

	// Merchant: first non-empty line that isn't a date/amount is a strong prior.
	for _, ln := range textLines {
		trimmed := strings.TrimSpace(ln)
		if len(trimmed) < 3 {
			continue
		}
		if _, ok := lastAmountOnLine(trimmed); ok {
			continue
		}
		if _, ok := parseDate(trimmed); ok {
			continue
		}
		f.Merchant = trimmed
		break
	}

	// Total / subtotal / tax by keyword; take the amount on the matching line.
	for _, ln := range textLines {
		lower := strings.ToLower(ln)
		amt, ok := lastAmountOnLine(ln)
		if !ok {
			continue
		}
		switch {
		case strings.Contains(lower, "subtotal") || strings.Contains(lower, "sub total"):
			f.Subtotal = decPtr(amt)
		case strings.Contains(lower, "vat") || strings.Contains(lower, "tax"):
			f.Tax = decPtr(amt)
		case strings.Contains(lower, "total") || strings.Contains(lower, "amount due") || strings.Contains(lower, "grand total"):
			// "total" appears in "subtotal" too, but subtotal is caught above.
			f.Total = decPtr(amt)
		}
	}

	if t, ok := findFirstDate(text); ok {
		f.Date = timePtr(t)
	}
}

var (
	openingRe  = regexp.MustCompile(`(?i)(opening balance|balance b/?f|balance brought forward)`)
	closingRe  = regexp.MustCompile(`(?i)(closing balance|balance c/?f|balance carried forward)`)
	acctNameRe = regexp.MustCompile(`(?i)account name[:\s]+([A-Za-z ,.'-]{3,60})`)
)

func (e *RuleFieldExtractor) extractStatement(f *Fields, text string) {
	textLines := strings.Split(text, "\n")

	for _, ln := range textLines {
		if openingRe.MatchString(ln) {
			if amt, ok := lastAmountOnLine(ln); ok {
				f.OpeningBalance = decPtr(amt)
			}
		}
		if closingRe.MatchString(ln) {
			if amt, ok := lastAmountOnLine(ln); ok {
				f.ClosingBalance = decPtr(amt)
			}
		}
	}

	if m := acctNameRe.FindStringSubmatch(text); m != nil {
		f.AccountName = strings.TrimSpace(m[1])
	}

	// Period: first and last dates seen in the document.
	var dates []time.Time
	for _, tok := range dateTokenRe.FindAllString(text, -1) {
		if t, ok := parseDate(tok); ok {
			dates = append(dates, t)
		}
	}
	if len(dates) > 0 {
		earliest, latest := dates[0], dates[0]
		for _, d := range dates[1:] {
			if d.Before(earliest) {
				earliest = d
			}
			if d.After(latest) {
				latest = d
			}
		}
		f.PeriodStart = timePtr(earliest)
		f.PeriodEnd = timePtr(latest)
	}

	// Note: full per-transaction extraction from arbitrary statement layouts is
	// handled by the LLM parser in the bridge; the deterministic layer captures
	// the header/summary fields that validation relies on.
}
