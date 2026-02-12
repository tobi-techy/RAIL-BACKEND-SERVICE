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
	TaxIDType      string `json:"tax_id_type" validate:"required,oneof=ssn passport national_id"`
	IssuingCountry string `json:"issuing_country" validate:"required,len=3"` // ISO 3166-1 alpha-3

	// Identity documents for Bridge KYC (base64 encoded with data URI prefix)
	IDDocumentFront string `json:"id_document_front" validate:"required"`
	IDDocumentBack  string `json:"id_document_back,omitempty"`

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
	Status       string            `json:"status"` // "submitted", "partial_failure"
	BridgeResult KYCProviderResult `json:"bridge_result"`
	AlpacaResult KYCProviderResult `json:"alpaca_result"`
	Message      string            `json:"message"`
}

// KYCProviderResult represents the result from a single provider.
type KYCProviderResult struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`           // Provider-specific status
	Error   string `json:"error,omitempty"`  // Error message if failed
}

// KYCStatusResponse for checking current KYC state.
type KYCStatusResponse struct {
	UserID            uuid.UUID  `json:"user_id"`
	Status            string     `json:"status"`
	Verified          bool       `json:"verified"`
	HasSubmitted      bool       `json:"has_submitted"`
	RequiresKYC       bool       `json:"requires_kyc"`
	RequiredFor       []string   `json:"required_for,omitempty"`
	LastSubmittedAt   *time.Time `json:"last_submitted_at,omitempty"`
	ApprovedAt        *time.Time `json:"approved_at,omitempty"`
	RejectionReason   *string    `json:"rejection_reason,omitempty"`
	ProviderReference *string    `json:"provider_reference,omitempty"`
	NextSteps         []string   `json:"next_steps,omitempty"`
	OverallStatus     string     `json:"overall_status,omitempty"` // pending, approved, rejected, not_started
	Bridge            KYCProviderStatus `json:"bridge,omitempty"`
	Alpaca            KYCProviderStatus `json:"alpaca,omitempty"`
	Capabilities      KYCCapabilities   `json:"capabilities,omitempty"`
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
