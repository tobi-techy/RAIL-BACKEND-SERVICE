package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"go.uber.org/zap"
)

// Context keys for audit data
type contextKey string

const (
	ContextKeyIPAddress contextKey = "audit_ip_address"
	ContextKeyUserAgent contextKey = "audit_user_agent"
	ContextKeyUserID    contextKey = "audit_user_id"
)

type Service struct {
	repo   repositories.AuditRepository
	logger *zap.Logger
}

func NewService(repo repositories.AuditRepository, logger *zap.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

// Log creates an audit log entry
func (s *Service) Log(ctx context.Context, userID uuid.UUID, action entities.AuditAction, resource string, resourceID *uuid.UUID, metadata map[string]interface{}) error {
	log := &entities.AuditLog{
		ID:         uuid.New(),
		UserID:     userID,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		IPAddress:  getStringFromContext(ctx, ContextKeyIPAddress),
		UserAgent:  getStringFromContext(ctx, ContextKeyUserAgent),
		Metadata:   metadata,
		CreatedAt:  time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, log); err != nil {
		s.logger.Error("failed to create audit log",
			zap.Error(err),
			zap.String("action", string(action)),
			zap.String("user_id", userID.String()),
		)
		return err
	}
	return nil
}

// LogDeposit logs a deposit operation
func (s *Service) LogDeposit(ctx context.Context, userID uuid.UUID, depositID uuid.UUID, amount string, chain string, status string) error {
	return s.Log(ctx, userID, entities.AuditActionDeposit, "deposit", &depositID, map[string]interface{}{
		"amount": amount,
		"chain":  chain,
		"status": status,
	})
}

// LogWithdrawal logs a withdrawal operation
func (s *Service) LogWithdrawal(ctx context.Context, userID uuid.UUID, withdrawalID uuid.UUID, amount string, status string) error {
	return s.Log(ctx, userID, entities.AuditActionWithdrawal, "withdrawal", &withdrawalID, map[string]interface{}{
		"amount": amount,
		"status": status,
	})
}

// LogStatusTransition logs a status transition for any entity (deposit, withdrawal, order, etc.)
func (s *Service) LogStatusTransition(ctx context.Context, userID uuid.UUID, entityID uuid.UUID, entityType, fromStatus, toStatus, triggeredBy string) error {
	s.logger.Info("Status transition",
		zap.String("entity_id", entityID.String()),
		zap.String("entity_type", entityType),
		zap.String("from_status", fromStatus),
		zap.String("to_status", toStatus),
		zap.String("triggered_by", triggeredBy),
	)
	return s.Log(ctx, userID, entities.AuditActionStatusTransition, entityType, &entityID, map[string]interface{}{
		"from_status":  fromStatus,
		"to_status":    toStatus,
		"triggered_by": triggeredBy,
	})
}

// LogTrade logs a trade operation
func (s *Service) LogTrade(ctx context.Context, userID uuid.UUID, orderID uuid.UUID, symbol string, side string, amount string) error {
	return s.Log(ctx, userID, entities.AuditActionTrade, "order", &orderID, map[string]interface{}{
		"symbol": symbol,
		"side":   side,
		"amount": amount,
	})
}

// LogLogin logs a login event
func (s *Service) LogLogin(ctx context.Context, userID uuid.UUID) error {
	return s.Log(ctx, userID, entities.AuditActionLogin, "session", nil, nil)
}

// LogLogout logs a logout event
func (s *Service) LogLogout(ctx context.Context, userID uuid.UUID) error {
	return s.Log(ctx, userID, entities.AuditActionLogout, "session", nil, nil)
}

// GetUserAuditLogs retrieves audit logs for a user
func (s *Service) GetUserAuditLogs(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AuditLog, int64, error) {
	filter := repositories.AuditLogFilter{
		UserID: &userID,
		Limit:  limit,
		Offset: offset,
	}

	logs, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	count, err := s.repo.Count(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	return logs, count, nil
}

// WithAuditContext adds audit context to the request context
func WithAuditContext(ctx context.Context, ipAddress, userAgent string, userID *uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, ContextKeyIPAddress, ipAddress)
	ctx = context.WithValue(ctx, ContextKeyUserAgent, userAgent)
	if userID != nil {
		ctx = context.WithValue(ctx, ContextKeyUserID, *userID)
	}
	return ctx
}

func getStringFromContext(ctx context.Context, key contextKey) string {
	if val, ok := ctx.Value(key).(string); ok {
		return val
	}
	return ""
}
