package funding

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func hmacHex(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestValidateWebhookSignatureFailsClosedWithoutSecret(t *testing.T) {
	svc := NewValidationService(nil, &FundingConfig{WebhookSecret: ""}, nil)
	err := svc.ValidateWebhookSignature([]byte(`{"x":1}`), "anything", time.Now().Unix())
	if err == nil {
		t.Fatal("expected error when no webhook secret is configured, got nil (fail-open)")
	}
	if !errors.Is(err, ErrWebhookNotConfigured) {
		t.Fatalf("expected ErrWebhookNotConfigured, got: %v", err)
	}
}

func TestValidateWebhookSignatureRejectsBadSignature(t *testing.T) {
	svc := NewValidationService(nil, &FundingConfig{WebhookSecret: "test-secret"}, nil)
	err := svc.ValidateWebhookSignature([]byte(`{"deposit":"1"}`), "wrong-signature", time.Now().Unix())
	if err == nil {
		t.Fatal("expected error for bad signature, got nil")
	}
	if errors.Is(err, ErrWebhookNotConfigured) {
		t.Fatalf("bad signature must not report ErrWebhookNotConfigured: %v", err)
	}
}

func TestValidateWebhookSignatureAcceptsValidSignature(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"deposit":"1"}`)
	svc := NewValidationService(nil, &FundingConfig{WebhookSecret: secret}, nil)

	for _, sig := range []string{hmacHex(secret, payload), "sha256=" + hmacHex(secret, payload)} {
		if err := svc.ValidateWebhookSignature(payload, sig, time.Now().Unix()); err != nil {
			t.Fatalf("valid signature %q rejected: %v", sig, err)
		}
	}
}

func TestValidateWebhookSignatureRejectsMissingSignature(t *testing.T) {
	svc := NewValidationService(nil, &FundingConfig{WebhookSecret: "test-secret"}, nil)
	if err := svc.ValidateWebhookSignature([]byte(`{}`), "", time.Now().Unix()); err == nil {
		t.Fatal("expected error for missing signature, got nil")
	}
}

func TestNewValidationServiceNilConfigFailsClosed(t *testing.T) {
	svc := NewValidationService(nil, nil, nil)
	if svc == nil {
		t.Fatal("expected non-nil service for nil config")
	}
	err := svc.ValidateWebhookSignature([]byte(`{"x":1}`), "anything", time.Now().Unix())
	if !errors.Is(err, ErrWebhookNotConfigured) {
		t.Fatalf("nil config must fail closed with ErrWebhookNotConfigured, got: %v", err)
	}
}
