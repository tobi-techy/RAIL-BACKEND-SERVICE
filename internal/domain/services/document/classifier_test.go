package document

import "testing"

func TestClassify(t *testing.T) {
	c := NewRuleClassifier()
	tests := []struct {
		name string
		text string
		want DocType
	}{
		{
			name: "bank statement",
			text: "Account Number 12345\nStatement Period Jan-Mar\nOpening Balance 1000\nDebit 50\nCredit 200\nClosing Balance 1150",
			want: DocBankStatement,
		},
		{
			name: "receipt",
			text: "STARBUCKS\nQty 1 Latte\nSubtotal 15.00\nTax 3.90\nTotal 18.90\nThank you",
			want: DocReceipt,
		},
		{
			name: "invoice",
			text: "ACME LTD\nInvoice Number INV-001\nDue Date 2026-02-01\nBill To John\nTotal 500",
			want: DocInvoice,
		},
		{
			name: "unknown",
			text: "just some random text with nothing financial",
			want: DocUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Classify(tt.text, nil); got != tt.want {
				t.Errorf("Classify() = %v, want %v", got, tt.want)
			}
		})
	}
}
