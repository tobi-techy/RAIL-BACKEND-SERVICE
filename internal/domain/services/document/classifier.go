package document

import "strings"

// RuleClassifier assigns a document type using keyword heuristics. Fast and
// deterministic — no LLM needed for classification.
type RuleClassifier struct{}

// NewRuleClassifier constructs the default rule-based classifier.
func NewRuleClassifier() *RuleClassifier { return &RuleClassifier{} }

// score counts how many of the keywords appear in the (lowercased) text.
func score(text string, keywords []string) int {
	n := 0
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			n++
		}
	}
	return n
}

var (
	statementKeywords = []string{
		"opening balance", "closing balance", "available balance",
		"account number", "statement period", "debit", "credit",
		"transaction date", "value date", "balance b/f", "balance c/f",
	}
	receiptKeywords = []string{
		"subtotal", "total", "tax", "vat", "cash", "change", "receipt",
		"qty", "cashier", "thank you", "tel", "amount due",
	}
	invoiceKeywords = []string{
		"invoice", "invoice number", "invoice no", "due date", "bill to",
		"purchase order", "po number", "payment terms",
	}
)

// Classify implements Classifier.
func (c *RuleClassifier) Classify(text string, _ []Line) DocType {
	lower := strings.ToLower(text)

	stmt := score(lower, statementKeywords)
	rcpt := score(lower, receiptKeywords)
	inv := score(lower, invoiceKeywords)

	// Statements have a distinctive balance vocabulary; require at least two
	// hits so a receipt mentioning "credit"/"debit" once doesn't win.
	if stmt >= 2 && stmt >= rcpt && stmt >= inv {
		return DocBankStatement
	}
	// Invoices are receipts with an invoice number + due date; disambiguate.
	if inv >= 2 && inv > rcpt {
		return DocInvoice
	}
	if rcpt >= 2 {
		return DocReceipt
	}
	// Single strong signals as a fallback before giving up.
	switch {
	case stmt >= 1 && rcpt == 0 && inv == 0:
		return DocBankStatement
	case inv >= 1 && rcpt == 0:
		return DocInvoice
	case rcpt >= 1:
		return DocReceipt
	}
	return DocUnknown
}
