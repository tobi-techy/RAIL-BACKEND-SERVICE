package ramphub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// VerifyWebhookSignature validates the HMAC-SHA256 signature RampHub sends in
// the x-ramphub-signature header. The signature is computed over the raw request
// body using the webhook endpoint's signing secret.
func VerifyWebhookSignature(body []byte, signature, secret string) error {
	if secret == "" {
		return fmt.Errorf("webhook secret not configured")
	}
	if signature == "" {
		return fmt.Errorf("missing signature")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison to avoid timing oracles. RampHub may prefix the
	// signature (e.g. "sha256="); accept both forms.
	sig := signature
	if len(sig) > 7 && sig[:7] == "sha256=" {
		sig = sig[7:]
	}
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
