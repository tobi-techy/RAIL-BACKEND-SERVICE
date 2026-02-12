package kyc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
)

var (
	ErrInvalidSSN         = errors.New("invalid SSN format")
	ErrInvalidImage       = errors.New("invalid image format")
	ErrImageTooLarge      = errors.New("image exceeds 10MB limit")
	ErrKYCAlreadyApproved = errors.New("KYC already approved")
	ErrNoBridgeCustomer   = errors.New("no Bridge customer ID found")
)

const maxImageSize = 10 * 1024 * 1024 // 10MB

type BridgeAdapter interface {
	UpdateCustomer(ctx context.Context, customerID string, req *bridge.UpdateCustomerRequest) (*bridge.Customer, error)
}

type AlpacaAdapter interface {
	CreateAccount(ctx context.Context, req *entities.AlpacaCreateAccountRequest) (*entities.AlpacaAccountResponse, error)
}

type Service struct {
	userRepo      UserRepository
	bridgeAdapter BridgeAdapter
	alpacaAdapter AlpacaAdapter
	logger        *zap.Logger
}

type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
	GetProfileByUserID(ctx context.Context, userID uuid.UUID) (*entities.UserProfile, error)
	Update(ctx context.Context, user *entities.User) error
}

func NewService(
	userRepo UserRepository,
	bridgeAdapter BridgeAdapter,
	alpacaAdapter AlpacaAdapter,
	logger *zap.Logger,
) *Service {
	return &Service{
		userRepo:      userRepo,
		bridgeAdapter: bridgeAdapter,
		alpacaAdapter: alpacaAdapter,
		logger:        logger,
	}
}

// SubmitKYC processes KYC submission to both Bridge and Alpaca.
// PII is never stored - only provider IDs are persisted.
func (s *Service) SubmitKYC(ctx context.Context, req *entities.KYCSubmitRequest) (*entities.KYCSubmitResponse, error) {
	s.logger.Info("KYC submission started",
		zap.String("user_id", req.UserID.String()),
		zap.String("tax_id_type", req.TaxIDType),
	)

	// Validate request
	if err := s.validateRequest(req); err != nil {
		return nil, err
	}

	// Get user profile
	user, err := s.userRepo.GetByID(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Get user profile for personal info
	profile, err := s.userRepo.GetProfileByUserID(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user profile: %w", err)
	}

	// Check eligibility
	if profile.BridgeCustomerID == nil || *profile.BridgeCustomerID == "" {
		return nil, ErrNoBridgeCustomer
	}

	if profile.KYCStatus == "approved" {
		return nil, ErrKYCAlreadyApproved
	}

	response := &entities.KYCSubmitResponse{
		Status: "submitted",
	}

	// Submit to Bridge (update existing customer with KYC data)
	bridgeResult := s.submitToBridge(ctx, *profile.BridgeCustomerID, profile, req)
	response.BridgeResult = bridgeResult

	// Submit to Alpaca (create new account)
	alpacaResult := s.submitToAlpaca(ctx, profile, req)
	response.AlpacaResult = alpacaResult

	// Update user status
	if bridgeResult.Success || alpacaResult.Success {
		user.KYCStatus = "pending"
		user.KYCSubmittedAt = timePtr(time.Now())

		if alpacaResult.Success {
			// Extract account ID from alpaca response status (format: "account_id:status")
			parts := strings.Split(alpacaResult.Status, ":")
			if len(parts) > 0 {
				user.AlpacaAccountID = &parts[0]
			}
		}

		if err := s.userRepo.Update(ctx, user); err != nil {
			s.logger.Error("Failed to update user after KYC submission",
				zap.Error(err),
				zap.String("user_id", req.UserID.String()),
			)
		}
	}

	// Determine overall status
	if !bridgeResult.Success && !alpacaResult.Success {
		response.Status = "failed"
		response.Message = "KYC submission failed for both providers. Please try again."
	} else if !bridgeResult.Success || !alpacaResult.Success {
		response.Status = "partial_failure"
		response.Message = "KYC partially submitted. Our team will review and contact you if needed."
	} else {
		response.Message = "KYC submitted successfully. You will be notified when verification is complete."
	}

	// Clear PII from memory
	req.TaxID = ""
	req.IDDocumentFront = ""
	req.IDDocumentBack = ""

	return response, nil
}

func (s *Service) submitToBridge(ctx context.Context, customerID string, profile *entities.UserProfile, req *entities.KYCSubmitRequest) entities.KYCProviderResult {
	// Build Bridge update request
	updateReq := &bridge.UpdateCustomerRequest{
		IdentifyingInformation: []bridge.IdentifyingInfo{
			{
				Type:           mapTaxIDTypeToBridge(req.TaxIDType),
				IssuingCountry: strings.ToLower(req.IssuingCountry),
				Number:         req.TaxID,
				ImageFront:     req.IDDocumentFront,
				ImageBack:      req.IDDocumentBack,
			},
		},
	}

	customer, err := s.bridgeAdapter.UpdateCustomer(ctx, customerID, updateReq)
	if err != nil {
		s.logger.Error("Bridge KYC submission failed",
			zap.Error(err),
			zap.String("user_id", req.UserID.String()),
		)
		return entities.KYCProviderResult{
			Success: false,
			Error:   "Failed to submit to Bridge",
		}
	}

	return entities.KYCProviderResult{
		Success: true,
		Status:  string(customer.Status),
	}
}

func (s *Service) submitToAlpaca(ctx context.Context, user *entities.UserProfile, req *entities.KYCSubmitRequest) entities.KYCProviderResult {
	// Build Alpaca account request
	alpacaReq := &entities.AlpacaCreateAccountRequest{
		Contact: entities.AlpacaContact{
			EmailAddress: user.Email,
			PhoneNumber:  stringValue(user.Phone),
			StreetAddress: []string{
				// Address should be from user profile (stored during signup)
				// For now, we'll need to add address fields to user table
			},
			City:       "", // From user profile
			State:      "", // From user profile
			PostalCode: "", // From user profile
			Country:    req.IssuingCountry,
		},
		Identity: entities.AlpacaIdentity{
			GivenName:             stringValue(user.FirstName),
			FamilyName:            stringValue(user.LastName),
			DateOfBirth:           formatDate(user.DateOfBirth),
			TaxID:                 req.TaxID,
			TaxIDType:             mapTaxIDTypeToAlpaca(req.TaxIDType),
			CountryOfTaxResidence: req.IssuingCountry,
		},
		Disclosures: entities.AlpacaDisclosures{
			IsControlPerson:             req.Disclosures.IsControlPerson,
			IsAffiliatedExchangeOrFINRA: req.Disclosures.IsAffiliatedExchangeOrFINRA,
			IsPoliticallyExposed:        req.Disclosures.IsPoliticallyExposed,
			ImmediateFamilyExposed:      req.Disclosures.ImmediateFamilyExposed,
		},
		Agreements: []entities.AlpacaAgreement{
			{
				Agreement: "account",
				SignedAt:  time.Now().Format(time.RFC3339),
				IPAddress: req.IPAddress,
			},
			{
				Agreement: "customer",
				SignedAt:  time.Now().Format(time.RFC3339),
				IPAddress: req.IPAddress,
			},
		},
	}

	account, err := s.alpacaAdapter.CreateAccount(ctx, alpacaReq)
	if err != nil {
		s.logger.Error("Alpaca account creation failed",
			zap.Error(err),
			zap.String("user_id", req.UserID.String()),
		)
		return entities.KYCProviderResult{
			Success: false,
			Error:   "Failed to create Alpaca account",
		}
	}

	return entities.KYCProviderResult{
		Success: true,
		Status:  fmt.Sprintf("%s:%s", account.ID, account.Status),
	}
}

func (s *Service) validateRequest(req *entities.KYCSubmitRequest) error {
	// Validate SSN format
	if req.TaxIDType == "ssn" {
		if !isValidSSN(req.TaxID) {
			return ErrInvalidSSN
		}
	}

	// Validate base64 images
	if !isValidBase64Image(req.IDDocumentFront) {
		return ErrInvalidImage
	}

	if req.IDDocumentBack != "" && !isValidBase64Image(req.IDDocumentBack) {
		return ErrInvalidImage
	}

	// Check image sizes
	if len(req.IDDocumentFront) > maxImageSize {
		return ErrImageTooLarge
	}

	if len(req.IDDocumentBack) > maxImageSize {
		return ErrImageTooLarge
	}

	return nil
}

func isValidSSN(ssn string) bool {
	// Remove dashes
	ssn = strings.ReplaceAll(ssn, "-", "")
	
	// Must be 9 digits
	if len(ssn) != 9 {
		return false
	}
	
	// Must be all digits
	matched, _ := regexp.MatchString(`^\d{9}$`, ssn)
	return matched
}

func isValidBase64Image(data string) bool {
	// Check for data URI prefix
	if !strings.HasPrefix(data, "data:image/") {
		return false
	}

	// Extract base64 part
	parts := strings.Split(data, ",")
	if len(parts) != 2 {
		return false
	}

	// Validate base64
	_, err := base64.StdEncoding.DecodeString(parts[1])
	return err == nil
}

func mapTaxIDTypeToBridge(taxIDType string) string {
	switch taxIDType {
	case "ssn":
		return "ssn"
	case "passport":
		return "passport"
	case "national_id":
		return "national_id"
	default:
		return "other"
	}
}

func mapTaxIDTypeToAlpaca(taxIDType string) string {
	switch taxIDType {
	case "ssn":
		return "USA_SSN"
	default:
		return "NOT_SPECIFIED"
	}
}

func formatDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// GetKYCStatus returns the current KYC status and capabilities for a user.
func (s *Service) GetKYCStatus(ctx context.Context, userID uuid.UUID) (*entities.KYCStatusResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	response := &entities.KYCStatusResponse{
		OverallStatus: determineOverallStatus(user),
		Bridge: entities.KYCProviderStatus{
			Status:      stringValue(user.BridgeKYCStatus),
			SubmittedAt: user.KYCSubmittedAt,
			ApprovedAt:  user.KYCApprovedAt,
		},
		Alpaca: entities.KYCProviderStatus{
			Status:      user.KYCStatus,
			SubmittedAt: user.KYCSubmittedAt,
			ApprovedAt:  user.KYCApprovedAt,
		},
		Capabilities: entities.KYCCapabilities{
			CanDepositCrypto: true, // Always true after signup
			CanDepositFiat:   user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "active",
			CanUseCard:       user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "active",
			CanInvest:        user.KYCStatus == "approved" && user.AlpacaAccountID != nil,
		},
	}

	return response, nil
}

func determineOverallStatus(user *entities.User) string {
	if user.KYCStatus == "approved" && user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "active" {
		return "approved"
	}
	if user.KYCStatus == "rejected" || (user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "rejected") {
		return "rejected"
	}
	if user.KYCSubmittedAt != nil {
		return "pending"
	}
	return "not_started"
}
