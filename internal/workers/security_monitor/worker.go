package security_monitor

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/services/security"
)

// Worker performs automated security monitoring and breach detection
type Worker struct {
	incidentService *security.IncidentResponseService
	fraudService    *security.FraudDetectionService
	mfaService      *security.MFAService
	logger          *zap.Logger
	interval        time.Duration
	stopCh          chan struct{}
}

// NewWorker creates a new security monitoring worker
func NewWorker(
	incidentService *security.IncidentResponseService,
	fraudService *security.FraudDetectionService,
	mfaService *security.MFAService,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		incidentService: incidentService,
		fraudService:    fraudService,
		mfaService:      mfaService,
		logger:          logger,
		interval:        5 * time.Minute,
		stopCh:          make(chan struct{}),
	}
}

// Start begins the security monitoring loop
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting security monitoring worker")

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run immediately on start
	w.runChecks(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Security monitoring worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("Security monitoring worker stopped")
			return
		case <-ticker.C:
			w.runChecks(ctx)
		}
	}
}

// Stop gracefully stops the worker
func (w *Worker) Stop() {
	close(w.stopCh)
}

// runChecks performs all security monitoring checks
func (w *Worker) runChecks(ctx context.Context) {
	w.logger.Debug("Running security monitoring checks")

	// 1. Detect breach attempts (credential stuffing, brute force)
	if err := w.incidentService.DetectBreachAttempt(ctx); err != nil {
		w.logger.Error("Breach detection check failed", zap.Error(err))
	}

	// 2. Enforce MFA for high-value accounts
	if count, err := w.mfaService.EnforceForHighValueAccounts(ctx); err != nil {
		w.logger.Error("MFA enforcement check failed", zap.Error(err))
	} else if count > 0 {
		w.logger.Info("MFA enforced for high-value accounts", zap.Int("count", count))
	}

	// 3. Update behavior patterns for active users (sample)
	w.updateBehaviorPatterns(ctx)

	// 4. Cleanup old security data
	w.cleanupOldData(ctx)

	w.logger.Debug("Security monitoring checks completed")
}

// updateBehaviorPatterns updates fraud detection patterns for active users
func (w *Worker) updateBehaviorPatterns(ctx context.Context) {
	// This would typically query for users with recent activity
	// and update their behavior patterns for anomaly detection
	// Implementation depends on how you want to sample users
}

// cleanupOldData removes old security-related data
func (w *Worker) cleanupOldData(ctx context.Context) {
	// Cleanup is handled by database functions
	// This is a placeholder for any additional cleanup logic
}

// RunOnce performs a single security check (useful for testing)
func (w *Worker) RunOnce(ctx context.Context) {
	w.runChecks(ctx)
}
