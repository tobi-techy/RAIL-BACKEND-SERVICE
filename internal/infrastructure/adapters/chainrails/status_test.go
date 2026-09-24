package chainrails

import (
	"encoding/json"
	"testing"
)

func TestIntentStatusClassification(t *testing.T) {
	success := []string{"COMPLETED", "COMPLETE", "SUCCESS", "SUCCEEDED", "SETTLED", "FULFILLED", " completed "}
	failure := []string{"FAILED", "FAILURE", "REFUNDED", "EXPIRED", "CANCELLED", "CANCELED", "REJECTED", " expired"}
	pending := []string{"PENDING", "FUNDED", "INITIATED", "PROCESSING", "", "SOMETHING_NEW"}

	for _, s := range success {
		if !IsTerminalSuccess(s) {
			t.Errorf("IsTerminalSuccess(%q) = false, want true", s)
		}
		if !IsTerminal(s) {
			t.Errorf("IsTerminal(%q) = false, want true", s)
		}
		if IsTerminalFailure(s) {
			t.Errorf("IsTerminalFailure(%q) = true, want false", s)
		}
	}
	for _, s := range failure {
		if !IsTerminalFailure(s) {
			t.Errorf("IsTerminalFailure(%q) = false, want true", s)
		}
		if !IsTerminal(s) {
			t.Errorf("IsTerminal(%q) = false, want true", s)
		}
		if IsTerminalSuccess(s) {
			t.Errorf("IsTerminalSuccess(%q) = true, want false", s)
		}
	}
	for _, s := range pending {
		if IsTerminal(s) {
			t.Errorf("IsTerminal(%q) = true, want false", s)
		}
	}
}

func TestIsTestnetChain(t *testing.T) {
	if !IsTestnetChain("BASE_TESTNET") {
		t.Error("BASE_TESTNET should be testnet")
	}
	if IsTestnetChain("BASE_MAINNET") {
		t.Error("BASE_MAINNET should not be testnet")
	}
	if IsTestnetChain("SOLANA_MAINNET") {
		t.Error("SOLANA_MAINNET should not be testnet")
	}
}

func TestStringMapCoercion(t *testing.T) {
	var m StringMap
	payload := `{"withdrawal_id":"abc","count":42,"flag":true,"nested":{"a":1},"nil":null}`
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["withdrawal_id"] != "abc" {
		t.Errorf("string passthrough failed: %q", m["withdrawal_id"])
	}
	if m["count"] != "42" {
		t.Errorf("number coercion failed: %q", m["count"])
	}
	if m["flag"] != "true" {
		t.Errorf("bool coercion failed: %q", m["flag"])
	}
	if m["nested"] != `{"a":1}` {
		t.Errorf("object fallback failed: %q", m["nested"])
	}

	// WebhookIntent with non-string metadata must still decode.
	var event WebhookEvent
	webhook := `{"id":"e1","type":"intent.completed","created_at":"2026-01-01T00:00:00Z",` +
		`"data":{"intent_id":7,"intent_address":"0xabc","status":"COMPLETED","metadata":{"withdrawal_id":"w1","retries":3}}}`
	if err := json.Unmarshal([]byte(webhook), &event); err != nil {
		t.Fatalf("webhook unmarshal with numeric metadata: %v", err)
	}
	if event.Data.Metadata["withdrawal_id"] != "w1" || event.Data.Metadata["retries"] != "3" {
		t.Errorf("metadata mismatch: %+v", event.Data.Metadata)
	}
}
