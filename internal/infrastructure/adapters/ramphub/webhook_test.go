package ramphub

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
	secret := "whsec_test"
	body := []byte(`{"type":"transaction.completed","data":{"transactionId":"RH-TX-1"}}`)
	good := sign(body, secret)

	tests := []struct {
		name    string
		body    []byte
		sig     string
		secret  string
		wantErr bool
	}{
		{"valid", body, good, secret, false},
		{"valid with prefix", body, "sha256=" + good, secret, false},
		{"tampered body", []byte(`{"type":"transaction.failed"}`), good, secret, true},
		{"wrong secret", body, sign(body, "other"), secret, true},
		{"empty signature", body, "", secret, true},
		{"empty secret", body, good, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyWebhookSignature(tt.body, tt.sig, tt.secret)
			if (err != nil) != tt.wantErr {
				t.Fatalf("VerifyWebhookSignature() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsActiveIntentConflict(t *testing.T) {
	err := &APIError{StatusCode: 409, Body: `{"code":"PAYCHAIN_ACTIVE_INTENT_CONFLICT"}`}
	if !IsActiveIntentConflict(err) {
		t.Fatal("expected active intent conflict to be detected")
	}
	if IsActiveIntentConflict(&APIError{StatusCode: 400, Body: "other"}) {
		t.Fatal("did not expect conflict for unrelated error")
	}
}

func TestIsUnauthorized(t *testing.T) {
	if !IsUnauthorized(&APIError{StatusCode: 401}) {
		t.Fatal("401 should be unauthorized")
	}
	if !IsUnauthorized(&APIError{StatusCode: 403}) {
		t.Fatal("403 should be unauthorized")
	}
	if IsUnauthorized(&APIError{StatusCode: 500}) {
		t.Fatal("500 should not be unauthorized")
	}
}
