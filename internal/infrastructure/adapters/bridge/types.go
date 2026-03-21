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
	CustomerStatusActive                CustomerStatus = "active"
	CustomerStatusAwaitingQuestionnaire CustomerStatus = "awaiting_questionnaire"
	CustomerStatusAwaitingUBO           CustomerStatus = "awaiting_ubo"
	CustomerStatusIncomplete            CustomerStatus = "incomplete"
	CustomerStatusNotStarted            CustomerStatus = "not_started"
	CustomerStatusOffboarded            CustomerStatus = "offboarded"
	CustomerStatusPaused                CustomerStatus = "paused"
	CustomerStatusRejected              CustomerStatus = "rejected"
	CustomerStatusUnderReview           CustomerStatus = "under_review"
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
	PaymentRailArbitrum  PaymentRail = "arbitrum"
	PaymentRailAvalanche PaymentRail = "avalanche_c_chain"
	PaymentRailBase      PaymentRail = "base"
	PaymentRailEthereum  PaymentRail = "ethereum"
	PaymentRailOptimism  PaymentRail = "optimism"
	PaymentRailPolygon   PaymentRail = "polygon"
	PaymentRailSolana    PaymentRail = "solana"
	PaymentRailStellar   PaymentRail = "stellar"
	PaymentRailTron      PaymentRail = "tron"
)

// Currency represents supported currencies
type Currency string

const (
	CurrencyUSD   Currency = "usd"
	CurrencyEUR   Currency = "eur"
	CurrencyGBP   Currency = "gbp"
	CurrencyMXN   Currency = "mxn"
	CurrencyBRL   Currency = "brl"
	CurrencyUSDB  Currency = "usdb"
	CurrencyUSDC  Currency = "usdc"
	CurrencyUSDT  Currency = "usdt"
	CurrencyDAI   Currency = "dai"
	CurrencyPYUSD Currency = "pyusd"
	CurrencyEURC  Currency = "eurc"
)

// Card funding strategies
const (
	CardFundingStrategyTopUp = "top_up"
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
	SourceOfFunds          string            `json:"source_of_funds,omitempty"`
}

// UpdateCustomerRequest represents a request to update a customer with KYC data
type UpdateCustomerRequest struct {
	IdentifyingInformation     []IdentifyingInfo `json:"identifying_information,omitempty"`
	SourceOfFunds              string            `json:"source_of_funds,omitempty"`
	EmploymentStatus           string            `json:"employment_status,omitempty"`
	ExpectedMonthlyPaymentsUSD string            `json:"expected_monthly_payments_usd,omitempty"`
	AccountPurpose             string            `json:"account_purpose,omitempty"`
	AccountPurposeOther        string            `json:"account_purpose_other,omitempty"`
	MostRecentOccupation       string            `json:"most_recent_occupation,omitempty"`
	ActingAsIntermediary       *bool             `json:"acting_as_intermediary,omitempty"`
	VerifiedGovidAt            string            `json:"verified_govid_at,omitempty"`
	VerifiedSelfieAt           string            `json:"verified_selfie_at,omitempty"`
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
	URL       string `json:"url"`
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
	// GBP (Faster Payments)
	SortCode      string `json:"sort_code,omitempty"`
	AccountNumber string `json:"account_number,omitempty"`
	// BRL (PIX)
	BRCode string `json:"br_code,omitempty"`
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

// WalletBalanceEntry represents a single balance entry within a wallet
type WalletBalanceEntry struct {
	Balance         string      `json:"balance"`
	Currency        Currency    `json:"currency"`
	Chain           PaymentRail `json:"chain"`
	ContractAddress string      `json:"contract_address,omitempty"`
}

// WalletBalance represents the balance response for a Bridge wallet
type WalletBalance struct {
	ID       string               `json:"id"`
	Chain    PaymentRail          `json:"chain"`
	Address  string               `json:"address"`
	Balances []WalletBalanceEntry `json:"balances"`
}

// GetUSDCAmount returns the USDC balance string, or "0" if not found
func (wb *WalletBalance) GetUSDCAmount() string {
	for _, b := range wb.Balances {
		if b.Currency == CurrencyUSDC || b.Currency == CurrencyUSDB {
			return b.Balance
		}
	}
	return "0"
}

// CryptoAccount represents a crypto account for cards
type CryptoAccount struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

const (
	CryptoAccountTypeStandard     = "standard"
	CryptoAccountTypeBridgeWallet = "bridge_wallet"
)

// CreateCardAccountRequest represents a request to create a card account
type CreateCardAccountRequest struct {
	ClientReferenceID string         `json:"client_reference_id,omitempty"`
	Currency          Currency       `json:"currency"`
	Chain             PaymentRail    `json:"chain"`
	CryptoAccount     *CryptoAccount `json:"crypto_account,omitempty"`
}

// EnableCardsRequest enables Bridge cards for a developer account.
// Sandbox must be initialized with funding strategy top_up.
type EnableCardsRequest struct {
	FundingStrategy string `json:"funding_strategy"`
}

const (
	CardActionInitiatorCustomer   = "customer"
	CardActionInitiatorDeveloper  = "developer"
	CardFreezeReasonPlannedInactivity = "planned_inactivity"
	CardFreezeReasonLostOrStolen  = "lost_or_stolen"
	CardFreezeReasonFraud         = "fraud"
	CardFreezeReasonMerchantAbuse = "merchant_abuse"
)

// FreezeCardAccountRequest represents a freeze card request payload.
type FreezeCardAccountRequest struct {
	Initiator string `json:"initiator"`
	Reason    string `json:"reason"`
}

// UnfreezeCardAccountRequest represents an unfreeze card request payload.
type UnfreezeCardAccountRequest struct {
	Initiator string `json:"initiator"`
}

// CardDetails represents card details
type CardDetails struct {
	Last4  string `json:"last_4"`
	Expiry string `json:"expiry"`
	BIN    string `json:"bin"`
}

// CardBalanceEntry represents a single balance entry (hold or available)
type CardBalanceEntry struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// CardBalances represents the hold and available balances on a card account
type CardBalances struct {
	Hold      CardBalanceEntry `json:"hold"`
	Available CardBalanceEntry `json:"available"`
}

// CardFundingInstructions represents the on-chain address to fund a card account
type CardFundingInstructions struct {
	Chain    string `json:"chain"`
	Address  string `json:"address"`
	Currency string `json:"currency"`
}

// CardFreezeEntry represents a single freeze record on a card account
type CardFreezeEntry struct {
	Initiator    string     `json:"initiator"`
	Reason       string     `json:"reason"`
	ReasonDetail string     `json:"reason_detail,omitempty"`
	StartingAt   *time.Time `json:"starting_at,omitempty"`
	EndingAt     *time.Time `json:"ending_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// CardAccount represents a Bridge card account
type CardAccount struct {
	ID                  string                  `json:"id"`
	CustomerID          string                  `json:"customer_id"`
	Status              CardAccountStatus       `json:"status"`
	CardImageURL        string                  `json:"card_image_url,omitempty"`
	CardDetails         CardDetails             `json:"card_details"`
	Currency            Currency                `json:"currency"`
	Chain               PaymentRail             `json:"chain"`
	Balances            CardBalances            `json:"balances"`
	FundingInstructions CardFundingInstructions `json:"funding_instructions"`
	Freezes             []CardFreezeEntry       `json:"freezes"`
	CreatedAt           time.Time               `json:"created_at"`
}

// EphemeralKeyRequest is the request body for creating a card ephemeral key
type EphemeralKeyRequest struct {
	ClientNonce string `json:"client_nonce"`
}

// EphemeralKeyResponse is the response from creating a card ephemeral key
type EphemeralKeyResponse struct {
	EphemeralKey string `json:"ephemeral_key"`
}

// CardPINUpdateURLResponse is the response from creating a card PIN update URL
type CardPINUpdateURLResponse struct {
	URL string `json:"url"`
}

// RealTimeAuthMerchant represents merchant data in a real-time auth request
type RealTimeAuthMerchant struct {
	Description  string `json:"description"`
	PostalCode   string `json:"postal_code"`
	State        string `json:"state"`
	Country      string `json:"country"`
	Category     string `json:"category"`
	CategoryCode string `json:"category_code"`
}

// RealTimeAuthLocalDetails represents local transaction details in a real-time auth request
type RealTimeAuthLocalDetails struct {
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
	ExchangeRate string `json:"exchange_rate"`
}

// RealTimeAuthVerification represents verification data in a real-time auth request
type RealTimeAuthVerification struct {
	CVVCheck                string `json:"cvv_check"`
	AddressCheck            string `json:"address_check"`
	AddressPostalCodeCheck  string `json:"address_postal_code_check"`
	PINCheck                string `json:"pin_check"`
	ThreeDSecureCheck       string `json:"three_d_secure_check"`
}

// RealTimeAuthData is the data object inside a real-time auth webhook
type RealTimeAuthData struct {
	AuthorizationID         string                   `json:"authorization_id"`
	AuthType                string                   `json:"auth_type"` // "auth" or "incremental_auth"
	PartialSupported        bool                     `json:"partial_supported"`
	Network                 string                   `json:"network"`
	International           bool                     `json:"international"`
	OriginalAuthorizationID *string                  `json:"original_authorization_id"`
	TransactionID           string                   `json:"transaction_id"`
	Account                 struct{ Last4 string `json:"last_4"` } `json:"account"`
	Currency                string                   `json:"currency"`
	Amount                  string                   `json:"amount"`
	BillingAmount           string                   `json:"billing_amount"`
	CashbackAmount          string                   `json:"cashback_amount"`
	LocalTransactionDetails RealTimeAuthLocalDetails `json:"local_transaction_details"`
	Merchant                RealTimeAuthMerchant     `json:"merchant"`
	EntryMethod             string                   `json:"entry_method"`
	CardPresent             bool                     `json:"card_present"`
	Recurring               bool                     `json:"recurring"`
	VerificationData        RealTimeAuthVerification `json:"verification_data"`
	Wallet                  *string                  `json:"wallet"`
	CardAccountID           string                   `json:"card_account_id"`
	CustomerID              string                   `json:"customer_id"`
	CreatedAt               time.Time                `json:"created_at"`
}

// RealTimeAuthRequest is the full payload Bridge sends to your real-time auth webhook
type RealTimeAuthRequest struct {
	EventID    string           `json:"event_id"`
	APIVersion string           `json:"api_version"`
	Timestamp  time.Time        `json:"timestamp"`
	Data       RealTimeAuthData `json:"data"`
}

// RealTimeAuthResponse is what your endpoint must return to Bridge (within 500ms)
type RealTimeAuthResponse struct {
	Approved       bool   `json:"approved"`
	DecisionReason string `json:"decision_reason,omitempty"`
}

// TransferSource represents the source of a transfer.
// Bridge populates additional fields after funds arrive (source-updates doc).
type TransferSource struct {
	PaymentRail         PaymentRail `json:"payment_rail"`
	Currency            Currency    `json:"currency"`
	BridgeWalletID      string      `json:"bridge_wallet_id,omitempty"`
	FromAddress         string      `json:"from_address,omitempty"`
	ExternalAccountID   string      `json:"external_account_id,omitempty"`
	// Populated by Bridge after funds arrive (wire/ACH/SEPA)
	BankBeneficiaryName string      `json:"bank_beneficiary_name,omitempty"`
	BankRoutingNumber   string      `json:"bank_routing_number,omitempty"`
	BankName            string      `json:"bank_name,omitempty"`
	IMAD                string      `json:"imad,omitempty"`
	Description         string      `json:"description,omitempty"` // ACH
	BIC                 string      `json:"bic,omitempty"`         // SEPA
	UETR                string      `json:"uetr,omitempty"`        // SEPA
	SenderName          string      `json:"sender_name,omitempty"` // SEPA
}

// TransferDestination represents the destination of a transfer.
// Bridge populates IMAD/TraceNumber after payment is processed (destination-updates doc).
type TransferDestination struct {
	PaymentRail       PaymentRail `json:"payment_rail"`
	Currency          Currency    `json:"currency"`
	BridgeWalletID    string      `json:"bridge_wallet_id,omitempty"`
	ToAddress         string      `json:"to_address,omitempty"`
	ExternalAccountID string      `json:"external_account_id,omitempty"`
	// Populated by Bridge after payment processed
	IMAD        string `json:"imad,omitempty"`         // wire
	TraceNumber string `json:"trace_number,omitempty"` // ACH
}

// TransferFeatures represents optional transfer features
type TransferFeatures struct {
	StaticTemplate    bool `json:"static_template,omitempty"`
	FlexibleAmount    bool `json:"flexible_amount,omitempty"`
	AllowAnyFromAddress bool `json:"allow_any_from_address,omitempty"`
}

// TransferReceipt represents the receipt of a completed transfer
type TransferReceipt struct {
	InitialAmount     string `json:"initial_amount"`
	DeveloperFee      string `json:"developer_fee"`
	ExchangeFee       string `json:"exchange_fee"`
	SubtotalAmount    string `json:"subtotal_amount,omitempty"`
	GasFee            string `json:"gas_fee,omitempty"`
	FinalAmount       string `json:"final_amount"`
	DestinationTxHash string `json:"destination_tx_hash,omitempty"`
	URL               string `json:"url,omitempty"`
}

// TransferSourceDepositInstructions represents deposit instructions for a transfer
type TransferSourceDepositInstructions struct {
	PaymentRail       string `json:"payment_rail,omitempty"`
	Currency          string `json:"currency,omitempty"`
	Amount            string `json:"amount,omitempty"`
	DepositMessage    string `json:"deposit_message,omitempty"`
	BankAccountNumber string `json:"bank_account_number,omitempty"`
	BankRoutingNumber string `json:"bank_routing_number,omitempty"`
	FromAddress       string `json:"from_address,omitempty"`
	ToAddress         string `json:"to_address,omitempty"`
	BlockchainMemo    string `json:"blockchain_memo,omitempty"` // Stellar, Tron
	IBAN              string `json:"iban,omitempty"`
	BIC               string `json:"bic,omitempty"`
	AccountHolderName string `json:"account_holder_name,omitempty"`
	BankName          string `json:"bank_name,omitempty"`
	BankAddress       string `json:"bank_address,omitempty"`
}

// CreateTransferRequest represents a request to create a transfer
type CreateTransferRequest struct {
	OnBehalfOf         string              `json:"on_behalf_of"`
	Amount             string              `json:"amount,omitempty"`
	Source             TransferSource      `json:"source"`
	Destination        TransferDestination `json:"destination"`
	DeveloperFee       string              `json:"developer_fee,omitempty"`
	DeveloperFeePercent string             `json:"developer_fee_percent,omitempty"`
	Features           *TransferFeatures   `json:"features,omitempty"`
}

// TransferStatus represents the status of a transfer
type TransferStatus string

const (
	TransferStatusAwaitingFunds       TransferStatus = "awaiting_funds"
	TransferStatusInReview            TransferStatus = "in_review"
	TransferStatusFundsReceived       TransferStatus = "funds_received"
	TransferStatusPaymentSubmitted    TransferStatus = "payment_submitted"
	TransferStatusPaymentProcessed    TransferStatus = "payment_processed"
	TransferStatusUndeliverable       TransferStatus = "undeliverable"
	TransferStatusReturned            TransferStatus = "returned"
	TransferStatusMissingReturnPolicy TransferStatus = "missing_return_policy"
	TransferStatusRefunded            TransferStatus = "refunded"
	TransferStatusCanceled            TransferStatus = "canceled"
	TransferStatusError               TransferStatus = "error"
)

// Transfer represents a Bridge transfer
type Transfer struct {
	ID                       string                            `json:"id"`
	State                    TransferStatus                    `json:"state"`
	OnBehalfOf               string                            `json:"on_behalf_of"`
	Amount                   string                            `json:"amount,omitempty"` // absent for flexible_amount until funds arrive
	DeveloperFee             string                            `json:"developer_fee,omitempty"`
	DeveloperFeePercent      string                            `json:"developer_fee_percent,omitempty"`
	Source                   TransferSource                    `json:"source"`
	Destination              TransferDestination               `json:"destination"`
	SourceDepositInstructions *TransferSourceDepositInstructions `json:"source_deposit_instructions,omitempty"`
	Receipt                  *TransferReceipt                  `json:"receipt,omitempty"`
	Features                 *TransferFeatures                 `json:"features,omitempty"`
	CreatedAt                time.Time                         `json:"created_at"`
	UpdatedAt                time.Time                         `json:"updated_at"`
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

// ExternalAccountAccountType represents the type of bank account
type ExternalAccountAccountType string

const (
	ExternalAccountChecking ExternalAccountAccountType = "checking"
	ExternalAccountSavings  ExternalAccountAccountType = "savings"
)

// ExternalAccountBankDetails holds ACH bank account details
type ExternalAccountBankDetails struct {
	AccountOwnerName string                     `json:"account_owner_name"`
	AccountType      ExternalAccountAccountType `json:"account_type"`
	RoutingNumber    string                     `json:"routing_number"`
	AccountNumber    string                     `json:"account_number"`
}

// CreateExternalAccountRequest represents a request to register a bank account
type CreateExternalAccountRequest struct {
	Currency    Currency                   `json:"currency"`
	BankDetails ExternalAccountBankDetails `json:"bank_details"`
}

// ExternalAccountStatus represents the status of an external account
type ExternalAccountStatus string

const (
	ExternalAccountStatusActive   ExternalAccountStatus = "active"
	ExternalAccountStatusInactive ExternalAccountStatus = "inactive"
)

// ExternalAccount represents a Bridge external bank account
type ExternalAccount struct {
	ID          string                `json:"id"`
	CustomerID  string                `json:"customer_id"`
	Currency    Currency              `json:"currency"`
	Status      ExternalAccountStatus `json:"status"`
	BankDetails ExternalAccountBankDetails `json:"bank_details"`
	CreatedAt   time.Time             `json:"created_at"`
}

// ListExternalAccountsResponse represents a paginated list of external accounts
type ListExternalAccountsResponse = PaginatedResponse[ExternalAccount]

// CreateLiquidationAddressRequest represents a request to create a liquidation address
type CreateLiquidationAddressRequest struct {
	Chain                    PaymentRail `json:"chain"`
	Currency                 Currency    `json:"currency"`
	DestinationPaymentRail   PaymentRail `json:"destination_payment_rail"`
	DestinationCurrency      Currency    `json:"destination_currency"`
	DestinationAddress       string      `json:"destination_address,omitempty"`
	ExternalAccountID        string      `json:"external_account_id,omitempty"`
	DestinationWireMessage   string      `json:"destination_wire_message,omitempty"`
	CustomDeveloperFeePercent string     `json:"custom_developer_fee_percent,omitempty"`
}

// LiquidationAddress represents a Bridge liquidation address
type LiquidationAddress struct {
	ID                     string      `json:"id"`
	CustomerID             string      `json:"customer_id"`
	Chain                  PaymentRail `json:"chain"`
	Currency               Currency    `json:"currency"`
	Address                string      `json:"address"`
	BlockchainMemo         string      `json:"blockchain_memo,omitempty"`
	DestinationPaymentRail PaymentRail `json:"destination_payment_rail,omitempty"`
	DestinationCurrency    Currency    `json:"destination_currency,omitempty"`
	DestinationAddress     string      `json:"destination_address,omitempty"`
	ExternalAccountID      string      `json:"external_account_id,omitempty"`
	CreatedAt              time.Time   `json:"created_at"`
	UpdatedAt              time.Time   `json:"updated_at"`
}

// ListLiquidationAddressesResponse represents a paginated list of liquidation addresses
type ListLiquidationAddressesResponse = PaginatedResponse[LiquidationAddress]

// DrainDestination represents the destination of a liquidation address drain
type DrainDestination struct {
	PaymentRail     PaymentRail `json:"payment_rail"`
	Currency        Currency    `json:"currency"`
	ToAddress       string      `json:"to_address,omitempty"`
	ExternalAccountID string    `json:"external_account_id,omitempty"`
	IMAD            string      `json:"imad,omitempty"`
	TraceNumber     string      `json:"trace_number,omitempty"`
}

// DrainReceipt represents the receipt of a drain
type DrainReceipt struct {
	InitialAmount     string `json:"initial_amount"`
	DeveloperFee      string `json:"developer_fee"`
	SubtotalAmount    string `json:"subtotal_amount"`
	ExchangeRate      string `json:"exchange_rate,omitempty"`
	ConvertedAmount   string `json:"converted_amount,omitempty"`
	OutgoingAmount    string `json:"outgoing_amount,omitempty"`
	DestinationCurrency string `json:"destination_currency,omitempty"`
	URL               string `json:"url,omitempty"`
}

// Drain represents a single drain record from a liquidation address
type Drain struct {
	ID                   string           `json:"id"`
	Amount               string           `json:"amount"`
	Currency             Currency         `json:"currency"`
	State                string           `json:"state"`
	CustomerID           string           `json:"customer_id"`
	LiquidationAddressID string           `json:"liquidation_address_id"`
	DepositTxHash        string           `json:"deposit_tx_hash,omitempty"`
	DestinationTxHash    string           `json:"destination_tx_hash,omitempty"`
	Destination          DrainDestination `json:"destination"`
	Receipt              *DrainReceipt    `json:"receipt,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

// ListDrainsResponse represents a paginated list of drains
type ListDrainsResponse = PaginatedResponse[Drain]
