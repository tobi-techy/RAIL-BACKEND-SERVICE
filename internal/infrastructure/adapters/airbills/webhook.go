package airbills

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// VerifyWebhookSignature validates the HMAC-SHA256 signature Airbills sends over
// the raw callback body. Airbills' callback posts the completed transaction
// object directly; when a signing secret is configured we verify it, mirroring
// the RampHub webhook contract. The signature header may carry an optional
// "sha256=" prefix and any case.
func VerifyWebhookSignature(body []byte, signature, secret string) error {
	if secret == "" {
		return fmt.Errorf("webhook secret not configured")
	}
	sig := strings.TrimSpace(signature)
	sig = strings.TrimPrefix(sig, "sha256=")
	sig = strings.ToLower(strings.TrimSpace(sig))
	if sig == "" {
		return fmt.Errorf("missing signature")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// CallbackEvent is the completed transaction object Airbills POSTs to the
// configured callbackUrl. Airbills sends the transaction directly with no
// status/message wrapper.
type CallbackEvent struct {
	ID          string                 `json:"id"`
	ProductCode string                 `json:"productCode"`
	Status      string                 `json:"status"`
	Extra       map[string]interface{} `json:"-"`
}
