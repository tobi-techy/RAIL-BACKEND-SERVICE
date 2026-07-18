package miriam

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// --- Self-review dependencies (narrow interfaces satisfied by existing repos) ---

// SelfReviewRepository persists self-reviews, reads recent receipts, and writes
// the learning signals that steer future money decisions. All money influence
// flows through CreateLearningSignal → RecentLearningBias → DecisionEngine, the
// same audited path used everywhere else; self-review never moves money itself.
type SelfReviewRepository interface {
	ListReceiptsSince(ctx context.Context, userID uuid.UUID, since time.Time) ([]entities.MiriamDecisionReceipt, error)
	CreateSelfReview(ctx context.Context, review *entities.MiriamSelfReview) error
	LastSelfReviewAt(ctx context.Context, userID uuid.UUID) (*time.Time, error)
	LastSelfReviewNoteAt(ctx context.Context, userID uuid.UUID) (*time.Time, error)
	CreateLearningSignal(ctx context.Context, signal *entities.MiriamLearningSignal) error
}

// SelfReviewNudgeReader reads recent nudges for messaging-engagement grading.
type SelfReviewNudgeReader interface {
	ListNudgesSince(ctx context.Context, userID uuid.UUID, since time.Time) ([]entities.ProactiveNudge, error)
}

// SelfReviewHealthReader reads the user's health-score history so self-review can
// compare before/after the review window.
type SelfReviewHealthReader interface {
	GetHealthTrend(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamFinancialHealthScore, error)
}

// Tuning constants for the self-review loop. Kept conservative: a single review
// can only nudge behavior a little, and can only make Miriam more cautious with
// user money, never more aggressive.
const (
	selfReviewWindow      = 7 * 24 * time.Hour
	selfReviewMinInterval = 24 * time.Hour
	selfReviewNoteEvery   = 6*24*time.Hour + 12*time.Hour // ~weekly

	// Health-score deltas that classify an action window as helped/harmed.
	healthHelpedDelta = 3
	healthHarmedDelta = -3

	// Learning-signal weights (bounded). Harm feeds a "reversed" signal (−2×
	// weight in RecentLearningBias); help feeds a small "accepted" signal.
	selfReviewHarmWeight = 0.5 // → −1.0 bias contribution
	selfReviewHelpWeight = 0.5 // → +0.5 bias contribution

	// Messaging fatigue threshold: reduce cadence when this share of a
	// meaningful sample of nudges was dismissed.
	nudgeFatigueMinSample = 3
	nudgeFatigueRatio     = 0.5
)

// SelfReviewEngine is Miriam's meta-pass: she grades her own recent actions and
// messaging, feeds the verdict back into the learning-bias and nudge-cadence
// levers, records an audit trail, and occasionally tells the user how she's
// doing. This is what makes her accountable for her own work — not just her
// forecasts (which OutcomeTracker already handles).
type SelfReviewEngine struct {
	repo     SelfReviewRepository
	nudges   SelfReviewNudgeReader
	health   SelfReviewHealthReader
	notifier Notifier
	logger   *zap.Logger
}

// NewSelfReviewEngine creates a self-review engine.
func NewSelfReviewEngine(
	repo SelfReviewRepository,
	nudges SelfReviewNudgeReader,
	health SelfReviewHealthReader,
	notifier Notifier,
	logger *zap.Logger,
) *SelfReviewEngine {
	return &SelfReviewEngine{
		repo: repo, nudges: nudges, health: health, notifier: notifier, logger: logger,
	}
}

// SetNotifier injects a Notifier after construction (deferred wiring).
func (e *SelfReviewEngine) SetNotifier(n Notifier) {
	e.notifier = n
}

// Run performs one self-review pass for a user. It self-gates to at most once
// per selfReviewMinInterval, so callers can invoke it on every worker tick
// cheaply. Returns the persisted review (nil when skipped).
func (e *SelfReviewEngine) Run(ctx context.Context, userID uuid.UUID) (*entities.MiriamSelfReview, error) {
	if e.repo == nil {
		return nil, nil
	}

	// 1. Daily gate.
	if last, err := e.repo.LastSelfReviewAt(ctx, userID); err == nil && last != nil {
		if time.Since(*last) < selfReviewMinInterval {
			return nil, nil
		}
	}

	now := time.Now().UTC()
	windowStart := now.Add(-selfReviewWindow)

	// 2. Grade actions and messaging.
	actions := e.gradeActions(ctx, userID, windowStart)
	messaging := e.gradeMessaging(ctx, userID, windowStart)

	// 3. Adjust money behavior via the existing learning-signal channel.
	adjustments := e.applyLearningAdjustments(ctx, userID, actions)
	adjustments["cadence_hint"] = messaging.cadenceHint

	review := &entities.MiriamSelfReview{
		ID:              uuid.New(),
		UserID:          userID,
		PeriodStart:     windowStart,
		PeriodEnd:       now,
		ActionsReviewed: actions.reviewed,
		ActionsHelped:   actions.helped,
		ActionsNeutral:  actions.neutral,
		ActionsHarmed:   actions.harmed,
		NudgesSent:      messaging.sent,
		NudgesDismissed: messaging.dismissed,
		AvgHealthBefore: actions.healthBefore,
		AvgHealthAfter:  actions.healthAfter,
		CadenceHint:     messaging.cadenceHint,
		Verdict:         buildSelfReviewVerdict(actions, messaging),
		Adjustments:     mustJSON(adjustments),
		CreatedAt:       now,
	}

	// 4. User-facing note (weekly, only when there's something worth saying).
	if e.shouldSendNote(ctx, userID, actions, messaging) {
		if note := selfReviewNote(actions, messaging); note != "" && e.notifier != nil {
			if err := e.notifier.SendGenericNotification(ctx, userID, "Miriam", note); err == nil {
				review.NoteSent = true
			} else if e.logger != nil {
				e.logger.Debug("self-review note send failed",
					zap.String("user_id", userID.String()), zap.Error(err))
			}
		}
	}

	// 5. Persist the audit row.
	if err := e.repo.CreateSelfReview(ctx, review); err != nil {
		return nil, fmt.Errorf("persist self review: %w", err)
	}

	if e.logger != nil {
		e.logger.Info("miriam self-review complete",
			zap.String("user_id", userID.String()),
			zap.Int("actions", actions.reviewed),
			zap.Int("helped", actions.helped),
			zap.Int("harmed", actions.harmed),
			zap.Int("nudges", messaging.sent),
			zap.Int("dismissed", messaging.dismissed),
			zap.String("cadence", messaging.cadenceHint))
	}
	return review, nil
}

// actionGrade holds the outcome of grading a user's executed actions.
type actionGrade struct {
	reviewed     int
	helped       int
	neutral      int
	harmed       int
	healthBefore int
	healthAfter  int
	// lastReceiptID anchors the aggregate learning signal (FK requires a real
	// receipt). Zero when no executed/failed receipt exists.
	lastReceiptID uuid.UUID
}

// gradeActions classifies executed/failed receipts in the window as helped,
// neutral, or harmed using the health-score trajectory across the window. A
// failed action always counts as harm; otherwise the window's health delta
// distributes the executed actions.
func (e *SelfReviewEngine) gradeActions(ctx context.Context, userID uuid.UUID, windowStart time.Time) actionGrade {
	var g actionGrade

	receipts, err := e.repo.ListReceiptsSince(ctx, userID, windowStart)
	if err != nil {
		if e.logger != nil {
			e.logger.Debug("self-review: list receipts failed",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
		return g
	}

	before, after := e.healthDelta(ctx, userID, windowStart)
	g.healthBefore = before
	g.healthAfter = after
	delta := after - before

	var executed int
	for _, r := range receipts {
		switch r.Status {
		case entities.MiriamReceiptStatusExecuted:
			g.reviewed++
			executed++
			g.lastReceiptID = r.ID
		case entities.MiriamReceiptStatusFailed:
			g.reviewed++
			g.harmed++
			g.lastReceiptID = r.ID
		default:
			// suggested / skipped are not "actions taken" — ignore.
		}
	}

	if executed > 0 {
		switch {
		case delta >= healthHelpedDelta:
			g.helped += executed
		case delta <= healthHarmedDelta:
			g.harmed += executed
		default:
			g.neutral += executed
		}
	}

	return g
}

// healthDelta returns the average health score before and after the review
// window. "Before" is the score nearest the window start; "after" is the latest.
func (e *SelfReviewEngine) healthDelta(ctx context.Context, userID uuid.UUID, windowStart time.Time) (before, after int) {
	if e.health == nil {
		return 0, 0
	}
	scores, err := e.health.GetHealthTrend(ctx, userID, 30)
	if err != nil || len(scores) == 0 {
		return 0, 0
	}
	// GetRecentScores returns newest-first.
	after = scores[0].OverallScore
	before = scores[len(scores)-1].OverallScore
	for _, s := range scores {
		if s.CreatedAt.Before(windowStart) {
			before = s.OverallScore
			break
		}
	}
	return before, after
}

// applyLearningAdjustments writes at most one bounded learning signal reflecting
// the dominant action verdict, and returns a description for the audit row.
func (e *SelfReviewEngine) applyLearningAdjustments(ctx context.Context, userID uuid.UUID, g actionGrade) map[string]interface{} {
	adj := map[string]interface{}{
		"signal":        "none",
		"signal_weight": 0,
	}
	if g.lastReceiptID == uuid.Nil {
		return adj
	}

	var signal string
	var weight float64
	switch {
	case g.harmed > g.helped:
		signal = entities.MiriamFeedbackReversed
		weight = selfReviewHarmWeight
	case g.helped > g.harmed && g.harmed == 0:
		signal = entities.MiriamFeedbackAccepted
		weight = selfReviewHelpWeight
	default:
		return adj // mixed / neutral — don't move the lever
	}

	sig := &entities.MiriamLearningSignal{
		ID:        uuid.New(),
		UserID:    userID,
		ReceiptID: g.lastReceiptID,
		Signal:    signal,
		Weight:    decimal.NewFromFloat(weight),
		Metadata: mustJSON(map[string]interface{}{
			"source":  "self_review",
			"helped":  g.helped,
			"harmed":  g.harmed,
			"neutral": g.neutral,
		}),
		CreatedAt: time.Now().UTC(),
	}
	if err := e.repo.CreateLearningSignal(ctx, sig); err != nil {
		if e.logger != nil {
			e.logger.Warn("self-review: learning signal write failed",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
		return adj
	}
	adj["signal"] = signal
	adj["signal_weight"] = weight
	return adj
}

// messagingGrade holds the outcome of grading Miriam's recent messaging.
type messagingGrade struct {
	sent        int
	dismissed   int
	cadenceHint string
}

// gradeMessaging measures dismissal fatigue across delivered nudges and derives
// a cadence hint. It only ever throttles messaging — it never touches money.
func (e *SelfReviewEngine) gradeMessaging(ctx context.Context, userID uuid.UUID, windowStart time.Time) messagingGrade {
	g := messagingGrade{cadenceHint: entities.NudgeCadenceNormal}
	if e.nudges == nil {
		return g
	}
	nudges, err := e.nudges.ListNudgesSince(ctx, userID, windowStart)
	if err != nil {
		if e.logger != nil {
			e.logger.Debug("self-review: list nudges failed",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
		return g
	}
	for _, n := range nudges {
		if n.DeliveredAt != nil {
			g.sent++
		}
		if n.DismissedAt != nil {
			g.dismissed++
		}
	}
	if g.sent >= nudgeFatigueMinSample {
		ratio := float64(g.dismissed) / float64(g.sent)
		if ratio >= nudgeFatigueRatio {
			g.cadenceHint = entities.NudgeCadenceReduce
		}
	}
	return g
}

// shouldSendNote gates the weekly user-facing note: only when there's something
// to report and enough time has passed since the last note.
func (e *SelfReviewEngine) shouldSendNote(ctx context.Context, userID uuid.UUID, a actionGrade, m messagingGrade) bool {
	if a.reviewed == 0 && m.sent == 0 {
		return false
	}
	last, err := e.repo.LastSelfReviewNoteAt(ctx, userID)
	if err != nil {
		return false
	}
	if last != nil && time.Since(*last) < selfReviewNoteEvery {
		return false
	}
	return true
}

// buildSelfReviewVerdict summarizes the pass for the audit row.
func buildSelfReviewVerdict(a actionGrade, m messagingGrade) string {
	return fmt.Sprintf(
		"Reviewed %d action(s): %d helped, %d neutral, %d harmed. Health %d→%d. Nudges %d sent / %d dismissed. Cadence: %s.",
		a.reviewed, a.helped, a.neutral, a.harmed, a.healthBefore, a.healthAfter,
		m.sent, m.dismissed, m.cadenceHint,
	)
}

// selfReviewNote turns the review into a short, discreet line for the user. It
// mirrors Miriam's voice: accountable, not chatty. Returns "" when there's
// nothing meaningful to say.
func selfReviewNote(a actionGrade, m messagingGrade) string {
	reduce := m.cadenceHint == entities.NudgeCadenceReduce

	switch {
	case a.harmed > a.helped && a.harmed > 0:
		if reduce {
			return "I reviewed how I've been helping. A couple of my moves didn't land the way I wanted, so I'm being more careful and I'll keep my nudges lighter this week."
		}
		return "I reviewed how I've been helping. A couple of my moves didn't land the way I wanted, so I'm being more careful with the next ones."
	case a.helped > 0 && a.harmed == 0:
		if reduce {
			return "I looked back at my moves this week — they landed well. I'll keep them up and ease off on the nudges so I'm not in your ear too much."
		}
		return "I looked back at my moves this week and they landed well. I'll keep doing what's working."
	case reduce:
		return "I've been checking in a lot. I'll keep it lighter this week and only reach out when it matters."
	default:
		return ""
	}
}
