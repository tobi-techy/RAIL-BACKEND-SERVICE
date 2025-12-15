package reconciliation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Service handles reconciliation operations
type Service struct {
	// Repositories
	reconciliationRepo repositories.ReconciliationRepository
	ledgerRepo         *repositories.LedgerRepository
	depositRepo        *repositories.DepositRepository
	withdrawalRepo     *repositories.WithdrawalRepository
	conversionRepo     *repositories.ConversionRepository

	// External services
	ledgerService  LedgerService
	circleClient   CircleClient
	alpacaClient   AlpacaClient

	// Observability
	logger         *logger.Logger
	metricsService MetricsService

	// Configuration
	config *Config
}

// Config holds reconciliation service configuration
type Config struct {
	AutoCorrectLowSeverity bool
	ToleranceCircle        decimal.Decimal
	ToleranceAlpaca        decimal.Decimal
	EnableAlerting         bool
	AlertWebhookURL        string
	AlertWebhookSecret     string
	PagerDutyRoutingKey    string
	SlackWebhookURL        string
}

// LedgerService interface for ledger operations
type LedgerService interface {
	GetSystemBufferBalance(ctx context.Context, accountType string) (decimal.Decimal, error)
	GetTotalUserFiatExposure(ctx context.Context) (decimal.Decimal, error)
}

// CircleClient interface for Circle API operations
type CircleClient interface {
	GetTotalUSDCBalance(ctx context.Context) (decimal.Decimal, error)
}

// AlpacaClient interface for Alpaca API operations
type AlpacaClient interface {
	GetTotalBuyingPower(ctx context.Context) (decimal.Decimal, error)
}

// MetricsService interface for metrics operations
type MetricsService interface {
	RecordReconciliationRun(runType string)
	RecordExceptionAutoCorrected(checkType string)
	RecordReconciliationAlert(checkType, severity string)
	RecordReconciliationCompleted(runType string, totalChecks, passedChecks, failedChecks, exceptionsCount int)
	RecordCheckResult(checkType string, passed bool, executionTime time.Duration)
	RecordDiscrepancyAmount(checkType string, amount decimal.Decimal)
}

// NewService creates a new reconciliation service
func NewService(
	reconciliationRepo repositories.ReconciliationRepository,
	ledgerRepo *repositories.LedgerRepository,
	depositRepo *repositories.DepositRepository,
	withdrawalRepo *repositories.WithdrawalRepository,
	conversionRepo *repositories.ConversionRepository,
	ledgerService LedgerService,
	circleClient CircleClient,
	alpacaClient AlpacaClient,
	logger *logger.Logger,
	metricsService MetricsService,
	config *Config,
) *Service {
	return &Service{
		reconciliationRepo: reconciliationRepo,
		ledgerRepo:         ledgerRepo,
		depositRepo:        depositRepo,
		withdrawalRepo:     withdrawalRepo,
		conversionRepo:     conversionRepo,
		ledgerService:      ledgerService,
		circleClient:       circleClient,
		alpacaClient:       alpacaClient,
		logger:             logger,
		metricsService:     metricsService,
		config:             config,
	}
}

// RunReconciliation executes a full reconciliation run
func (s *Service) RunReconciliation(ctx context.Context, runType string) (*entities.ReconciliationReport, error) {
	ctx, span := otel.Tracer("reconciliation.service").Start(ctx, "RunReconciliation")
	defer span.End()

	span.SetAttributes(attribute.String("run_type", runType))

	s.logger.Info("Starting reconciliation run", "run_type", runType)

	// Create reconciliation report
	report := entities.NewReconciliationReport(runType)
	report.Status = entities.ReconciliationStatusInProgress

	if err := s.reconciliationRepo.CreateReport(ctx, report); err != nil {
		s.logger.Error("Failed to create reconciliation report", "error", err)
		return nil, fmt.Errorf("failed to create reconciliation report: %w", err)
	}

	// Record metric
	s.metricsService.RecordReconciliationRun(runType)

	// Run all checks
	checkResults := s.runAllChecks(ctx, report.ID)

	// Process results and create check records
	var allExceptions []*entities.ReconciliationException
	for _, result := range checkResults {
		check := s.createCheckRecord(report.ID, result)
		if err := s.reconciliationRepo.CreateCheck(ctx, check); err != nil {
			s.logger.Error("Failed to create check record", "check_type", result.CheckType, "error", err)
		}

		report.TotalChecks++
		if result.Passed {
			report.PassedChecks++
		} else {
			report.FailedChecks++
		}

		// Collect exceptions
		for _, exception := range result.Exceptions {
			exc := exception
			exc.CheckID = check.ID
			allExceptions = append(allExceptions, &exc)
		}
	}

	// Save exceptions in batch
	if len(allExceptions) > 0 {
		if err := s.reconciliationRepo.CreateExceptionsBatch(ctx, allExceptions); err != nil {
			s.logger.Error("Failed to save exceptions", "error", err)
		}
		report.ExceptionsCount = len(allExceptions)

		// Auto-correct low severity exceptions
		if s.config.AutoCorrectLowSeverity {
			s.autoCorrectExceptions(ctx, allExceptions)
		}

		// Send alerts for high/critical exceptions
		if s.config.EnableAlerting {
			s.sendAlerts(ctx, allExceptions)
		}
	}

	// Update report with final status
	now := time.Now()
	report.CompletedAt = &now
	report.Status = entities.ReconciliationStatusCompleted

	if err := s.reconciliationRepo.UpdateReport(ctx, report); err != nil {
		s.logger.Error("Failed to update reconciliation report", "error", err)
	}

	// Record metrics
	s.recordMetrics(report, checkResults)

	s.logger.Info("Reconciliation run completed",
		"report_id", report.ID,
		"total_checks", report.TotalChecks,
		"passed", report.PassedChecks,
		"failed", report.FailedChecks,
		"exceptions", report.ExceptionsCount,
	)

	return report, nil
}

// runAllChecks executes all reconciliation checks
func (s *Service) runAllChecks(ctx context.Context, reportID uuid.UUID) []*entities.ReconciliationCheckResult {
	checks := []func(context.Context, uuid.UUID) (*entities.ReconciliationCheckResult, error){
		s.CheckLedgerConsistency,
		s.CheckCircleBalance,
		s.CheckAlpacaBalance,
		s.CheckDeposits,
		s.CheckConversionJobs,
		s.CheckWithdrawals,
	}

	results := make([]*entities.ReconciliationCheckResult, 0, len(checks))

	for _, checkFunc := range checks {
		result, err := checkFunc(ctx, reportID)
		if err != nil {
			s.logger.Error("Check failed with error", "error", err)
			// Create a failed result
			result = &entities.ReconciliationCheckResult{
				Passed:       false,
				ErrorMessage: err.Error(),
			}
		}
		results = append(results, result)
	}

	return results
}

// createCheckRecord converts check result to database record
func (s *Service) createCheckRecord(reportID uuid.UUID, result *entities.ReconciliationCheckResult) *entities.ReconciliationCheck {
	return &entities.ReconciliationCheck{
		ID:              uuid.New(),
		ReportID:        reportID,
		CheckType:       result.CheckType,
		Status:          entities.ReconciliationStatusCompleted,
		ExpectedValue:   result.ExpectedValue,
		ActualValue:     result.ActualValue,
		Difference:      result.Difference,
		Passed:          result.Passed,
		ErrorMessage:    result.ErrorMessage,
		ExecutionTimeMs: result.ExecutionTime.Milliseconds(),
		Metadata:        result.Metadata,
		CreatedAt:       time.Now(),
	}
}

// autoCorrectExceptions attempts to auto-correct low severity exceptions
func (s *Service) autoCorrectExceptions(ctx context.Context, exceptions []*entities.ReconciliationException) {
	for _, exception := range exceptions {
		if exception.CanAutoCorrect() {
			s.logger.Info("Auto-correcting exception",
				"exception_id", exception.ID,
				"check_type", exception.CheckType,
				"difference", exception.Difference.String(),
			)

			// Apply correction based on check type
			correctionAction := s.determineCorrectionAction(exception)
			if correctionAction != "" {
				exception.MarkCorrected(correctionAction)
				if err := s.reconciliationRepo.UpdateException(ctx, exception); err != nil {
					s.logger.Error("Failed to update auto-corrected exception", "error", err)
				}

				// Record metric
				s.metricsService.RecordExceptionAutoCorrected(string(exception.CheckType))
			}
		}
	}
}

// determineCorrectionAction determines the appropriate correction action for an exception
func (s *Service) determineCorrectionAction(exception *entities.ReconciliationException) string {
	// For now, log the discrepancy - actual correction logic would be implemented
	// based on specific business rules for each check type
	switch exception.CheckType {
	case entities.ReconciliationCheckLedgerConsistency:
		return "Logged ledger inconsistency for manual review"
	case entities.ReconciliationCheckCircleBalance:
		return "Logged Circle balance discrepancy for investigation"
	case entities.ReconciliationCheckAlpacaBalance:
		return "Logged Alpaca balance discrepancy for investigation"
	default:
		return "Logged discrepancy for manual review"
	}
}

// sendAlerts sends alerts for high/critical exceptions
func (s *Service) sendAlerts(ctx context.Context, exceptions []*entities.ReconciliationException) {
	highPriorityExceptions := s.filterHighPriorityExceptions(exceptions)

	if len(highPriorityExceptions) == 0 {
		return
	}

	s.logger.Warn("High priority reconciliation exceptions detected",
		"count", len(highPriorityExceptions),
	)

	// Record alert metric
	for _, exception := range highPriorityExceptions {
		s.metricsService.RecordReconciliationAlert(
			string(exception.CheckType),
			string(exception.Severity),
		)
	}

	// Send webhook notification (if configured)
	if s.config.AlertWebhookURL != "" {
		go s.sendWebhookAlert(ctx, highPriorityExceptions)
	}

	// Send PagerDuty alerts for critical issues
	if s.config.PagerDutyRoutingKey != "" {
		go s.sendPagerDutyAlerts(ctx, highPriorityExceptions)
	}

	// Send Slack notification
	if s.config.SlackWebhookURL != "" {
		go s.sendSlackAlert(ctx, highPriorityExceptions)
	}
}

// filterHighPriorityExceptions filters exceptions by severity
func (s *Service) filterHighPriorityExceptions(exceptions []*entities.ReconciliationException) []*entities.ReconciliationException {
	var highPriority []*entities.ReconciliationException
	for _, exception := range exceptions {
		if exception.Severity == entities.ExceptionSeverityHigh || exception.Severity == entities.ExceptionSeverityCritical {
			highPriority = append(highPriority, exception)
		}
	}
	return highPriority
}

// sendWebhookAlert sends a webhook notification for exceptions
func (s *Service) sendWebhookAlert(ctx context.Context, exceptions []*entities.ReconciliationException) {
	if s.config.AlertWebhookURL == "" {
		return
	}

	payload := struct {
		EventType  string                              `json:"event_type"`
		Timestamp  time.Time                           `json:"timestamp"`
		Severity   string                              `json:"severity"`
		Count      int                                 `json:"count"`
		Exceptions []*entities.ReconciliationException `json:"exceptions"`
	}{
		EventType:  "reconciliation_alert",
		Timestamp:  time.Now().UTC(),
		Severity:   s.getHighestSeverity(exceptions),
		Count:      len(exceptions),
		Exceptions: exceptions,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		s.logger.Error("Failed to marshal webhook payload", "error", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.config.AlertWebhookURL, bytes.NewReader(body))
	if err != nil {
		s.logger.Error("Failed to create webhook request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Add HMAC signature if secret is configured
	if s.config.AlertWebhookSecret != "" {
		mac := hmac.New(sha256.New, []byte(s.config.AlertWebhookSecret))
		mac.Write(body)
		signature := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Webhook-Signature", signature)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.logger.Error("Webhook request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		s.logger.Error("Webhook returned error status", "status", resp.StatusCode)
	} else {
		s.logger.Info("Webhook alert sent successfully", "exceptions_count", len(exceptions))
	}
}

// sendPagerDutyAlerts sends alerts to PagerDuty for critical issues
func (s *Service) sendPagerDutyAlerts(ctx context.Context, exceptions []*entities.ReconciliationException) {
	for _, exc := range exceptions {
		if exc.Severity != entities.ExceptionSeverityCritical {
			continue
		}

		affectedUserID := ""
		if exc.AffectedUserID != nil {
			affectedUserID = exc.AffectedUserID.String()
		}

		payload := map[string]interface{}{
			"routing_key":  s.config.PagerDutyRoutingKey,
			"event_action": "trigger",
			"payload": map[string]interface{}{
				"summary":  fmt.Sprintf("Reconciliation Exception: %s", exc.CheckType),
				"severity": "critical",
				"source":   "rail-reconciliation",
				"custom_details": map[string]interface{}{
					"check_type":       exc.CheckType,
					"affected_user_id": affectedUserID,
					"description":      exc.Description,
					"created_at":       exc.CreatedAt,
				},
			},
		}

		body, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://events.pagerduty.com/v2/enqueue", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			s.logger.Error("PagerDuty request failed", "error", err)
			continue
		}
		resp.Body.Close()
	}
}

// sendSlackAlert sends a summary alert to Slack
func (s *Service) sendSlackAlert(ctx context.Context, exceptions []*entities.ReconciliationException) {
	blocks := []map[string]interface{}{
		{
			"type": "header",
			"text": map[string]string{
				"type": "plain_text",
				"text": "🚨 Reconciliation Alert",
			},
		},
		{
			"type": "section",
			"text": map[string]string{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*%d high-priority exceptions detected*", len(exceptions)),
			},
		},
	}

	for i, exc := range exceptions {
		if i >= 5 { // Limit to 5 exceptions in Slack message
			blocks = append(blocks, map[string]interface{}{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": fmt.Sprintf("_...and %d more_", len(exceptions)-5),
				},
			})
			break
		}
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"fields": []map[string]string{
				{"type": "mrkdwn", "text": fmt.Sprintf("*Type:* %s", exc.CheckType)},
				{"type": "mrkdwn", "text": fmt.Sprintf("*Severity:* %s", exc.Severity)},
			},
		})
	}

	payload := map[string]interface{}{"blocks": blocks}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", s.config.SlackWebhookURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.logger.Error("Slack request failed", "error", err)
		return
	}
	resp.Body.Close()
	s.logger.Info("Slack alert sent successfully")
}

// getHighestSeverity returns the highest severity from exceptions
func (s *Service) getHighestSeverity(exceptions []*entities.ReconciliationException) string {
	for _, exc := range exceptions {
		if exc.Severity == entities.ExceptionSeverityCritical {
			return string(entities.ExceptionSeverityCritical)
		}
	}
	for _, exc := range exceptions {
		if exc.Severity == entities.ExceptionSeverityHigh {
			return string(entities.ExceptionSeverityHigh)
		}
	}
	return string(entities.ExceptionSeverityMedium)
}

// recordMetrics records reconciliation metrics
func (s *Service) recordMetrics(report *entities.ReconciliationReport, results []*entities.ReconciliationCheckResult) {
	// Record overall reconciliation metrics
	s.metricsService.RecordReconciliationCompleted(
		report.RunType,
		report.TotalChecks,
		report.PassedChecks,
		report.FailedChecks,
		report.ExceptionsCount,
	)

	// Record per-check metrics
	for _, result := range results {
		s.metricsService.RecordCheckResult(
			string(result.CheckType),
			result.Passed,
			result.ExecutionTime,
		)

		// Record discrepancy amount if check failed
		if !result.Passed && !result.Difference.IsZero() {
			s.metricsService.RecordDiscrepancyAmount(
				string(result.CheckType),
				result.Difference.Abs(),
			)
		}
	}
}

// GetLatestReport retrieves the most recent reconciliation report
func (s *Service) GetLatestReport(ctx context.Context, runType string) (*entities.ReconciliationReport, error) {
	return s.reconciliationRepo.GetLatestReportByType(ctx, runType)
}

// GetReportWithDetails retrieves a report with all checks and exceptions
func (s *Service) GetReportWithDetails(ctx context.Context, reportID uuid.UUID) (*ReportDetails, error) {
	report, err := s.reconciliationRepo.GetReportByID(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("failed to get report: %w", err)
	}

	checks, err := s.reconciliationRepo.GetChecksByReportID(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("failed to get checks: %w", err)
	}

	exceptions, err := s.reconciliationRepo.GetExceptionsByReportID(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("failed to get exceptions: %w", err)
	}

	return &ReportDetails{
		Report:     report,
		Checks:     checks,
		Exceptions: exceptions,
	}, nil
}

// GetUnresolvedExceptions retrieves all unresolved exceptions
func (s *Service) GetUnresolvedExceptions(ctx context.Context, severity entities.ExceptionSeverity) ([]*entities.ReconciliationException, error) {
	return s.reconciliationRepo.GetUnresolvedExceptions(ctx, severity)
}

// ResolveException manually resolves an exception
func (s *Service) ResolveException(ctx context.Context, exceptionID uuid.UUID, resolvedBy, notes string) error {
	exceptions, err := s.reconciliationRepo.GetExceptionsByReportID(ctx, uuid.Nil) // Need to get by ID
	if err != nil {
		return fmt.Errorf("failed to get exception: %w", err)
	}

	// Find the exception (this is a simplified implementation)
	var exception *entities.ReconciliationException
	for _, exc := range exceptions {
		if exc.ID == exceptionID {
			exception = exc
			break
		}
	}

	if exception == nil {
		return fmt.Errorf("exception not found: %s", exceptionID)
	}

	exception.MarkResolved(resolvedBy, notes)

	if err := s.reconciliationRepo.UpdateException(ctx, exception); err != nil {
		return fmt.Errorf("failed to update exception: %w", err)
	}

	s.logger.Info("Exception manually resolved",
		"exception_id", exceptionID,
		"resolved_by", resolvedBy,
	)

	return nil
}

// ReportDetails contains a reconciliation report with all related data
type ReportDetails struct {
	Report     *entities.ReconciliationReport
	Checks     []*entities.ReconciliationCheck
	Exceptions []*entities.ReconciliationException
}
