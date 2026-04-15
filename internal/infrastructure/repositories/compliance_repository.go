package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

type ComplianceRepository struct {
	db     *sqlx.DB
	logger *zap.Logger
}

func NewComplianceRepository(db *sqlx.DB, logger *zap.Logger) *ComplianceRepository {
	return &ComplianceRepository{db: db, logger: logger}
}

func (r *ComplianceRepository) CreateScreening(ctx context.Context, s *entities.ComplianceScreening) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	detailsJSON, err := json.Marshal(s.Details)
	if err != nil {
		return fmt.Errorf("marshal details: %w", err)
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO compliance_screenings (id, user_id, screening_type, direction, amount, currency, didit_txn_uuid, reference_id, status, score, severity, details, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		s.ID, s.UserID, s.ScreeningType, s.Direction, s.Amount, s.Currency,
		s.DiditTxnUUID, s.ReferenceID, s.Status, s.Score, s.Severity, detailsJSON, s.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert screening: %w", err)
	}
	return nil
}

func (r *ComplianceRepository) CreateAlert(ctx context.Context, a *entities.ComplianceAlert) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO compliance_alerts (id, user_id, screening_id, alert_type, severity, description, status, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		a.ID, a.UserID, a.ScreeningID, a.AlertType, a.Severity, a.Description, a.Status, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert alert: %w", err)
	}
	return nil
}

func (r *ComplianceRepository) UpdateScreeningStatus(ctx context.Context, diditTxnUUID, status, severity string, score int) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE compliance_screenings SET status=$1, score=$2, severity=$3, updated_at=NOW() WHERE didit_txn_uuid=$4`,
		status, score, severity, diditTxnUUID,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("no screening found for didit_txn_uuid %s", diditTxnUUID)
	}
	return nil
}

func (r *ComplianceRepository) GetScreeningByDiditUUID(ctx context.Context, diditTxnUUID string) (*entities.ComplianceScreening, error) {
	var s entities.ComplianceScreening
	err := r.db.GetContext(ctx, &s, `SELECT id, user_id, screening_type, direction, status, score, severity, created_at FROM compliance_screenings WHERE didit_txn_uuid=$1`, diditTxnUUID)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
