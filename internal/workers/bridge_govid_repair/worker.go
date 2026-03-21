package bridgegovidrepair

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type UserRepository interface {
	FindApprovedNotActiveBridge(ctx context.Context, limit int) ([]uuid.UUID, error)
}

type KYCService interface {
	RepairBridgeGovID(ctx context.Context, userID uuid.UUID) error
}

type Worker struct {
	userRepo  UserRepository
	kycSvc    KYCService
	logger    *zap.Logger
	interval  time.Duration
	batchSize int
}

func NewWorker(userRepo UserRepository, kycSvc KYCService, logger *zap.Logger) *Worker {
	return &Worker{
		userRepo:  userRepo,
		kycSvc:    kycSvc,
		logger:    logger,
		interval:  10 * time.Minute,
		batchSize: 20,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting bridge gov ID repair worker")
	w.run(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.run(ctx)
		}
	}
}

func (w *Worker) run(ctx context.Context) {
	ids, err := w.userRepo.FindApprovedNotActiveBridge(ctx, w.batchSize)
	if err != nil {
		w.logger.Error("bridge_govid_repair: failed to query users", zap.Error(err))
		return
	}
	for _, id := range ids {
		if err := w.kycSvc.RepairBridgeGovID(ctx, id); err != nil {
			w.logger.Warn("bridge_govid_repair: repair failed",
				zap.String("user_id", id.String()), zap.Error(err))
		}
	}
}
