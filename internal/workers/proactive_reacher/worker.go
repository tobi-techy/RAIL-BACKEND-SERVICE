// Package proactive_reacher runs Miriam's proactive loop: on a schedule it asks
// the Python agent (MIRIAM's LLM brain) whether any reachable user has one
// genuinely useful thing worth telling them right now, and delivers the drafted
// message through the iMessage bridge.
//
// Division of labour:
//   - Python decides ("should_reach_out" + message), fail-open to "stay quiet".
//   - Go owns global schedule, quiet hours / daily cap, and delivery.
//
// To avoid burning model tokens, the worker peeks the proactive guard (quiet
// hours + category flags) BEFORE calling the analyst; the actual send consumes
// the daily cap via the bridge dispatcher's own guard check.
package proactive_reacher

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

// IdentityRepo lists platform identities that completed a handshake, so the
// worker only analyses users it could actually reach.
type IdentityRepo interface {
	ListLinkedByPlatform(ctx context.Context, platform entities.Platform) ([]*entities.PlatformIdentity, error)
}

// Analyzer asks the Python agent for a proactive decision about one user.
type Analyzer interface {
	AnalyzeProactive(ctx context.Context, userID uuid.UUID, email, role string) (*ai.PythonProactiveOutcome, error)
}

// Guard reports whether a message may be attempted before the expensive
// analysis runs, without consuming the daily cap.
type Guard interface {
	CanSendCategory(ctx context.Context, userID uuid.UUID, category string) bool
}

// Sender delivers a proactive message on the user's channel.
type Sender interface {
	SendChatMessage(ctx context.Context, userID uuid.UUID, message string) error
}

// Worker periodically analyses linked users and nudges those with something
// worth saying.
type Worker struct {
	identities IdentityRepo
	analyzer   Analyzer
	guard      Guard
	sender     Sender
	interval   time.Duration
	logger     *zap.Logger
}

// NewWorker builds a reacher. interval<=0 defaults to 30 minutes.
func NewWorker(
	identities IdentityRepo,
	analyzer Analyzer,
	guard Guard,
	sender Sender,
	interval time.Duration,
	logger *zap.Logger,
) *Worker {
	if logger == nil {
		logger = zap.NewNop()
	}
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	return &Worker{
		identities: identities,
		analyzer:   analyzer,
		guard:      guard,
		sender:     sender,
		interval:   interval,
		logger:     logger,
	}
}

// Start runs the reacher loop until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.logger.Info("proactive reacher started",
		zap.Duration("interval", w.interval))

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("proactive reacher stopping")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// runOnce inspects every user linked on the channel exactly once and nudges
// those the analyst flags — each guarded by quiet hours and the daily cap.
func (w *Worker) runOnce(ctx context.Context) {
	identities, err := w.identities.ListLinkedByPlatform(ctx, entities.PlatformIMessage)
	if err != nil {
		w.logger.Warn("proactive reacher: list linked identities failed", zap.Error(err))
		return
	}

	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, pi := range identities {
		if pi == nil {
			continue
		}
		if _, dup := seen[pi.UserID]; dup {
			continue
		}
		seen[pi.UserID] = struct{}{}

		w.analyzeAndSend(ctx, pi.UserID)
	}
}

func (w *Worker) analyzeAndSend(ctx context.Context, userID uuid.UUID) {
	if !w.guard.CanSendCategory(ctx, userID, platform.ProactiveCategoryNudge) {
		return
	}

	outcome, err := w.analyzer.AnalyzeProactive(ctx, userID, "", "user")
	if err != nil {
		w.logger.Debug("proactive reacher: analysis failed",
			zap.Stringer("user_id", userID), zap.Error(err))
		return
	}
	if outcome == nil || !outcome.ShouldReachOut || strings.TrimSpace(outcome.Message) == "" {
		return
	}

	if err := w.sender.SendChatMessage(ctx, userID, outcome.Message); err != nil {
		w.logger.Warn("proactive reacher: delivery failed",
			zap.Stringer("user_id", userID),
			zap.String("priority", outcome.Priority),
			zap.String("category", outcome.Category),
			zap.Error(err))
		return
	}
	w.logger.Info("proactive reacher: message sent",
		zap.Stringer("user_id", userID),
		zap.String("priority", outcome.Priority),
		zap.String("category", outcome.Category),
		zap.String("reason", outcome.Reason))
}
