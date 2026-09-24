package chainrails

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

// StringMap is webhook metadata with tolerant decoding. The documented
// metadata shape is an open object and ChainRails echoes back whatever the
// intent was created with — a single non-string value would otherwise fail
// the whole delivery (400 → retry loop). Scalars coerce to strings; nested
// objects/arrays decode to their compact JSON form.
type StringMap map[string]string

// UnmarshalJSON implements json.Unmarshaler.
func (m *StringMap) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*m = nil
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			out[k] = s
			continue
		}
		var n json.Number
		if err := json.Unmarshal(v, &n); err == nil {
			out[k] = n.String()
			continue
		}
		var b bool
		if err := json.Unmarshal(v, &b); err == nil {
			out[k] = strconv.FormatBool(b)
			continue
		}
		out[k] = string(v)
	}
	*m = out
	return nil
}

const maxTimestampAge = 5 * time.Minute

// WebhookEvent represents a ChainRails webhook payload.
type WebhookEvent struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	CreatedAt string        `json:"created_at"`
	Data      WebhookIntent `json:"data"`
}

type WebhookIntent struct {
	IntentID         int       `json:"intent_id"`
	IntentAddress    string    `json:"intent_address"`
	SourceChain      string    `json:"source_chain"`
	DestinationChain string    `json:"destination_chain"`
	Status           string    `json:"status"`
	TxHash           string    `json:"tx_hash"`
	Sender           string    `json:"sender"`
	Recipient        string    `json:"recipient"`
	Amount           string    `json:"amount"`
	TokenIn          string    `json:"token_in"`
	TokenOut         string    `json:"token_out"`
	Metadata         StringMap `json:"metadata"`
}

// VerifyWebhookSignature validates the HMAC-SHA256 signature from ChainRails.
// Signature = HMAC-SHA256(secret, timestamp + "." + body)
func VerifyWebhookSignature(body []byte, signature, timestamp, secret string) error {
	// Compute expected signature first to prevent timing oracle attacks
	signed := timestamp + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	expected := hex.EncodeToString(mac.Sum(nil))

	// Verify signature before timestamp validation
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("signature mismatch")
	}

	// Only validate timestamp freshness after signature is confirmed
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	age := time.Duration(math.Abs(float64(time.Now().Unix()-ts))) * time.Second
	if age > maxTimestampAge {
		return fmt.Errorf("webhook timestamp too old: %v", age)
	}

	return nil
}
