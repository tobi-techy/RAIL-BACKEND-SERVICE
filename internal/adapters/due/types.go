package due

import (
	"fmt"
	"time"
)

// Account Types
type AccountType string

const (
	AccountTypeBusiness   AccountType = "business"
	AccountTypeIndividual AccountType = "individual"
)

// KYC Status
type KYCStatus string

const (
	KYCStatusPending              KYCStatus = "pending"
	KYCStatusPassed               KYCStatus = "passed"
	KYCStatusResubmissionRequired KYCStatus = "resubmission_required"
	KYCStatusFailed               KYCStatus = "failed"
)



// LinkWalletRequest represents wallet linking request
type LinkWalletRequest struct {
	Address string `json:"address"` // Format: "evm:0x..." or "solana:..."
}

// LinkWalletResponse represents wallet linking response
type LinkWalletResponse struct {
	ID         string    `json:"id"`
	Address    string    `json:"address"`
	Blockchain string    `json:"blockchain"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
}

// CreateRecipientRequest represents recipient creation request
type CreateRecipientRequest struct {
	Name       string           `json:"name"`
	Details    RecipientDetails `json:"details"`
	IsExternal bool             `json:"isExternal"`
}

// RecipientDetails contains recipient payment details
type RecipientDetails struct {
	Schema  string `json:"schema"`  // "evm" or "solana"
	Address string `json:"address"` // Blockchain address
}

// CreateRecipientResponse represents recipient creation response
type CreateRecipientResponse struct {
	ID         string           `json:"id"`
	Label      string           `json:"label"`
	Details    RecipientDetails `json:"details"`
	IsExternal bool             `json:"isExternal"`
	IsActive   bool             `json:"isActive"`
}

// TransferStatus represents transfer status
type TransferStatus string

const (
	TransferStatusPending          TransferStatus = "pending"
	TransferStatusPaymentProcessed TransferStatus = "payment_processed"
	TransferStatusCompleted        TransferStatus = "completed"
	TransferStatusFailed           TransferStatus = "failed"
)

// KYCStatusResponse represents KYC status response
type KYCStatusResponse struct {
	Status      KYCStatus `json:"status"`
	Link        string    `json:"link"`
	ApplicantID string    `json:"applicantId,omitempty"`
}

// KYCInitiateResponse represents KYC initiation response
type KYCInitiateResponse struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Email       string    `json:"email"`
	ApplicantID string    `json:"applicantId"`
	ExternalLink string   `json:"externalLink"`
	Status      KYCStatus `json:"status"`
	Country     string    `json:"country"`
	Token       string    `json:"token"`
}

// CreateTransferRequest represents transfer creation request
type CreateTransferRequest struct {
	SourceID      string `json:"sourceId"`      // Virtual account ID
	DestinationID string `json:"destinationId"` // Recipient ID
	Amount        string `json:"amount"`        // Amount to transfer
	Currency      string `json:"currency"`      // Currency (USDC)
	Reference     string `json:"reference"`     // Unique reference
}

// CreateTransferResponse represents transfer creation response
type CreateTransferResponse struct {
	ID          string         `json:"id"`
	OwnerID     string         `json:"ownerId"`
	Status      TransferStatus `json:"status"`
	Source      TransferLeg    `json:"source"`
	Destination TransferLeg    `json:"destination"`
	FXRate      float64        `json:"fxRate,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// TOSAcceptResponse represents ToS acceptance response
type TOSAcceptResponse struct {
	ID        string    `json:"id"`
	EntityName string   `json:"entityName"`
	Status    string    `json:"status"`
	AcceptedAt time.Time `json:"acceptedAt"`
	Token     string    `json:"token"`
}

// WebhookEvent represents a Due webhook event
type WebhookEvent struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

// TransferWebhookData represents transfer webhook data
type TransferWebhookData struct {
	ID          string         `json:"id"`
	OwnerID     string         `json:"ownerId"`
	Status      TransferStatus `json:"status"`
	Source      TransferLeg    `json:"source"`
	Destination TransferLeg    `json:"destination"`
	FXRate      float64        `json:"fxRate"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// TransferLeg represents one leg of a transfer
type TransferLeg struct {
	Amount   string `json:"amount"`
	Fee      string `json:"fee"`
	Currency string `json:"currency"`
	Rail     string `json:"rail"`
	ID       string `json:"id,omitempty"`
}

// ListRecipientsResponse represents paginated recipients response
type ListRecipientsResponse struct {
	Data       []CreateRecipientResponse `json:"data"`
	Total      int                       `json:"total"`
	Limit      int                       `json:"limit"`
	Offset     int                       `json:"offset"`
}

// VirtualAccountFilters represents filters for virtual accounts
type VirtualAccountFilters struct {
	Destination string // Required: recipient ID for the virtual account
	SchemaIn    string // Required: input payment method schema
	CurrencyIn  string // Required: input currency
	RailOut     string // Required: output rail
	CurrencyOut string // Required: output currency
	Reference   string // Optional: reference for tracking
}

// ListVirtualAccountsResponse represents paginated virtual accounts response
type ListVirtualAccountsResponse struct {
	Data  []CreateVirtualAccountResponse `json:"data"`
	Total int                            `json:"total"`
}

// TransferFilters represents filters for transfers
type TransferFilters struct {
	Limit  int
	Order  string // "asc" or "desc"
	Status TransferStatus
}

// ListTransfersResponse represents paginated transfers response
type ListTransfersResponse struct {
	Data       []CreateTransferResponse `json:"data"`
	Total      int                      `json:"total"`
	HasMore    bool                     `json:"hasMore"`
}

// Channel represents a payment channel
type Channel struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"` // "static_deposit" or "withdrawal"
	Schema      string   `json:"schema"`
	Currency    string   `json:"currency"`
	Rail        string   `json:"rail"`
	Countries   []string `json:"countries"`
	IsActive    bool     `json:"isActive"`
}

// ChannelsResponse represents available channels response
type ChannelsResponse struct {
	Channels []Channel `json:"channels"`
}

// CreateQuoteRequest represents quote creation request
type CreateQuoteRequest struct {
	Sender    string `json:"sender"`    // Wallet ID
	Recipient string `json:"recipient"` // Recipient ID
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
}

// QuoteResponse represents quote response
type QuoteResponse struct {
	ID          string      `json:"id"`
	Source      TransferLeg `json:"source"`
	Destination TransferLeg `json:"destination"`
	FXRate      float64     `json:"fxRate"`
	ExpiresAt   time.Time   `json:"expiresAt"`
	CreatedAt   time.Time   `json:"createdAt"`
}

// ListWalletsResponse represents list of wallets response
type ListWalletsResponse struct {
	Data  []LinkWalletResponse `json:"data"`
	Total int                  `json:"total"`
}

// CreateWebhookRequest represents webhook endpoint creation request
type CreateWebhookRequest struct {
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Description string   `json:"description,omitempty"`
}

// WebhookEndpointResponse represents webhook endpoint response
type WebhookEndpointResponse struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Events      []string  `json:"events"`
	Description string    `json:"description"`
	IsActive    bool      `json:"isActive"`
	Secret      string    `json:"secret"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ListWebhookEndpointsResponse represents list of webhook endpoints
type ListWebhookEndpointsResponse struct {
	Data  []WebhookEndpointResponse `json:"data"`
	Total int                       `json:"total"`
}

// WebhookEventFilters represents filters for webhook events
type WebhookEventFilters struct {
	Limit     int    `json:"limit,omitempty"`
	EventType string `json:"eventType,omitempty"`
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
}

// WebhookEventData represents webhook event data
type WebhookEventData struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data"`
	CreatedAt time.Time              `json:"createdAt"`
	Delivered bool                   `json:"delivered"`
}

// ListWebhookEventsResponse represents list of webhook events
type ListWebhookEventsResponse struct {
	Data  []WebhookEventData `json:"data"`
	Total int                `json:"total"`
}

// AccountCategory represents an account category
type AccountCategory struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"` // "individual" or "business"
	Countries   []string `json:"countries"`
	Currencies  []string `json:"currencies"`
	IsActive    bool     `json:"isActive"`
}

// AccountCategoriesResponse represents account categories response
type AccountCategoriesResponse struct {
	Categories []AccountCategory `json:"categories"`
}

// ErrorResponse represents Due API error response
type ErrorResponse struct {
	StatusCode int                    `json:"statusCode"`
	Message    string                 `json:"message"`
	Code       string                 `json:"code"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

// KYCLinkResponse represents KYC link response
type KYCLinkResponse struct {
	Link        string    `json:"link"`
	Status      KYCStatus `json:"status"`
	ApplicantID string    `json:"applicantId,omitempty"`
}

// KYCSessionRequest represents KYC session creation request
type KYCSessionRequest struct {
	RedirectURL string `json:"redirectUrl"`
	WebhookURL  string `json:"webhookUrl,omitempty"`
}

// KYCSessionResponse represents KYC session response
type KYCSessionResponse struct {
	SessionID   string `json:"sessionId"`
	AccessToken string `json:"accessToken"`
	ExpiresAt   string `json:"expiresAt"`
	RedirectURL string `json:"redirectUrl"`
}

// UpdateVirtualAccountRequest represents virtual account update request
type UpdateVirtualAccountRequest struct {
	IsActive bool `json:"isActive"`
}

// FundingAddressRequest represents funding address creation request
type FundingAddressRequest struct {
	Rail string `json:"rail"` // "ethereum", "polygon", etc.
}

// FundingAddressResponse represents funding address response
type FundingAddressResponse struct {
	Address   string                `json:"address"`
	Rail      string                `json:"rail"`
	Currency  string                `json:"currency"`
	ExpiresAt string                `json:"expiresAt"`
	Details   FundingAddressDetails `json:"details"`
}

// TOSDataResponse represents Terms of Service data
type TOSDataResponse struct {
	Token      string `json:"token"`
	EntityName string `json:"entityName"`
	Content    string `json:"content"`
	Version    string `json:"version"`
	Required   bool   `json:"required"`
}

// FinancialInstitution represents a financial institution
type FinancialInstitution struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Country     string            `json:"country"`
	Schema      string            `json:"schema"`
	Currency    string            `json:"currency"`
	Fields      map[string]string `json:"fields"`
	IsActive    bool              `json:"isActive"`
}

// FinancialInstitutionsResponse represents list of financial institutions
type FinancialInstitutionsResponse struct {
	Data  []FinancialInstitution `json:"data"`
	Total int                    `json:"total"`
}

// SimulatePayInRequest represents sandbox pay-in simulation request
type SimulatePayInRequest struct {
	VirtualAccountKey string `json:"virtualAccountKey"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	Reference        string `json:"reference"`
}

// SimulatePayInResponse represents sandbox pay-in simulation response
type SimulatePayInResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	Reference string `json:"reference"`
}

// Error implements error interface
func (e *ErrorResponse) Error() string {
	if len(e.Details) > 0 {
		return fmt.Sprintf("Due API error [%d]: %s (code: %s, details: %v)", e.StatusCode, e.Message, e.Code, e.Details)
	}
	return fmt.Sprintf("Due API error [%d]: %s (code: %s)", e.StatusCode, e.Message, e.Code)
}

// On-Ramp Types (USD to USDC)

// OnRampQuoteRequest represents a quote request for on-ramp
type OnRampQuoteRequest struct {
	Source      QuoteSource      `json:"source"`
	Destination QuoteDestination `json:"destination"`
}

// QuoteSource represents the source of funds
type QuoteSource struct {
	Rail     string `json:"rail"`     // "ach"
	Currency string `json:"currency"` // "USD"
	Amount   string `json:"amount"`   // USD amount
}

// QuoteDestination represents the destination
type QuoteDestination struct {
	Rail     string `json:"rail"`     // "ethereum", "solana"
	Currency string `json:"currency"` // "USDC"
	Amount   string `json:"amount"`   // "0" to calculate
}

// OnRampQuoteResponse represents the quote response
type OnRampQuoteResponse struct {
	Token       string           `json:"token"`
	Source      QuoteSource      `json:"source"`
	Destination QuoteDestination `json:"destination"`
	FXRate      float64          `json:"fxRate"`
	FXMarkup    float64          `json:"fxMarkup"`
	ExpiresAt   time.Time        `json:"expiresAt"`
}

// OnRampTransferRequest represents a transfer request
type OnRampTransferRequest struct {
	Quote     string `json:"quote"`     // Quote token
	Sender    string `json:"sender"`    // Virtual account/wallet ID
	Recipient string `json:"recipient"` // Recipient ID
	Memo      string `json:"memo,omitempty"`
}

// OnRampTransferResponse represents the transfer response
type OnRampTransferResponse struct {
	ID                   string           `json:"id"`
	OwnerID              string           `json:"ownerId"`
	Status               string           `json:"status"`
	Source               QuoteSource      `json:"source"`
	Destination          QuoteDestination `json:"destination"`
	FXRate               float64          `json:"fxRate"`
	FXMarkup             float64          `json:"fxMarkup"`
	TransferInstructions TransferInstructions `json:"transferInstructions"`
	CreatedAt            time.Time        `json:"createdAt"`
	ExpiresAt            time.Time        `json:"expiresAt"`
}

// TransferInstructions contains instructions for completing the transfer
type TransferInstructions struct {
	Type string `json:"type"` // "TransferIntent" or "FundingAddress"
}

// FundingAddressDetails contains the funding address
type FundingAddressDetails struct {
	Address string `json:"address"`
	Schema  string `json:"schema"`
}

// WalletBalanceResponse represents wallet balance response
type WalletBalanceResponse struct {
	Balances []Balance `json:"balances"`
}

// Balance represents a currency balance
type Balance struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	Network  string `json:"network,omitempty"`
}

// TransferIntentRequest represents transfer intent request
type TransferIntentRequest struct {
	Signature string `json:"signature"`
	PublicKey string `json:"publicKey"`
}

// SubmitTransferIntentRequest represents submit transfer intent request
type SubmitTransferIntentRequest struct {
	IntentID  string `json:"intentId"`
	Signature string `json:"signature"`
}

// TransferIntentResponse represents transfer intent response
type TransferIntentResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// InitCredentialsRequest represents vault credentials initialization request
type InitCredentialsRequest struct {
	Email string `json:"email"`
}

// InitCredentialsResponse represents vault credentials initialization response
type InitCredentialsResponse struct {
	ChallengeID string `json:"challengeId"`
	Challenge   string `json:"challenge"`
}

// CreateCredentialsRequest represents vault credentials creation request
type CreateCredentialsRequest struct {
	ChallengeID string `json:"challengeId"`
	Attestation string `json:"attestation"`
}

// CredentialsResponse represents vault credentials response
type CredentialsResponse struct {
	ID        string    `json:"id"`
	PublicKey string    `json:"publicKey"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateVaultRequest represents vault creation request
type CreateVaultRequest struct {
	CredentialID string `json:"credentialId"`
	Network      string `json:"network"` // ethereum, polygon, arbitrum, etc.
}

// VaultResponse represents vault response
type VaultResponse struct {
	ID        string    `json:"id"`
	Address   string    `json:"address"`
	Network   string    `json:"network"`
	CreatedAt time.Time `json:"createdAt"`
}

// SignRequest represents transaction signing request
type SignRequest struct {
	VaultID string                 `json:"vaultId"`
	Data    map[string]interface{} `json:"data"`
}

// SignResponse represents transaction signing response
type SignResponse struct {
	Signature string `json:"signature"`
	TxHash    string `json:"txHash,omitempty"`
}

// FXMarketsResponse represents FX markets response
type FXMarketsResponse struct {
	Markets []FXMarket `json:"markets"`
}

// FXMarket represents an FX market pair
type FXMarket struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Rate float64 `json:"rate"`
}

// FXQuoteRequest represents FX quote request
type FXQuoteRequest struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

// FXQuoteResponse represents FX quote response
type FXQuoteResponse struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Rate      float64   `json:"rate"`
	Amount    string    `json:"amount"`
	Total     string    `json:"total"`
	ExpiresAt time.Time `json:"expiresAt"`
}
