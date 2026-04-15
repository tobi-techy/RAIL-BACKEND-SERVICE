package compliance

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
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

// UserFreezer freezes a user account on compliance violations.
type UserFreezer interface {
	FreezeUser(ctx context.Context, userID uuid.UUID, reason string) error
}

// Service orchestrates Didit transaction monitoring and AML screening.
type Service struct {
	didit  DiditComplianceClient
	repo   ComplianceRepo
	freeze UserFreezer
	logger *zap.Logger
}

func NewService(didit DiditComplianceClient, repo ComplianceRepo, logger *zap.Logger) *Service {
	return &Service{didit: didit, repo: repo, logger: logger}
}

func (s *Service) SetUserFreezer(f UserFreezer) { s.freeze = f }

// ScreenTransaction submits a deposit or withdrawal to Didit for monitoring.
// Returns the Didit status. Callers should block on DECLINED.
func (s *Service) ScreenTransaction(ctx context.Context, userID uuid.UUID, referenceID, direction string, amount decimal.Decimal, currency, userFullName string) (string, error) {
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
		s.logger.Error("Didit transaction screening failed",
			zap.Error(err), zap.String("user_id", userID.String()), zap.String("ref", referenceID))
		// Fail CLOSED: hold transaction for manual review when screening is unavailable
		return "IN_REVIEW", err
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
	}

	// Create alert for non-approved transactions
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
		s.logger.Error("Didit AML screening failed", zap.Error(err), zap.String("user_id", userID.String()))
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

	// Alert on any non-approved result
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

	// Update local screening record
	if err := s.repo.UpdateScreeningStatus(ctx, payload.UUID, payload.Status, payload.Severity, payload.Score); err != nil {
		s.logger.Error("Failed to update screening from webhook", zap.Error(err), zap.String("uuid", payload.UUID))
		return fmt.Errorf("update screening status: %w", err)
	}

	// On DECLINED, freeze the user
	if payload.Status == "DECLINED" && s.freeze != nil {
		screening, err := s.repo.GetScreeningByDiditUUID(ctx, payload.UUID)
		if err != nil {
			s.logger.Error("Failed to load screening for freeze", zap.Error(err))
			return err
		}
		reason := fmt.Sprintf("Transaction declined by compliance screening (score: %d, severity: %s)", payload.Score, payload.Severity)
		if err := s.freeze.FreezeUser(ctx, screening.UserID, reason); err != nil {
			s.logger.Error("Failed to freeze user", zap.Error(err), zap.String("user_id", screening.UserID.String()))
			return err
		}
		s.logger.Warn("User frozen due to declined transaction",
			zap.String("user_id", screening.UserID.String()),
			zap.String("didit_uuid", payload.UUID))
	}

	return nil
}
