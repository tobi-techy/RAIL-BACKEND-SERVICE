package graph

// Graph (useoval.com) API request/response types.
//
// Graph wraps all successful responses in an envelope:
//
//	{ "data": {...}, "message": "...", "meta": null, "status": "success" }
//
// The typed helpers below unmarshal into `envelope[T]` and return the inner
// Data. See client.go for the transport.

// envelope is Graph's standard response wrapper.
type envelope[T any] struct {
	Data    T      `json:"data"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// ── People ───────────────────────────────────────────────────────

// Address is the person/business address block Graph requires.
type Address struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	State      string `json:"state"`
	Country    string `json:"country"` // ISO Alpha-2, e.g. "NG"
	PostalCode string `json:"postal_code"`
}

// BackgroundInformation is the optional KYC background block for a person.
type BackgroundInformation struct {
	EmploymentStatus      string `json:"employment_status,omitempty"`
	Occupation            string `json:"occupation,omitempty"`
	PrimaryPurpose        string `json:"primary_purpose,omitempty"`
	SourceOfFunds         string `json:"source_of_funds,omitempty"`
	ExpectedMonthlyInflow int    `json:"expected_monthly_inflow,omitempty"`
}

// Document is a single document upload attached to a Create Person request.
type Document struct {
	Type       string `json:"type"` // passport, national_id, drivers_licence, voters_card, bank_statement, utility_bill, ...
	URL        string `json:"url"`
	IssueDate  string `json:"issue_date,omitempty"`  // YYYY-MM-DD
	ExpiryDate string `json:"expiry_date,omitempty"` // YYYY-MM-DD
}

// CreatePersonRequest creates a person on Graph. For an NGN account, all fields
// listed in the NGN requirements doc are required (name_other, id_type,
// id_number, id_country, bank_id_number/BVN, address, documents).
type CreatePersonRequest struct {
	NameFirst string `json:"name_first"`
	NameLast  string `json:"name_last"`
	NameOther string `json:"name_other,omitempty"`
	Phone     string `json:"phone"` // full number with country prefix, e.g. +2348024056288
	Email     string `json:"email"`
	DOB       string `json:"dob"` // YYYY-MM-DD

	IDType    string `json:"id_type,omitempty"` // passport, drivers_license, national_id, voters_card
	IDNumber  string `json:"id_number,omitempty"`
	IDCountry string `json:"id_country"`         // ISO Alpha-2, e.g. "NG"
	IDLevel   string `json:"id_level,omitempty"` // primary (passport) | secondary (local IDs)

	BankIDNumber string `json:"bank_id_number,omitempty"` // BVN for Nigerian ID holders
	KYCLevel     string `json:"kyc_level,omitempty"`      // preliminary | basic

	Address               Address                `json:"address"`
	BackgroundInformation *BackgroundInformation `json:"background_information,omitempty"`
	Documents             []Document             `json:"documents,omitempty"`
}

// Person is the Graph person record.
type Person struct {
	ID           string `json:"id"`
	NameFirst    string `json:"name_first"`
	NameLast     string `json:"name_last"`
	NameOther    string `json:"name_other"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	DOB          string `json:"dob"`
	IDLevel      string `json:"id_level"`
	IDType       string `json:"id_type"`
	IDNumber     string `json:"id_number"`
	IDCountry    string `json:"id_country"`
	BankIDNumber string `json:"bank_id_number"`
	BankIDType   string `json:"bank_id_type"`
	Status       string `json:"status"`     // active, ...
	KYCStatus    string `json:"kyc_status"` // verified, pending, ...
	KYCLevel     string `json:"kyc_level"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// UpgradePersonKYCRequest upgrades a person's KYC (e.g. secondary → primary ID).
type UpgradePersonKYCRequest struct {
	IDType    string `json:"id_type"` // passport, drivers_license, nin, voters_card
	IDNumber  string `json:"id_number"`
	IDCountry string `json:"id_country"`
	IDUpload  string `json:"id_upload"` // URL of the uploaded ID document
}

// ── Documents ────────────────────────────────────────────────────

// CreateDocumentRequest attaches a document to an existing person or business.
type CreateDocumentRequest struct {
	EntityType   string `json:"entity_type"` // person | business
	PersonID     string `json:"person_id,omitempty"`
	BusinessID   string `json:"business_id,omitempty"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	IssuanceDate string `json:"issuance_date,omitempty"`
	ExpiryDate   string `json:"expiry_date,omitempty"`
}

// EntityDocument is a stored document record.
type EntityDocument struct {
	ID         string `json:"id"`
	EntityType string `json:"entity_type"`
	PersonID   string `json:"person_id"`
	BusinessID string `json:"business_id"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	FileType   string `json:"file_type"`
	CreatedAt  string `json:"created_at"`
}

// ── Bank Accounts ────────────────────────────────────────────────

// WhitelistAccount restricts NGN deposits to a single source account.
type WhitelistAccount struct {
	AccountNumber string `json:"account_number"`
	BankID        string `json:"bank_id"`
}

// CreateBankAccountRequest provisions a virtual bank account for a person.
type CreateBankAccountRequest struct {
	PersonID         string            `json:"person_id,omitempty"`
	BusinessID       string            `json:"business_id,omitempty"`
	Label            string            `json:"label"`
	Currency         string            `json:"currency"` // USD, NGN, EUR
	AutosweepEnabled bool              `json:"autosweep_enabled"`
	WhitelistEnabled bool              `json:"whitelist_enabled,omitempty"`
	Whitelist        *WhitelistAccount `json:"whitelist,omitempty"`
}

// BankAccount is the Graph bank account record. For NGN accounts the
// account_number / account_name / bank details are populated asynchronously
// (status pending → active), delivered via issuance webhook.
type BankAccount struct {
	ID            string  `json:"id"`
	HolderID      string  `json:"holder_id"`
	HolderType    string  `json:"holder_type"`
	Label         string  `json:"label"`
	AccountName   string  `json:"account_name"`
	AccountNumber string  `json:"account_number"`
	RoutingNumber string  `json:"routing_number"`
	BankName      string  `json:"bank_name"`
	BankCode      string  `json:"bank_code"`
	BankAddress   string  `json:"bank_address"`
	Currency      string  `json:"currency"`
	Balance       float64 `json:"balance"`
	Type          string  `json:"type"`
	Status        string  `json:"status"` // pending, active, ...
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// ListBankAccountsResponse wraps a list of bank accounts.
type ListBankAccountsResponse struct {
	Data []BankAccount `json:"data"`
}

// ── Rates & Conversions ──────────────────────────────────────────

// Rate is an exchange rate for a currency pair.
type Rate struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Rate   float64 `json:"rate"`
}

// CreateConversionRequest converts a balance from one currency to another
// (e.g. NGN in the master wallet → USDC stablecoin).
type CreateConversionRequest struct {
	Source       string `json:"source"`                 // e.g. "NGN"
	Target       string `json:"target"`                 // e.g. "USDC"
	Amount       string `json:"amount"`                 // decimal string in source currency
	SourceWallet string `json:"source_wallet,omitempty"`
	Reference    string `json:"reference,omitempty"` // idempotency reference
}

// Conversion is the result of a currency conversion.
type Conversion struct {
	ID           string  `json:"id"`
	Source       string  `json:"source"`
	Target       string  `json:"target"`
	SourceAmount string  `json:"source_amount"`
	TargetAmount string  `json:"target_amount"`
	Rate         float64 `json:"rate"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"created_at"`
}
