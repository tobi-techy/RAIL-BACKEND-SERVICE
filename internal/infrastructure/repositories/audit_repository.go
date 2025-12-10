package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
)

type AuditRepository struct {
	db *sqlx.DB
}

func NewAuditRepository(db *sqlx.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

func (r *AuditRepository) Create(ctx context.Context, log *entities.AuditLog) error {
	metadata, err := json.Marshal(log.Metadata)
	if err != nil {
		metadata = []byte("{}")
	}

	query := `
		INSERT INTO audit_logs (id, user_id, action, resource, resource_id, ip_address, user_agent, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err = r.db.ExecContext(ctx, query,
		log.ID, log.UserID, log.Action, log.Resource, log.ResourceID,
		log.IPAddress, log.UserAgent, metadata, log.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create audit log: %w", err)
	}
	return nil
}

func (r *AuditRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.AuditLog, error) {
	query := `SELECT id, user_id, action, resource, resource_id, ip_address, user_agent, metadata, created_at FROM audit_logs WHERE id = $1`
	
	var log entities.AuditLog
	var metadata []byte
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&log.ID, &log.UserID, &log.Action, &log.Resource, &log.ResourceID,
		&log.IPAddress, &log.UserAgent, &metadata, &log.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get audit log: %w", err)
	}
	
	if len(metadata) > 0 {
		json.Unmarshal(metadata, &log.Metadata)
	}
	return &log, nil
}

func (r *AuditRepository) List(ctx context.Context, filter repositories.AuditLogFilter) ([]*entities.AuditLog, error) {
	var sb strings.Builder
	var args []interface{}
	argIdx := 1

	sb.WriteString(`SELECT id, user_id, action, resource, resource_id, ip_address, user_agent, metadata, created_at FROM audit_logs WHERE 1=1`)

	if filter.UserID != nil {
		sb.WriteString(fmt.Sprintf(" AND user_id = $%d", argIdx))
		args = append(args, *filter.UserID)
		argIdx++
	}
	if filter.Action != nil {
		sb.WriteString(fmt.Sprintf(" AND action = $%d", argIdx))
		args = append(args, *filter.Action)
		argIdx++
	}
	if filter.Resource != nil {
		sb.WriteString(fmt.Sprintf(" AND resource = $%d", argIdx))
		args = append(args, *filter.Resource)
		argIdx++
	}
	if filter.ResourceID != nil {
		sb.WriteString(fmt.Sprintf(" AND resource_id = $%d", argIdx))
		args = append(args, *filter.ResourceID)
		argIdx++
	}
	if filter.StartDate != nil {
		sb.WriteString(fmt.Sprintf(" AND created_at >= $%d", argIdx))
		args = append(args, *filter.StartDate)
		argIdx++
	}
	if filter.EndDate != nil {
		sb.WriteString(fmt.Sprintf(" AND created_at <= $%d", argIdx))
		args = append(args, *filter.EndDate)
		argIdx++
	}

	sb.WriteString(" ORDER BY created_at DESC")

	if filter.Limit > 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT $%d", argIdx))
		args = append(args, filter.Limit)
		argIdx++
	}
	if filter.Offset > 0 {
		sb.WriteString(fmt.Sprintf(" OFFSET $%d", argIdx))
		args = append(args, filter.Offset)
	}

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []*entities.AuditLog
	for rows.Next() {
		var log entities.AuditLog
		var metadata []byte
		if err := rows.Scan(&log.ID, &log.UserID, &log.Action, &log.Resource, &log.ResourceID,
			&log.IPAddress, &log.UserAgent, &metadata, &log.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan audit log: %w", err)
		}
		if len(metadata) > 0 {
			json.Unmarshal(metadata, &log.Metadata)
		}
		logs = append(logs, &log)
	}
	return logs, rows.Err()
}

func (r *AuditRepository) Count(ctx context.Context, filter repositories.AuditLogFilter) (int64, error) {
	var sb strings.Builder
	var args []interface{}
	argIdx := 1

	sb.WriteString(`SELECT COUNT(*) FROM audit_logs WHERE 1=1`)

	if filter.UserID != nil {
		sb.WriteString(fmt.Sprintf(" AND user_id = $%d", argIdx))
		args = append(args, *filter.UserID)
		argIdx++
	}
	if filter.Action != nil {
		sb.WriteString(fmt.Sprintf(" AND action = $%d", argIdx))
		args = append(args, *filter.Action)
		argIdx++
	}
	if filter.Resource != nil {
		sb.WriteString(fmt.Sprintf(" AND resource = $%d", argIdx))
		args = append(args, *filter.Resource)
		argIdx++
	}
	if filter.ResourceID != nil {
		sb.WriteString(fmt.Sprintf(" AND resource_id = $%d", argIdx))
		args = append(args, *filter.ResourceID)
		argIdx++
	}
	if filter.StartDate != nil {
		sb.WriteString(fmt.Sprintf(" AND created_at >= $%d", argIdx))
		args = append(args, *filter.StartDate)
		argIdx++
	}
	if filter.EndDate != nil {
		sb.WriteString(fmt.Sprintf(" AND created_at <= $%d", argIdx))
		args = append(args, *filter.EndDate)
	}

	var count int64
	err := r.db.QueryRowContext(ctx, sb.String(), args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count audit logs: %w", err)
	}
	return count, nil
}
