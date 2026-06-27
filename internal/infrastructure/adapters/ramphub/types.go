package ramphub

import "strings"

// mapStatusLabel normalizes a RampHub status/stage label (e.g. "Awaiting
// settlement", "settling", "Marked completed") to an internal status.
func mapStatusLabel(status string) string {
	l := strings.ToLower(status)
	switch {
	case strings.Contains(l, "complet"):
		return "completed"
	case strings.Contains(l, "fail"), strings.Contains(l, "cancel"), strings.Contains(l, "denied"):
		return "failed"
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

// --- Quotes ---

// QuoteRequest is the body for POST /api/developer/quotes.
// For buy (onramp) set FiatAmount; for sell (offramp) set TokenAmount.
type QuoteRequest struct {
	Side         string  `json:"side"` // "buy" or "sell"
	FiatAmount   float64 `json:"fiatAmount,omitempty"`
	TokenAmount  float64 `json:"tokenAmount,omitempty"`
	FiatCurrency string  `json:"fiatCurrency"` // e.g. "NGN"
	Asset        string  `json:"asset"`        // e.g. "USDC", "USDT"
	Chain        string  `json:"chain"`        // e.g. "solana", "base"
}

// QuoteOption is a single provider route returned by the quotes endpoint.
type QuoteOption struct {
	Provider        string  `json:"provider"`
	Rate            float64 `json:"rate"`
	EstimatedOutput float64 `json:"estimatedOutput"`
	Fee             float64 `json:"fee,omitempty"`
}

// QuoteResponse is the response from the quotes endpoint. BestQuote is the
// recommended route RampHub already selected; Quotes holds all options.
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

	// Customer scoping for the active-intent payment window.
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
//   - Buy: VirtualAccount is the bank account the customer pays into; AmountToPay
//     is the fiat amount.
//   - Sell: DepositAddress is where we send crypto; AmountToSend is the crypto
//     amount to send.
type ProviderDetails struct {
	Provider       string          `json:"provider,omitempty"`
	Status         string          `json:"status,omitempty"`
	Reference      string          `json:"reference,omitempty"`
	Note           string          `json:"note,omitempty"`
	Sandbox        bool            `json:"sandbox,omitempty"`
	VirtualAccount *VirtualAccount `json:"virtualAccount,omitempty"` // buy
	AmountToPay    float64         `json:"amountToPay,omitempty"`    // buy: fiat to pay
	DepositAddress string          `json:"depositAddress,omitempty"` // sell
	Network        string          `json:"network,omitempty"`        // sell
	AmountToSend   float64         `json:"amountToSend,omitempty"`   // sell: crypto to send
}

// VirtualAccount is the bank account a buy-order customer pays into.
type VirtualAccount struct {
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	BankName      string `json:"bankName"`
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

type WebhookData struct {
	TransactionID string  `json:"transactionId"`
	Status        string  `json:"status"`
	Provider      string  `json:"provider"`
	Asset         string  `json:"asset"`
	Chain         string  `json:"chain"`
	FiatAmount    float64 `json:"fiatAmount,omitempty"`
	TokenAmount   float64 `json:"tokenAmount,omitempty"`
	Rate          float64 `json:"rate,omitempty"`
	TxHash        string  `json:"txHash,omitempty"`
}
