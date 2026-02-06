package bridge

import "time"

// CustomerType represents the type of customer
type CustomerType string

const (
	CustomerTypeIndividual CustomerType = "individual"
	CustomerTypeBusiness   CustomerType = "business"
)

// CustomerStatus represents the status of a customer
type CustomerStatus string

const (
	CustomerStatusActive               CustomerStatus = "active"
	CustomerStatusAwaitingQuestionnaire CustomerStatus = "awaiting_questionnaire"
	CustomerStatusAwaitingUBO          CustomerStatus = "awaiting_ubo"
	CustomerStatusIncomplete           CustomerStatus = "incomplete"
	CustomerStatusNotStarted           CustomerStatus = "not_started"
	CustomerStatusOffboarded           CustomerStatus = "offboarded"
	CustomerStatusPaused               CustomerStatus = "paused"
	CustomerStatusRejected             CustomerStatus = "rejected"
	CustomerStatusUnderReview          CustomerStatus = "under_review"
)

// CapabilityStatus represents the status of a capability
type CapabilityStatus string

const (
	CapabilityStatusPending  CapabilityStatus = "pending"
	CapabilityStatusActive   CapabilityStatus = "active"
	CapabilityStatusInactive CapabilityStatus = "inactive"
	CapabilityStatusRejected CapabilityStatus = "rejected"
)

// EndorsementStatus represents the status of an endorsement
type EndorsementStatus string

const (
	EndorsementStatusIncomplete EndorsementStatus = "incomplete"
	EndorsementStatusApproved   EndorsementStatus = "approved"
	EndorsementStatusRevoked    EndorsementStatus = "revoked"
)

// VirtualAccountStatus represents the status of a virtual account
type VirtualAccountStatus string

const (
	VirtualAccountStatusActivated   VirtualAccountStatus = "activated"
	VirtualAccountStatusDeactivated VirtualAccountStatus = "deactivated"
)

// CardAccountStatus represents the status of a card account
type CardAccountStatus string

const (
	CardAccountStatusActive    CardAccountStatus = "active"
	CardAccountStatusFrozen    CardAccountStatus = "frozen"
	CardAccountStatusCancelled CardAccountStatus = "cancelled"
)

// PaymentRail represents supported blockchain networks
type PaymentRail string

const (
	PaymentRailArbitrum       PaymentRail = "arbitrum"
	PaymentRailAvalanche      PaymentRail = "avalanche_c_chain"
	PaymentRailBase           PaymentRail = "base"
	PaymentRailEthereum       PaymentRail = "ethereum"
	PaymentRailOptimism       PaymentRail = "optimism"
	PaymentRailPolygon        PaymentRail = "polygon"
	PaymentRailSolana         PaymentRail = "solana"
	PaymentRailStellar        PaymentRail = "stellar"
	PaymentRailTron           PaymentRail = "tron"
)

// Currency represents supported currencies
type Currency string

const (
	CurrencyUSD  Currency = "usd"
	CurrencyEUR  Currency = "eur"
	CurrencyMXN  Currency = "mxn"
	CurrencyUSDB Currency = "usdb"
	CurrencyUSDC Currency = "usdc"
	CurrencyUSDT Currency = "usdt"
	CurrencyDAI  Currency = "dai"
	CurrencyPYUSD Currency = "pyusd"
	CurrencyEURC Currency = "eurc"
)

// Address represents a physical address
type Address struct {
	StreetLine1 string `json:"street_line_1"`
	StreetLine2 string `json:"street_line_2,omitempty"`
	City        string `json:"city"`
	Subdivision string `json:"subdivision,omitempty"`
	PostalCode  string `json:"postal_code,omitempty"`
	Country     string `json:"country"`
}

// IdentifyingInfo represents identification information
type IdentifyingInfo struct {
	Type           string `json:"type"`
	IssuingCountry string `json:"issuing_country"`
	Number         string `json:"number,omitempty"`
	Description    string `json:"description,omitempty"`
	Expiration     string `json:"expiration,omitempty"`
	ImageFront     string `json:"image_front,omitempty"`
	ImageBack      string `json:"image_back,omitempty"`
}

// CreateCustomerRequest represents a request to create a customer
type CreateCustomerRequest struct {
	Type                   CustomerType      `json:"type"`
	FirstName              string            `json:"first_name,omitempty"`
	MiddleName             string            `json:"middle_name,omitempty"`
	LastName               string            `json:"last_name,omitempty"`
	Email                  string            `json:"email,omitempty"`
	Phone                  string            `json:"phone,omitempty"`
	ResidentialAddress     *Address          `json:"residential_address,omitempty"`
	BirthDate              string            `json:"birth_date,omitempty"`
	SignedAgreementID      string            `json:"signed_agreement_id,omitempty"`
	Endorsements           []string          `json:"endorsements,omitempty"`
	IdentifyingInformation []IdentifyingInfo `json:"identifying_information,omitempty"`
}

// Capabilities represents customer capabilities
type Capabilities struct {
	PayinCrypto  CapabilityStatus `json:"payin_crypto"`
	PayoutCrypto CapabilityStatus `json:"payout_crypto"`
	PayinFiat    CapabilityStatus `json:"payin_fiat"`
	PayoutFiat   CapabilityStatus `json:"payout_fiat"`
}

// EndorsementRequirements represents endorsement requirements
type EndorsementRequirements struct {
	Complete []string               `json:"complete"`
	Pending  []string               `json:"pending"`
	Missing  map[string]interface{} `json:"missing"`
	Issues   []interface{}          `json:"issues"`
}

// Endorsement represents a customer endorsement
type Endorsement struct {
	Name         string                  `json:"name"`
	Status       EndorsementStatus       `json:"status"`
	Requirements EndorsementRequirements `json:"requirements"`
}

// RejectionReason represents a rejection reason
type RejectionReason struct {
	DeveloperReason string `json:"developer_reason"`
	Reason          string `json:"reason"`
	CreatedAt       string `json:"created_at"`
}

// Customer represents a Bridge customer
type Customer struct {
	ID                        string            `json:"id"`
	FirstName                 string            `json:"first_name"`
	MiddleName                string            `json:"middle_name,omitempty"`
	LastName                  string            `json:"last_name"`
	Email                     string            `json:"email"`
	Phone                     string            `json:"phone,omitempty"`
	Status                    CustomerStatus    `json:"status"`
	Type                      CustomerType      `json:"type"`
	Capabilities              Capabilities      `json:"capabilities"`
	Endorsements              []Endorsement     `json:"endorsements"`
	RejectionReasons          []RejectionReason `json:"rejection_reasons"`
	HasAcceptedTermsOfService bool              `json:"has_accepted_terms_of_service"`
	RequirementsDue           []string          `json:"requirements_due"`
	FutureRequirementsDue     []string          `json:"future_requirements_due"`
	CreatedAt                 time.Time         `json:"created_at"`
	UpdatedAt                 time.Time         `json:"updated_at"`
}

// KYCLinkResponse represents a KYC link response
type KYCLinkResponse struct {
	KYCLink   string `json:"url"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// TOSLinkResponse represents a Terms of Service link response
type TOSLinkResponse struct {
	TOSLink   string `json:"tos_link"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// VirtualAccountSource represents the source configuration
type VirtualAccountSource struct {
	Currency Currency `json:"currency"`
}

// VirtualAccountDestination represents the destination configuration
type VirtualAccountDestination struct {
	Currency       Currency    `json:"currency"`
	PaymentRail    PaymentRail `json:"payment_rail"`
	Address        string      `json:"address,omitempty"`
	BlockchainMemo string      `json:"blockchain_memo,omitempty"`
	BridgeWalletID string      `json:"bridge_wallet_id,omitempty"`
}

// CreateVirtualAccountRequest represents a request to create a virtual account
type CreateVirtualAccountRequest struct {
	Source              VirtualAccountSource      `json:"source"`
	Destination         VirtualAccountDestination `json:"destination"`
	DeveloperFeePercent string                    `json:"developer_fee_percent,omitempty"`
}

// SourceDepositInstructions represents deposit instructions
type SourceDepositInstructions struct {
	Currency               Currency `json:"currency"`
	PaymentRails           []string `json:"payment_rails"`
	BankName               string   `json:"bank_name,omitempty"`
	BankAddress            string   `json:"bank_address,omitempty"`
	BankRoutingNumber      string   `json:"bank_routing_number,omitempty"`
	BankAccountNumber      string   `json:"bank_account_number,omitempty"`
	BankBeneficiaryName    string   `json:"bank_beneficiary_name,omitempty"`
	BankBeneficiaryAddress string   `json:"bank_beneficiary_address,omitempty"`
	AccountHolderName      string   `json:"account_holder_name,omitempty"`
	IBAN                   string   `json:"iban,omitempty"`
	BIC                    string   `json:"bic,omitempty"`
	CLABE                  string   `json:"clabe,omitempty"`
}

// VirtualAccount represents a Bridge virtual account
type VirtualAccount struct {
	ID                        string                    `json:"id"`
	Status                    VirtualAccountStatus      `json:"status"`
	CustomerID                string                    `json:"customer_id"`
	DeveloperFeePercent       string                    `json:"developer_fee_percent,omitempty"`
	SourceDepositInstructions SourceDepositInstructions `json:"source_deposit_instructions"`
	Destination               VirtualAccountDestination `json:"destination"`
	CreatedAt                 time.Time                 `json:"created_at"`
}

// WalletType represents the type of wallet
type WalletType string

const (
	WalletTypeUser     WalletType = "user"
	WalletTypeTreasury WalletType = "treasury"
)

// CreateWalletRequest represents a request to create a wallet
type CreateWalletRequest struct {
	Chain      PaymentRail `json:"chain"`
	Currency   Currency    `json:"currency"`
	WalletType WalletType  `json:"wallet_type,omitempty"`
}

// Wallet represents a Bridge custodial wallet
type Wallet struct {
	ID         string      `json:"id"`
	CustomerID string      `json:"customer_id,omitempty"`
	Chain      PaymentRail `json:"chain"`
	Currency   Currency    `json:"currency"`
	Address    string      `json:"address"`
	WalletType WalletType  `json:"wallet_type"`
	Status     string      `json:"status"`
	CreatedAt  time.Time   `json:"created_at"`
}

// WalletBalance represents a wallet balance
type WalletBalance struct {
	Currency Currency `json:"currency"`
	Amount   string   `json:"amount"`
}

// CryptoAccount represents a crypto account for cards
type CryptoAccount struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

// CreateCardAccountRequest represents a request to create a card account
type CreateCardAccountRequest struct {
	ClientReferenceID string        `json:"client_reference_id,omitempty"`
	Currency          Currency      `json:"currency"`
	Chain             PaymentRail   `json:"chain"`
	CryptoAccount     CryptoAccount `json:"crypto_account"`
}

// CardDetails represents card details
type CardDetails struct {
	Last4  string `json:"last_4"`
	Expiry string `json:"expiry"`
	BIN    string `json:"bin"`
}

// CardAccount represents a Bridge card account
type CardAccount struct {
	ID           string            `json:"id"`
	CustomerID   string            `json:"customer_id"`
	Status       CardAccountStatus `json:"status"`
	CardImageURL string            `json:"card_image_url,omitempty"`
	CardDetails  CardDetails       `json:"card_details"`
	Currency     Currency          `json:"currency"`
	Chain        PaymentRail       `json:"chain"`
	CreatedAt    time.Time         `json:"created_at"`
}

// TransferSource represents the source of a transfer
type TransferSource struct {
	PaymentRail PaymentRail `json:"payment_rail"`
	Currency    Currency    `json:"currency"`
	FromAddress string      `json:"from_address,omitempty"`
	WalletID    string      `json:"wallet_id,omitempty"`
}

// TransferDestination represents the destination of a transfer
type TransferDestination struct {
	PaymentRail PaymentRail `json:"payment_rail"`
	Currency    Currency    `json:"currency"`
	ToAddress   string      `json:"to_address,omitempty"`
	WalletID    string      `json:"wallet_id,omitempty"`
}

// CreateTransferRequest represents a request to create a transfer
type CreateTransferRequest struct {
	Amount      string              `json:"amount"`
	Source      TransferSource      `json:"source"`
	Destination TransferDestination `json:"destination"`
}

// TransferStatus represents the status of a transfer
type TransferStatus string

const (
	TransferStatusPending   TransferStatus = "pending"
	TransferStatusCompleted TransferStatus = "completed"
	TransferStatusFailed    TransferStatus = "failed"
)

// Transfer represents a Bridge transfer
type Transfer struct {
	ID          string              `json:"id"`
	CustomerID  string              `json:"customer_id"`
	Status      TransferStatus      `json:"status"`
	Amount      string              `json:"amount"`
	Source      TransferSource      `json:"source"`
	Destination TransferDestination `json:"destination"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// WebhookEvent represents a Bridge webhook event
type WebhookEvent struct {
	APIVersion         string                 `json:"api_version"`
	EventID            string                 `json:"event_id"`
	EventCategory      string                 `json:"event_category"`
	EventType          string                 `json:"event_type"`
	EventObjectID      string                 `json:"event_object_id"`
	EventObjectStatus  string                 `json:"event_object_status"`
	EventObject        map[string]interface{} `json:"event_object"`
	EventObjectChanges map[string]interface{} `json:"event_object_changes"`
	EventCreatedAt     time.Time              `json:"event_created_at"`
}

// PaginatedResponse represents a paginated API response
type PaginatedResponse[T any] struct {
	Data    []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	Cursor  string `json:"cursor,omitempty"`
}

// ListCustomersResponse represents a paginated list of customers
type ListCustomersResponse = PaginatedResponse[Customer]

// ListVirtualAccountsResponse represents a paginated list of virtual accounts
type ListVirtualAccountsResponse = PaginatedResponse[VirtualAccount]

// ListWalletsResponse represents a paginated list of wallets
type ListWalletsResponse = PaginatedResponse[Wallet]

// ListTransfersResponse represents a paginated list of transfers
type ListTransfersResponse = PaginatedResponse[Transfer]
