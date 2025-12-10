package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ReconciliationRepository defines the interface for reconciliation data persistence
type ReconciliationRepository interface {
	// Report operations
	CreateReport(ctx context.Context, report *entities.ReconciliationReport) error
	UpdateReport(ctx context.Context, report *entities.ReconciliationReport) error
	GetReportByID(ctx context.Context, id uuid.UUID) (*entities.ReconciliationReport, error)
	GetLatestReportByType(ctx context.Context, runType string) (*entities.ReconciliationReport, error)
	ListReports(ctx context.Context, limit, offset int) ([]*entities.ReconciliationReport, error)

	// Check operations
	CreateCheck(ctx context.Context, check *entities.ReconciliationCheck) error
	GetChecksByReportID(ctx context.Context, reportID uuid.UUID) ([]*entities.ReconciliationCheck, error)

	// Exception operations
	CreateException(ctx context.Context, exception *entities.ReconciliationException) error
	CreateExceptionsBatch(ctx context.Context, exceptions []*entities.ReconciliationException) error
	GetExceptionsByReportID(ctx context.Context, reportID uuid.UUID) ([]*entities.ReconciliationException, error)
	GetUnresolvedExceptions(ctx context.Context, severity entities.ExceptionSeverity) ([]*entities.ReconciliationException, error)
	UpdateException(ctx context.Context, exception *entities.ReconciliationException) error
}

// PostgresReconciliationRepository implements ReconciliationRepository using PostgreSQL
type PostgresReconciliationRepository struct {
	db *sql.DB
}

// NewPostgresReconciliationRepository creates a new PostgreSQL reconciliation repository
func NewPostgresReconciliationRepository(db *sql.DB) ReconciliationRepository {
	return &PostgresReconciliationRepository{db: db}
}

// CreateReport creates a new reconciliation report
func (r *PostgresReconciliationRepository) CreateReport(ctx context.Context, report *entities.ReconciliationReport) error {
	metadataJSON, err := json.Marshal(report.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO reconciliation_reports (
			id, run_type, status, started_at, completed_at, 
			total_checks, passed_checks, failed_checks, exceptions_count,
			error_message, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err = r.db.ExecContext(ctx, query,
		report.ID,
		report.RunType,
		report.Status,
		report.StartedAt,
		report.CompletedAt,
		report.TotalChecks,
		report.PassedChecks,
		report.FailedChecks,
		report.ExceptionsCount,
		report.ErrorMessage,
		metadataJSON,
		report.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create reconciliation report: %w", err)
	}

	return nil
}

// UpdateReport updates an existing reconciliation report
func (r *PostgresReconciliationRepository) UpdateReport(ctx context.Context, report *entities.ReconciliationReport) error {
	metadataJSON, err := json.Marshal(report.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		UPDATE reconciliation_reports
		SET status = $2, completed_at = $3, total_checks = $4,
		    passed_checks = $5, failed_checks = $6, exceptions_count = $7,
		    error_message = $8, metadata = $9
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		report.ID,
		report.Status,
		report.CompletedAt,
		report.TotalChecks,
		report.PassedChecks,
		report.FailedChecks,
		report.ExceptionsCount,
		report.ErrorMessage,
		metadataJSON,
	)

	if err != nil {
		return fmt.Errorf("failed to update reconciliation report: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("reconciliation report not found: %s", report.ID)
	}

	return nil
}

// GetReportByID retrieves a reconciliation report by ID
func (r *PostgresReconciliationRepository) GetReportByID(ctx context.Context, id uuid.UUID) (*entities.ReconciliationReport, error) {
	query := `
		SELECT id, run_type, status, started_at, completed_at,
		       total_checks, passed_checks, failed_checks, exceptions_count,
		       error_message, metadata, created_at
		FROM reconciliation_reports
		WHERE id = $1
	`

	var report entities.ReconciliationReport
	var metadataJSON []byte

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&report.ID,
		&report.RunType,
		&report.Status,
		&report.StartedAt,
		&report.CompletedAt,
		&report.TotalChecks,
		&report.PassedChecks,
		&report.FailedChecks,
		&report.ExceptionsCount,
		&report.ErrorMessage,
		&metadataJSON,
		&report.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("reconciliation report not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get reconciliation report: %w", err)
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &report.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return &report, nil
}

// GetLatestReportByType retrieves the most recent reconciliation report of a given type
func (r *PostgresReconciliationRepository) GetLatestReportByType(ctx context.Context, runType string) (*entities.ReconciliationReport, error) {
	query := `
		SELECT id, run_type, status, started_at, completed_at,
		       total_checks, passed_checks, failed_checks, exceptions_count,
		       error_message, metadata, created_at
		FROM reconciliation_reports
		WHERE run_type = $1
		ORDER BY created_at DESC
		LIMIT 1
	`

	var report entities.ReconciliationReport
	var metadataJSON []byte

	err := r.db.QueryRowContext(ctx, query, runType).Scan(
		&report.ID,
		&report.RunType,
		&report.Status,
		&report.StartedAt,
		&report.CompletedAt,
		&report.TotalChecks,
		&report.PassedChecks,
		&report.FailedChecks,
		&report.ExceptionsCount,
		&report.ErrorMessage,
		&metadataJSON,
		&report.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no reconciliation report found for type: %s", runType)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get latest reconciliation report: %w", err)
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &report.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return &report, nil
}

// ListReports lists reconciliation reports with pagination
func (r *PostgresReconciliationRepository) ListReports(ctx context.Context, limit, offset int) ([]*entities.ReconciliationReport, error) {
	query := `
		SELECT id, run_type, status, started_at, completed_at,
		       total_checks, passed_checks, failed_checks, exceptions_count,
		       error_message, metadata, created_at
		FROM reconciliation_reports
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list reconciliation reports: %w", err)
	}
	defer rows.Close()

	var reports []*entities.ReconciliationReport
	for rows.Next() {
		var report entities.ReconciliationReport
		var metadataJSON []byte

		err := rows.Scan(
			&report.ID,
			&report.RunType,
			&report.Status,
			&report.StartedAt,
			&report.CompletedAt,
			&report.TotalChecks,
			&report.PassedChecks,
			&report.FailedChecks,
			&report.ExceptionsCount,
			&report.ErrorMessage,
			&metadataJSON,
			&report.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan reconciliation report: %w", err)
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &report.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		reports = append(reports, &report)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating reconciliation reports: %w", err)
	}

	return reports, nil
}

// CreateCheck creates a new reconciliation check
func (r *PostgresReconciliationRepository) CreateCheck(ctx context.Context, check *entities.ReconciliationCheck) error {
	metadataJSON, err := json.Marshal(check.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO reconciliation_checks (
			id, report_id, check_type, status, expected_value, actual_value,
			difference, passed, error_message, execution_time_ms, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err = r.db.ExecContext(ctx, query,
		check.ID,
		check.ReportID,
		check.CheckType,
		check.Status,
		check.ExpectedValue,
		check.ActualValue,
		check.Difference,
		check.Passed,
		check.ErrorMessage,
		check.ExecutionTimeMs,
		metadataJSON,
		check.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create reconciliation check: %w", err)
	}

	return nil
}

// GetChecksByReportID retrieves all checks for a given report
func (r *PostgresReconciliationRepository) GetChecksByReportID(ctx context.Context, reportID uuid.UUID) ([]*entities.ReconciliationCheck, error) {
	query := `
		SELECT id, report_id, check_type, status, expected_value, actual_value,
		       difference, passed, error_message, execution_time_ms, metadata, created_at
		FROM reconciliation_checks
		WHERE report_id = $1
		ORDER BY created_at ASC
	`

	rows, err := r.db.QueryContext(ctx, query, reportID)
	if err != nil {
		return nil, fmt.Errorf("failed to get reconciliation checks: %w", err)
	}
	defer rows.Close()

	var checks []*entities.ReconciliationCheck
	for rows.Next() {
		var check entities.ReconciliationCheck
		var metadataJSON []byte

		err := rows.Scan(
			&check.ID,
			&check.ReportID,
			&check.CheckType,
			&check.Status,
			&check.ExpectedValue,
			&check.ActualValue,
			&check.Difference,
			&check.Passed,
			&check.ErrorMessage,
			&check.ExecutionTimeMs,
			&metadataJSON,
			&check.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan reconciliation check: %w", err)
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &check.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		checks = append(checks, &check)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating reconciliation checks: %w", err)
	}

	return checks, nil
}

// CreateException creates a new reconciliation exception
func (r *PostgresReconciliationRepository) CreateException(ctx context.Context, exception *entities.ReconciliationException) error {
	metadataJSON, err := json.Marshal(exception.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO reconciliation_exceptions (
			id, report_id, check_id, check_type, severity, description,
			expected_value, actual_value, difference, currency,
			affected_user_id, affected_entity, auto_corrected, correction_action,
			resolved_at, resolved_by, resolution_notes, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`

	_, err = r.db.ExecContext(ctx, query,
		exception.ID,
		exception.ReportID,
		exception.CheckID,
		exception.CheckType,
		exception.Severity,
		exception.Description,
		exception.ExpectedValue,
		exception.ActualValue,
		exception.Difference,
		exception.Currency,
		exception.AffectedUserID,
		exception.AffectedEntity,
		exception.AutoCorrected,
		exception.CorrectionAction,
		exception.ResolvedAt,
		exception.ResolvedBy,
		exception.ResolutionNotes,
		metadataJSON,
		exception.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create reconciliation exception: %w", err)
	}

	return nil
}

// CreateExceptionsBatch creates multiple reconciliation exceptions in a single transaction
func (r *PostgresReconciliationRepository) CreateExceptionsBatch(ctx context.Context, exceptions []*entities.ReconciliationException) error {
	if len(exceptions) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO reconciliation_exceptions (
			id, report_id, check_id, check_type, severity, description,
			expected_value, actual_value, difference, currency,
			affected_user_id, affected_entity, auto_corrected, correction_action,
			resolved_at, resolved_by, resolution_notes, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, exception := range exceptions {
		metadataJSON, err := json.Marshal(exception.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}

		_, err = stmt.ExecContext(ctx,
			exception.ID,
			exception.ReportID,
			exception.CheckID,
			exception.CheckType,
			exception.Severity,
			exception.Description,
			exception.ExpectedValue,
			exception.ActualValue,
			exception.Difference,
			exception.Currency,
			exception.AffectedUserID,
			exception.AffectedEntity,
			exception.AutoCorrected,
			exception.CorrectionAction,
			exception.ResolvedAt,
			exception.ResolvedBy,
			exception.ResolutionNotes,
			metadataJSON,
			exception.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to insert exception: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetExceptionsByReportID retrieves all exceptions for a given report
func (r *PostgresReconciliationRepository) GetExceptionsByReportID(ctx context.Context, reportID uuid.UUID) ([]*entities.ReconciliationException, error) {
	query := `
		SELECT id, report_id, check_id, check_type, severity, description,
		       expected_value, actual_value, difference, currency,
		       affected_user_id, affected_entity, auto_corrected, correction_action,
		       resolved_at, resolved_by, resolution_notes, metadata, created_at
		FROM reconciliation_exceptions
		WHERE report_id = $1
		ORDER BY severity DESC, created_at ASC
	`

	return r.scanExceptions(ctx, query, reportID)
}

// GetUnresolvedExceptions retrieves all unresolved exceptions of a given severity
func (r *PostgresReconciliationRepository) GetUnresolvedExceptions(ctx context.Context, severity entities.ExceptionSeverity) ([]*entities.ReconciliationException, error) {
	query := `
		SELECT id, report_id, check_id, check_type, severity, description,
		       expected_value, actual_value, difference, currency,
		       affected_user_id, affected_entity, auto_corrected, correction_action,
		       resolved_at, resolved_by, resolution_notes, metadata, created_at
		FROM reconciliation_exceptions
		WHERE severity = $1 AND resolved_at IS NULL
		ORDER BY created_at DESC
	`

	return r.scanExceptions(ctx, query, severity)
}

// UpdateException updates an existing reconciliation exception
func (r *PostgresReconciliationRepository) UpdateException(ctx context.Context, exception *entities.ReconciliationException) error {
	metadataJSON, err := json.Marshal(exception.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		UPDATE reconciliation_exceptions
		SET auto_corrected = $2, correction_action = $3,
		    resolved_at = $4, resolved_by = $5, resolution_notes = $6,
		    metadata = $7
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		exception.ID,
		exception.AutoCorrected,
		exception.CorrectionAction,
		exception.ResolvedAt,
		exception.ResolvedBy,
		exception.ResolutionNotes,
		metadataJSON,
	)

	if err != nil {
		return fmt.Errorf("failed to update reconciliation exception: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("reconciliation exception not found: %s", exception.ID)
	}

	return nil
}

// scanExceptions is a helper function to scan multiple exceptions
func (r *PostgresReconciliationRepository) scanExceptions(ctx context.Context, query string, args ...interface{}) ([]*entities.ReconciliationException, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query reconciliation exceptions: %w", err)
	}
	defer rows.Close()

	var exceptions []*entities.ReconciliationException
	for rows.Next() {
		var exception entities.ReconciliationException
		var metadataJSON []byte
		var affectedUserID sql.NullString

		err := rows.Scan(
			&exception.ID,
			&exception.ReportID,
			&exception.CheckID,
			&exception.CheckType,
			&exception.Severity,
			&exception.Description,
			&exception.ExpectedValue,
			&exception.ActualValue,
			&exception.Difference,
			&exception.Currency,
			&affectedUserID,
			&exception.AffectedEntity,
			&exception.AutoCorrected,
			&exception.CorrectionAction,
			&exception.ResolvedAt,
			&exception.ResolvedBy,
			&exception.ResolutionNotes,
			&metadataJSON,
			&exception.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan reconciliation exception: %w", err)
		}

		if affectedUserID.Valid {
			userID, err := uuid.Parse(affectedUserID.String)
			if err == nil {
				exception.AffectedUserID = &userID
			}
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &exception.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		exceptions = append(exceptions, &exception)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating reconciliation exceptions: %w", err)
	}

	return exceptions, nil
}
