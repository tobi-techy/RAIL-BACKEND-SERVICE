package graph

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// WebhookHeader is the header Graph uses to deliver the HMAC signature.
const WebhookHeader = "x-graph-signature"

// VerifyWebhookSignature validates the HMAC-SHA256 signature Graph sends over
// the raw request body using the webhook signing secret.
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

// WebhookEvent is the envelope Graph sends for issuance/transaction/deposit
// events. The concrete payload shape depends on `type`; the fields below cover
// the account-activation and deposit cases this integration handles.
type WebhookEvent struct {
	Type     string        `json:"type"`
	Event    string        `json:"event"`
	LiveMode bool          `json:"livemode"`
	Data     WebhookData   `json:"data"`
}

// WebhookData carries the relevant fields across issuance and transaction
// events. Graph nests the resource under `data`.
type WebhookData struct {
	ID            string  `json:"id"`
	Reference     string  `json:"reference"`
	Type          string  `json:"type"`
	Status        string  `json:"status"`
	Currency      string  `json:"currency"`
	Amount        string  `json:"amount"`
	AccountID     string  `json:"account_id"`
	BankAccountID string  `json:"bank_account_id"`
	PersonID      string  `json:"person_id"`
	AccountNumber string  `json:"account_number"`
	AccountName   string  `json:"account_name"`
	BankName      string  `json:"bank_name"`
	BankCode      string  `json:"bank_code"`
	Direction     string  `json:"direction"` // credit | debit
	Narration     string  `json:"narration"`
	Balance       float64 `json:"balance"`
}
