package ramphub

import (
	"encoding/json"
	"strconv"
	"strings"
)

// mapStatusLabel normalizes a RampHub status/stage label (e.g. "Awaiting
// settlement", "settling", "Marked completed") to an internal status.
func mapStatusLabel(status string) string {
	l := strings.ToLower(status)
	switch {
	case strings.Contains(l, "complet"):
		return "completed"
	case strings.Contains(l, "fail"), strings.Contains(l, "cancel"), strings.Contains(l, "denied"):
		return "failed"
	case strings.Contains(l, "paid"), strings.Contains(l, "payment received"):
		return "paid"
	case strings.Contains(l, "settl"), strings.Contains(l, "forward"), strings.Contains(l, "process"), strings.Contains(l, "await"):
		return "processing"
	default:
		return "pending"
	}
}

// MapEventStatus normalizes a webhook event type + data status to an internal
// status. Event type takes precedence (terminal events are authoritative).
func MapEventStatus(eventType, status string) string {
	switch eventType {
	case "transaction.completed":
		return "completed"
	case "transaction.failed":
		return "failed"
	}
	return mapStatusLabel(status)
}

// --- Quote ---

type QuoteRequest struct {
	Side         string  `json:"side"`                  // "buy" or "sell"
	FiatAmount   float64 `json:"fiatAmount,omitempty"`  // buy: fiat to spend
	TokenAmount  float64 `json:"tokenAmount,omitempty"` // sell: crypto to sell
	FiatCurrency string  `json:"fiatCurrency"`
	Asset        string  `json:"asset"`
	Chain        string  `json:"chain"`
}

type QuoteOption struct {
	Provider             string      `json:"provider"`
	Rate                 float64     `json:"rate"`
	EstimatedOutput      float64     `json:"estimatedOutput"`
	Fee                  float64     `json:"fee,omitempty"`
	GrossEstimatedOutput float64     `json:"grossEstimatedOutput,omitempty"`
	ProviderFeeUsd       float64     `json:"providerFeeUsd,omitempty"`
	ProviderFeeToken     float64     `json:"providerFeeToken,omitempty"`
	ProviderFeeFiat      float64     `json:"providerFeeFiat,omitempty"`
	FeeTreatment         string      `json:"feeTreatment,omitempty"`
	RawResponse          interface{} `json:"rawResponse,omitempty"`
	PlatformFeePercent   float64     `json:"platformFeePercent,omitempty"`
	PlatformFeeToken     float64     `json:"platformFeeToken,omitempty"`
	PlatformFeeFiat      float64     `json:"platformFeeFiat,omitempty"`
	NetAfterPlatformFee  float64     `json:"netAfterPlatformFee,omitempty"`
	ProviderFee          *struct {
		Usd       float64 `json:"usd,omitempty"`
		Token     float64 `json:"token,omitempty"`
		Fiat      float64 `json:"fiat,omitempty"`
		Treatment string  `json:"treatment,omitempty"`
	} `json:"providerFee,omitempty"`
	RampHubFee *struct {
		Enabled bool    `json:"enabled"`
		Ratio   float64 `json:"ratio,omitempty"`
		Percent float64 `json:"percent,omitempty"`
		Token   float64 `json:"token,omitempty"`
		Fiat    float64 `json:"fiat,omitempty"`
	} `json:"rampHubFee,omitempty"`
}

type QuoteResponse struct {
	BestQuote QuoteOption   `json:"bestQuote"`
	Quotes    []QuoteOption `json:"quotes"`
}

// --- Orders ---

// OrderRequest is the body for POST /api/developer/orders (buy and sell).
type OrderRequest struct {
	Side         string  `json:"side"` // "buy" or "sell"
	Amount       float64 `json:"amount,omitempty"`
	FiatAmount   float64 `json:"fiatAmount,omitempty"`
	FiatCurrency string  `json:"fiatCurrency"`
	Asset        string  `json:"asset"`
	Chain        string  `json:"chain"`

	// Buy (onramp): destination wallet that receives the crypto.
	WalletAddress string `json:"walletAddress,omitempty"`

	// Sell (offramp): resolved payout bank details.
	BankCode      string `json:"bankCode,omitempty"`
	AccountNumber string `json:"accountNumber,omitempty"`
	AccountName   string `json:"accountName,omitempty"`
	BankName      string `json:"bankName,omitempty"`

	// Customer scoping for the active-intent payment window. RampHub validates the
	// order body strictly and rejects unknown fields, so only these two identity
	// fields may be sent (there is no `name` field). The provider derives the
	// customer's virtual pay-in account name from ExternalCustomerId, which must
	// therefore be alphanumeric (a dashed UUID fails Nigerian bank name rules).
	Email              string `json:"email,omitempty"`
	ExternalCustomerID string `json:"externalCustomerId,omitempty"`

	// DeveloperFeePercent is Rail's business fee on the order (e.g. 0.5 for
	// 0.5%). RampHub accrues it internally. Omitted when zero.
	DeveloperFeePercent float64 `json:"developerFeePercent,omitempty"`

	// OverrideActiveIntent takes over an existing payment window on
	// PAYCHAIN_ACTIVE_INTENT_CONFLICT.
	OverrideActiveIntent bool `json:"overrideActiveIntent,omitempty"`
}

// OrderResponse is the response from creating an order.
type OrderResponse struct {
	TransactionID    string          `json:"transactionId"`
	RequestReference string          `json:"requestReference"`
	Side             string          `json:"side"`
	SelectedProvider string          `json:"selectedProvider"`
	BestRateUsed     float64         `json:"bestRateUsed"`
	Status           string          `json:"status"`
	ProviderDetails  ProviderDetails `json:"providerDetails"`
	OurCryptoAddress string          `json:"ourCryptoAddress"` // sell: where we send crypto (null on buy)
	Asset            string          `json:"asset"`
	Chain            string          `json:"chain"`
	Environment      string          `json:"environment,omitempty"`
	Sandbox          bool            `json:"sandbox,omitempty"`
	Trackable        bool            `json:"trackable,omitempty"`
}

// ProviderDetails carries the next-step instructions for an order.
//   - Buy: the bank account the customer pays into; AmountToPay is the fiat
//     amount. RampHub does NOT normalize this across providers — it passes each
//     provider's native shape, so the pay-in account lives in different places:
//     Paycrest uses VirtualAccount (camelCase); UseBread nests it under
//     Data.Deposit (snake_case). Use OrderResponse.PayInAccount to read it.
//   - Sell: DepositAddress is where we send crypto; AmountToSend is the crypto
//     amount to send.
type ProviderDetails struct {
	Provider       string          `json:"provider,omitempty"`
	Status         string          `json:"status,omitempty"`
	Reference      string          `json:"reference,omitempty"`
	Note           string          `json:"note,omitempty"`
	Sandbox        bool            `json:"sandbox,omitempty"`
	VirtualAccount *VirtualAccount `json:"virtualAccount,omitempty"` // buy: Paycrest-style
	Data           *ProviderData   `json:"data,omitempty"`           // buy: UseBread-style
	AmountToPay    float64         `json:"amountToPay,omitempty"`    // buy: fiat to pay
	DepositAddress string          `json:"depositAddress,omitempty"` // sell
	Network        string          `json:"network,omitempty"`        // sell
	AmountToSend   float64         `json:"amountToSend,omitempty"`   // sell: crypto to send
}

// VirtualAccount is the Paycrest-style pay-in account (camelCase, at the top of
// providerDetails).
type VirtualAccount struct {
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	BankName      string `json:"bankName"`
}

// ProviderData is the UseBread-style envelope nested under providerDetails.data.
type ProviderData struct {
	Status  string           `json:"status,omitempty"`
	Type    string           `json:"type,omitempty"`
	Deposit *ProviderDeposit `json:"deposit,omitempty"` // buy: pay-in account; sell: crypto deposit
}

// ProviderDeposit is the UseBread-style deposit details.
//   - Buy: populated with fiat bank account details (account_number, account_name, bank_name, bank_code).
//   - Sell: populated with crypto deposit details (address, asset, note).
type ProviderDeposit struct {
	AccountNumber string   `json:"account_number,omitempty"`
	AccountName   string   `json:"account_name,omitempty"`
	BankName      string   `json:"bank_name,omitempty"`
	BankCode      string   `json:"bank_code,omitempty"`
	Amount        float64  `json:"amount"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	Address       string   `json:"address,omitempty"` // sell: crypto deposit address
	Asset         string   `json:"asset,omitempty"`   // sell: e.g. "solana:usdc"
	Note          []string `json:"note,omitempty"`    // sell: deposit instructions
}

// PayInAccount returns the fiat bank account a buy-order customer must transfer
// to, normalized across providers. Returns empty strings when no pay-in account
// is present (the order is then unusable for a bank-transfer UX).
func (r *OrderResponse) PayInAccount() (accountNumber, accountName, bankName string) {
	pd := r.ProviderDetails
	// Paycrest-style: providerDetails.virtualAccount
	if va := pd.VirtualAccount; va != nil && va.AccountNumber != "" {
		return va.AccountNumber, va.AccountName, va.BankName
	}
	// UseBread-style: providerDetails.data.deposit (snake_case)
	if pd.Data != nil && pd.Data.Deposit != nil && pd.Data.Deposit.AccountNumber != "" {
		d := pd.Data.Deposit
		return d.AccountNumber, d.AccountName, d.BankName
	}
	return "", "", ""
}

// CryptoDepositAddress returns the crypto deposit address for a sell order,
// normalized across providers. Returns empty string when unavailable.
//   - UseBread: providerDetails.data.deposit.address
//   - Paycrest: providerDetails.depositAddress (handled by normalization in service)
func (r *OrderResponse) CryptoDepositAddress() string {
	if r.OurCryptoAddress != "" {
		return r.OurCryptoAddress
	}
	if r.ProviderDetails.DepositAddress != "" {
		return r.ProviderDetails.DepositAddress
	}
	if r.ProviderDetails.Data != nil && r.ProviderDetails.Data.Deposit != nil && r.ProviderDetails.Data.Deposit.Address != "" {
		return r.ProviderDetails.Data.Deposit.Address
	}
	return ""
}

// CryptoDepositAmount returns the crypto amount to send for a sell order,
// normalizing across providers. Returns 0 when unavailable.
func (r *OrderResponse) CryptoDepositAmount() float64 {
	if r.ProviderDetails.AmountToSend > 0 {
		return r.ProviderDetails.AmountToSend
	}
	if r.ProviderDetails.Data != nil && r.ProviderDetails.Data.Deposit != nil && r.ProviderDetails.Data.Deposit.Amount > 0 {
		return r.ProviderDetails.Data.Deposit.Amount
	}
	return 0
}

// --- Order intent (active payment window) ---

// OrderIntent is the active payment window for a customer/asset/chain.
type OrderIntent struct {
	TransactionID  string `json:"transactionId"`
	Status         string `json:"status"`
	DepositAddress string `json:"depositAddress"`
	ExpiresAt      string `json:"expiresAt"`
}

// --- Transaction status (poll / webhook verification) ---

// Transaction is the canonical order state returned by monitor-status.
// It reports terminal/completed as booleans (more reliable than the status
// label) and does not echo amounts.
type Transaction struct {
	Success       bool   `json:"success"`
	TransactionID string `json:"transactionId"`
	Status        string `json:"status"`
	Terminal      bool   `json:"terminal"`
	Completed     bool   `json:"completed"`
	SyncedAt      string `json:"synchronizedAt"`
	Trackable     bool   `json:"trackable"`
	Sandbox       bool   `json:"sandbox"`
	// Optional fields (not always present on monitor-status).
	Side        string  `json:"side,omitempty"`
	Provider    string  `json:"provider,omitempty"`
	Asset       string  `json:"asset,omitempty"`
	Chain       string  `json:"chain,omitempty"`
	FiatAmount  float64 `json:"fiatAmount,omitempty"`
	TokenAmount float64 `json:"tokenAmount,omitempty"`
	Rate        float64 `json:"rate,omitempty"`
	TxHash      string  `json:"txHash,omitempty"`
}

// MappedStatus normalizes a monitor-status result to an internal status using
// the authoritative completed/terminal booleans, falling back to the label.
func (t *Transaction) MappedStatus() string {
	switch {
	case t.Completed:
		return "completed"
	case t.Terminal:
		return "failed"
	default:
		return mapStatusLabel(t.Status)
	}
}

// --- Catalog & Banks ---

// Catalog is the response from GET /api/developer/catalog.
type Catalog struct {
	Providers []CatalogProvider `json:"providers"`
}

// CatalogProvider describes a routable provider and its capabilities.
type CatalogProvider struct {
	Name         string `json:"name"`
	SupportsBuy  bool   `json:"supportsBuy"`
	SupportsSell bool   `json:"supportsSell"`
	Notes        string `json:"notes,omitempty"`
}

// Bank is a supported payout bank (provider-scoped bank code + name).
type Bank struct {
	BankCode string `json:"bankCode"`
	BankName string `json:"bankName"`
}

// ProviderBankList is the response from GET /api/developer/provider-bank-list/:provider.
type ProviderBankList struct {
	Success  bool   `json:"success"`
	Provider string `json:"provider"`
	Banks    []Bank `json:"banks"`
}

// ResolvedAccount is the result of validating a bank account.
type ResolvedAccount struct {
	Success       bool   `json:"success"`
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	BankCode      string `json:"bankCode"`
}

// --- Webhooks ---

// WebhookEvent is the signed event RampHub posts to the configured endpoint.
type WebhookEvent struct {
	ID        string      `json:"id"`   // e.g. "evt_01hzyx8j3m4w9v0g8s2f6t"
	Type      string      `json:"type"` // transaction.created|updated|completed|failed
	CreatedAt string      `json:"createdAt"`
	LiveMode  bool        `json:"livemode"`
	Data      WebhookData `json:"data"`
}

// WebhookData maps the RampHub webhook data payload. RampHub sends numeric
// fields (fiatAmount, cryptoAmount, exchangeRate) as JSON strings, so they
// use a custom decoder that accepts both string and number inputs.
type WebhookData struct {
	// RampHub has referenced the order under different keys across schema
	// revisions: data.id (current), data.transactionId (what order-create
	// returns and we persist as ramphub_transaction_id), and data.reference /
	// data.requestReference (persisted as request_reference). Capture all of
	// them so the order lookup matches regardless of which key is populated —
	// see Identifiers().
	TransactionID    string    `json:"id"`
	AltTransactionID string    `json:"transactionId"`
	Reference        string    `json:"reference"`
	RequestReference string    `json:"requestReference"`
	Status           string    `json:"status"`
	Provider         string    `json:"platformUsed"`
	Asset            string    `json:"cryptoSymbol"`
	Chain            string    `json:"network"`
	FiatAmount       FlexFloat `json:"fiatAmount"`
	TokenAmount      FlexFloat `json:"cryptoAmount"`
	Rate             FlexFloat `json:"exchangeRate"`
	TxHash           string    `json:"blockchainTxHash,omitempty"`
}

// Identifiers returns every distinct, non-empty identifier RampHub may use to
// reference the order in a webhook payload. The order lookup matches on all of
// them (against both ramphub_transaction_id and request_reference) so a schema
// change in which field carries the id does not break order matching.
func (d WebhookData) Identifiers() []string {
	seen := make(map[string]struct{}, 4)
	var ids []string
	for _, v := range []string{d.TransactionID, d.AltTransactionID, d.Reference, d.RequestReference} {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		ids = append(ids, v)
	}
	return ids
}

// FlexFloat accepts both JSON string and number for a float64 value.
type FlexFloat float64

func (f *FlexFloat) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*f = 0
		return nil
	}
	// Try number first
	if data[0] != '"' {
		var v float64
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*f = FlexFloat(v)
		return nil
	}
	// Unquote string then parse
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s == "" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*f = FlexFloat(v)
	return nil
}
