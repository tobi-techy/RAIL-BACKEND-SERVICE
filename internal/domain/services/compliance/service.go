package compliance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Risk tiers determine how transactions are screened.
type RiskTier int

const (
	TierMedium RiskTier = 2 // Sync with short timeout — fallback to approve on timeout only
	TierHigh   RiskTier = 3 // Sync strict — block and wait, fail closed
)

// Thresholds
var (
	highTierThreshold = decimal.NewFromInt(5000)
	matureAccountAge  = 90 * 24 * time.Hour // 3 months
)

// DiditComplianceClient is the subset of the Didit client used for compliance.
type DiditComplianceClient interface {
	CreateTransaction(ctx context.Context, req *didit.CreateTransactionRequest) (*didit.TransactionResponse, error)
	GetTransaction(ctx context.Context, txnUUID string) (*didit.TransactionResponse, error)
	ScreenAML(ctx context.Context, req *didit.AMLScreeningRequest) (*didit.AMLScreeningResponse, error)
}

// ComplianceRepo persists screening and alert records.
type ComplianceRepo interface {
	CreateScreening(ctx context.Context, s *entities.ComplianceScreening) error
	CreateAlert(ctx context.Context, a *entities.ComplianceAlert) error
	UpdateScreeningStatus(ctx context.Context, diditTxnUUID, status, severity string, score int) error
	GetScreeningByDiditUUID(ctx context.Context, diditTxnUUID string) (*entities.ComplianceScreening, error)
}

// UserLookup fetches user data for risk tier calculation.
type UserLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// UserFreezer freezes a user account on compliance violations.
type UserFreezer interface {
	FreezeUser(ctx context.Context, userID uuid.UUID, reason string) error
}

// Service orchestrates Didit transaction monitoring and AML screening.
type Service struct {
	didit  DiditComplianceClient
	repo   ComplianceRepo
	users  UserLookup
	freeze UserFreezer
	logger *zap.Logger
	wg     sync.WaitGroup
}

func NewService(didit DiditComplianceClient, repo ComplianceRepo, logger *zap.Logger) *Service {
	return &Service{didit: didit, repo: repo, logger: logger}
}

func (s *Service) SetUserLookup(u UserLookup)  { s.users = u }
func (s *Service) SetUserFreezer(f UserFreezer) { s.freeze = f }

// Shutdown drains in-flight async screenings.
func (s *Service) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// classifyRisk determines the screening tier.
// All outbound transactions are at least TierHigh.
// Inbound: >$5000 or new account (<3 months) = TierHigh, else TierMedium.
func (s *Service) classifyRisk(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, direction string) RiskTier {
	// All outbound (withdrawals) are high risk
	if direction == "outbound" {
		return TierHigh
	}

	// Large inbound amounts are high risk
	if amount.GreaterThan(highTierThreshold) {
		return TierHigh
	}

	// Check account age — mature accounts get medium tier
	if s.users != nil {
		profile, err := s.users.GetByID(ctx, userID)
		if err == nil && time.Since(profile.CreatedAt) >= matureAccountAge {
			return TierMedium
		}
	}

	// New accounts or lookup failure = high risk
	return TierHigh
}

// ScreenTransaction submits a deposit or withdrawal to Didit for monitoring.
//   - TierMedium: sync with 5s timeout, approve ONLY on context.DeadlineExceeded
//   - TierHigh:   sync strict, fail closed on any error
//
// Returns "APPROVED" or an error. Callers must reject anything that isn't "APPROVED".
func (s *Service) ScreenTransaction(ctx context.Context, userID uuid.UUID, referenceID, direction string, amount decimal.Decimal, currency, userFullName string) (string, error) {
	tier := s.classifyRisk(ctx, userID, amount, direction)

	s.logger.Info("Compliance screening",
		zap.String("user_id", userID.String()),
		zap.String("direction", direction),
		zap.String("amount", amount.StringFixed(2)),
		zap.Int("tier", int(tier)),
		zap.String("ref", referenceID))

	switch tier {
	case TierMedium:
		// Sync with short timeout — approve ONLY on timeout, not on other errors
		screenCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		status, err := s.screenAndRecord(screenCtx, userID, referenceID, direction, amount, currency, userFullName)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				s.logger.Warn("Medium-tier screening timed out, approving with async follow-up",
					zap.String("user_id", userID.String()), zap.String("ref", referenceID))
				s.wg.Add(1)
				go func() {
					defer s.wg.Done()
					bgCtx, bgCancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer bgCancel()
					s.screenAndRecord(bgCtx, userID, referenceID, direction, amount, currency, userFullName)
				}()
				return "APPROVED", nil
			}
			// Hard failure — fail closed
			return "IN_REVIEW", fmt.Errorf("compliance screening failed: %w", err)
		}
		return status, nil

	default: // TierHigh
		status, err := s.screenAndRecord(ctx, userID, referenceID, direction, amount, currency, userFullName)
		if err != nil {
			return "IN_REVIEW", fmt.Errorf("compliance screening failed: %w", err)
		}
		return status, nil
	}
}

// screenAndRecord calls Didit, persists the screening, and creates alerts.
func (s *Service) screenAndRecord(ctx context.Context, userID uuid.UUID, referenceID, direction string, amount decimal.Decimal, currency, userFullName string) (string, error) {
	amlFlag := true
	req := &didit.CreateTransactionRequest{
		TransactionID:       referenceID,
		TransactionCategory: "finance",
		TransactionDetails: didit.TransactionDetails{
			Direction:    direction,
			Amount:       amount.InexactFloat64(),
			Currency:     currency,
			CurrencyKind: "fiat",
		},
		Subject: didit.TransactionSubject{
			VendorData: userID.String(),
			FullName:   userFullName,
			EntityType: "person",
		},
		TransactionAt:       time.Now().UTC().Format(time.RFC3339),
		IncludeAMLScreening: &amlFlag,
	}

	resp, err := s.didit.CreateTransaction(ctx, req)
	if err != nil {
		return "", err
	}

	now := time.Now()
	screening := &entities.ComplianceScreening{
		ID:            uuid.New(),
		UserID:        userID,
		ScreeningType: "transaction",
		Direction:     direction,
		Amount:        amount.StringFixed(2),
		Currency:      currency,
		DiditTxnUUID:  resp.UUID,
		ReferenceID:   referenceID,
		Status:        resp.Status,
		Score:         resp.Score,
		Severity:      resp.Severity,
		CreatedAt:     now,
	}
	if err := s.repo.CreateScreening(ctx, screening); err != nil {
		s.logger.Error("Failed to persist screening", zap.Error(err))
		return "", fmt.Errorf("persist screening: %w", err)
	}

	if resp.Status != "APPROVED" {
		alertType := "transaction_flagged"
		if resp.Status == "DECLINED" {
			alertType = "transaction_declined"
		}
		alert := &entities.ComplianceAlert{
			ID:          uuid.New(),
			UserID:      userID,
			ScreeningID: screening.ID,
			AlertType:   alertType,
			Severity:    resp.Severity,
			Description: fmt.Sprintf("%s %s $%s %s — score %d, status %s", direction, currency, amount.StringFixed(2), referenceID, resp.Score, resp.Status),
			Status:      "open",
			CreatedAt:   now,
		}
		if err := s.repo.CreateAlert(ctx, alert); err != nil {
			s.logger.Error("Failed to persist alert", zap.Error(err))
		}
	}

	return resp.Status, nil
}

// ScreenUserAML runs standalone AML screening on a user (at KYC approval).
func (s *Service) ScreenUserAML(ctx context.Context, userID uuid.UUID, fullName, dob, nationality, docNumber string) (string, error) {
	req := &didit.AMLScreeningRequest{
		FullName:                 fullName,
		EntityType:               "person",
		DateOfBirth:              dob,
		Nationality:              nationality,
		DocumentNumber:           docNumber,
		IncludeAdverseMedia:      true,
		IncludeOngoingMonitoring: true,
		SaveAPIRequest:           true,
		VendorData:               userID.String(),
	}

	resp, err := s.didit.ScreenAML(ctx, req)
	if err != nil {
		return "", err
	}

	now := time.Now()
	screening := &entities.ComplianceScreening{
		ID:            uuid.New(),
		UserID:        userID,
		ScreeningType: "aml_kyc",
		DiditTxnUUID:  resp.RequestID,
		ReferenceID:   "kyc-" + userID.String(),
		Status:        resp.AML.Status,
		Score:         resp.AML.Score,
		Details: map[string]interface{}{
			"total_hits": resp.AML.TotalHits,
			"warnings":   resp.AML.Warnings,
		},
		CreatedAt: now,
	}
	if err := s.repo.CreateScreening(ctx, screening); err != nil {
		s.logger.Error("Failed to persist AML screening", zap.Error(err))
	}

	if resp.AML.Status != "Approved" {
		alertType := "aml_hit"
		for _, hit := range resp.AML.Hits {
			for _, ds := range hit.Datasets {
				if ds == "Sanctions" {
					alertType = "sanctions_match"
					break
				}
			}
		}
		alert := &entities.ComplianceAlert{
			ID:          uuid.New(),
			UserID:      userID,
			ScreeningID: screening.ID,
			AlertType:   alertType,
			Severity:    "HIGH",
			Description: fmt.Sprintf("AML screening: %s — %d hits, score %d", resp.AML.Status, resp.AML.TotalHits, resp.AML.Score),
			Status:      "open",
			CreatedAt:   now,
		}
		if err := s.repo.CreateAlert(ctx, alert); err != nil {
			s.logger.Error("Failed to persist AML alert", zap.Error(err))
		}
	}

	return resp.AML.Status, nil
}

// HandleTransactionWebhook processes Didit transaction.status.updated webhooks.
func (s *Service) HandleTransactionWebhook(ctx context.Context, payload *didit.TransactionWebhookPayload) error {
	if payload == nil {
		return fmt.Errorf("nil payload")
	}

	// Idempotency: skip if already at this status
	existing, err := s.repo.GetScreeningByDiditUUID(ctx, payload.UUID)
	if err != nil {
		return fmt.Errorf("load screening: %w", err)
	}
	if existing.Status == payload.Status {
		return nil // Already processed
	}

	if err := s.repo.UpdateScreeningStatus(ctx, payload.UUID, payload.Status, payload.Severity, payload.Score); err != nil {
		return fmt.Errorf("update screening status: %w", err)
	}

	if payload.Status == "DECLINED" && s.freeze != nil {
		reason := fmt.Sprintf("Transaction declined by compliance screening (score: %d, severity: %s)", payload.Score, payload.Severity)
		if err := s.freeze.FreezeUser(ctx, existing.UserID, reason); err != nil {
			return fmt.Errorf("freeze user: %w", err)
		}
		s.logger.Warn("User frozen due to declined transaction",
			zap.String("user_id", existing.UserID.String()),
			zap.String("didit_uuid", payload.UUID))
	}

	return nil
}
