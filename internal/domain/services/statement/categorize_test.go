package statement

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeStatementCategory(t *testing.T) {
	tests := []struct {
		name, category, description, txnType, want string
	}{
		{"keeps groceries", "groceries", "POS Shoprite", "debit", BucketGroceries},
		{"alias grocery", "Grocery", "market run", "debit", BucketGroceries},
		{"betting overrides shopping", "shopping", "WEB BET9JA/0123", "debit", BucketBetting},
		{"salary from narration", "other", "SALARY PAYMENT ACME", "credit", BucketSalary},
		{"bare transfer credit", "transfer", "John Doe", "credit", BucketTransferIn},
		{"bare transfer debit", "transfer", "John Doe", "debit", BucketTransferOut},
		{"nip credit is transfer in, not out", "", "NIP CREDIT GTB/OBADEJO/0123456789/TRANSFER", "credit", BucketTransferIn},
		{"nip debit is transfer out", "", "NIP GTB/JOHN/0123/TRANSFER", "debit", BucketTransferOut},
		{"utility name", "", "IKEDC PAYMENT 01234", "debit", BucketUtilities},
		{"unknown stays other", "misc", "SOME RANDOM TEXT", "debit", BucketOther},
		{"empty credit counts as income bucket", "", "John Doe", "credit", BucketOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeStatementCategory(tt.category, tt.description, tt.txnType))
		})
	}
}

func TestPartitionSpendSeparatesTransfers(t *testing.T) {
	consumption, movement := PartitionSpend(map[string]float64{
		"groceries":    40000,
		"Grocery":      10000,
		"transfer_out": 200000,
		"BETTING":      5000,
	})
	assert.InDelta(t, 50000, consumption[BucketGroceries], 0.01)
	assert.InDelta(t, 5000, consumption[BucketBetting], 0.01)
	assert.InDelta(t, 200000, movement[BucketTransferOut], 0.01)
	_, spent := consumption[BucketTransferOut]
	assert.False(t, spent)
}

func TestUnderstandLineConfidence(t *testing.T) {
	bet := UnderstandLine("shopping", "WEB BET9JA/0123", "debit")
	assert.Equal(t, BucketBetting, bet.Bucket)
	assert.InDelta(t, 0.9, bet.Confidence, 0.001)
	assert.Equal(t, "Bet9ja", bet.Counterparty)

	hint := UnderstandLine("groceries", "weekend market", "debit")
	assert.Equal(t, BucketGroceries, hint.Bucket)
	assert.InDelta(t, 0.55, hint.Confidence, 0.001)

	unknown := UnderstandLine("", "SOME RANDOM TEXT", "debit")
	assert.Equal(t, BucketOther, unknown.Bucket)
	assert.Less(t, unknown.Confidence, AdviceConfidenceFloor)
}

func TestAssignRecurrenceStableAmountIsSubscription(t *testing.T) {
	txns := []*entities.BankStatementTransaction{
		{Description: "NETFLIX", Counterparty: "Netflix", Type: "debit", Amount: decimal.NewFromInt(4500)},
		{Description: "NETFLIX", Counterparty: "Netflix", Type: "debit", Amount: decimal.NewFromInt(4500)},
		{Description: "NETFLIX", Counterparty: "Netflix", Type: "debit", Amount: decimal.NewFromInt(4600)},
		{Description: "IKEJA", Counterparty: "Ikeja", Type: "debit", Amount: decimal.NewFromInt(8000)},
		{Description: "IKEJA", Counterparty: "Ikeja", Type: "debit", Amount: decimal.NewFromInt(20000)},
		{Description: "IKEJA", Counterparty: "Ikeja", Type: "debit", Amount: decimal.NewFromInt(12000)},
	}
	AssignRecurrence(txns)
	assert.Equal(t, entities.StatementRecurrenceSubscription, txns[0].Recurrence)
	assert.Equal(t, entities.StatementRecurrenceBill, txns[3].Recurrence)
}

func TestSummarizeForChatUsesNarrationNotBlankCategory(t *testing.T) {
	summary := SummarizeForChat(&ParseResult{
		Currency: "NGN",
		Transactions: []ParsedTxn{
			{Description: "SALARY PAYMENT", Amount: 400000, Type: "credit"},
			{Description: "POS PURCHASE SHOPRITE LEKKI", Amount: 25000, Type: "debit"},
			{Description: "NIP GTB/JOHN/0123/TRANSFER", Amount: 80000, Type: "debit"},
		},
	})
	assert.Contains(t, summary, "Income was about NGN 400000")
	assert.Contains(t, summary, "spending about NGN 25000")
	assert.Contains(t, summary, "Groceries")
	assert.NotContains(t, summary, "80000")
}

func TestFoldCashflowIgnoresTransfers(t *testing.T) {
	income, spend := FoldCashflow([]CashflowLine{
		{Type: "credit", Category: "salary", Amount: decimal.NewFromInt(500000)},
		{Type: "credit", Category: "transfer_in", Amount: decimal.NewFromInt(200000)},
		{Type: "debit", Category: "groceries", Amount: decimal.NewFromInt(40000)},
		{Type: "debit", Category: "transfer_out", Amount: decimal.NewFromInt(300000)},
		{Type: "debit", Category: "savings", Amount: decimal.NewFromInt(50000)},
	})
	assert.True(t, income.Equal(decimal.NewFromInt(500000)))
	assert.True(t, spend.Equal(decimal.NewFromInt(40000)))
}

func TestValidateCategoryRuleRejectsBroadPatterns(t *testing.T) {
	_, _, _, err := ValidateCategoryRule("contains", "a", "groceries")
	assert.Error(t, err)
	_, _, _, err = ValidateCategoryRule("contains", "pos", "shopping")
	assert.Error(t, err)
	matchType, pattern, bucket, err := ValidateCategoryRule("", "Shoprite", "grocery")
	assert.NoError(t, err)
	assert.Equal(t, "contains", matchType)
	assert.Equal(t, "Shoprite", pattern)
	assert.Equal(t, BucketGroceries, bucket)
}

func TestApplyCategoryRulesPrefersLongerPattern(t *testing.T) {
	txns := []*entities.BankStatementTransaction{{
		Description:  "POS SHOPRITE LEKKI",
		Counterparty: "Shoprite Lekki",
		Category:     BucketShopping,
		Type:         "debit",
	}}
	ApplyCategoryRules(txns, []entities.StatementCategoryRule{
		{MatchType: entities.StatementRuleContains, Pattern: "shoprite", Bucket: BucketGroceries},
		{MatchType: entities.StatementRuleContains, Pattern: "shoprite lekki", Bucket: BucketFood},
	})
	assert.Equal(t, BucketFood, txns[0].Category)
	assert.InDelta(t, 0.99, txns[0].CategoryConfidence, 0.001)
}

func TestToEntitiesNormalizesCategory(t *testing.T) {
	parser := &TransactionParser{}
	parsed := &ParseResult{
		Currency: "NGN",
		Transactions: []ParsedTxn{
			{Date: "2025-03-05", Description: "WEB BET9JA", Amount: 2000, Type: "debit", Category: "shopping"},
		},
	}
	txns, _, _ := parser.ToEntities(parsed, uuid.New(), uuid.New())
	if assert.Len(t, txns, 1) {
		assert.Equal(t, BucketBetting, txns[0].Category)
	}
}

func TestUnknownCreditCountsAsIncome(t *testing.T) {
	// Regression: directionFallback used to return transfer_in for unknown
	// credits, which FoldCashflow excludes from income — refunds, interest
	// and gifts silently counted as zero income.
	assert.Equal(t, BucketOther, NormalizeStatementCategory("", "John Doe", "credit"))
	income, _ := FoldCashflow([]CashflowLine{
		{Type: "credit", Category: "other", Amount: decimal.NewFromInt(50000)},
	})
	assert.True(t, income.Equal(decimal.NewFromInt(50000)))
	summary := SummarizeForChat(&ParseResult{
		Currency: "NGN",
		Transactions: []ParsedTxn{
			{Description: "John Doe", Amount: 50000, Type: "credit"},
		},
	})
	assert.Contains(t, summary, "Income was about NGN 50000")
}

func TestSummarizeExcludesTransferOutCredits(t *testing.T) {
	// Same exclusion set as FoldCashflow: transfer_out credits are movement.
	summary := SummarizeForChat(&ParseResult{
		Currency: "NGN",
		Transactions: []ParsedTxn{
			{Description: "SALARY PAYMENT", Amount: 400000, Type: "credit"},
			{Description: "NIP TRANSFER TO JOHN", Amount: 80000, Type: "credit"},
		},
	})
	assert.Contains(t, summary, "Income was about NGN 400000")
	assert.NotContains(t, summary, "480000")
}

func TestExtractCounterpartyStripsPOSReference(t *testing.T) {
	assert.Equal(t, "Shoprite Lekki", ExtractCounterparty("POS PURCHASE SHOPRITE LEKKI 012345"))
}

func TestSalaryIsNotEssential(t *testing.T) {
	assert.False(t, IsEssentialBucket(BucketSalary))
	assert.True(t, IsEssentialBucket(BucketGroceries))
}

func TestRecurrenceNeedsTimeSpread(t *testing.T) {
	// Three similar debits in one week are repeat spending, not a subscription.
	week := []*entities.BankStatementTransaction{
		{Description: "SHOPRITE LEKKI", Counterparty: "Shoprite Lekki", Type: "debit", Amount: decimal.NewFromInt(20000), TransactionDate: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)},
		{Description: "SHOPRITE LEKKI", Counterparty: "Shoprite Lekki", Type: "debit", Amount: decimal.NewFromInt(20500), TransactionDate: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)},
		{Description: "SHOPRITE LEKKI", Counterparty: "Shoprite Lekki", Type: "debit", Amount: decimal.NewFromInt(19800), TransactionDate: time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)},
	}
	AssignRecurrence(week)
	for _, txn := range week {
		assert.Equal(t, entities.StatementRecurrenceOneOff, txn.Recurrence)
	}
	// Same shape across three months is a subscription.
	months := []*entities.BankStatementTransaction{
		{Description: "NETFLIX", Counterparty: "Netflix", Type: "debit", Amount: decimal.NewFromInt(4500), TransactionDate: time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)},
		{Description: "NETFLIX", Counterparty: "Netflix", Type: "debit", Amount: decimal.NewFromInt(4500), TransactionDate: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)},
		{Description: "NETFLIX", Counterparty: "Netflix", Type: "debit", Amount: decimal.NewFromInt(4600), TransactionDate: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)},
	}
	AssignRecurrence(months)
	for _, txn := range months {
		assert.Equal(t, entities.StatementRecurrenceSubscription, txn.Recurrence)
	}
}

func TestRecurrenceIgnoresUnknownCounterparty(t *testing.T) {
	txns := []*entities.BankStatementTransaction{
		{Description: "AAA 1", Counterparty: "Unknown", Type: "debit", Amount: decimal.NewFromInt(1000), TransactionDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		{Description: "BBB 2", Counterparty: "Unknown", Type: "debit", Amount: decimal.NewFromInt(1000), TransactionDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		{Description: "CCC 3", Counterparty: "Unknown", Type: "debit", Amount: decimal.NewFromInt(1000), TransactionDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
	}
	AssignRecurrence(txns)
	for _, txn := range txns {
		assert.Equal(t, entities.StatementRecurrenceOneOff, txn.Recurrence)
	}
}
