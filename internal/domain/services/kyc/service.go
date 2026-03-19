package kyc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/sumsub"
)

var (
	ErrInvalidSSN            = errors.New("invalid SSN format")
	ErrInvalidITIN           = errors.New("invalid ITIN format")
	ErrUnsupportedTaxIDType  = errors.New("unsupported tax_id_type for issuing_country")
	ErrInvalidIssuingCountry = errors.New("issuing_country must be an ISO alpha-3 code")
	ErrInvalidImage          = errors.New("invalid image format")
	ErrImageTooLarge         = errors.New("image exceeds 10MB limit")
	ErrKYCAlreadyApproved    = errors.New("KYC already approved")
	ErrNoBridgeCustomer      = errors.New("no Bridge customer ID found")
	ErrSumsubNotConfigured   = errors.New("sumsub KYC provider is not configured")
	ErrDiditNotConfigured    = errors.New("didit KYC provider is not configured")
	ErrMissingTaxID          = errors.New("tax_id is required")
	ErrMissingTaxIDType      = errors.New("tax_id_type is required")
	ErrMissingDocumentFront  = errors.New("id_document_front is required")

	sumsubExistingApplicantIDPattern = regexp.MustCompile(`(?i)already exists:\s*([a-z0-9]+)`)
	isoAlpha3Pattern                 = regexp.MustCompile(`^[A-Z]{3}$`)
	dataURIImagePattern              = regexp.MustCompile(`^data:(image\/[a-zA-Z0-9.+-]+);base64,`)
)

const (
	maxImageDecodedBytes = 10 * 1024 * 1024 // 10MB
	// Base64 expands by ~4/3. Keep a hard cap to avoid unnecessary decode work.
	maxImageEncodedBytes = 14 * 1024 * 1024
)

const (
	defaultKYCSyncMaxAttempts = 5
)

var allowedKYCImageMIMETypes = map[string]struct{}{
	"image/jpeg": {},
	"image/jpg":  {},
	"image/png":  {},
}

// IncompleteProfileError describes missing user profile fields required before KYC provider submission.
type IncompleteProfileError struct {
	MissingFields []string
}

func (e *IncompleteProfileError) Error() string {
	return "missing required profile fields for KYC submission"
}

type BridgeAdapter interface {
	UpdateCustomer(ctx context.Context, customerID string, req *bridge.UpdateCustomerRequest) (*bridge.Customer, error)
}

type AlpacaAdapter interface {
	CreateAccount(ctx context.Context, req *entities.AlpacaCreateAccountRequest) (*entities.AlpacaAccountResponse, error)
}

type Service struct {
	userRepo               UserRepository
	kycSubmissionRepo      KYCSubmissionRepository
	bridgeAdapter          BridgeAdapter
	alpacaAdapter          AlpacaAdapter
	sumsubAdapter          SumsubAdapter
	diditAdapter           DiditAdapter
	sumsubWebhookEventRepo SumsubWebhookEventRepository
	kycSyncJobRepo         KYCSyncJobRepository
	sumsubLevelName        string
	logger                 *zap.Logger
}

type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
	GetProfileByUserID(ctx context.Context, userID uuid.UUID) (*entities.UserProfile, error)
	Update(ctx context.Context, user *entities.User) error
}

type KYCSubmissionRepository interface {
	Create(ctx context.Context, submission *entities.KYCSubmission) error
	GetByProviderRef(ctx context.Context, providerRef string) (*entities.KYCSubmission, error)
	Update(ctx context.Context, submission *entities.KYCSubmission) error
}

type SumsubWebhookEventRepository interface {
	CreateIfNotExists(ctx context.Context, event *entities.SumsubWebhookEvent) (bool, error)
}

type KYCSyncJobRepository interface {
	Enqueue(ctx context.Context, job *entities.KYCSyncJob) (bool, error)
	EnqueueProviderRetry(ctx context.Context, userID, provider string, payload []byte) (bool, error)
}

type SumsubAdapter interface {
	CreateApplicant(ctx context.Context, req *sumsub.CreateApplicantRequest, levelName string) (*sumsub.ApplicantResponse, error)
	CreateAccessToken(ctx context.Context, applicantID, externalUserID, levelName string, ttlSeconds int) (*sumsub.AccessTokenResponse, error)
	GetApplicantData(ctx context.Context, applicantID string) (*sumsub.ApplicantDataResponse, error)
	GetApplicantImageMetadata(ctx context.Context, applicantID string) (*sumsub.ImageMetadataResponse, error)
	GetInspectionImage(ctx context.Context, inspectionID, imageID string) ([]byte, string, error)
	VerifyWebhookSignature(body []byte, digestHeader, digestAlgHeader string) error
}

type DiditAdapter interface {
	CreateSession(ctx context.Context, vendorData string) (*didit.SessionResponse, error)
	GetSessionDecision(ctx context.Context, sessionID string) (*didit.SessionDecision, error)
	VerifyWebhookSignature(body []byte, signatureHeader, timestampHeader string) error
}

func NewService(
	userRepo UserRepository,
	kycSubmissionRepo KYCSubmissionRepository,
	bridgeAdapter BridgeAdapter,
	alpacaAdapter AlpacaAdapter,
	sumsubAdapter SumsubAdapter,
	sumsubWebhookEventRepo SumsubWebhookEventRepository,
	kycSyncJobRepo KYCSyncJobRepository,
	sumsubLevelName string,
	logger *zap.Logger,
	diditAdapter ...DiditAdapter,
) *Service {
	if isNilSumsubAdapter(sumsubAdapter) {
		sumsubAdapter = nil
	}

	svc := &Service{
		userRepo:               userRepo,
		kycSubmissionRepo:      kycSubmissionRepo,
		bridgeAdapter:          bridgeAdapter,
		alpacaAdapter:          alpacaAdapter,
		sumsubAdapter:          sumsubAdapter,
		sumsubWebhookEventRepo: sumsubWebhookEventRepo,
		kycSyncJobRepo:         kycSyncJobRepo,
		sumsubLevelName:        strings.TrimSpace(sumsubLevelName),
		logger:                 logger,
	}
	if len(diditAdapter) > 0 && diditAdapter[0] != nil {
		svc.diditAdapter = diditAdapter[0]
	}
	return svc
}

// SubmitKYC processes KYC submission to both Bridge and Alpaca.
// PII is never stored - only provider IDs are persisted.
func (s *Service) SubmitKYC(ctx context.Context, req *entities.KYCSubmitRequest) (*entities.KYCSubmitResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("missing request")
	}
	normalizeKYCSubmitRequest(req)
	defer scrubKYCSubmitRequest(req)

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
	if missingFields := collectMissingKYCProfileFields(profile); len(missingFields) > 0 {
		return nil, &IncompleteProfileError{MissingFields: missingFields}
	}

	response := &entities.KYCSubmitResponse{
		Status: "submitted",
	}

	// Submit to Bridge (update existing customer with KYC data)
	bridgeResult := s.submitToBridge(ctx, *profile.BridgeCustomerID, profile, req)
	response.BridgeResult = bridgeResult

	// Alpaca KYC disabled for v1 launch — only Bridge is required
	alpacaResult := entities.KYCProviderResult{
		Success: true,
		Status:  "skipped",
	}
	response.AlpacaResult = alpacaResult

	// Update user status
	if bridgeResult.Success || alpacaResult.Success {
		submittedAt := time.Now()
		providerRef := uuid.NewString()
		providerReferencePersisted := false

		if s.kycSubmissionRepo != nil {
			expiresAt := submittedAt.Add(30 * 24 * time.Hour)
			submission := &entities.KYCSubmission{
				ID:             uuid.New(),
				UserID:         req.UserID,
				Provider:       "bridge_alpaca",
				ProviderRef:    providerRef,
				SubmissionType: "unified_kyc",
				Status:         entities.KYCStatusProcessing,
				VerificationData: map[string]any{
					"bridge_success": bridgeResult.Success,
					"bridge_status":  bridgeResult.Status,
					"alpaca_success": alpacaResult.Success,
					"alpaca_status":  alpacaResult.Status,
				},
				SubmittedAt: submittedAt,
				ExpiresAt:   &expiresAt,
				CreatedAt:   submittedAt,
				UpdatedAt:   submittedAt,
			}

			if err := s.kycSubmissionRepo.Create(ctx, submission); err != nil {
				s.logger.Warn("Failed to persist KYC submission provider reference",
					zap.Error(err),
					zap.String("user_id", req.UserID.String()),
				)
			} else {
				providerReferencePersisted = true
				user.KYCProviderRef = &providerRef
				response.ProviderReference = &providerRef
			}
		}

		user.KYCStatus = "pending"
		user.KYCSubmittedAt = timePtr(submittedAt)

		if alpacaResult.Success && alpacaResult.Status != "skipped" {
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

		if !providerReferencePersisted {
			s.logger.Warn("KYC provider reference not persisted; callback correlation may be unavailable",
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

	return response, nil
}

// StartSumsubSession creates a Sumsub applicant and WebSDK access token.
func (s *Service) StartSumsubSession(ctx context.Context, userID uuid.UUID, req *entities.KYCSumsubSessionRequest) (*entities.KYCSumsubSessionResponse, error) {
	if isNilSumsubAdapter(s.sumsubAdapter) {
		return nil, ErrSumsubNotConfigured
	}
	if err := s.validateSumsubSessionRequest(req); err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	profile, err := s.userRepo.GetProfileByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user profile: %w", err)
	}
	if profile.BridgeCustomerID == nil || *profile.BridgeCustomerID == "" {
		return nil, ErrNoBridgeCustomer
	}
	if profile.KYCStatus == "approved" {
		return nil, ErrKYCAlreadyApproved
	}

	// Submit source of funds to Bridge immediately (required before KYC approval)
	if req.SourceOfFunds != "" {
		bridgeUpdateReq := &bridge.UpdateCustomerRequest{
			SourceOfFunds:              req.SourceOfFunds,
			EmploymentStatus:           req.EmploymentStatus,
			ExpectedMonthlyPaymentsUSD: req.ExpectedMonthlyPaymentsUSD,
			AccountPurpose:             req.AccountPurpose,
			AccountPurposeOther:        req.AccountPurposeOther,
			MostRecentOccupation:       req.MostRecentOccupation,
			ActingAsIntermediary:       req.ActingAsIntermediary,
		}
		if _, bridgeErr := s.bridgeAdapter.UpdateCustomer(ctx, *profile.BridgeCustomerID, bridgeUpdateReq); bridgeErr != nil {
			s.logger.Warn("Failed to submit source of funds to Bridge",
				zap.Error(bridgeErr),
				zap.String("user_id", userID.String()))
			// Non-fatal — continue with Sumsub session
		}
	}

	levelName := s.sumsubLevelName
	if levelName == "" {
		levelName = "basic-kyc"
	}

	applyReq := &sumsub.CreateApplicantRequest{
		ExternalUserID: userID.String(),
		Email:          profile.Email,
		Phone:          stringValue(profile.Phone),
		FixedInfo: &sumsub.ApplicantFixedInfo{
			FirstName: stringValue(profile.FirstName),
			LastName:  stringValue(profile.LastName),
			DOB:       formatDate(profile.DateOfBirth),
			Country:   strings.ToUpper(req.IssuingCountry),
		},
	}

	applicant, createApplicantErr := s.sumsubAdapter.CreateApplicant(ctx, applyReq, levelName)
	applicantID := ""
	inspectionID := ""
	if createApplicantErr != nil {
		if user.KYCProviderRef != nil && strings.TrimSpace(*user.KYCProviderRef) != "" {
			applicantID = strings.TrimSpace(*user.KYCProviderRef)
			s.logger.Warn("Falling back to existing Sumsub applicant for session token",
				zap.Error(createApplicantErr),
				zap.String("user_id", userID.String()),
				zap.String("applicant_id", applicantID))
		} else if existingApplicantID := extractExistingSumsubApplicantID(createApplicantErr); existingApplicantID != "" {
			applicantID = existingApplicantID
			s.logger.Warn("Using existing Sumsub applicant from conflict response",
				zap.Error(createApplicantErr),
				zap.String("user_id", userID.String()),
				zap.String("applicant_id", applicantID))
		} else {
			return nil, fmt.Errorf("failed to create sumsub applicant: %w", createApplicantErr)
		}
	} else {
		applicantID = applicant.ID
		inspectionID = applicant.InspectionID
	}

	if inspectionID == "" && applicantID != "" {
		existingApplicant, applicantDataErr := s.sumsubAdapter.GetApplicantData(ctx, applicantID)
		if applicantDataErr != nil {
			s.logger.Warn("Failed to load existing Sumsub applicant after conflict fallback",
				zap.Error(applicantDataErr),
				zap.String("user_id", userID.String()),
				zap.String("applicant_id", applicantID))
		} else {
			inspectionID = existingApplicant.InspectionID
		}
	}

	accessToken, err := s.sumsubAdapter.CreateAccessToken(ctx, applicantID, userID.String(), levelName, 3600)
	if err != nil {
		return nil, fmt.Errorf("failed to create sumsub access token: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(30 * 24 * time.Hour)
	verificationData := map[string]any{
		"sumsub_level_name":    levelName,
		"sumsub_inspection_id": inspectionID,
		"tax_id":               req.TaxID,
		"tax_id_type":          req.TaxIDType,
		"issuing_country":      req.IssuingCountry,
		"disclosures": map[string]any{
			"is_control_person":               req.Disclosures.IsControlPerson,
			"is_affiliated_exchange_or_finra": req.Disclosures.IsAffiliatedExchangeOrFINRA,
			"is_politically_exposed":          req.Disclosures.IsPoliticallyExposed,
			"immediate_family_exposed":        req.Disclosures.ImmediateFamilyExposed,
		},
	}

	existingSubmission, existingErr := s.kycSubmissionRepo.GetByProviderRef(ctx, applicantID)
	if existingErr != nil && !isSubmissionNotFoundErr(existingErr) {
		return nil, fmt.Errorf("failed to load existing sumsub submission: %w", existingErr)
	}
	if existingErr != nil {
		submission := &entities.KYCSubmission{
			ID:               uuid.New(),
			UserID:           userID,
			Provider:         "sumsub",
			ProviderRef:      applicantID,
			SubmissionType:   "sumsub_websdk",
			Status:           entities.KYCStatusProcessing,
			VerificationData: verificationData,
			SubmittedAt:      now,
			ExpiresAt:        &expiresAt,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := s.kycSubmissionRepo.Create(ctx, submission); err != nil {
			return nil, fmt.Errorf("failed to persist sumsub submission: %w", err)
		}
	} else {
		existingSubmission.Status = entities.KYCStatusProcessing
		existingSubmission.VerificationData = verificationData
		existingSubmission.SubmittedAt = now
		existingSubmission.ExpiresAt = &expiresAt
		existingSubmission.UpdatedAt = now
		if err := s.kycSubmissionRepo.Update(ctx, existingSubmission); err != nil {
			return nil, fmt.Errorf("failed to update existing sumsub submission: %w", err)
		}
	}

	// Do not mark the user as submitted at session creation time.
	// A user may close the SDK before uploading/submitting documents.
	user.KYCProviderRef = &applicantID
	if err := s.userRepo.Update(ctx, user); err != nil {
		s.logger.Warn("Failed to update user after Sumsub session creation",
			zap.Error(err),
			zap.String("user_id", userID.String()))
	}

	return &entities.KYCSumsubSessionResponse{
		Status:      "pending",
		ApplicantID: applicantID,
		Token:       accessToken.Token,
		LevelName:   levelName,
	}, nil
}

// EnqueueSumsubWebhook persists webhook receipt and queues async sync processing.
func (s *Service) EnqueueSumsubWebhook(ctx context.Context, payload *entities.SumsubWebhookPayload, rawPayload []byte) (bool, error) {
	if payload == nil {
		return false, fmt.Errorf("missing sumsub payload")
	}
	if strings.TrimSpace(payload.ApplicantID) == "" {
		return false, fmt.Errorf("missing sumsub applicantId")
	}
	if s.kycSyncJobRepo == nil {
		return false, fmt.Errorf("kyc sync job repository is not configured")
	}

	dedupeKey := sumsubWebhookDedupeKey(payload)
	now := time.Now()

	job := &entities.KYCSyncJob{
		ID:            uuid.New(),
		DedupeKey:     dedupeKey,
		ApplicantID:   strings.TrimSpace(payload.ApplicantID),
		CorrelationID: strings.TrimSpace(payload.CorrelationID),
		EventType:     strings.TrimSpace(payload.Type),
		Payload:       append([]byte(nil), rawPayload...),
		Status:        entities.KYCSyncJobStatusPending,
		AttemptCount:  0,
		MaxAttempts:   defaultKYCSyncMaxAttempts,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := job.Validate(); err != nil {
		return false, err
	}

	queued, err := s.kycSyncJobRepo.Enqueue(ctx, job)
	if err != nil {
		return false, err
	}
	if !queued {
		return false, nil
	}

	if s.sumsubWebhookEventRepo != nil {
		event := &entities.SumsubWebhookEvent{
			ID:            uuid.New(),
			DedupeKey:     dedupeKey,
			ApplicantID:   strings.TrimSpace(payload.ApplicantID),
			CorrelationID: strings.TrimSpace(payload.CorrelationID),
			EventType:     strings.TrimSpace(payload.Type),
			Payload:       append([]byte(nil), rawPayload...),
			ReceivedAt:    now,
			CreatedAt:     now,
		}
		if _, eventErr := s.sumsubWebhookEventRepo.CreateIfNotExists(ctx, event); eventErr != nil {
			s.logger.Warn("Failed to persist Sumsub webhook event record",
				zap.Error(eventErr),
				zap.String("dedupe_key", dedupeKey),
				zap.String("applicant_id", payload.ApplicantID))
		}
	}

	return true, nil
}

// ProcessSumsubWebhook applies Sumsub verification outcomes and syncs providers.
func (s *Service) ProcessSumsubWebhook(ctx context.Context, payload *entities.SumsubWebhookPayload) error {
	if payload == nil {
		return fmt.Errorf("missing sumsub payload")
	}
	if strings.TrimSpace(payload.ApplicantID) == "" {
		return fmt.Errorf("missing sumsub applicantId")
	}

	submission, err := s.kycSubmissionRepo.GetByProviderRef(ctx, payload.ApplicantID)
	if err != nil {
		return fmt.Errorf("failed to load KYC submission for applicant %s: %w", payload.ApplicantID, err)
	}

	switch strings.ToLower(strings.TrimSpace(payload.Type)) {
	case "applicantreviewed":
		answer := strings.ToUpper(strings.TrimSpace(payload.ReviewResult.ReviewAnswer))
		switch answer {
		case "GREEN":
			return s.processSumsubApproved(ctx, submission, payload)
		case "RED":
			return s.processSumsubRejected(ctx, submission, payload)
		default:
			return s.markSubmissionProcessing(ctx, submission, payload)
		}
	default:
		return s.markSubmissionProcessing(ctx, submission, payload)
	}
}

// VerifySumsubWebhookSignature validates Sumsub webhook digest headers.
func (s *Service) VerifySumsubWebhookSignature(body []byte, digestHeader, digestAlgHeader string) error {
	if isNilSumsubAdapter(s.sumsubAdapter) {
		return ErrSumsubNotConfigured
	}
	return s.sumsubAdapter.VerifyWebhookSignature(body, digestHeader, digestAlgHeader)
}

func (s *Service) processSumsubApproved(ctx context.Context, submission *entities.KYCSubmission, payload *entities.SumsubWebhookPayload) error {
	if submission.Status == entities.KYCStatusApproved {
		return nil
	}

	if err := s.hydrateSubmissionFromSumsubApplicant(ctx, submission); err != nil {
		s.logger.Warn("Failed to hydrate KYC submission from Sumsub applicant data",
			zap.Error(err),
			zap.String("submission_id", submission.ID.String()),
			zap.String("applicant_id", submission.ProviderRef))
	}

	user, err := s.userRepo.GetByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	profile, err := s.userRepo.GetProfileByUserID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user profile: %w", err)
	}

	request := sumsubRequestFromVerificationData(submission.VerificationData)
	request.UserID = submission.UserID
	request.IPAddress = "sumsub"

	// Fetch approved document images from Sumsub to forward to Bridge.
	s.fetchAndAttachSumsubImages(ctx, submission.ProviderRef, payload.InspectionID, request)

	// Defer scrubbing sensitive images to ensure they're always cleared even on early returns
	defer func() {
		request.IDDocumentFront = ""
		request.IDDocumentBack = ""
	}()

	bridgeResult := entities.KYCProviderResult{Success: false, Status: "skipped", Error: "Bridge customer not found"}
	if profile.BridgeCustomerID != nil && *profile.BridgeCustomerID != "" {
		bridgeResult = s.submitToBridgeFromSumsub(ctx, *profile.BridgeCustomerID, request)
	}

	alpacaResult := entities.KYCProviderResult{Success: true, Status: "skipped"} // Alpaca disabled for v1

	now := time.Now()
	if bridgeResult.Success {
		bridgeStatus := "pending" // Bridge activates asynchronously via webhook
		user.BridgeKYCStatus = &bridgeStatus
	} else if profile.BridgeCustomerID != nil && *profile.BridgeCustomerID != "" {
		// Enqueue a Bridge-specific retry job.
		if s.kycSyncJobRepo != nil {
			retryPayload, _ := encodeProviderRetryPayload(submission.VerificationData, submission.UserID.String())
			if _, enqErr := s.kycSyncJobRepo.EnqueueProviderRetry(ctx, submission.UserID.String(), "bridge", retryPayload); enqErr != nil {
				s.logger.Warn("Failed to enqueue Bridge retry job",
					zap.Error(enqErr),
					zap.String("user_id", submission.UserID.String()))
			}
		}
	}

	// Sumsub approval is sufficient — approve KYC regardless of Bridge result.
	// Bridge activates asynchronously and will update bridge_kyc_status via webhook.
	user.KYCStatus = string(entities.KYCStatusApproved)
	user.KYCApprovedAt = &now
	user.KYCRejectionReason = nil

	_ = alpacaResult // used in verification data below

	if user.KYCSubmittedAt == nil {
		user.KYCSubmittedAt = &now
	}

	// Advance onboarding status when KYC is approved so the frontend routing guard
	// lets the user through to the dashboard.
	if user.KYCStatus == string(entities.KYCStatusApproved) {
		user.OnboardingStatus = entities.OnboardingStatusCompleted
	}

	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user after sumsub approval: %w", err)
	}

	submission.UpdatedAt = now
	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	submission.VerificationData["sumsub_webhook_type"] = payload.Type
	submission.VerificationData["sumsub_review_status"] = payload.ReviewStatus
	submission.VerificationData["sumsub_review_answer"] = payload.ReviewResult.ReviewAnswer
	submission.VerificationData["bridge_sync"] = map[string]any{
		"success": bridgeResult.Success,
		"status":  bridgeResult.Status,
		"error":   bridgeResult.Error,
	}
	submission.VerificationData["alpaca_sync"] = map[string]any{
		"success": alpacaResult.Success,
		"status":  alpacaResult.Status,
		"error":   alpacaResult.Error,
	}

	if bridgeResult.Success && alpacaResult.Success {
		submission.MarkReviewed(entities.KYCStatusApproved, nil)
	} else {
		submission.Status = entities.KYCStatusProcessing
	}

	if err := s.kycSubmissionRepo.Update(ctx, submission); err != nil {
		return fmt.Errorf("failed to update sumsub submission: %w", err)
	}

	return nil
}

// fetchAndAttachSumsubImages fetches approved document images from Sumsub and attaches
// them to the request as base64 data URIs. Failures are non-fatal warnings.
func (s *Service) fetchAndAttachSumsubImages(ctx context.Context, applicantID, inspectionID string, req *entities.KYCSubmitRequest) {
	if isNilSumsubAdapter(s.sumsubAdapter) || applicantID == "" || inspectionID == "" {
		return
	}

	meta, err := s.sumsubAdapter.GetApplicantImageMetadata(ctx, applicantID)
	if err != nil {
		s.logger.Warn("Failed to fetch Sumsub image metadata", zap.Error(err), zap.String("applicant_id", applicantID))
		return
	}

	for _, item := range meta.Items {
		if item.Deactivated || strings.ToUpper(item.ReviewResult.ReviewAnswer) != "GREEN" {
			continue
		}
		subType := strings.ToUpper(item.IdDocDef.IDDocSubType)
		if subType != "FRONT_SIDE" && subType != "BACK_SIDE" {
			continue
		}
		if subType == "FRONT_SIDE" && req.IDDocumentFront != "" {
			continue
		}
		if subType == "BACK_SIDE" && req.IDDocumentBack != "" {
			continue
		}

		imgBytes, contentType, imgErr := s.sumsubAdapter.GetInspectionImage(ctx, inspectionID, item.ID)
		if imgErr != nil {
			s.logger.Warn("Failed to fetch Sumsub image", zap.Error(imgErr), zap.String("image_id", item.ID))
			continue
		}

		mimeType := strings.SplitN(contentType, ";", 2)[0]
		dataURI := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(imgBytes)

		if subType == "FRONT_SIDE" {
			req.IDDocumentFront = dataURI
		} else {
			req.IDDocumentBack = dataURI
		}
	}
}

func (s *Service) processSumsubRejected(ctx context.Context, submission *entities.KYCSubmission, payload *entities.SumsubWebhookPayload) error {
	rejectionReasons := extractRejectReasons(payload.ReviewResult.RejectLabels)
	user, err := s.userRepo.GetByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	now := time.Now()
	user.KYCStatus = string(entities.KYCStatusRejected)
	user.KYCApprovedAt = nil
	user.KYCSubmittedAt = &now
	user.OnboardingStatus = entities.OnboardingStatusKYCRejected
	if len(rejectionReasons) > 0 {
		reason := strings.Join(rejectionReasons, "; ")
		user.KYCRejectionReason = &reason
	}
	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user after sumsub rejection: %w", err)
	}

	submission.MarkReviewed(entities.KYCStatusRejected, rejectionReasons)
	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	submission.VerificationData["sumsub_webhook_type"] = payload.Type
	submission.VerificationData["sumsub_review_status"] = payload.ReviewStatus
	submission.VerificationData["sumsub_review_answer"] = payload.ReviewResult.ReviewAnswer
	if err := s.kycSubmissionRepo.Update(ctx, submission); err != nil {
		return fmt.Errorf("failed to update rejected sumsub submission: %w", err)
	}

	return nil
}

func (s *Service) markSubmissionProcessing(ctx context.Context, submission *entities.KYCSubmission, payload *entities.SumsubWebhookPayload) error {
	submission.Status = entities.KYCStatusProcessing
	now := time.Now()
	submission.UpdatedAt = now
	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	submission.VerificationData["sumsub_webhook_type"] = payload.Type
	submission.VerificationData["sumsub_review_status"] = payload.ReviewStatus
	if payload.ReviewResult.ReviewAnswer != "" {
		submission.VerificationData["sumsub_review_answer"] = payload.ReviewResult.ReviewAnswer
	}
	if err := s.kycSubmissionRepo.Update(ctx, submission); err != nil {
		return err
	}

	// Mark user as in-review only after webhook-driven processing starts.
	user, err := s.userRepo.GetByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user for processing state: %w", err)
	}
	if user.KYCSubmittedAt == nil {
		user.KYCSubmittedAt = &now
	}
	if user.KYCStatus != string(entities.KYCStatusApproved) &&
		user.KYCStatus != string(entities.KYCStatusRejected) {
		user.KYCStatus = string(entities.KYCStatusProcessing)
	}
	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user processing state: %w", err)
	}

	return nil
}

func (s *Service) hydrateSubmissionFromSumsubApplicant(ctx context.Context, submission *entities.KYCSubmission) error {
	if isNilSumsubAdapter(s.sumsubAdapter) || submission == nil {
		return nil
	}

	applicantID := strings.TrimSpace(submission.ProviderRef)
	if applicantID == "" {
		return nil
	}

	applicant, err := s.sumsubAdapter.GetApplicantData(ctx, applicantID)
	if err != nil {
		return err
	}

	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	mergeApplicantDataIntoVerification(submission.VerificationData, applicant)

	return nil
}

func isNilSumsubAdapter(adapter SumsubAdapter) bool {
	if adapter == nil {
		return true
	}

	value := reflect.ValueOf(adapter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func extractExistingSumsubApplicantID(err error) string {
	if err == nil {
		return ""
	}

	match := sumsubExistingApplicantIDPattern.FindStringSubmatch(err.Error())
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
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
		SourceOfFunds:              req.SourceOfFunds,
		EmploymentStatus:           req.EmploymentStatus,
		ExpectedMonthlyPaymentsUSD: req.ExpectedMonthlyPaymentsUSD,
		AccountPurpose:             req.AccountPurpose,
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

func (s *Service) submitToBridgeFromSumsub(ctx context.Context, customerID string, req *entities.KYCSubmitRequest) entities.KYCProviderResult {
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
		SourceOfFunds:              req.SourceOfFunds,
		EmploymentStatus:           req.EmploymentStatus,
		ExpectedMonthlyPaymentsUSD: req.ExpectedMonthlyPaymentsUSD,
		AccountPurpose:             req.AccountPurpose,
	}

	customer, err := s.bridgeAdapter.UpdateCustomer(ctx, customerID, updateReq)
	if err != nil {
		s.logger.Error("Bridge sync from Sumsub failed",
			zap.Error(err),
			zap.String("user_id", req.UserID.String()))
		return entities.KYCProviderResult{
			Success: false,
			Error:   "Failed to sync Sumsub KYC to Bridge",
		}
	}

	return entities.KYCProviderResult{
		Success: true,
		Status:  string(customer.Status),
	}
}

func (s *Service) submitToAlpaca(ctx context.Context, user *entities.UserProfile, req *entities.KYCSubmitRequest) entities.KYCProviderResult {
	if existingAccountID := strings.TrimSpace(stringValue(user.AlpacaAccountID)); existingAccountID != "" {
		return entities.KYCProviderResult{
			Success: true,
			Status:  fmt.Sprintf("%s:%s", existingAccountID, "existing_account"),
		}
	}

	streetAddress := []string{}
	if street := stringValue(user.AddressStreet); street != "" {
		streetAddress = append(streetAddress, street)
	}

	contactCountry := stringValue(user.AddressCountry)
	if contactCountry == "" {
		contactCountry = req.IssuingCountry
	}
	// Alpaca requires ISO 3166-1 alpha-3; address form may store alpha-2
	contactCountry = toAlpha3(contactCountry)

	// Build Alpaca account request
	alpacaReq := &entities.AlpacaCreateAccountRequest{
		Contact: entities.AlpacaContact{
			EmailAddress:  user.Email,
			PhoneNumber:   stringValue(user.Phone),
			StreetAddress: streetAddress,
			City:          stringValue(user.AddressCity),
			State:         stringValue(user.AddressState),
			PostalCode:    stringValue(user.AddressPostalCode),
			Country:       contactCountry,
		},
		Identity: entities.AlpacaIdentity{
			GivenName:             stringValue(user.FirstName),
			FamilyName:            stringValue(user.LastName),
			DateOfBirth:           formatDate(user.DateOfBirth),
			TaxID:                 req.TaxID,
			TaxIDType:             MapTaxIDTypeToAlpaca(req.TaxIDType),
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
				Agreement: "account_agreement",
				SignedAt:  time.Now().Format(time.RFC3339),
				IPAddress: req.IPAddress,
			},
			{
				Agreement: "customer_agreement",
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
	if req == nil {
		return fmt.Errorf("missing request")
	}
	if req.TaxID == "" {
		return ErrMissingTaxID
	}
	if req.TaxIDType == "" {
		return ErrMissingTaxIDType
	}
	if req.IssuingCountry == "" || !isoAlpha3Pattern.MatchString(req.IssuingCountry) {
		return ErrInvalidIssuingCountry
	}
	if !isTaxIDTypeSupportedForCountry(req.IssuingCountry, req.TaxIDType) {
		return ErrUnsupportedTaxIDType
	}

	switch strings.ToLower(req.TaxIDType) {
	case "ssn":
		if !isValidSSN(req.TaxID) {
			return ErrInvalidSSN
		}
	case "itin":
		if !isValidITIN(req.TaxID) {
			return ErrInvalidITIN
		}
	}

	if req.IDDocumentFront == "" {
		return ErrMissingDocumentFront
	}
	if err := validateKYCImageDataURI(req.IDDocumentFront); err != nil {
		return err
	}
	if req.IDDocumentBack != "" {
		if err := validateKYCImageDataURI(req.IDDocumentBack); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) validateSumsubSessionRequest(req *entities.KYCSumsubSessionRequest) error {
	if req == nil {
		return fmt.Errorf("missing request")
	}
	if !isTaxIDTypeSupportedForCountry(req.IssuingCountry, req.TaxIDType) {
		return ErrUnsupportedTaxIDType
	}
	switch strings.ToLower(req.TaxIDType) {
	case "ssn":
		if !isValidSSN(req.TaxID) {
			return ErrInvalidSSN
		}
	case "itin":
		if !isValidITIN(req.TaxID) {
			return ErrInvalidITIN
		}
	}
	return nil
}

func normalizeKYCSubmitRequest(req *entities.KYCSubmitRequest) {
	if req == nil {
		return
	}
	req.TaxID = strings.TrimSpace(req.TaxID)
	req.TaxIDType = strings.ToLower(strings.TrimSpace(req.TaxIDType))
	req.IssuingCountry = strings.ToUpper(strings.TrimSpace(req.IssuingCountry))
	req.IDDocumentFront = strings.TrimSpace(req.IDDocumentFront)
	req.IDDocumentBack = strings.TrimSpace(req.IDDocumentBack)
}

func scrubKYCSubmitRequest(req *entities.KYCSubmitRequest) {
	if req == nil {
		return
	}
	req.TaxID = ""
	req.IDDocumentFront = ""
	req.IDDocumentBack = ""
}

func collectMissingKYCProfileFields(profile *entities.UserProfile) []string {
	if profile == nil {
		return []string{
			"first_name",
			"last_name",
			"date_of_birth",
			"phone",
			"address_street",
			"address_city",
			"address_postal_code",
			"address_country",
		}
	}

	missing := make([]string, 0, 8)
	if strings.TrimSpace(stringValue(profile.FirstName)) == "" {
		missing = append(missing, "first_name")
	}
	if strings.TrimSpace(stringValue(profile.LastName)) == "" {
		missing = append(missing, "last_name")
	}
	if profile.DateOfBirth == nil {
		missing = append(missing, "date_of_birth")
	}
	if strings.TrimSpace(stringValue(profile.Phone)) == "" {
		missing = append(missing, "phone")
	}
	if strings.TrimSpace(stringValue(profile.AddressStreet)) == "" {
		missing = append(missing, "address_street")
	}
	if strings.TrimSpace(stringValue(profile.AddressCity)) == "" {
		missing = append(missing, "address_city")
	}
	if strings.TrimSpace(stringValue(profile.AddressPostalCode)) == "" {
		missing = append(missing, "address_postal_code")
	}
	if strings.TrimSpace(stringValue(profile.AddressCountry)) == "" {
		missing = append(missing, "address_country")
	}

	return missing
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

func isValidITIN(itin string) bool {
	// ITIN is 9 digits and starts with 9.
	itin = strings.ReplaceAll(itin, "-", "")
	matched, _ := regexp.MatchString(`^9\d{8}$`, itin)
	return matched
}

func validateKYCImageDataURI(dataURI string) error {
	if len(dataURI) > maxImageEncodedBytes {
		return ErrImageTooLarge
	}

	headerMatch := dataURIImagePattern.FindStringSubmatch(dataURI)
	if len(headerMatch) != 2 {
		return ErrInvalidImage
	}
	mimeType := strings.ToLower(strings.TrimSpace(headerMatch[1]))
	if _, ok := allowedKYCImageMIMETypes[mimeType]; !ok {
		return ErrInvalidImage
	}

	parts := strings.SplitN(dataURI, ",", 2)
	if len(parts) != 2 {
		return ErrInvalidImage
	}

	base64Payload := strings.TrimSpace(parts[1])
	if base64Payload == "" {
		return ErrInvalidImage
	}
	if base64.StdEncoding.DecodedLen(len(base64Payload)) > maxImageDecodedBytes {
		return ErrImageTooLarge
	}

	decoded, err := base64.StdEncoding.DecodeString(base64Payload)
	if err != nil {
		return ErrInvalidImage
	}
	if len(decoded) == 0 {
		return ErrInvalidImage
	}

	return nil
}

func mapTaxIDTypeToBridge(taxIDType string) string {
	switch strings.ToLower(strings.TrimSpace(taxIDType)) {
	case "ssn":
		return "ssn"
	case "itin":
		return "itin"
	case "nino":
		return "nino"
	case "utr":
		return "utr"
	case "nin":
		return "nin"
	case "bvn":
		return "bvn"
	case "tin":
		return "tin"
	case "passport":
		return "passport"
	case "national_id":
		return "national_id"
	default:
		return "other"
	}
}

// MapTaxIDTypeToAlpaca converts a Rail tax ID type to Alpaca's format.
func MapTaxIDTypeToAlpaca(taxIDType string) string {
	switch strings.ToLower(strings.TrimSpace(taxIDType)) {
	case "ssn":
		return "USA_SSN"
	case "itin":
		return "USA_ITIN"
	case "nino":
		return "GBR_NINO"
	case "utr":
		return "GBR_UTR"
	case "passport":
		return "PASSPORT"
	case "national_id":
		return "NATIONAL_ID"
	case "nin", "bvn", "tin":
		return "NOT_SPECIFIED"
	default:
		return "NOT_SPECIFIED"
	}
}

// AlpacaTaxIDTypeForCountry returns the Alpaca tax ID type for a country code.
func AlpacaTaxIDTypeForCountry(country string) string {
	if t := GetSupportedTaxIDType(country); t != "" {
		return MapTaxIDTypeToAlpaca(t)
	}
	return "NOT_SPECIFIED"
}

func isTaxIDTypeSupportedForCountry(issuingCountry, taxIDType string) bool {
	supported := GetSupportedTaxIDType(issuingCountry)
	if supported == "" {
		// Unknown country — accept any valid type
		switch strings.ToLower(strings.TrimSpace(taxIDType)) {
		case "ssn", "itin", "nino", "utr", "nin", "bvn", "tin", "passport", "national_id":
			return true
		default:
			return false
		}
	}
	return strings.ToLower(strings.TrimSpace(taxIDType)) == supported
}

// GetSupportedTaxIDType returns the single accepted tax ID type for a country.
// Returns empty string for unknown countries.
func GetSupportedTaxIDType(issuingCountry string) string {
	switch strings.ToUpper(strings.TrimSpace(issuingCountry)) {
	case "USA":
		return "ssn"
	case "GBR":
		return "nino"
	case "NGA":
		return "nin"
	default:
		return ""
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

// toAlpha3 converts an ISO 3166-1 alpha-2 country code to alpha-3.
// If the code is already alpha-3 or unknown it is returned as-is.
var alpha2ToAlpha3 = map[string]string{
	"US": "USA", "GB": "GBR", "CA": "CAN", "AU": "AUS", "DE": "DEU",
	"FR": "FRA", "IT": "ITA", "ES": "ESP", "NL": "NLD", "SE": "SWE",
	"NO": "NOR", "DK": "DNK", "FI": "FIN", "CH": "CHE", "AT": "AUT",
	"BE": "BEL", "IE": "IRL", "PT": "PRT", "GR": "GRC", "PL": "POL",
	"CZ": "CZE", "HU": "HUN", "SK": "SVK", "SI": "SVN", "HR": "HRV",
	"RO": "ROU", "BG": "BGR", "LT": "LTU", "LV": "LVA", "EE": "EST",
	"LU": "LUX", "MT": "MLT", "CY": "CYP", "JP": "JPN", "KR": "KOR",
	"CN": "CHN", "IN": "IND", "SG": "SGP", "HK": "HKG", "TW": "TWN",
	"MY": "MYS", "TH": "THA", "ID": "IDN", "PH": "PHL", "VN": "VNM",
	"BR": "BRA", "MX": "MEX", "AR": "ARG", "CL": "CHL", "CO": "COL",
	"PE": "PER", "UY": "URY", "ZA": "ZAF", "NG": "NGA", "KE": "KEN",
	"EG": "EGY", "MA": "MAR", "GH": "GHA", "TZ": "TZA", "UG": "UGA",
	"AE": "ARE", "SA": "SAU", "IL": "ISR", "TR": "TUR", "NZ": "NZL",
}

func toAlpha3(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) == 3 {
		return code // already alpha-3
	}
	if a3, ok := alpha2ToAlpha3[code]; ok {
		return a3
	}
	return code
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func sumsubWebhookDedupeKey(payload *entities.SumsubWebhookPayload) string {
	eventType := strings.ToLower(strings.TrimSpace(payload.Type))
	if eventType == "" {
		eventType = "unknown"
	}
	if corr := strings.TrimSpace(payload.CorrelationID); corr != "" {
		return fmt.Sprintf("sumsub:%s:corr:%s", eventType, corr)
	}
	if inspectionID := strings.TrimSpace(payload.InspectionID); inspectionID != "" {
		return fmt.Sprintf("sumsub:%s:inspection:%s", eventType, inspectionID)
	}
	if createdAt := strings.TrimSpace(payload.CreatedAtMs); createdAt != "" {
		return fmt.Sprintf("sumsub:%s:applicant:%s:created:%s", eventType, strings.TrimSpace(payload.ApplicantID), createdAt)
	}
	return fmt.Sprintf("sumsub:%s:applicant:%s", eventType, strings.TrimSpace(payload.ApplicantID))
}

func mergeApplicantDataIntoVerification(data map[string]any, applicant *sumsub.ApplicantDataResponse) {
	if data == nil || applicant == nil {
		return
	}

	snapshot := map[string]any{
		"applicant_id":       strings.TrimSpace(applicant.ID),
		"inspection_id":      strings.TrimSpace(applicant.InspectionID),
		"external_user_id":   strings.TrimSpace(applicant.ExternalUserID),
		"level_name":         strings.TrimSpace(applicant.LevelName),
		"review_status":      strings.TrimSpace(applicant.ReviewStatus),
		"fetched_at":         time.Now().UTC().Format(time.RFC3339),
		"first_name":         "",
		"last_name":          "",
		"dob":                "",
		"country":            "",
		"tax_id":             "",
		"id_doc_type":        "",
		"id_doc_country":     "",
		"id_doc_number_tail": "",
	}

	country := ""
	if applicant.Info != nil {
		if firstName := strings.TrimSpace(applicant.Info.FirstName); firstName != "" {
			snapshot["first_name"] = firstName
		}
		if lastName := strings.TrimSpace(applicant.Info.LastName); lastName != "" {
			snapshot["last_name"] = lastName
		}
		if dob := strings.TrimSpace(applicant.Info.DOB); dob != "" {
			snapshot["dob"] = dob
		}

		country = normalizeCountryCode(applicant.Info.Country)
		if country != "" {
			snapshot["country"] = country
		}

		if taxID := strings.TrimSpace(applicant.Info.TaxID); taxID != "" {
			snapshot["tax_id"] = taxID
		}

		if len(applicant.Info.IDDocs) > 0 {
			idDoc := applicant.Info.IDDocs[0]
			docType := normalizeDocType(idDoc.IDDocType)
			docCountry := normalizeCountryCode(idDoc.Country)
			docNumber := strings.TrimSpace(idDoc.Number)

			if docType != "" {
				snapshot["id_doc_type"] = docType
			}
			if docCountry != "" {
				snapshot["id_doc_country"] = docCountry
				if country == "" {
					country = docCountry
				}
			}
			if docNumber != "" {
				snapshot["id_doc_number_tail"] = tail(docNumber, 4)
			}

			if getMapString(data, "tax_id") == "" && docNumber != "" {
				data["tax_id"] = docNumber
			}
			if getMapString(data, "tax_id_type") == "" {
				if inferred := inferTaxIDTypeFromDoc(docType); inferred != "" {
					data["tax_id_type"] = inferred
				}
			}
		}
	}

	if country == "" && applicant.FixedInfo != nil {
		country = normalizeCountryCode(applicant.FixedInfo.Country)
	}
	if country != "" {
		data["issuing_country"] = country
	}

	if taxID, ok := snapshot["tax_id"].(string); ok && strings.TrimSpace(taxID) != "" && getMapString(data, "tax_id") == "" {
		data["tax_id"] = strings.TrimSpace(taxID)
	}
	if getMapString(data, "tax_id_type") == "" {
		if inferred := GetSupportedTaxIDType(country); inferred != "" {
			data["tax_id_type"] = inferred
		}
	}

	if existing, ok := data["sumsub_applicant_snapshot"].(map[string]any); ok {
		for k, v := range snapshot {
			existing[k] = v
		}
	} else {
		data["sumsub_applicant_snapshot"] = snapshot
	}
}

func normalizeCountryCode(country string) string {
	value := strings.ToUpper(strings.TrimSpace(country))
	switch value {
	case "US", "USA":
		return "USA"
	case "GB", "UK", "GBR":
		return "GBR"
	case "NG", "NGA":
		return "NGA"
	default:
		if len(value) == 3 {
			return value
		}
		return ""
	}
}

func normalizeDocType(docType string) string {
	docType = strings.ToUpper(strings.TrimSpace(docType))
	switch docType {
	case "ID_CARD", "IDCARD", "NATIONAL_ID":
		return "NATIONAL_ID"
	case "PASSPORT":
		return "PASSPORT"
	default:
		return docType
	}
}

func inferTaxIDTypeFromDoc(docType string) string {
	switch strings.ToUpper(strings.TrimSpace(docType)) {
	case "PASSPORT":
		return "passport"
	case "NATIONAL_ID", "ID_CARD":
		return "national_id"
	default:
		return ""
	}
}

func tail(value string, n int) string {
	if n <= 0 {
		return ""
	}
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= n {
		return trimmed
	}
	return trimmed[len(trimmed)-n:]
}

func sumsubRequestFromVerificationData(data map[string]any) *entities.KYCSubmitRequest {
	req := &entities.KYCSubmitRequest{
		TaxID:          getMapString(data, "tax_id"),
		TaxIDType:      getMapString(data, "tax_id_type"),
		IssuingCountry: strings.ToUpper(getMapString(data, "issuing_country")),
	}
	if req.TaxIDType == "" {
		req.TaxIDType = "ssn"
	}
	if req.IssuingCountry == "" {
		req.IssuingCountry = "USA"
	}
	req.Disclosures = entities.KYCDisclosures{
		IsControlPerson:             getNestedMapBool(data, "disclosures", "is_control_person"),
		IsAffiliatedExchangeOrFINRA: getNestedMapBool(data, "disclosures", "is_affiliated_exchange_or_finra"),
		IsPoliticallyExposed:        getNestedMapBool(data, "disclosures", "is_politically_exposed"),
		ImmediateFamilyExposed:      getNestedMapBool(data, "disclosures", "immediate_family_exposed"),
	}
	return req
}

func getMapString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, ok := data[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func getNestedMapBool(data map[string]any, parent, child string) bool {
	if data == nil {
		return false
	}
	rawParent, ok := data[parent]
	if !ok || rawParent == nil {
		return false
	}
	parentMap, ok := rawParent.(map[string]any)
	if !ok {
		return false
	}
	rawChild, ok := parentMap[child]
	if !ok || rawChild == nil {
		return false
	}
	switch v := rawChild.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func extractRejectReasons(labels []entities.SumsubRejectLabel) []string {
	if len(labels) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(labels))
	for _, label := range labels {
		switch {
		case strings.TrimSpace(label.Description) != "":
			reasons = append(reasons, strings.TrimSpace(label.Description))
		case strings.TrimSpace(label.Label) != "":
			reasons = append(reasons, strings.TrimSpace(label.Label))
		}
	}
	return reasons
}

func isSubmissionNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

// GetKYCStatus returns the current KYC status and capabilities for a user.
func (s *Service) GetKYCStatus(ctx context.Context, userID uuid.UUID) (*entities.KYCStatusResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	overall := determineOverallStatus(user)

	// If still pending and user has a Didit session, poll Didit directly as webhook fallback.
	if overall == "pending" && s.diditAdapter != nil && user.KYCProviderRef != nil && *user.KYCProviderRef != "" {
		decision, err := s.diditAdapter.GetSessionDecision(ctx, *user.KYCProviderRef)
		if err != nil {
			s.logger.Warn("Didit poll: failed to get session decision",
				zap.Error(err), zap.String("session_id", *user.KYCProviderRef))
		} else if decision != nil {
			switch decision.Status {
			case entities.DiditStatusApproved:
				payload := &entities.DiditWebhookPayload{SessionID: decision.SessionID, Status: decision.Status}
				if procErr := s.ProcessDiditWebhook(ctx, payload); procErr != nil {
					s.logger.Warn("Didit poll: failed to process approved status", zap.Error(procErr))
				} else {
					overall = "approved"
					if freshUser, reloadErr := s.userRepo.GetByID(ctx, userID); reloadErr != nil {
						s.logger.Error("Didit poll: failed to reload user after approval", zap.Error(reloadErr))
					} else {
						user = freshUser
					}
				}
			case entities.DiditStatusDeclined:
				payload := &entities.DiditWebhookPayload{SessionID: decision.SessionID, Status: decision.Status}
				if procErr := s.ProcessDiditWebhook(ctx, payload); procErr != nil {
					s.logger.Warn("Didit poll: failed to process declined status", zap.Error(procErr))
				} else {
					overall = "rejected"
					if freshUser, reloadErr := s.userRepo.GetByID(ctx, userID); reloadErr != nil {
						s.logger.Error("Didit poll: failed to reload user after decline", zap.Error(reloadErr))
					} else {
						user = freshUser
					}
				}
			}
		}
	}
	hasSubmitted := overall != "not_started"

	response := &entities.KYCStatusResponse{
		UserID:          userID,
		Status:          overall,
		OverallStatus:   overall,
		Verified:        overall == "approved",
		HasSubmitted:    hasSubmitted,
		RequiresKYC:     overall != "approved",
		LastSubmittedAt: user.KYCSubmittedAt,
		ApprovedAt:      user.KYCApprovedAt,
		RejectionReason: user.KYCRejectionReason,
		Bridge: entities.KYCProviderStatus{
			Status:      stringValue(user.BridgeKYCStatus),
			SubmittedAt: user.KYCSubmittedAt,
			ApprovedAt:  user.KYCApprovedAt,
		},
		Alpaca: entities.KYCProviderStatus{
			Status: "skipped", // Alpaca disabled for v1
		},
		Capabilities: entities.KYCCapabilities{
			CanDepositCrypto: true, // Always true after signup
			CanDepositFiat:   user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "active",
			CanUseCard:       user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "active",
			CanInvest:        false, // Alpaca disabled for v1
		},
	}

	// Populate supported tax ID type from user's profile country
	if profile, err := s.userRepo.GetProfileByUserID(ctx, userID); err == nil && profile != nil && profile.Country != nil {
		response.SupportedTaxIDType = GetSupportedTaxIDType(*profile.Country)
	}

	return response, nil
}

func determineOverallStatus(user *entities.User) string {
	if user == nil {
		return "not_started"
	}

	kycStatus := strings.ToLower(strings.TrimSpace(user.KYCStatus))
	bridgeStatus := strings.ToLower(strings.TrimSpace(stringValue(user.BridgeKYCStatus)))

	// Primary approval: Sumsub approval or an active Bridge customer both unlock the account.
	if kycStatus == "approved" || bridgeStatus == "active" {
		return "approved"
	}
	if kycStatus == "rejected" || kycStatus == "expired" || bridgeStatus == "rejected" {
		return "rejected"
	}
	if user.KYCSubmittedAt != nil {
		return "pending"
	}
	if kycStatus == "processing" {
		return "pending"
	}
	switch bridgeStatus {
	case "pending", "processing", "under_review", "in_review":
		return "pending"
	}
	return "not_started"
}

// encodeProviderRetryPayload serialises verification_data as JSON, injecting userID so retry handlers can locate the user.
func encodeProviderRetryPayload(verificationData map[string]any, userID string) ([]byte, error) {
	merged := make(map[string]any, len(verificationData)+1)
	for k, v := range verificationData {
		merged[k] = v
	}
	merged["user_id"] = userID
	return json.Marshal(merged)
}

// RetryBridgeSync re-fetches Sumsub images and re-submits to Bridge using the job payload.
func (s *Service) RetryBridgeSync(ctx context.Context, payload []byte) error {
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("invalid bridge retry payload: %w", err)
	}

	userIDStr := getMapString(data, "user_id")
	if userIDStr == "" {
		return fmt.Errorf("bridge retry payload missing user_id")
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return fmt.Errorf("bridge retry payload invalid user_id: %w", err)
	}

	profile, err := s.userRepo.GetProfileByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user profile for bridge retry: %w", err)
	}
	if profile.BridgeCustomerID == nil || *profile.BridgeCustomerID == "" {
		return fmt.Errorf("no bridge customer ID for user %s", userIDStr)
	}

	req := sumsubRequestFromVerificationData(data)
	req.UserID = userID
	req.IPAddress = "retry"

	applicantID := getMapString(data, "applicant_id")
	inspectionID := getMapString(data, "sumsub_inspection_id")
	if applicantID == "" {
		if snap, ok := data["sumsub_applicant_snapshot"].(map[string]any); ok {
			applicantID = getMapString(snap, "applicant_id")
			inspectionID = getMapString(snap, "inspection_id")
		}
	}
	s.fetchAndAttachSumsubImages(ctx, applicantID, inspectionID, req)

	result := s.submitToBridgeFromSumsub(ctx, *profile.BridgeCustomerID, req)
	req.IDDocumentFront = ""
	req.IDDocumentBack = ""

	if !result.Success {
		return fmt.Errorf("bridge retry failed: %s", result.Error)
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user for bridge retry update: %w", err)
	}
	bridgeStatus := "pending" // Bridge activates asynchronously via webhook
	user.BridgeKYCStatus = &bridgeStatus
	return s.userRepo.Update(ctx, user)
}

// RetryAlpacaSync re-submits to Alpaca using the job payload.
func (s *Service) RetryAlpacaSync(ctx context.Context, payload []byte) error {
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("invalid alpaca retry payload: %w", err)
	}

	userIDStr := getMapString(data, "user_id")
	if userIDStr == "" {
		return fmt.Errorf("alpaca retry payload missing user_id")
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return fmt.Errorf("alpaca retry payload invalid user_id: %w", err)
	}

	profile, err := s.userRepo.GetProfileByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user profile for alpaca retry: %w", err)
	}

	req := sumsubRequestFromVerificationData(data)
	req.UserID = userID
	req.IPAddress = "retry"

	result := s.submitToAlpaca(ctx, profile, req)
	if !result.Success {
		return fmt.Errorf("alpaca retry failed: %s", result.Error)
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user for alpaca retry update: %w", err)
	}
	now := time.Now()
	user.KYCStatus = string(entities.KYCStatusApproved)
	user.KYCApprovedAt = &now
	user.KYCRejectionReason = nil
	parts := strings.Split(result.Status, ":")
	if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
		user.AlpacaAccountID = &parts[0]
	}
	return s.userRepo.Update(ctx, user)
}

// RefreshSumsubToken issues a new short-lived WebSDK access token for the user's
// existing Sumsub applicant. Used when the SDK token expires mid-flow.
func (s *Service) RefreshSumsubToken(ctx context.Context, userID uuid.UUID) (*entities.KYCSumsubSessionResponse, error) {
	if isNilSumsubAdapter(s.sumsubAdapter) {
		return nil, ErrSumsubNotConfigured
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user.KYCProviderRef == nil || strings.TrimSpace(*user.KYCProviderRef) == "" {
		return nil, fmt.Errorf("no existing sumsub applicant for user")
	}
	applicantID := strings.TrimSpace(*user.KYCProviderRef)

	levelName := s.sumsubLevelName
	if levelName == "" {
		levelName = "basic-kyc"
	}

	accessToken, err := s.sumsubAdapter.CreateAccessToken(ctx, applicantID, userID.String(), levelName, 3600)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh sumsub access token: %w", err)
	}

	return &entities.KYCSumsubSessionResponse{
		Status:      "pending",
		ApplicantID: applicantID,
		Token:       accessToken.Token,
		LevelName:   levelName,
	}, nil
}

// ---------------------------------------------------------------------------
// Didit KYC provider methods
// ---------------------------------------------------------------------------

// StartDiditSession creates a Didit verification session and persists the submission.
func (s *Service) StartDiditSession(ctx context.Context, userID uuid.UUID, req *entities.KYCDigitSessionRequest) (*entities.KYCDigitSessionResponse, error) {
	if s.diditAdapter == nil {
		return nil, ErrDiditNotConfigured
	}
	if req == nil {
		return nil, fmt.Errorf("missing request")
	}
	if !isTaxIDTypeSupportedForCountry(req.IssuingCountry, req.TaxIDType) {
		return nil, ErrUnsupportedTaxIDType
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	profile, err := s.userRepo.GetProfileByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user profile: %w", err)
	}
	if profile.BridgeCustomerID == nil || *profile.BridgeCustomerID == "" {
		return nil, ErrNoBridgeCustomer
	}
	if profile.KYCStatus == "approved" {
		return nil, ErrKYCAlreadyApproved
	}

	// Submit source of funds to Bridge (non-fatal).
	if req.SourceOfFunds != "" {
		bridgeUpdateReq := &bridge.UpdateCustomerRequest{
			SourceOfFunds:              req.SourceOfFunds,
			EmploymentStatus:           req.EmploymentStatus,
			ExpectedMonthlyPaymentsUSD: req.ExpectedMonthlyPaymentsUSD,
			AccountPurpose:             req.AccountPurpose,
			AccountPurposeOther:        req.AccountPurposeOther,
			MostRecentOccupation:       req.MostRecentOccupation,
			ActingAsIntermediary:       req.ActingAsIntermediary,
		}
		if _, bridgeErr := s.bridgeAdapter.UpdateCustomer(ctx, *profile.BridgeCustomerID, bridgeUpdateReq); bridgeErr != nil {
			s.logger.Warn("Failed to submit source of funds to Bridge",
				zap.Error(bridgeErr), zap.String("user_id", userID.String()))
		}
	}

	sess, err := s.diditAdapter.CreateSession(ctx, userID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create didit session: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(30 * 24 * time.Hour)
	verificationData := map[string]any{
		"didit_session_id": sess.SessionID,
		"tax_id":           req.TaxID,
		"tax_id_type":      req.TaxIDType,
		"issuing_country":  req.IssuingCountry,
		"disclosures": map[string]any{
			"is_control_person":               req.Disclosures.IsControlPerson,
			"is_affiliated_exchange_or_finra": req.Disclosures.IsAffiliatedExchangeOrFINRA,
			"is_politically_exposed":          req.Disclosures.IsPoliticallyExposed,
			"immediate_family_exposed":        req.Disclosures.ImmediateFamilyExposed,
		},
	}

	existingSubmission, existingErr := s.kycSubmissionRepo.GetByProviderRef(ctx, sess.SessionID)
	if existingErr != nil && !isSubmissionNotFoundErr(existingErr) {
		return nil, fmt.Errorf("failed to load existing didit submission: %w", existingErr)
	}
	if existingErr != nil {
		submission := &entities.KYCSubmission{
			ID:               uuid.New(),
			UserID:           userID,
			Provider:         "didit",
			ProviderRef:      sess.SessionID,
			SubmissionType:   "didit_sdk",
			Status:           entities.KYCStatusProcessing,
			VerificationData: verificationData,
			SubmittedAt:      now,
			ExpiresAt:        &expiresAt,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := s.kycSubmissionRepo.Create(ctx, submission); err != nil {
			return nil, fmt.Errorf("failed to persist didit submission: %w", err)
		}
	} else {
		existingSubmission.Status = entities.KYCStatusProcessing
		existingSubmission.VerificationData = verificationData
		existingSubmission.SubmittedAt = now
		existingSubmission.ExpiresAt = &expiresAt
		existingSubmission.UpdatedAt = now
		if err := s.kycSubmissionRepo.Update(ctx, existingSubmission); err != nil {
			return nil, fmt.Errorf("failed to update existing didit submission: %w", err)
		}
	}

	providerRef := sess.SessionID
	user.KYCProviderRef = &providerRef
	if err := s.userRepo.Update(ctx, user); err != nil {
		s.logger.Warn("Failed to update user after Didit session creation",
			zap.Error(err), zap.String("user_id", userID.String()))
	}

	return &entities.KYCDigitSessionResponse{
		Status:       "pending",
		SessionID:    sess.SessionID,
		SessionToken: sess.SessionToken,
		URL:          sess.URL,
	}, nil
}

// VerifyDiditWebhookSignature validates Didit webhook X-Signature-V2 + X-Timestamp.
func (s *Service) VerifyDiditWebhookSignature(body []byte, signatureHeader, timestampHeader string) error {
	if s.diditAdapter == nil {
		return ErrDiditNotConfigured
	}
	return s.diditAdapter.VerifyWebhookSignature(body, signatureHeader, timestampHeader)
}

// EnqueueDiditWebhook deduplicates and queues a Didit webhook for async processing.
func (s *Service) EnqueueDiditWebhook(ctx context.Context, payload *entities.DiditWebhookPayload, rawPayload []byte) (bool, error) {
	if payload == nil {
		return false, fmt.Errorf("missing didit payload")
	}
	if strings.TrimSpace(payload.SessionID) == "" {
		return false, fmt.Errorf("missing didit session_id")
	}
	if s.kycSyncJobRepo == nil {
		return false, fmt.Errorf("kyc sync job repository is not configured")
	}

	dedupeKey := fmt.Sprintf("didit:%s:%s:%s", strings.TrimSpace(payload.WebhookType), strings.TrimSpace(payload.Status), strings.TrimSpace(payload.SessionID))
	now := time.Now()

	job := &entities.KYCSyncJob{
		ID:            uuid.New(),
		DedupeKey:     dedupeKey,
		ApplicantID:   strings.TrimSpace(payload.SessionID),
		CorrelationID: strings.TrimSpace(payload.VendorData),
		EventType:     strings.TrimSpace(payload.WebhookType),
		Payload:       append([]byte(nil), rawPayload...),
		Status:        entities.KYCSyncJobStatusPending,
		AttemptCount:  0,
		MaxAttempts:   defaultKYCSyncMaxAttempts,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := job.Validate(); err != nil {
		return false, err
	}

	queued, err := s.kycSyncJobRepo.Enqueue(ctx, job)
	if err != nil {
		return false, err
	}
	return queued, nil
}

// ProcessDiditWebhook applies Didit verification outcomes and syncs providers.
func (s *Service) ProcessDiditWebhook(ctx context.Context, payload *entities.DiditWebhookPayload) error {
	if payload == nil {
		return fmt.Errorf("missing didit payload")
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return fmt.Errorf("missing didit session_id")
	}

	submission, err := s.kycSubmissionRepo.GetByProviderRef(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load KYC submission for session %s: %w", sessionID, err)
	}

	switch payload.Status {
	case entities.DiditStatusApproved:
		return s.processDiditApproved(ctx, submission, payload)
	case entities.DiditStatusDeclined:
		return s.processDiditRejected(ctx, submission, payload)
	case entities.DiditStatusInReview, entities.DiditStatusInProgress, entities.DiditStatusResubmitted:
		return s.markDiditProcessing(ctx, submission, payload)
	case entities.DiditStatusExpired, entities.DiditStatusAbandoned, entities.DiditStatusKYCExpired:
		return s.markDiditExpired(ctx, submission, payload)
	default:
		return s.markDiditProcessing(ctx, submission, payload)
	}
}

func (s *Service) processDiditApproved(ctx context.Context, submission *entities.KYCSubmission, payload *entities.DiditWebhookPayload) error {
	if submission.Status == entities.KYCStatusApproved {
		return nil
	}

	// Hydrate from Didit decision API.
	s.hydrateSubmissionFromDidit(ctx, submission)

	user, err := s.userRepo.GetByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	profile, err := s.userRepo.GetProfileByUserID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user profile: %w", err)
	}

	// Bridge sync — Didit hosts document images, so we submit KYC data without images.
	bridgeResult := entities.KYCProviderResult{Success: false, Status: "skipped", Error: "Bridge customer not found"}
	if profile.BridgeCustomerID != nil && *profile.BridgeCustomerID != "" {
		bridgeResult = s.submitToBridgeFromDidit(ctx, *profile.BridgeCustomerID, submission)
	}

	now := time.Now()
	if bridgeResult.Success {
		bridgeStatus := "pending"
		user.BridgeKYCStatus = &bridgeStatus
	} else if profile.BridgeCustomerID != nil && *profile.BridgeCustomerID != "" {
		if s.kycSyncJobRepo != nil {
			retryPayload, _ := encodeProviderRetryPayload(submission.VerificationData, submission.UserID.String())
			if _, enqErr := s.kycSyncJobRepo.EnqueueProviderRetry(ctx, submission.UserID.String(), "bridge", retryPayload); enqErr != nil {
				s.logger.Warn("Failed to enqueue Bridge retry job",
					zap.Error(enqErr), zap.String("user_id", submission.UserID.String()))
			}
		}
	}

	user.KYCStatus = string(entities.KYCStatusApproved)
	user.KYCApprovedAt = &now
	user.KYCRejectionReason = nil
	if user.KYCSubmittedAt == nil {
		user.KYCSubmittedAt = &now
	}
	user.OnboardingStatus = entities.OnboardingStatusCompleted

	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user after didit approval: %w", err)
	}

	submission.UpdatedAt = now
	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	submission.VerificationData["didit_webhook_type"] = payload.WebhookType
	submission.VerificationData["didit_status"] = payload.Status
	submission.VerificationData["bridge_sync"] = map[string]any{
		"success": bridgeResult.Success,
		"status":  bridgeResult.Status,
		"error":   bridgeResult.Error,
	}

	if bridgeResult.Success {
		submission.MarkReviewed(entities.KYCStatusApproved, nil)
	} else {
		submission.Status = entities.KYCStatusProcessing
	}

	if err := s.kycSubmissionRepo.Update(ctx, submission); err != nil {
		return fmt.Errorf("failed to update didit submission: %w", err)
	}
	return nil
}

func (s *Service) processDiditRejected(ctx context.Context, submission *entities.KYCSubmission, payload *entities.DiditWebhookPayload) error {
	user, err := s.userRepo.GetByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	now := time.Now()
	user.KYCStatus = string(entities.KYCStatusRejected)
	user.KYCApprovedAt = nil
	user.KYCSubmittedAt = &now
	user.OnboardingStatus = entities.OnboardingStatusKYCRejected
	reason := "Verification declined by Didit"
	user.KYCRejectionReason = &reason

	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user after didit rejection: %w", err)
	}

	submission.MarkReviewed(entities.KYCStatusRejected, []string{reason})
	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	submission.VerificationData["didit_webhook_type"] = payload.WebhookType
	submission.VerificationData["didit_status"] = payload.Status
	if err := s.kycSubmissionRepo.Update(ctx, submission); err != nil {
		return fmt.Errorf("failed to update rejected didit submission: %w", err)
	}
	return nil
}

func (s *Service) markDiditProcessing(ctx context.Context, submission *entities.KYCSubmission, payload *entities.DiditWebhookPayload) error {
	now := time.Now()
	submission.Status = entities.KYCStatusProcessing
	submission.UpdatedAt = now
	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	submission.VerificationData["didit_webhook_type"] = payload.WebhookType
	submission.VerificationData["didit_status"] = payload.Status
	if err := s.kycSubmissionRepo.Update(ctx, submission); err != nil {
		return err
	}

	user, err := s.userRepo.GetByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user for processing state: %w", err)
	}
	if user.KYCSubmittedAt == nil {
		user.KYCSubmittedAt = &now
	}
	if user.KYCStatus != string(entities.KYCStatusApproved) &&
		user.KYCStatus != string(entities.KYCStatusRejected) {
		user.KYCStatus = string(entities.KYCStatusProcessing)
	}
	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user processing state: %w", err)
	}
	return nil
}

func (s *Service) markDiditExpired(ctx context.Context, submission *entities.KYCSubmission, payload *entities.DiditWebhookPayload) error {
	now := time.Now()
	submission.Status = entities.KYCStatusExpired
	submission.UpdatedAt = now
	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	submission.VerificationData["didit_webhook_type"] = payload.WebhookType
	submission.VerificationData["didit_status"] = payload.Status
	if err := s.kycSubmissionRepo.Update(ctx, submission); err != nil {
		return err
	}

	// Reset user KYC status so they can retry.
	user, err := s.userRepo.GetByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user for expired state: %w", err)
	}
	if user.KYCStatus != string(entities.KYCStatusApproved) {
		user.KYCStatus = string(entities.KYCStatusExpired)
		user.KYCProviderRef = nil
		if err := s.userRepo.Update(ctx, user); err != nil {
			return fmt.Errorf("failed to update user expired state: %w", err)
		}
	}
	return nil
}

// hydrateSubmissionFromDidit fetches the session decision and merges ID data into verification data.
func (s *Service) hydrateSubmissionFromDidit(ctx context.Context, submission *entities.KYCSubmission) {
	if s.diditAdapter == nil || submission.ProviderRef == "" {
		return
	}
	decision, err := s.diditAdapter.GetSessionDecision(ctx, submission.ProviderRef)
	if err != nil {
		s.logger.Warn("Failed to fetch Didit session decision",
			zap.Error(err), zap.String("session_id", submission.ProviderRef))
		return
	}
	if submission.VerificationData == nil {
		submission.VerificationData = map[string]any{}
	}
	if len(decision.IDVerifications) > 0 {
		v := decision.IDVerifications[0]
		submission.VerificationData["didit_first_name"] = v.FirstName
		submission.VerificationData["didit_last_name"] = v.LastName
		submission.VerificationData["didit_dob"] = v.DateOfBirth
		submission.VerificationData["didit_doc_type"] = v.DocumentType
		submission.VerificationData["didit_issuing_state"] = v.IssuingState
		if v.DocumentNumber != "" {
			submission.VerificationData["didit_doc_number_tail"] = tail(v.DocumentNumber, 4)
		}
	}
}

// submitToBridgeFromDidit forwards KYC data to Bridge after Didit approval (no images).
func (s *Service) submitToBridgeFromDidit(ctx context.Context, bridgeCustomerID string, submission *entities.KYCSubmission) entities.KYCProviderResult {
	data := submission.VerificationData
	if data == nil {
		return entities.KYCProviderResult{Success: false, Status: "failed", Error: "no verification data"}
	}

	taxID, _ := data["tax_id"].(string)
	taxIDType, _ := data["tax_id_type"].(string)
	country, _ := data["issuing_country"].(string)

	if taxID == "" {
		return entities.KYCProviderResult{Success: false, Status: "failed", Error: "missing tax_id"}
	}

	req := &bridge.UpdateCustomerRequest{
		IdentifyingInformation: []bridge.IdentifyingInfo{
			{
				Type:           mapTaxIDTypeToBridge(taxIDType),
				IssuingCountry: strings.ToLower(country),
				Number:         taxID,
			},
		},
	}

	customer, err := s.bridgeAdapter.UpdateCustomer(ctx, bridgeCustomerID, req)
	if err != nil {
		s.logger.Warn("Failed to sync Didit KYC to Bridge",
			zap.Error(err), zap.String("bridge_customer_id", bridgeCustomerID))
		return entities.KYCProviderResult{Success: false, Status: "failed", Error: err.Error()}
	}
	return entities.KYCProviderResult{Success: true, Status: string(customer.Status)}
}
