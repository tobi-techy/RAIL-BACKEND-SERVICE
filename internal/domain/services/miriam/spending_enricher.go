package miriam

import (
	"context"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/enrichment"
	"go.uber.org/zap"
)

// SpendingEnricher enriches SpendingTransaction results with plain-English descriptions
// by calling the ML sidecar batch endpoint. This bridges the gap between the enrichment
// pipeline (which processes blockchain transactions) and the spending tools (which show
// card/P2P/withdrawal outflows).
type SpendingEnricher struct {
	client *enrichment.Client
	logger *zap.Logger
}

// NewSpendingEnricher creates a spending enricher.
func NewSpendingEnricher(client *enrichment.Client, logger *zap.Logger) *SpendingEnricher {
	return &SpendingEnricher{client: client, logger: logger}
}

// EnrichTransactions adds plain-English descriptions to spending transactions
// by calling the ML sidecar. Transactions that already have enrichment or that
// fail to enrich are returned unchanged.
func (e *SpendingEnricher) EnrichTransactions(ctx context.Context, txns []entities.SpendingTransaction) []entities.SpendingTransaction {
	if e.client == nil || len(txns) == 0 {
		return txns
	}

	// Build raw descriptions from source + category for the sidecar
	descriptions := make([]string, len(txns))
	for i, t := range txns {
		descriptions[i] = buildRawDescription(t)
	}

	// Batch call the sidecar
	results, err := e.client.EnrichBatch(ctx, descriptions)
	if err != nil {
		e.logger.Debug("spending enrichment batch failed, using raw data", zap.Error(err))
		return txns
	}

	// Merge enrichment results into spending transactions
	enriched := make([]entities.SpendingTransaction, len(txns))
	for i, t := range txns {
		enriched[i] = t
		if i < len(results) {
			r := results[i]
			if r.PlainDescription != "" {
				enriched[i].PlainDescription = r.PlainDescription
			}
			if r.MerchantContext != "" {
				enriched[i].MerchantContext = r.MerchantContext
			}
			if r.Counterparty != "" {
				enriched[i].Counterparty = r.Counterparty
			}
			if r.IsEssential {
				enriched[i].IsEssential = &r.IsEssential
			}
			if r.SpendBucket != "" && isGenericCategory(t.Category) {
				enriched[i].Category = r.SpendBucket
			}
		}
	}

	return enriched
}

// buildRawDescription is the narration the enrichment sidecar classifies.
// The merchant (Source) leads. Putting the category first ("groceries Shoprite")
// made brand lookup miss the shop. Amounts are omitted because they are not
// part of the merchant name and the trailing-number cleaner strips them anyway.
func buildRawDescription(t entities.SpendingTransaction) string {
	source := strings.TrimSpace(t.Source)
	category := strings.TrimSpace(t.Category)
	if source != "" && !isGenericSource(source) {
		return source
	}
	if category != "" && !isGenericCategory(category) && source != "" {
		return category + " " + source
	}
	if source != "" {
		return source
	}
	return category
}

func isGenericSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", "card", "card spend", "withdrawal", "withdrawals", "p2p", "p2p transfer",
		"transfer", "spend", "rail", "unknown", "debit", "outflow":
		return true
	default:
		return false
	}
}

func isGenericCategory(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "", "other", "uncategorized", "unknown", "card", "card spend", "withdrawal",
		"withdrawals", "p2p", "spend", "debit", "general":
		return true
	default:
		return false
	}
}

// MergeEnrichedIntoSpending merges enrichment data from miriam_enriched_transactions
// into spending transactions by matching on amount and date proximity.
// This is used when enrichment data is already persisted (from the background pipeline).
func MergeEnrichedIntoSpending(txns []entities.SpendingTransaction, enriched map[string]*entities.EnrichedTransaction) []entities.SpendingTransaction {
	if len(enriched) == 0 {
		return txns
	}

	result := make([]entities.SpendingTransaction, len(txns))
	for i, t := range txns {
		result[i] = t
		// Build a lookup key from date + amount
		key := t.Date + "|" + t.Amount.StringFixed(8)
		if e, ok := enriched[key]; ok {
			if e.PlainDescription != "" {
				result[i].PlainDescription = e.PlainDescription
			}
			if e.MerchantContext != "" {
				result[i].MerchantContext = e.MerchantContext
			}
			if e.Counterparty != "" {
				result[i].Counterparty = e.Counterparty
			}
			isEss := e.IsEssential
			result[i].IsEssential = &isEss
		}
	}
	return result
}
