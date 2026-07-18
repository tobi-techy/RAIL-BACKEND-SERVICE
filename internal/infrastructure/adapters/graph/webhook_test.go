package graph

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "whsec_test_123"
	body := []byte(`{"type":"transaction","data":{"id":"tx_1","amount":"50000"}}`)
	valid := sign(body, secret)

	tests := []struct {
		name      string
		body      []byte
		signature string
		secret    string
		wantErr   bool
	}{
		{"valid", body, valid, secret, false},
		{"valid with sha256 prefix", body, "sha256=" + valid, secret, false},
		{"wrong signature", body, sign(body, "other"), secret, true},
		{"tampered body", []byte(`{"amount":"999999"}`), valid, secret, true},
		{"empty signature", body, "", secret, true},
		{"empty secret", body, valid, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyWebhookSignature(tt.body, tt.signature, tt.secret)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
