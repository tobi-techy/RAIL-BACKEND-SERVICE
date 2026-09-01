package context

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// EnrichMerchantMap builds a case-insensitive map from merchant name → enrichment.
func EnrichMerchantMap(ctx context.Context, enricher core.MerchantEnricher, userID uuid.UUID) map[string]*entities.EnrichedTransaction {
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

// LookupEnrichment finds the best enrichment match for a merchant name.
func LookupEnrichment(merchant string, enrichmentMap map[string]*entities.EnrichedTransaction) *entities.EnrichedTransaction {
	if enrichmentMap == nil || merchant == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(merchant))
	if et, ok := enrichmentMap[lower]; ok {
		return et
	}
	for key, et := range enrichmentMap {
		if strings.Contains(lower, key) || strings.Contains(key, lower) {
			return et
		}
	}
	return nil
}

// EnrichMerchantEntry adds enrichment context to a merchant map entry.
func EnrichMerchantEntry(entry map[string]interface{}, enrichmentMap map[string]*entities.EnrichedTransaction) {
	merchant, _ := entry["merchant"].(string)
	if merchant == "" {
		merchant, _ = entry["name"].(string)
	}
	et := LookupEnrichment(merchant, enrichmentMap)
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
