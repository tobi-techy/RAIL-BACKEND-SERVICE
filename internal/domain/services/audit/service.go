package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"go.uber.org/zap"
)

type contextKey string

const (
	ContextKeyIPAddress contextKey = "audit_ip_address"
	ContextKeyUserAgent contextKey = "audit_user_agent"
	ContextKeyUserID    contextKey = "audit_user_id"
)

type Service struct {
	repo          repositories.AuditRepository
	logger        *zap.Logger
	wormEnabled   bool
	lastHash      string
	lastHashMutex sync.Mutex
}

func NewService(repo repositories.AuditRepository, logger *zap.Logger) *Service {
	return &Service{
		repo:        repo,
		logger:      logger,
		wormEnabled: true,
	}
}

func (s *Service) EnableWORM(enabled bool) {
	s.wormEnabled = enabled
}

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

	if s.wormEnabled {
		previousHash := s.getLastHash()
		log.SetIntegrityFields(previousHash)
	}

	if err := s.repo.Create(ctx, log); err != nil {
		s.logger.Error("failed to create audit log",
			zap.Error(err),
			zap.String("action", string(action)),
			zap.String("user_id", userID.String()),
		)
		return err
	}

	if s.wormEnabled {
		s.setLastHash(log.CurrentHash)
	}

	s.logger.Info("Audit log created",
		zap.String("action", string(action)),
		zap.String("user_id", userID.String()),
		zap.String("resource", resource),
		zap.String("hash", log.CurrentHash),
	)

	return nil
}

func (s *Service) getLastHash() string {
	s.lastHashMutex.Lock()
	defer s.lastHashMutex.Unlock()
	return s.lastHash
}

func (s *Service) setLastHash(hash string) {
	s.lastHashMutex.Lock()
	defer s.lastHashMutex.Unlock()
	s.lastHash = hash
}

func (s *Service) LogDeposit(ctx context.Context, userID uuid.UUID, depositID uuid.UUID, amount string, chain string, status string) error {
	return s.Log(ctx, userID, entities.AuditActionDeposit, "deposit", &depositID, map[string]interface{}{
		"amount": amount,
		"chain":  chain,
		"status": status,
	})
}

func (s *Service) LogWithdrawal(ctx context.Context, userID uuid.UUID, withdrawalID uuid.UUID, amount string, status string) error {
	return s.Log(ctx, userID, entities.AuditActionWithdrawal, "withdrawal", &withdrawalID, map[string]interface{}{
		"amount": amount,
		"status": status,
	})
}

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

func (s *Service) LogTrade(ctx context.Context, userID uuid.UUID, orderID uuid.UUID, symbol string, side string, amount string) error {
	return s.Log(ctx, userID, entities.AuditActionTrade, "order", &orderID, map[string]interface{}{
		"symbol": symbol,
		"side":   side,
		"amount": amount,
	})
}

func (s *Service) LogLogin(ctx context.Context, userID uuid.UUID) error {
	return s.Log(ctx, userID, entities.AuditActionLogin, "session", nil, nil)
}

func (s *Service) LogLogout(ctx context.Context, userID uuid.UUID) error {
	return s.Log(ctx, userID, entities.AuditActionLogout, "session", nil, nil)
}

func (s *Service) LogAPIKeyCreate(ctx context.Context, userID uuid.UUID, keyID string) error {
	return s.Log(ctx, userID, entities.AuditActionAPIKeyCreate, "api_key", nil, map[string]interface{}{
		"key_id": keyID,
	})
}

func (s *Service) LogAPIKeyRevoke(ctx context.Context, userID uuid.UUID, keyID string) error {
	return s.Log(ctx, userID, entities.AuditActionAPIKeyRevoke, "api_key", nil, map[string]interface{}{
		"key_id": keyID,
	})
}

func (s *Service) LogPasswordChange(ctx context.Context, userID uuid.UUID) error {
	return s.Log(ctx, userID, entities.AuditActionPasswordChange, "user", &userID, nil)
}

func (s *Service) LogMFAEnable(ctx context.Context, userID uuid.UUID, method string) error {
	return s.Log(ctx, userID, entities.AuditActionMFAEnable, "user", &userID, map[string]interface{}{
		"method": method,
	})
}

func (s *Service) LogMFADisable(ctx context.Context, userID uuid.UUID) error {
	return s.Log(ctx, userID, entities.AuditActionMFADisable, "user", &userID, nil)
}

func (s *Service) LogAdminAction(ctx context.Context, adminID uuid.UUID, targetUserID uuid.UUID, action string, details map[string]interface{}) error {
	return s.Log(ctx, adminID, entities.AuditActionAdminAction, "admin", &targetUserID, details)
}

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

func (s *Service) GetAuditLogs(ctx context.Context, filter repositories.AuditLogFilter) ([]*entities.AuditLog, int64, error) {
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

func (s *Service) VerifyIntegrity(ctx context.Context, startTime, endTime time.Time) (*IntegrityVerificationResult, error) {
	filter := repositories.AuditLogFilter{
		StartDate: &startTime,
		EndDate:   &endTime,
		Limit:     10000,
		Offset:    0,
	}

	logs, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve audit logs: %w", err)
	}

	result := &IntegrityVerificationResult{
		PeriodStart:  startTime,
		PeriodEnd:    endTime,
		TotalLogs:    int64(len(logs)),
		VerifiedAt:   time.Now().UTC(),
		BrokenLinks:  []string{},
		TamperedLogs: []string{},
	}

	var previousHash string
	for i, log := range logs {
		expectedHash := log.CalculateHash()

		if i == 0 && log.PreviousHash != "" {
			result.BrokenLinks = append(result.BrokenLinks, log.ID.String())
		}

		if log.CurrentHash != expectedHash {
			result.TamperedLogs = append(result.TamperedLogs, log.ID.String())
			result.IntegrityStatus = "compromised"
		}

		if log.PreviousHash != previousHash {
			if previousHash != "" {
				result.BrokenLinks = append(result.BrokenLinks, log.ID.String())
			}
		}

		previousHash = log.CurrentHash
	}

	if len(result.TamperedLogs) == 0 && len(result.BrokenLinks) == 0 {
		result.IntegrityStatus = "verified"
	} else if len(result.TamperedLogs) > 0 {
		result.IntegrityStatus = "compromised"
	} else {
		result.IntegrityStatus = "chain_broken"
	}

	s.logger.Info("Integrity verification completed",
		zap.String("status", result.IntegrityStatus),
		zap.Int64("total_logs", result.TotalLogs),
		zap.Int("tampered_count", len(result.TamperedLogs)),
		zap.Int("broken_links", len(result.BrokenLinks)),
	)

	return result, nil
}

func (s *Service) GenerateComplianceReport(ctx context.Context, reportType string, periodStart, periodEnd time.Time) (*entities.AuditComplianceReport, error) {
	filter := repositories.AuditLogFilter{
		StartDate: &periodStart,
		EndDate:   &periodEnd,
		Limit:     100000,
		Offset:    0,
	}

	logs, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve audit logs: %w", err)
	}

	totalCount, err := s.repo.Count(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to count audit logs: %w", err)
	}

	uniqueUsers := make(map[string]bool)
	actionBreakdown := make(map[string]int64)
	securityEvents := int64(0)
	failedLogins := int64(0)
	dataExports := int64(0)
	permissionChanges := int64(0)

	for _, log := range logs {
		uniqueUsers[log.UserID.String()] = true
		actionBreakdown[string(log.Action)]++

		if isSecurityEvent(log.Action) {
			securityEvents++
		}

		switch log.Action {
		case entities.AuditActionLogin:
			if status, ok := log.Metadata["status"].(string); ok && status == "failed" {
				failedLogins++
			}
		case entities.AuditActionDataExport:
			dataExports++
		case entities.AuditActionPermissionChange, entities.AuditActionAPIKeyCreate, entities.AuditActionAPIKeyRevoke:
			permissionChanges++
		}
	}

	integrityResult, err := s.VerifyIntegrity(ctx, periodStart, periodEnd)
	if err != nil {
		s.logger.Warn("failed to verify integrity for compliance report", zap.Error(err))
	}

	report := &entities.AuditComplianceReport{
		ID:                   uuid.New(),
		ReportType:           reportType,
		PeriodStart:          periodStart,
		PeriodEnd:            periodEnd,
		GeneratedAt:          time.Now().UTC(),
		TotalEvents:          totalCount,
		UniqueUsers:          int64(len(uniqueUsers)),
		ActionBreakdown:      actionBreakdown,
		SecurityEvents:       securityEvents,
		FailedLogins:         failedLogins,
		DataExports:          dataExports,
		PermissionChanges:    permissionChanges,
		IntegrityCheckStatus: integrityResult.IntegrityStatus,
		HashChainValid:       integrityResult.IntegrityStatus == "verified",
	}

	s.logger.Info("Compliance report generated",
		zap.String("report_type", reportType),
		zap.Time("period_start", periodStart),
		zap.Time("period_end", periodEnd),
		zap.Int64("total_events", totalCount),
		zap.Int64("unique_users", int64(len(uniqueUsers))),
	)

	return report, nil
}

func (s *Service) ExportAuditLogs(ctx context.Context, filter repositories.AuditLogFilter) ([]byte, error) {
	logs, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve audit logs: %w", err)
	}

	jsonBytes, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal audit logs: %w", err)
	}

	return jsonBytes, nil
}

func isSecurityEvent(action entities.AuditAction) bool {
	securityActions := map[entities.AuditAction]bool{
		entities.AuditActionLogin:            true,
		entities.AuditActionLogout:           true,
		entities.AuditActionPasswordChange:   true,
		entities.AuditActionMFAEnable:        true,
		entities.AuditActionMFADisable:       true,
		entities.AuditActionAPIKeyCreate:     true,
		entities.AuditActionAPIKeyRevoke:     true,
		entities.AuditActionPermissionChange: true,
		entities.AuditActionAdminAction:      true,
		entities.AuditActionDataDelete:       true,
	}
	return securityActions[action]
}

type IntegrityVerificationResult struct {
	PeriodStart     time.Time
	PeriodEnd       time.Time
	TotalLogs       int64
	VerifiedAt      time.Time
	IntegrityStatus string
	BrokenLinks     []string
	TamperedLogs    []string
}

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
