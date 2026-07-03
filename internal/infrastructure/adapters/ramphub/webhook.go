package ramphub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// VerifyWebhookSignature validates the HMAC-SHA256 signature RampHub sends in
// the x-ramphub-signature header. The signature is computed over the raw request
// body using the webhook endpoint's signing secret.
func VerifyWebhookSignature(body []byte, signature, secret string) error {
	if secret == "" {
		return fmt.Errorf("webhook secret not configured")
	}
	// Normalize: trim whitespace (proxy/copy artifacts), strip an optional
	// "sha256=" prefix, and lower-case so a valid upper-case hex digest still
	// matches. hex.EncodeToString emits lower-case, so expected is lower-case.
	sig := strings.TrimSpace(signature)
	sig = strings.TrimPrefix(sig, "sha256=")
	sig = strings.ToLower(strings.TrimSpace(sig))
	if sig == "" {
		return fmt.Errorf("missing signature")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison to avoid timing oracles.
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
