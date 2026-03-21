package entities

import (
	"time"

	"github.com/google/uuid"
)

// KYCSubmitRequest is the unified KYC submission request.
// PII fields are transient - processed and discarded, never stored.
type KYCSubmitRequest struct {
	UserID uuid.UUID `json:"-"` // Set from auth context

	// Tax identification (required for both Bridge and Alpaca)
	TaxID          string `json:"tax_id" validate:"required"`
	TaxIDType      string `json:"tax_id_type" validate:"required,oneof=ssn itin nino utr nin bvn tin passport national_id"`
	IssuingCountry string `json:"issuing_country" validate:"required,len=3"` // ISO 3166-1 alpha-3

	// Identity documents for Bridge KYC (base64 encoded with data URI prefix)
	IDDocumentFront string `json:"id_document_front" validate:"required"`
	IDDocumentBack  string `json:"id_document_back,omitempty"`

	// Source of funds questionnaire (required by Bridge)
	SourceOfFunds              string `json:"source_of_funds,omitempty"`
	EmploymentStatus           string `json:"employment_status,omitempty"`
	ExpectedMonthlyPaymentsUSD string `json:"expected_monthly_payments_usd,omitempty"`
	AccountPurpose             string `json:"account_purpose,omitempty"`
	AccountPurposeOther        string `json:"account_purpose_other,omitempty"`
	MostRecentOccupation       string `json:"most_recent_occupation,omitempty"`
	ActingAsIntermediary       *bool  `json:"acting_as_intermediary,omitempty"`

	// Alpaca regulatory disclosures
	Disclosures KYCDisclosures `json:"disclosures" validate:"required"`

	// Sumsub/legacy KYC provider fields
	DocumentType string                 `json:"document_type,omitempty"`
	Documents    []KYCDocumentUpload    `json:"documents,omitempty"`
	PersonalInfo *KYCPersonalInfo       `json:"personal_info,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`

	// Request metadata (non-PII, set from context)
	IPAddress string `json:"-"`
}

// KYCDisclosures contains Alpaca-required regulatory disclosures.
type KYCDisclosures struct {
	IsControlPerson             bool `json:"is_control_person"`
	IsAffiliatedExchangeOrFINRA bool `json:"is_affiliated_exchange_or_finra"`
	IsPoliticallyExposed        bool `json:"is_politically_exposed"`
	ImmediateFamilyExposed      bool `json:"immediate_family_exposed"`
}

// KYCSubmitResponse is returned after KYC submission.
type KYCSubmitResponse struct {
	Status            string            `json:"status"` // "submitted", "partial_failure"
	ProviderReference *string           `json:"provider_reference,omitempty"`
	BridgeResult      KYCProviderResult `json:"bridge_result"`
	AlpacaResult      KYCProviderResult `json:"alpaca_result"`
	Message           string            `json:"message"`
}

// KYCProviderResult represents the result from a single provider.
type KYCProviderResult struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`          // Provider-specific status
	Error   string `json:"error,omitempty"` // Error message if failed
}

// KYCStatusResponse for checking current KYC state.
type KYCStatusResponse struct {
	UserID              uuid.UUID         `json:"user_id"`
	Status              string            `json:"status"`
	Verified            bool              `json:"verified"`
	HasSubmitted        bool              `json:"has_submitted"`
	RequiresKYC         bool              `json:"requires_kyc"`
	RequiredFor         []string          `json:"required_for,omitempty"`
	LastSubmittedAt     *time.Time        `json:"last_submitted_at,omitempty"`
	ApprovedAt          *time.Time        `json:"approved_at,omitempty"`
	RejectionReason     *string           `json:"rejection_reason,omitempty"`
	ProviderReference   *string           `json:"provider_reference,omitempty"`
	NextSteps           []string          `json:"next_steps,omitempty"`
	OverallStatus       string            `json:"overall_status,omitempty"` // pending, approved, rejected, not_started
	SupportedTaxIDType  string            `json:"supported_tax_id_type,omitempty"` // Single tax ID type for user's country (e.g. ssn, nino, nin)
	Bridge              KYCProviderStatus `json:"bridge,omitempty"`
	Alpaca              KYCProviderStatus `json:"alpaca,omitempty"`
	Capabilities        KYCCapabilities   `json:"capabilities,omitempty"`
}

// KYCProviderStatus represents status for a single provider.
type KYCProviderStatus struct {
	Status           string     `json:"status"`
	SubmittedAt      *time.Time `json:"submitted_at,omitempty"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
	RejectionReasons []string   `json:"rejection_reasons,omitempty"`
}

// KYCCapabilities shows what features are unlocked.
type KYCCapabilities struct {
	CanDepositCrypto bool `json:"can_deposit_crypto"` // Always true after signup
	CanDepositFiat   bool `json:"can_deposit_fiat"`   // Requires Bridge KYC
	CanUseCard       bool `json:"can_use_card"`       // Requires Bridge KYC
	CanInvest        bool `json:"can_invest"`         // Requires Alpaca KYC
}

// KYCDocumentUpload represents a document uploaded for KYC verification
type KYCDocumentUpload struct {
	DocumentType string `json:"document_type"` // passport, drivers_license, national_id
	Type         string `json:"type"`          // Alias used by Sumsub provider
	FrontImage   string `json:"front_image"`   // Base64 encoded image
	BackImage    string `json:"back_image,omitempty"`
	FileURL      string `json:"file_url,omitempty"` // URL-based upload for Sumsub provider
	ContentType  string `json:"content_type,omitempty"`
}

// KYCPersonalInfo represents personal information for KYC verification
type KYCPersonalInfo struct {
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	DateOfBirth *time.Time `json:"date_of_birth,omitempty"`
	Country     string     `json:"country,omitempty"`
	Address     *Address   `json:"address,omitempty"`
	TaxID       string     `json:"tax_id,omitempty"`
	TaxIDType   string     `json:"tax_id_type,omitempty"`
}

// KYCSumsubSessionRequest starts a hosted Sumsub verification session.
type KYCSumsubSessionRequest struct {
	TaxID                      string         `json:"tax_id" validate:"required"`
	TaxIDType                  string         `json:"tax_id_type" validate:"required,oneof=ssn itin nino utr nin bvn tin passport national_id"`
	IssuingCountry             string         `json:"issuing_country" validate:"required,len=3"` // ISO 3166-1 alpha-3
	Disclosures                KYCDisclosures `json:"disclosures" validate:"required"`
	SourceOfFunds              string         `json:"source_of_funds,omitempty"`
	EmploymentStatus           string         `json:"employment_status,omitempty"`
	ExpectedMonthlyPaymentsUSD string         `json:"expected_monthly_payments_usd,omitempty"`
	AccountPurpose             string         `json:"account_purpose,omitempty"`
	AccountPurposeOther        string         `json:"account_purpose_other,omitempty"`
	MostRecentOccupation       string         `json:"most_recent_occupation,omitempty"`
	ActingAsIntermediary       *bool          `json:"acting_as_intermediary,omitempty"`
}

// KYCSumsubSessionResponse returns the data needed to launch Sumsub WebSDK.
type KYCSumsubSessionResponse struct {
	Status      string `json:"status"`
	ApplicantID string `json:"applicant_id"`
	Token       string `json:"token"`
	LevelName   string `json:"level_name"`
}

// KYCDigitSessionRequest starts a hosted Didit verification session.
type KYCDigitSessionRequest struct {
	TaxID                      string         `json:"tax_id" validate:"required"`
	TaxIDType                  string         `json:"tax_id_type" validate:"required,oneof=ssn itin nino utr nin bvn tin passport national_id"`
	IssuingCountry             string         `json:"issuing_country" validate:"required,len=3"`
	Disclosures                KYCDisclosures `json:"disclosures" validate:"required"`
	SourceOfFunds              string         `json:"source_of_funds,omitempty"`
	EmploymentStatus           string         `json:"employment_status,omitempty"`
	ExpectedMonthlyPaymentsUSD string         `json:"expected_monthly_payments_usd,omitempty"`
	AccountPurpose             string         `json:"account_purpose,omitempty"`
	AccountPurposeOther        string         `json:"account_purpose_other,omitempty"`
	MostRecentOccupation       string         `json:"most_recent_occupation,omitempty"`
	ActingAsIntermediary       *bool          `json:"acting_as_intermediary,omitempty"`
}

// KYCDigitSessionResponse returns the data needed to launch Didit SDK.
type KYCDigitSessionResponse struct {
	Status       string `json:"status"`
	SessionID    string `json:"session_id"`
	SessionToken string `json:"session_token"`
	URL          string `json:"url,omitempty"`
}

// DiditWebhookPayload contains the relevant fields from a Didit webhook.
type DiditWebhookPayload struct {
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	WebhookType string `json:"webhook_type"`
	VendorData  string `json:"vendor_data"`
	WorkflowID  string `json:"workflow_id"`
	Decision    *DiditWebhookDecision `json:"decision,omitempty"`
}

// DiditWebhookDecision is the inline decision object in a Didit v3 webhook payload.
type DiditWebhookDecision struct {
	IDVerifications []DiditIDVerification `json:"id_verifications"`
}

// DiditIDVerification holds the document data from a Didit id_verifications entry.
type DiditIDVerification struct {
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	DateOfBirth    string `json:"date_of_birth"`
	DocumentType   string `json:"document_type"`
	DocumentNumber string `json:"document_number"`
	PersonalNumber string `json:"personal_number"`
	IssuingState   string `json:"issuing_state"`
	Nationality    string `json:"nationality"`
	Gender         string `json:"gender"`
	ExpirationDate string `json:"expiration_date"`
	FrontImage     string `json:"front_image"`
	BackImage      string `json:"back_image"`
	FullFrontImage string `json:"full_front_image"`
	FullBackImage  string `json:"full_back_image"`
	PortraitImage  string `json:"portrait_image"`
	ParsedAddress  *DiditParsedAddress `json:"parsed_address,omitempty"`
}

// DiditParsedAddress holds the parsed address from a Didit id_verification entry.
type DiditParsedAddress struct {
	Street1    string `json:"street_1"`
	City       string `json:"city"`
	Region     string `json:"region"`
	Country    string `json:"country"`
	PostalCode string `json:"postal_code"`
}

// Didit verification statuses (case-sensitive, with spaces).
const (
	DiditStatusNotStarted = "Not Started"
	DiditStatusInProgress = "In Progress"
	DiditStatusApproved   = "Approved"
	DiditStatusDeclined   = "Declined"
	DiditStatusInReview   = "In Review"
	DiditStatusResubmitted = "Resubmitted"
	DiditStatusExpired    = "Expired"
	DiditStatusAbandoned  = "Abandoned"
	DiditStatusKYCExpired = "Kyc Expired"
)

// SumsubWebhookPayload contains the relevant fields used by webhook processing.
type SumsubWebhookPayload struct {
	ApplicantID    string             `json:"applicantId"`
	InspectionID   string             `json:"inspectionId"`
	CorrelationID  string             `json:"correlationId"`
	LevelName      string             `json:"levelName"`
	ExternalUserID string             `json:"externalUserId"`
	Type           string             `json:"type"`
	ReviewStatus   string             `json:"reviewStatus"`
	ReviewResult   SumsubReviewResult `json:"reviewResult"`
	CreatedAtMs    string             `json:"createdAtMs"`
}

// SumsubReviewResult captures KYC decision details from Sumsub.
type SumsubReviewResult struct {
	ReviewAnswer string              `json:"reviewAnswer"`
	RejectLabels []SumsubRejectLabel `json:"rejectLabels"`
}

// SumsubRejectLabel represents a specific rejection label in Sumsub.
type SumsubRejectLabel struct {
	Label       string `json:"label"`
	LabelType   string `json:"labelType"`
	Description string `json:"description"`
}
