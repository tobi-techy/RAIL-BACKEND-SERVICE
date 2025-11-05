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

// CreateAccountRequest represents Due account creation request
type CreateAccountRequest struct {
	Type     AccountType `json:"type"`
	Name     string      `json:"name"`
	Email    string      `json:"email"`
	Country  string      `json:"country"`
	Category string      `json:"category,omitempty"`
}

// CreateAccountResponse represents Due account creation response
type CreateAccountResponse struct {
	ID     string         `json:"id"`
	Type   AccountType    `json:"type"`
	Name   string         `json:"name"`
	Email  string         `json:"email"`
	Country string        `json:"country"`
	Status string         `json:"status"`
	KYC    KYCInfo        `json:"kyc"`
	TOS    TOSInfo        `json:"tos"`
}

// KYCInfo represents KYC information
type KYCInfo struct {
	Status KYCStatus `json:"status"`
	Link   string    `json:"link"`
}

// TOSInfo represents Terms of Service information
type TOSInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Link   string `json:"link"`
}

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
	ID         string                 `json:"id"`
	Country    string                 `json:"country"`
	Name       string                 `json:"name"`
	Email      string                 `json:"email"`
	Details    map[string]interface{} `json:"details"`
	IsExternal bool                   `json:"isExternal,omitempty"`
	IsActive   bool                   `json:"isActive,omitempty"`
}

// CreateRecipientResponse represents recipient creation response
type CreateRecipientResponse struct {
	ID         string                 `json:"id"`
	Country    string                 `json:"country"`
	Name       string                 `json:"name"`
	Email      string                 `json:"email"`
	Details    map[string]interface{} `json:"details"`
	IsExternal bool                   `json:"isExternal"`
	IsActive   bool                   `json:"isActive"`
	CreatedAt  time.Time              `json:"createdAt"`
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
	CurrencyIn string
	RailOut    string
	Limit      int
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

// ErrorResponse represents Due API error response
type ErrorResponse struct {
	StatusCode int                    `json:"statusCode"`
	Message    string                 `json:"message"`
	Code       string                 `json:"code"`
	Details    map[string]interface{} `json:"details,omitempty"`
}

// Error implements error interface
func (e *ErrorResponse) Error() string {
	if len(e.Details) > 0 {
		return fmt.Sprintf("Due API error [%d]: %s (code: %s, details: %v)", e.StatusCode, e.Message, e.Code, e.Details)
	}
	return fmt.Sprintf("Due API error [%d]: %s (code: %s)", e.StatusCode, e.Message, e.Code)
}
