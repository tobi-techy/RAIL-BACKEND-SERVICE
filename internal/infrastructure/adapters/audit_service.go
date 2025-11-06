package adapters

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// AuditLog represents an audit log entry in the database
type AuditLog struct {
	ID           uuid.UUID              `db:"id"`
	UserID       *uuid.UUID             `db:"user_id"`
	Actor        string                 `db:"actor"`
	Action       string                 `db:"action"`
	ResourceType string                 `db:"resource_type"`
	ResourceID   *string                `db:"resource_id"`
	Changes      map[string]interface{} `db:"changes"`
	Status       string                 `db:"status"`
	ErrorMessage *string                `db:"error_message"`
	IPAddress    *string                `db:"ip_address"`
	UserAgent    *string                `db:"user_agent"`
	Amount       *decimal.Decimal       `db:"amount"`
	Currency     *string                `db:"currency"`
	Signature    string                 `db:"signature"`
	CreatedAt    time.Time              `db:"at"`
}

// AuditService implements the audit service interface with database persistence
type AuditService struct {
	db        *sql.DB
	logger    *zap.Logger
	secretKey string
}

// NewAuditService creates a new audit service
func NewAuditService(db *sql.DB, logger *zap.Logger) *AuditService {
	// In production, this should come from secure configuration
	secretKey := "audit-hmac-secret-key-change-in-production"
	
	return &AuditService{
		db:        db,
		logger:    logger,
		secretKey: secretKey,
	}
}

// LogOnboardingEvent logs an onboarding-related audit event
func (a *AuditService) LogOnboardingEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error {
	return a.logEvent(ctx, "onboarding-service", userID, action, entity, before, after, nil, "success", nil)
}

// LogWalletEvent logs a wallet-related audit event
func (a *AuditService) LogWalletEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error {
	return a.logEvent(ctx, "wallet-service", userID, action, entity, before, after, nil, "success", nil)
}

// LogFundingEvent logs a funding-related audit event
func (a *AuditService) LogFundingEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error {
	return a.logEvent(ctx, "funding-service", userID, action, entity, before, after, nil, "success", nil)
}

// LogFinancialTransaction logs a financial transaction with amount and integrity signature
func (a *AuditService) LogFinancialTransaction(ctx context.Context, userID uuid.UUID, action string, amount decimal.Decimal, currency string, metadata map[string]interface{}) error {
	return a.logFinancialEvent(ctx, "financial-service", userID, action, "transaction", nil, metadata, &amount, &currency, "success", nil)
}

// LogFailedAction logs a failed action with error details
func (a *AuditService) LogFailedAction(ctx context.Context, actor string, userID *uuid.UUID, action, entity string, err error) error {
	errorMsg := err.Error()
	var uid uuid.UUID
	if userID != nil {
		uid = *userID
	}
	return a.logEvent(ctx, actor, uid, action, entity, nil, nil, nil, "failed", &errorMsg)
}

// LogSystemEvent logs a system-level event
func (a *AuditService) LogSystemEvent(ctx context.Context, action, entity string, metadata map[string]interface{}) error {
	return a.logEvent(ctx, "system", uuid.Nil, action, entity, nil, metadata, nil, "success", nil)
}

// logEvent is the internal method for logging audit events with HMAC signature
func (a *AuditService) logEvent(ctx context.Context, actor string, userID uuid.UUID, action, resourceType string, before, after, metadata interface{}, status string, errorMessage *string) error {
	auditLog := AuditLog{
		ID:           uuid.New(),
		Actor:        actor,
		Action:       action,
		ResourceType: resourceType,
		Status:       status,
		ErrorMessage: errorMessage,
		CreatedAt:    time.Now().UTC(),
	}

	if userID != uuid.Nil {
		auditLog.UserID = &userID
	}

	// Build changes map
	changes := make(map[string]interface{})
	if before != nil {
		changes["before"] = before
	}
	if after != nil {
		changes["after"] = after
	}
	if metadata != nil {
		changes["metadata"] = metadata
	}
	auditLog.Changes = changes

	// Generate HMAC signature for integrity
	signature, err := a.generateSignature(auditLog)
	if err != nil {
		return fmt.Errorf("failed to generate signature: %w", err)
	}
	auditLog.Signature = signature

	return a.insertAuditLog(ctx, auditLog)
}

// logFinancialEvent logs financial events with amount tracking
func (a *AuditService) logFinancialEvent(ctx context.Context, actor string, userID uuid.UUID, action, resourceType string, before, after interface{}, amount *decimal.Decimal, currency *string, status string, errorMessage *string) error {
	auditLog := AuditLog{
		ID:           uuid.New(),
		Actor:        actor,
		Action:       action,
		ResourceType: resourceType,
		Status:       status,
		ErrorMessage: errorMessage,
		Amount:       amount,
		Currency:     currency,
		CreatedAt:    time.Now().UTC(),
	}

	if userID != uuid.Nil {
		auditLog.UserID = &userID
	}

	// Build changes map
	changes := make(map[string]interface{})
	if before != nil {
		changes["before"] = before
	}
	if after != nil {
		changes["after"] = after
	}
	auditLog.Changes = changes

	// Generate HMAC signature for integrity
	signature, err := a.generateSignature(auditLog)
	if err != nil {
		return fmt.Errorf("failed to generate signature: %w", err)
	}
	auditLog.Signature = signature

	return a.insertAuditLog(ctx, auditLog)
}

// generateSignature generates HMAC signature for audit log integrity
func (a *AuditService) generateSignature(log AuditLog) (string, error) {
	// Create payload for signing
	payload := fmt.Sprintf("%s|%s|%s|%s|%s|%d",
		log.Actor,
		log.Action,
		log.ResourceType,
		log.UserID,
		log.Amount,
		log.CreatedAt.Unix(),
	)

	// Generate HMAC
	h := hmac.New(sha256.New, []byte(a.secretKey))
	h.Write([]byte(payload))
	signature := hex.EncodeToString(h.Sum(nil))

	return signature, nil
}

// insertAuditLog inserts the audit log into the database
func (a *AuditService) insertAuditLog(ctx context.Context, log AuditLog) error {
	changesJSON, err := json.Marshal(log.Changes)
	if err != nil {
		return fmt.Errorf("failed to marshal changes: %w", err)
	}

	query := `
		INSERT INTO audit_logs (
			id, user_id, actor, action, resource_type, resource_id, changes, 
			status, error_message, ip_address, user_agent, amount, currency, 
			signature, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	_, err = a.db.ExecContext(ctx, query,
		log.ID, log.UserID, log.Actor, log.Action, log.ResourceType, log.ResourceID,
		changesJSON, log.Status, log.ErrorMessage, log.IPAddress, log.UserAgent,
		log.Amount, log.Currency, log.Signature, log.CreatedAt,
	)

	if err != nil {
		a.logger.Error("Failed to insert audit log",
			zap.Error(err),
			zap.String("action", log.Action),
			zap.String("resource_type", log.ResourceType),
		)
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	a.logger.Info("Audit event logged",
		zap.String("id", log.ID.String()),
		zap.String("action", log.Action),
		zap.String("resource_type", log.ResourceType),
		zap.String("status", log.Status),
	)

	return nil
}

// VerifyLogIntegrity verifies the HMAC signature of an audit log
func (a *AuditService) VerifyLogIntegrity(log AuditLog) bool {
	expectedSignature, err := a.generateSignature(log)
	if err != nil {
		a.logger.Error("Failed to generate signature for verification", zap.Error(err))
		return false
	}
	
	return hmac.Equal([]byte(log.Signature), []byte(expectedSignature))
}

// GetUserAuditTrail retrieves audit trail for a specific user
func (a *AuditService) GetUserAuditTrail(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]AuditLog, error) {
	query := `
		SELECT id, user_id, actor, action, resource_type, resource_id, changes,
			   status, error_message, ip_address, user_agent, amount, currency,
			   signature, created_at
		FROM audit_logs 
		WHERE user_id = $1 
		ORDER BY created_at DESC 
		LIMIT $2 OFFSET $3`

	rows, err := a.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit trail: %w", err)
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var log AuditLog
		var changesJSON []byte

		err := rows.Scan(
			&log.ID, &log.UserID, &log.Actor, &log.Action, &log.ResourceType, &log.ResourceID,
			&changesJSON, &log.Status, &log.ErrorMessage, &log.IPAddress, &log.UserAgent,
			&log.Amount, &log.Currency, &log.Signature, &log.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan audit log: %w", err)
		}

		// Unmarshal changes
		if len(changesJSON) > 0 {
			if err := json.Unmarshal(changesJSON, &log.Changes); err != nil {
				a.logger.Warn("Failed to unmarshal audit changes", zap.Error(err))
			}
		}

		logs = append(logs, log)
	}

	return logs, nil
}

// LogWalletWorkerEvent logs a wallet worker event with detailed context
func (a *AuditService) LogWalletWorkerEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}, resourceID *string, status string, errorMsg *string) error {
	return a.logEventWithResource(ctx, "wallet-worker", userID, action, entity, before, after, resourceID, status, errorMsg)
}

// LogAction logs a generic action event with optional user ID
// This method is used by webhook workers and other background processes
func (a *AuditService) LogAction(ctx context.Context, userID *uuid.UUID, action, entity string, before, after interface{}) error {
	return a.logEventWithNullableUser(ctx, "webhook-worker", userID, action, entity, before, after, nil, "success", nil)
}

// logEventWithNullableUser persists an audit log with nullable user ID to the database
func (a *AuditService) logEventWithNullableUser(
	ctx context.Context,
	actor string,
	userID *uuid.UUID,
	action string,
	entity string,
	before interface{},
	after interface{},
	resourceID *string,
	status string,
	errorMsg *string,
) error {
	// Build changes JSON
	changes := make(map[string]interface{})

	if before != nil {
		changes["before"] = before
	}

	if after != nil {
		changes["after"] = after
	}

	changesJSON, err := json.Marshal(changes)
	if err != nil {
		a.logger.Warn("Failed to marshal audit changes", zap.Error(err))
		changesJSON = []byte("{}")
	}

	// Extract context values if available
	var ipAddress *string
	var userAgent *string

	if ctxIP := ctx.Value("ip_address"); ctxIP != nil {
		if ip, ok := ctxIP.(string); ok {
			ipAddress = &ip
		}
	}

	if ctxUA := ctx.Value("user_agent"); ctxUA != nil {
		if ua, ok := ctxUA.(string); ok {
			userAgent = &ua
		}
	}

	// Insert audit log
	query := `
		INSERT INTO audit_logs (
			id,
			user_id,
			action,
			resource_type,
			resource_id,
			entity,
			before,
			after,
			changes,
			status,
			error_message,
			ip_address,
			user_agent,
			at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13, $14
		)`

	auditID := uuid.New()
	var beforeJSON []byte
	if before != nil {
		if data, marshalErr := json.Marshal(before); marshalErr != nil {
			a.logger.Warn("Failed to marshal audit before state", zap.Error(marshalErr))
			beforeJSON = []byte("{}")
		} else {
			beforeJSON = data
		}
	}

	var afterJSON []byte
	if after != nil {
		if data, marshalErr := json.Marshal(after); marshalErr != nil {
			a.logger.Warn("Failed to marshal audit after state", zap.Error(marshalErr))
			afterJSON = []byte("{}")
		} else {
			afterJSON = data
		}
	}

	var beforeParam interface{}
	if beforeJSON != nil {
		beforeParam = beforeJSON
	}

	var afterParam interface{}
	if afterJSON != nil {
		afterParam = afterJSON
	}

	_, err = a.db.ExecContext(ctx, query,
		auditID,
		userID, // This can be nil
		fmt.Sprintf("%s:%s", actor, action), // Combine actor and action
		entity,
		resourceID,
		entity,
		beforeParam,
		afterParam,
		changesJSON,
		status,
		errorMsg,
		ipAddress,
		userAgent,
		time.Now().UTC(),
	)

	if err != nil {
		userIDStr := "nil"
		if userID != nil {
			userIDStr = userID.String()
		}
		a.logger.Error("Failed to persist audit log",
			zap.Error(err),
			zap.String("user_id", userIDStr),
			zap.String("actor", actor),
			zap.String("action", action),
			zap.String("entity", entity))
		// Don't fail the operation if audit logging fails
		return nil
	}

	userIDStr := "nil"
	if userID != nil {
		userIDStr = userID.String()
	}
	a.logger.Debug("Audit log persisted",
		zap.String("audit_id", auditID.String()),
		zap.String("user_id", userIDStr),
		zap.String("actor", actor),
		zap.String("action", action),
		zap.String("entity", entity),
		zap.String("status", status))

	return nil
}

// logEventWithResource persists an audit log to the database with resource ID support
func (a *AuditService) logEventWithResource(
	ctx context.Context,
	actor string,
	userID uuid.UUID,
	action string,
	entity string,
	before interface{},
	after interface{},
	resourceID *string,
	status string,
	errorMsg *string,
) error {
	// Build changes JSON
	changes := make(map[string]interface{})

	if before != nil {
		changes["before"] = before
	}

	if after != nil {
		changes["after"] = after
	}

	changesJSON, err := json.Marshal(changes)
	if err != nil {
		a.logger.Warn("Failed to marshal audit changes", zap.Error(err))
		changesJSON = []byte("{}")
	}

	// Extract context values if available
	var ipAddress *string
	var userAgent *string

	if ctxIP := ctx.Value("ip_address"); ctxIP != nil {
		if ip, ok := ctxIP.(string); ok {
			ipAddress = &ip
		}
	}

	if ctxUA := ctx.Value("user_agent"); ctxUA != nil {
		if ua, ok := ctxUA.(string); ok {
			userAgent = &ua
		}
	}

	// Insert audit log
	query := `
		INSERT INTO audit_logs (
			id,
			user_id,
			action,
			resource_type,
			resource_id,
			entity,
			before,
			after,
			changes,
			status,
			error_message,
			ip_address,
			user_agent,
			at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13, $14
		)`

	auditID := uuid.New()
	var beforeJSON []byte
	if before != nil {
		if data, marshalErr := json.Marshal(before); marshalErr != nil {
			a.logger.Warn("Failed to marshal audit before state", zap.Error(marshalErr))
			beforeJSON = []byte("{}")
		} else {
			beforeJSON = data
		}
	}

	var afterJSON []byte
	if after != nil {
		if data, marshalErr := json.Marshal(after); marshalErr != nil {
			a.logger.Warn("Failed to marshal audit after state", zap.Error(marshalErr))
			afterJSON = []byte("{}")
		} else {
			afterJSON = data
		}
	}

	var beforeParam interface{}
	if beforeJSON != nil {
		beforeParam = beforeJSON
	}

	var afterParam interface{}
	if afterJSON != nil {
		afterParam = afterJSON
	}

	_, err = a.db.ExecContext(ctx, query,
		auditID,
		&userID,
		fmt.Sprintf("%s:%s", actor, action), // Combine actor and action
		entity,
		resourceID,
		entity,
		beforeParam,
		afterParam,
		changesJSON,
		status,
		errorMsg,
		ipAddress,
		userAgent,
		time.Now().UTC(),
	)

	if err != nil {
		a.logger.Error("Failed to persist audit log",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("actor", actor),
			zap.String("action", action),
			zap.String("entity", entity))
		// Don't fail the operation if audit logging fails
		return nil
	}

	a.logger.Debug("Audit log persisted",
		zap.String("audit_id", auditID.String()),
		zap.String("user_id", userID.String()),
		zap.String("actor", actor),
		zap.String("action", action),
		zap.String("entity", entity),
		zap.String("status", status))

	return nil
}

// GetAuditLogs retrieves audit logs for a user with optional filters
func (a *AuditService) GetAuditLogs(ctx context.Context, userID uuid.UUID, action *string, limit int, offset int) ([]AuditLog, error) {
	query := `
		SELECT id, user_id, action, resource_type, resource_id,
		       changes, status, error_message, ip_address, user_agent,
		       at AS created_at
		FROM audit_logs
		WHERE user_id = $1`

	args := []interface{}{userID}
	paramCount := 1

	if action != nil {
		paramCount++
		query += fmt.Sprintf(" AND action LIKE $%d", paramCount)
		args = append(args, "%"+*action+"%")
	}

	query += " ORDER BY at DESC"

	if limit > 0 {
		paramCount++
		query += fmt.Sprintf(" LIMIT $%d", paramCount)
		args = append(args, limit)
	}

	if offset > 0 {
		paramCount++
		query += fmt.Sprintf(" OFFSET $%d", paramCount)
		args = append(args, offset)
	}

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		a.logger.Error("Failed to query audit logs", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to query audit logs: %w", err)
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var log AuditLog
		var changesJSON []byte

		err := rows.Scan(
			&log.ID,
			&log.UserID,
			&log.Action,
			&log.ResourceType,
			&log.ResourceID,
			&changesJSON,
			&log.Status,
			&log.ErrorMessage,
			&log.IPAddress,
			&log.UserAgent,
			&log.CreatedAt,
		)

		if err != nil {
			a.logger.Error("Failed to scan audit log", zap.Error(err))
			continue
		}

		if len(changesJSON) > 0 {
			if err := json.Unmarshal(changesJSON, &log.Changes); err != nil {
				a.logger.Warn("Failed to unmarshal changes JSON", zap.Error(err))
				log.Changes = make(map[string]interface{})
			}
		}

		logs = append(logs, log)
	}

	return logs, nil
}
