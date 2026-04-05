package chainrails

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"time"
)

const maxTimestampAge = 5 * time.Minute

// WebhookEvent represents a ChainRails webhook payload.
type WebhookEvent struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	CreatedAt string         `json:"created_at"`
	Data      WebhookIntent  `json:"data"`
}

type WebhookIntent struct {
	IntentID         int               `json:"intent_id"`
	IntentAddress    string            `json:"intent_address"`
	SourceChain      string            `json:"source_chain"`
	DestinationChain string            `json:"destination_chain"`
	Status           string            `json:"status"`
	TxHash           string            `json:"tx_hash"`
	Sender           string            `json:"sender"`
	Recipient        string            `json:"recipient"`
	Amount           string            `json:"amount"`
	TokenIn          string            `json:"token_in"`
	TokenOut         string            `json:"token_out"`
	Metadata         map[string]string `json:"metadata"`
}

// VerifyWebhookSignature validates the HMAC-SHA256 signature from ChainRails.
// Signature = HMAC-SHA256(secret, timestamp + "." + body)
func VerifyWebhookSignature(body []byte, signature, timestamp, secret string) error {
	// Validate timestamp freshness
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	age := time.Duration(math.Abs(float64(time.Now().Unix()-ts))) * time.Second
	if age > maxTimestampAge {
		return fmt.Errorf("webhook timestamp too old: %v", age)
	}

	// Compute expected signature
	signed := timestamp + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
