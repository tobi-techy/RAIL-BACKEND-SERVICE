package ramphub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
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
		{"valid with surrounding whitespace", body, "  " + good + "\n", secret, false},
		{"valid uppercase hex", body, strings.ToUpper(good), secret, false},
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

func TestWebhookDataIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "id field only (current schema)",
			body: `{"data":{"id":"7a5fdfb8-8a35-4e11-bb9c-b8548b62302f"}}`,
			want: []string{"7a5fdfb8-8a35-4e11-bb9c-b8548b62302f"},
		},
		{
			name: "transactionId field only (pre-schema-change)",
			body: `{"data":{"transactionId":"RH-TX-1"}}`,
			want: []string{"RH-TX-1"},
		},
		{
			name: "id and reference both present, de-duped",
			body: `{"data":{"id":"abc","transactionId":"abc","reference":"RH-TX-9"}}`,
			want: []string{"abc", "RH-TX-9"},
		},
		{
			name: "no identifier",
			body: `{"data":{"status":"completed"}}`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ev WebhookEvent
			if err := json.Unmarshal([]byte(tt.body), &ev); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := ev.Data.Identifiers()
			if len(got) != len(tt.want) {
				t.Fatalf("Identifiers() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("Identifiers()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
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
