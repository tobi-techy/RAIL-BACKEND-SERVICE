package ai

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// MerchantEnricher looks up enrichment data for merchants from the enrichment store.
type MerchantEnricher interface {
	GetEnrichedByUser(ctx context.Context, userID uuid.UUID, limit int) ([]entities.EnrichedTransaction, error)
}

// enrichMerchantMap builds a case-insensitive map from merchant name → enrichment
// from the user's recent enriched transactions. Used by spending tools to add
// plain_description and merchant_context to aggregated results.
func enrichMerchantMap(ctx context.Context, enricher MerchantEnricher, userID uuid.UUID) map[string]*entities.EnrichedTransaction {
	if enricher == nil {
		return nil
	}
	recent, err := enricher.GetEnrichedByUser(ctx, userID, 100)
	if err != nil || len(recent) == 0 {
		return nil
	}
	m := make(map[string]*entities.EnrichedTransaction, len(recent))
	for i := range recent {
		cp := strings.ToLower(strings.TrimSpace(recent[i].Counterparty))
		if cp != "" {
			m[cp] = &recent[i]
		}
	}
	return m
}

// lookupEnrichment finds the best enrichment match for a merchant name.
func lookupEnrichment(merchant string, enrichmentMap map[string]*entities.EnrichedTransaction) *entities.EnrichedTransaction {
	if enrichmentMap == nil || merchant == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(merchant))
	// Exact match
	if et, ok := enrichmentMap[lower]; ok {
		return et
	}
	// Substring match — merchant name contains an enriched counterparty
	for key, et := range enrichmentMap {
		if strings.Contains(lower, key) || strings.Contains(key, lower) {
			return et
		}
	}
	return nil
}

// enrichMerchantEntry adds enrichment context to a merchant map entry
// if enrichment data is available. Checks both "merchant" and "name" keys.
func enrichMerchantEntry(entry map[string]interface{}, enrichmentMap map[string]*entities.EnrichedTransaction) {
	merchant, _ := entry["merchant"].(string)
	if merchant == "" {
		merchant, _ = entry["name"].(string)
	}
	et := lookupEnrichment(merchant, enrichmentMap)
	if et == nil {
		return
	}
	if et.PlainDescription != "" {
		entry["plain_description"] = et.PlainDescription
	}
	if et.MerchantContext != "" {
		entry["merchant_context"] = et.MerchantContext
	}
	if et.IsEssential {
		entry["is_essential"] = true
	}
	if et.Bank != "" {
		entry["bank"] = et.Bank
	}
	if et.TxType != "" {
		entry["tx_type"] = et.TxType
	}
}
