package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ValidateDueSignature validates Due webhook signature
func ValidateDueSignature(payload []byte, signature, secret string) error {
	if signature == "" {
		return fmt.Errorf("missing signature")
	}

	expectedSig := ComputeDueSignature(payload, secret)
	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// ComputeDueSignature computes HMAC-SHA256 signature for Due webhooks
func ComputeDueSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
