package statement

import (
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

const (
	// MinTransactionsForStatement is the minimum number of valid transactions
	// required to consider a document a bank statement (not a receipt/invoice/random PDF).
	MinTransactionsForStatement = 3

	// MaxFutureDays is how far in the future a transaction date can be before it's dropped.
	MaxFutureDays = 7
)

// ValidationResult holds the outcome of post-parse validation.
type ValidationResult struct {
	Valid            bool
	TotalTransactions int
	ValidTransactions int
	DroppedCount     int
	BalanceMismatches int
	Confidence       float64 // 0.0 to 1.0
}

// ValidateTransactions checks parsed transactions for consistency and drops suspicious ones.
// It validates:
// 1. Consecutive balance_after values match (prev_balance ± amount = next_balance)
// 2. No duplicate transactions (same date + amount + description)
// 3. Amounts are within reasonable bounds for the currency
func ValidateTransactions(txns []*entities.BankStatementTransaction) (*ValidationResult, []*entities.BankStatementTransaction) {
	if len(txns) == 0 {
		return &ValidationResult{Valid: false}, nil
	}

	result := &ValidationResult{TotalTransactions: len(txns)}
	var valid []*entities.BankStatementTransaction
	seen := make(map[string]bool)

	// Max reasonable single transaction for NGN (100M), USD/GBP/EUR (1M)
	maxNGN := decimal.NewFromInt(100_000_000)
	maxOther := decimal.NewFromInt(1_000_000)

	maxDate := time.Now().AddDate(0, 0, MaxFutureDays)
	var prevBalance *decimal.Decimal

	for _, txn := range txns {
		// Future date check: drop transactions dated too far in the future
		if txn.TransactionDate.After(maxDate) {
			result.DroppedCount++
			continue
		}

		// Dedup check: same date + amount + first 30 chars of description
		desc := txn.Description
		if len(desc) > 30 {
			desc = desc[:30]
		}
		key := txn.TransactionDate.Format("2006-01-02") + "|" + txn.Amount.String() + "|" + txn.Type + "|" + desc
		if seen[key] {
			result.DroppedCount++
			continue
		}
		seen[key] = true

		// Amount bounds check
		maxAmount := maxOther
		if txn.Currency == "NGN" {
			maxAmount = maxNGN
		}
		if txn.Amount.GreaterThan(maxAmount) {
			result.DroppedCount++
			continue
		}

		// Balance continuity check (if balance_after is available)
		if txn.BalanceAfter != nil && prevBalance != nil {
			var expected decimal.Decimal
			if txn.Type == entities.StatementTxnTypeDebit {
				expected = prevBalance.Sub(txn.Amount)
			} else {
				expected = prevBalance.Add(txn.Amount)
			}
			diff := expected.Sub(*txn.BalanceAfter).Abs()
			// Allow 1 unit tolerance (rounding)
			if diff.GreaterThan(decimal.NewFromInt(1)) {
				result.BalanceMismatches++
				// Don't drop — the LLM might have the balance right but our math wrong
				// (fees, charges not shown as separate transactions)
			}
		}

		if txn.BalanceAfter != nil {
			bal := *txn.BalanceAfter
			prevBalance = &bal
		}

		valid = append(valid, txn)
		result.ValidTransactions++
	}

	// Calculate confidence score
	if result.TotalTransactions > 0 {
		validRatio := float64(result.ValidTransactions) / float64(result.TotalTransactions)
		mismatchPenalty := float64(result.BalanceMismatches) / float64(max(result.ValidTransactions, 1))
		result.Confidence = validRatio * (1.0 - mismatchPenalty*0.5)
		if result.Confidence < 0 {
			result.Confidence = 0
		}
	}

	result.Valid = result.ValidTransactions >= MinTransactionsForStatement && result.Confidence > 0.3
	return result, valid
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
