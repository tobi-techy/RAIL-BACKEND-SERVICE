package enrichment

import (
	"encoding/json"
	"testing"
)

func TestEnrichResponseJSON(t *testing.T) {
	raw := `{
		"counterparty": "Starbucks",
		"category_l1": "Food & Drink",
		"category_l2": "Coffee",
		"is_essential": false,
		"confidence": 0.92,
		"classification_layer": "ml",
		"plain_description": "Coffee at Starbucks",
		"merchant_context": "Coffee chain",
		"behavior_tags": [
			{"tag": "weekly_pattern", "confidence": 0.8, "metadata": {"avg_days": 7.0}}
		],
		"facts": [
			{"type": "merchant_preference", "value": "User uses Starbucks", "confidence": 0.7, "category": "preference"}
		],
		"embedding": [0.1, 0.2, 0.3],
		"bank": null,
		"tx_type": "card"
	}`

	var resp EnrichResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("failed to unmarshal EnrichResponse: %v", err)
	}

	if resp.Counterparty != "Starbucks" {
		t.Errorf("expected counterparty Starbucks, got %s", resp.Counterparty)
	}
	if resp.CategoryL1 == nil || *resp.CategoryL1 != "Food & Drink" {
		t.Errorf("expected category_l1 Food & Drink, got %v", resp.CategoryL1)
	}
	if resp.Confidence != 0.92 {
		t.Errorf("expected confidence 0.92, got %f", resp.Confidence)
	}
	if len(resp.BehaviorTags) != 1 {
		t.Errorf("expected 1 behavior tag, got %d", len(resp.BehaviorTags))
	}
	if resp.BehaviorTags[0].Tag != "weekly_pattern" {
		t.Errorf("expected tag weekly_pattern, got %s", resp.BehaviorTags[0].Tag)
	}
	if len(resp.Facts) != 1 {
		t.Errorf("expected 1 fact, got %d", len(resp.Facts))
	}
	if resp.Facts[0].Type != "merchant_preference" {
		t.Errorf("expected fact type merchant_preference, got %s", resp.Facts[0].Type)
	}
	if len(resp.Embedding) != 3 {
		t.Errorf("expected 3-dim embedding, got %d", len(resp.Embedding))
	}
	if resp.TxType == nil || *resp.TxType != "card" {
		t.Errorf("expected tx_type card, got %v", resp.TxType)
	}
}

func TestEnrichRequestJSON(t *testing.T) {
	mcc := 5814
	amt := 1500.0
	req := EnrichRequest{
		RawDescription:    "SQ *STARBUCKS 1234 SEATTLE WA",
		MCCCode:           &mcc,
		Amount:            &amt,
		HistoricalAmounts: []float64{1200, 1300, 1400},
		HistoricalDates:   []string{"2026-01-10", "2026-02-10", "2026-03-10"},
	}

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal EnrichRequest: %v", err)
	}

	var decoded EnrichRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to unmarshal EnrichRequest: %v", err)
	}

	if decoded.RawDescription != "SQ *STARBUCKS 1234 SEATTLE WA" {
		t.Errorf("unexpected raw_description: %s", decoded.RawDescription)
	}
	if decoded.MCCCode == nil || *decoded.MCCCode != 5814 {
		t.Errorf("expected mcc_code 5814, got %v", decoded.MCCCode)
	}
	if len(decoded.HistoricalAmounts) != 3 {
		t.Errorf("expected 3 historical amounts, got %d", len(decoded.HistoricalAmounts))
	}
}
