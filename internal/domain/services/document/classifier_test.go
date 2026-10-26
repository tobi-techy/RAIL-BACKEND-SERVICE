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
		{
			name: "stock book",
			text: "MAMA TOBI STORES\nOPENING STOCK 12\nGOODS SUPPLIED 20\nSOLD 14\nBALANCE LEFT 18",
			want: DocStockBook,
		},
		{
			name: "delivery note",
			text: "DANGOTE DELIVERY NOTE\nGoods Received Note\nReceived By ______\nDelivered By ______",
			want: DocDeliveryNote,
		},
		{
			// One goods word does not turn an invoice into a stock book: the
			// invoice vocabulary still out-scores it.
			name: "invoice that mentions goods supplied",
			text: "ACME LTD\nInvoice Number INV-9\nDue Date 2026-02-01\nGoods Supplied: 12 cartons",
			want: DocInvoice,
		},
		{
			// Same guard on the other side: a shop receipt saying "sold" once
			// is still a receipt.
			name: "receipt that mentions sold",
			text: "CORNER SHOP\nQty 1 SOLD\nSubtotal 10.00\nTotal 12.00\nCash 20\nChange 8\nThank you",
			want: DocReceipt,
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
