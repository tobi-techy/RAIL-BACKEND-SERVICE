package due

import (
	"time"
)

// CreateAccountRequest represents the request payload for creating a Due account
type CreateAccountRequest struct {
	Type     string `json:"type" validate:"required,oneof=business individual"`
	Name     string `json:"name" validate:"required"`
	Email    string `json:"email" validate:"required,email"`
	Country  string `json:"country" validate:"required,len=2"`
	Category string `json:"category,omitempty"`
}

// Account represents a Due account
type Account struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Name      string      `json:"name"`
	Email     string      `json:"email"`
	Country   string      `json:"country"`
	Category  string      `json:"category,omitempty"`
	Status    string      `json:"status"`
	StatusLog []StatusLog `json:"statusLog"`
	KYC       KYCStatus   `json:"kyc"`
	TOS       TOSStatus   `json:"tos"`
}

// StatusLog represents an account status change
type StatusLog struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// KYCStatus represents the KYC verification status
type KYCStatus struct {
	Status string `json:"status"` // pending, passed, resubmission_required, failed
	Link   string `json:"link,omitempty"`
}

// TOSStatus represents the Terms of Service acceptance status
type TOSStatus struct {
	ID            string            `json:"id"`
	EntityName    string            `json:"entityName"`
	Status        string            `json:"status"`
	Link          string            `json:"link"`
	DocumentLinks map[string]string `json:"documentLinks,omitempty"`
	AcceptedAt    *time.Time        `json:"acceptedAt,omitempty"`
}

// LinkWalletRequest represents the request payload for linking a wallet
type LinkWalletRequest struct {
	Address string `json:"address" validate:"required"` // Format: "evm:0x..." or "starknet:0x..."
}

// Wallet represents a linked wallet
type Wallet struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Address   string    `json:"address"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateVirtualAccountRequest represents the request payload for creating a virtual account
type CreateVirtualAccountRequest struct {
Destination string `json:"destination" validate:"required"` // Crypto address or recipient ID for settlement
SchemaIn    string `json:"schemaIn" validate:"required"`    // Input payment method (bank_sepa, bank_us, evm, tron)
CurrencyIn  string `json:"currencyIn" validate:"required"`  // Input currency (EUR, USD, USDC, USDT)
 RailOut     string `json:"railOut" validate:"required"`     // Settlement rail (ethereum, polygon, sepa, ach)
 	CurrencyOut string `json:"currencyOut" validate:"required"` // Output currency (USDC, EURC, EUR, USD)
 	Reference   string `json:"reference" validate:"required"`   // Your unique reference for tracking
 }

// VirtualAccountDetails represents the receiving account details
type VirtualAccountDetails struct {
IBAN            string `json:"IBAN,omitempty"`
BankName        string `json:"bankName,omitempty"`
BeneficiaryName string `json:"beneficiaryName,omitempty"`
// Add other fields as needed based on payment method
}

// CreateVirtualAccountResponse represents the response from creating a virtual account
type CreateVirtualAccountResponse struct {
OwnerID      string                 `json:"ownerId"`
DestinationID string                 `json:"destinationId"`
SchemaIn     string                 `json:"schemaIn"`
CurrencyIn   string                 `json:"currencyIn"`
RailOut      string                 `json:"railOut"`
CurrencyOut  string                 `json:"currencyOut"`
Nonce        string                 `json:"nonce"`        // Unique identifier/reference
Details      VirtualAccountDetails `json:"details"`      // Receiving account details
IsActive     bool                   `json:"isActive"`
CreatedAt    time.Time              `json:"createdAt"`
}

 // ListVirtualAccountsResponse represents the response from listing virtual accounts
 type ListVirtualAccountsResponse struct {
 	VirtualAccounts []VirtualAccountSummary `json:"virtual_accounts"`
 	Total           int                      `json:"total"`
 	Page            int                      `json:"page"`
 	Limit           int                      `json:"limit"`
 }

 // VirtualAccountSummary represents a summary of a virtual account in list responses
 type VirtualAccountSummary struct {
 	OwnerID      string    `json:"ownerId"`
 	DestinationID string    `json:"destinationId"`
 	SchemaIn     string    `json:"schemaIn"`
 	CurrencyIn   string    `json:"currencyIn"`
 	RailOut      string    `json:"railOut"`
 	CurrencyOut  string    `json:"currencyOut"`
 	Nonce        string    `json:"nonce"`
 	IsActive     bool      `json:"isActive"`
 	CreatedAt    time.Time `json:"createdAt"`
 }

 // GetVirtualAccountResponse represents the response from getting a specific virtual account
 type GetVirtualAccountResponse struct {
 	OwnerID      string                 `json:"ownerId"`
 	DestinationID string                 `json:"destinationId"`
 	SchemaIn     string                 `json:"schemaIn"`
 	CurrencyIn   string                 `json:"currencyIn"`
 	RailOut      string                 `json:"railOut"`
 	CurrencyOut  string                 `json:"currencyOut"`
 	Nonce        string                 `json:"nonce"`
 	Details      VirtualAccountDetails `json:"details"`
 	IsActive     bool                   `json:"isActive"`
 	CreatedAt    time.Time              `json:"createdAt"`
 }

// DueErrorResponse represents an error response from the Due API
type DueErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// CreateTransferRequest represents the request payload for creating a transfer
type CreateTransferRequest struct {
	Quote     string `json:"quote" validate:"required"`     // Quote token from quote endpoint
	Sender    string `json:"sender" validate:"required"`    // Wallet ID or address
	Recipient string `json:"recipient" validate:"required"` // Recipient ID for fiat payouts
	Memo      string `json:"memo,omitempty"`                // Optional memo for tracking
}

// CreateQuoteRequest represents the request payload for creating a transfer quote
type CreateQuoteRequest struct {
	Source      QuoteSide `json:"source" validate:"required"`
	Destination QuoteSide `json:"destination" validate:"required"`
}

// QuoteSide represents one side of a quote (source or destination)
type QuoteSide struct {
	Rail     string `json:"rail" validate:"required"`     // ethereum, ach, sepa, etc.
	Currency string `json:"currency" validate:"required"` // USDC, USD, EUR, etc.
	Amount   string `json:"amount,omitempty"`             // Amount (required for source, optional for destination)
}

// CreateQuoteResponse represents the response from creating a transfer quote
type CreateQuoteResponse struct {
	Token       string    `json:"token"`
	Source      QuoteLeg  `json:"source"`
	Destination QuoteLeg  `json:"destination"`
	FXRate      float64   `json:"fxRate"`
	FXMarkup    float64   `json:"fxMarkup"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// QuoteLeg represents one leg of a quote
type QuoteLeg struct {
	Rail     string `json:"rail"`
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
	Fee      string `json:"fee"`
}

// CreateTransferResponse represents the response from creating a transfer
type CreateTransferResponse struct {
	ID                   string                 `json:"id"`
	OwnerID              string                 `json:"ownerId"`
	Status               string                 `json:"status"`
	Source               TransferLeg            `json:"source"`
	Destination          TransferLeg            `json:"destination"`
	FXRate               float64                `json:"fxRate"`
	FXMarkup             float64                `json:"fxMarkup"`
	TransferInstructions TransferInstructions   `json:"transferInstructions,omitempty"`
	CreatedAt            time.Time              `json:"createdAt"`
	ExpiresAt            time.Time              `json:"expiresAt"`
}

// TransferLeg represents one leg of a transfer
type TransferLeg struct {
	Amount   string `json:"amount"`
	Fee      string `json:"fee"`
	Currency string `json:"currency"`
	Rail     string `json:"rail"`
	ID       string `json:"id,omitempty"`
	Label    string `json:"label,omitempty"`
	Details  map[string]interface{} `json:"details,omitempty"`
}

// TransferInstructions contains instructions for completing the transfer
type TransferInstructions struct {
	Type string `json:"type"` // "TransferIntent" for crypto transfers
}

// CreateTransferIntentRequest represents the request to create a transfer intent
type CreateTransferIntentRequest struct {
	TransferID string `json:"-"` // Not in JSON, used for URL path
}

// CreateTransferIntentResponse represents the response from creating a transfer intent
type CreateTransferIntentResponse struct {
	Token       string        `json:"token"`
	ID          string        `json:"id"`
	Sender      string        `json:"sender"`
	AmountIn    string        `json:"amountIn"`
	To          map[string]string `json:"to"`
	TokenIn     string        `json:"tokenIn"`
	TokenOut    string        `json:"tokenOut"`
	NetworkIDIn string        `json:"networkIdIn"`
	NetworkIDOut string       `json:"networkIdOut"`
	GasFee      string        `json:"gasFee"`
	Signables   []SignableTxn `json:"signables"`
	Nonce       string        `json:"nonce"`
	Hash        string        `json:"hash"`
	Reference   string        `json:"reference"`
	ExpiresAt   time.Time     `json:"expiresAt"`
	CreatedAt   time.Time     `json:"createdAt"`
}

// SignableTxn represents a transaction that needs to be signed
type SignableTxn struct {
	Hash    string                 `json:"hash"`
	Type    string                 `json:"type"` // "EIP712", etc.
	Data    map[string]interface{} `json:"data"`
	Signature string               `json:"signature,omitempty"` // Added after signing
}

// SubmitTransferIntentRequest represents the request to submit a signed transfer intent
type SubmitTransferIntentRequest struct {
	Token      string        `json:"token"`
	ID         string        `json:"id"`
	Sender     string        `json:"sender"`
	AmountIn   string        `json:"amountIn"`
	To         map[string]string `json:"to"`
	TokenIn    string        `json:"tokenIn"`
	TokenOut   string        `json:"tokenOut"`
	NetworkIDIn string       `json:"networkIdIn"`
	NetworkIDOut string      `json:"networkIdOut"`
	GasFee     string        `json:"gasFee"`
	Signables  []SignableTxn `json:"signables"`
	Nonce      string        `json:"nonce"`
	Hash       string        `json:"hash"`
	Reference  string        `json:"reference"`
	ExpiresAt  time.Time     `json:"expiresAt"`
	CreatedAt  time.Time     `json:"createdAt"`
}

// CreateFundingAddressRequest represents the request to create a funding address
type CreateFundingAddressRequest struct {
	TransferID string `json:"-"` // Not in JSON, used for URL path
}

// CreateFundingAddressResponse represents the response from creating a funding address
type CreateFundingAddressResponse struct {
	Details FundingAddressDetails `json:"details"`
}

// FundingAddressDetails contains the funding address information
type FundingAddressDetails struct {
	Address string `json:"address"`
	Schema  string `json:"schema"` // "evm", etc.
}

// GetTransferResponse represents the response from getting transfer details
type GetTransferResponse struct {
	ID                   string                 `json:"id"`
	OwnerID              string                 `json:"ownerId"`
	Status               string                 `json:"status"`
	Source               TransferLeg            `json:"source"`
	Destination          TransferLeg            `json:"destination"`
	FXRate               float64                `json:"fxRate"`
	FXMarkup             float64                `json:"fxMarkup"`
	TransferInstructions TransferInstructions   `json:"transferInstructions,omitempty"`
	CreatedAt            time.Time              `json:"createdAt"`
	ExpiresAt            time.Time              `json:"expiresAt"`
	CompletedAt          *time.Time             `json:"completedAt,omitempty"`
	FailedAt             *time.Time             `json:"failedAt,omitempty"`
}

func (e *DueErrorResponse) Error() string {
	return e.Message
}
