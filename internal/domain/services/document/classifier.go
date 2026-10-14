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
	// A trader's own stock book. Its columns are goods, not money: what was on
	// hand, what came in, what went out, what is left. Two hits are required,
	// and the page must out-score the money vocabularies — an invoice that says
	// "goods supplied" once is still an invoice.
	stockBookKeywords = []string{
		"stock book", "stock card", "store record", "sales book", "goods book",
		"opening stock", "closing stock", "stock balance", "stock on hand",
		"goods supplied", "supplied", "sold", "balance left", "left in store",
	}
	// A distributor's slip that travels with the goods and is signed off on
	// arrival: delivery note / waybill / goods received note. "received by" and
	// "delivered by" are the signature lines; no money vocabulary carries both.
	deliveryNoteKeywords = []string{
		"delivery note", "waybill", "way bill", "goods received note",
		"goods received", "received in good condition", "received by",
		"delivered by", "signed for", "dispatched", "vehicle no",
	}
)

// Classify implements Classifier.
//
// Order matters. Shop documents are checked first because they carry goods
// vocabulary that would otherwise be read as a receipt ("sold", "supplied") or
// as a statement (a stock book's carried-forward balance) — and because a
// misrouted stock book would be answered with a receipt summary instead of a
// real read. The money vocabularies keep their original precedence below.
func (c *RuleClassifier) Classify(text string, _ []Line) DocType {
	lower := strings.ToLower(text)

	stmt := score(lower, statementKeywords)
	rcpt := score(lower, receiptKeywords)
	inv := score(lower, invoiceKeywords)
	stock := score(lower, stockBookKeywords)
	delivery := score(lower, deliveryNoteKeywords)

	if delivery >= 2 && delivery >= stmt {
		return DocDeliveryNote
	}
	if stock >= 2 && stock > stmt && stock > rcpt && stock > inv {
		return DocStockBook
	}
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
