package miriam

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- fakes ---

type fakeSelfReviewRepo struct {
	receipts   []entities.MiriamDecisionReceipt
	lastReview *time.Time
	lastNote   *time.Time

	created []entities.MiriamSelfReview
	signals []entities.MiriamLearningSignal
}

func (f *fakeSelfReviewRepo) ListReceiptsSince(_ context.Context, _ uuid.UUID, _ time.Time) ([]entities.MiriamDecisionReceipt, error) {
	return f.receipts, nil
}

func (f *fakeSelfReviewRepo) CreateSelfReview(_ context.Context, r *entities.MiriamSelfReview) error {
	f.created = append(f.created, *r)
	return nil
}

func (f *fakeSelfReviewRepo) LastSelfReviewAt(_ context.Context, _ uuid.UUID) (*time.Time, error) {
	return f.lastReview, nil
}

func (f *fakeSelfReviewRepo) LastSelfReviewNoteAt(_ context.Context, _ uuid.UUID) (*time.Time, error) {
	return f.lastNote, nil
}

func (f *fakeSelfReviewRepo) CreateLearningSignal(_ context.Context, s *entities.MiriamLearningSignal) error {
	f.signals = append(f.signals, *s)
	return nil
}

type fakeNudgeReader struct {
	nudges []entities.ProactiveNudge
}

func (f *fakeNudgeReader) ListNudgesSince(_ context.Context, _ uuid.UUID, _ time.Time) ([]entities.ProactiveNudge, error) {
	return f.nudges, nil
}

type fakeHealthReader struct {
	scores []entities.MiriamFinancialHealthScore
}

func (f *fakeHealthReader) GetHealthTrend(_ context.Context, _ uuid.UUID, _ int) ([]entities.MiriamFinancialHealthScore, error) {
	return f.scores, nil
}

type fakeNotifier struct {
	sent int
	last string
}

func (f *fakeNotifier) SendGenericNotification(_ context.Context, _ uuid.UUID, _, message string) error {
	f.sent++
	f.last = message
	return nil
}

// --- helpers ---

func executedReceipt(at time.Time) entities.MiriamDecisionReceipt {
	return entities.MiriamDecisionReceipt{
		ID:         uuid.New(),
		Status:     entities.MiriamReceiptStatusExecuted,
		ActionType: entities.MiriamMandateTransferToStash,
		Amount:     decimal.NewFromInt(50),
		CreatedAt:  at,
	}
}

func failedReceipt(at time.Time) entities.MiriamDecisionReceipt {
	r := executedReceipt(at)
	r.Status = entities.MiriamReceiptStatusFailed
	return r
}

// healthScores builds a newest-first trend from oldest→newest overall scores.
func healthScores(now time.Time, oldestToNewest ...int) []entities.MiriamFinancialHealthScore {
	out := make([]entities.MiriamFinancialHealthScore, 0, len(oldestToNewest))
	n := len(oldestToNewest)
	for i := n - 1; i >= 0; i-- {
		out = append(out, entities.MiriamFinancialHealthScore{
			OverallScore: oldestToNewest[i],
			CreatedAt:    now.Add(-time.Duration(i) * 24 * time.Hour),
		})
	}
	return out
}

// --- tests ---

func TestSelfReview_DailyGate(t *testing.T) {
	recent := time.Now().UTC().Add(-2 * time.Hour)
	repo := &fakeSelfReviewRepo{lastReview: &recent}
	eng := NewSelfReviewEngine(repo, &fakeNudgeReader{}, &fakeHealthReader{}, &fakeNotifier{}, zap.NewNop())

	got, err := eng.Run(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Nil(t, got, "should skip when a review ran within the last day")
	assert.Empty(t, repo.created)
}

func TestSelfReview_HarmedDominant_WritesNegativeSignal(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeSelfReviewRepo{
		receipts: []entities.MiriamDecisionReceipt{
			executedReceipt(now.Add(-6 * 24 * time.Hour)),
			failedReceipt(now.Add(-3 * 24 * time.Hour)),
		},
	}
	// Declining health: 70 → 55.
	health := &fakeHealthReader{scores: healthScores(now, 70, 62, 55)}
	eng := NewSelfReviewEngine(repo, &fakeNudgeReader{}, health, &fakeNotifier{}, zap.NewNop())

	got, err := eng.Run(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Greater(t, got.ActionsHarmed, got.ActionsHelped)
	require.Len(t, repo.signals, 1)
	assert.Equal(t, entities.MiriamFeedbackReversed, repo.signals[0].Signal)
	assert.True(t, repo.signals[0].Weight.Equal(decimal.NewFromFloat(selfReviewHarmWeight)))
}

func TestSelfReview_HelpedDominant_WritesPositiveSignal(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeSelfReviewRepo{
		receipts: []entities.MiriamDecisionReceipt{
			executedReceipt(now.Add(-6 * 24 * time.Hour)),
			executedReceipt(now.Add(-2 * 24 * time.Hour)),
		},
	}
	// Improving health: 50 → 70.
	health := &fakeHealthReader{scores: healthScores(now, 50, 60, 70)}
	eng := NewSelfReviewEngine(repo, &fakeNudgeReader{}, health, &fakeNotifier{}, zap.NewNop())

	got, err := eng.Run(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, 2, got.ActionsHelped)
	assert.Equal(t, 0, got.ActionsHarmed)
	require.Len(t, repo.signals, 1)
	assert.Equal(t, entities.MiriamFeedbackAccepted, repo.signals[0].Signal)
}

func TestSelfReview_NeutralHealth_NoSignal(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeSelfReviewRepo{
		receipts: []entities.MiriamDecisionReceipt{executedReceipt(now.Add(-4 * 24 * time.Hour))},
	}
	// Flat health: 60 → 61 (within neutral band).
	health := &fakeHealthReader{scores: healthScores(now, 60, 60, 61)}
	eng := NewSelfReviewEngine(repo, &fakeNudgeReader{}, health, &fakeNotifier{}, zap.NewNop())

	got, err := eng.Run(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, 1, got.ActionsNeutral)
	assert.Empty(t, repo.signals, "neutral outcome must not move the money lever")
}

func TestSelfReview_NudgeFatigue_ReducesCadence(t *testing.T) {
	now := time.Now().UTC()
	delivered := now.Add(-time.Hour)
	dismissed := now.Add(-30 * time.Minute)
	mk := func(dism bool) entities.ProactiveNudge {
		n := entities.ProactiveNudge{ID: uuid.New(), DeliveredAt: &delivered, CreatedAt: now.Add(-2 * time.Hour)}
		if dism {
			n.DismissedAt = &dismissed
		}
		return n
	}
	repo := &fakeSelfReviewRepo{}
	nudges := &fakeNudgeReader{nudges: []entities.ProactiveNudge{mk(true), mk(true), mk(false)}}
	eng := NewSelfReviewEngine(repo, nudges, &fakeHealthReader{}, &fakeNotifier{}, zap.NewNop())

	got, err := eng.Run(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, 3, got.NudgesSent)
	assert.Equal(t, 2, got.NudgesDismissed)
	assert.Equal(t, entities.NudgeCadenceReduce, got.CadenceHint)
}

func TestSelfReview_EmptyPeriod_NoNote(t *testing.T) {
	repo := &fakeSelfReviewRepo{}
	notifier := &fakeNotifier{}
	eng := NewSelfReviewEngine(repo, &fakeNudgeReader{}, &fakeHealthReader{}, notifier, zap.NewNop())

	got, err := eng.Run(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, 0, notifier.sent, "no note when nothing happened")
	assert.False(t, got.NoteSent)
	assert.Equal(t, entities.NudgeCadenceNormal, got.CadenceHint)
}

func TestSelfReview_NoteGatedByRecentNote(t *testing.T) {
	now := time.Now().UTC()
	recentNote := now.Add(-2 * 24 * time.Hour) // within the ~weekly window
	repo := &fakeSelfReviewRepo{
		lastNote: &recentNote,
		receipts: []entities.MiriamDecisionReceipt{executedReceipt(now.Add(-3 * 24 * time.Hour))},
	}
	health := &fakeHealthReader{scores: healthScores(now, 50, 60, 70)}
	notifier := &fakeNotifier{}
	eng := NewSelfReviewEngine(repo, &fakeNudgeReader{}, health, notifier, zap.NewNop())

	got, err := eng.Run(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, 0, notifier.sent, "note suppressed when one was sent recently")
	assert.False(t, got.NoteSent)
}

func TestSelfReview_SendsNote_WhenDue(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeSelfReviewRepo{
		receipts: []entities.MiriamDecisionReceipt{
			executedReceipt(now.Add(-5 * 24 * time.Hour)),
			executedReceipt(now.Add(-1 * 24 * time.Hour)),
		},
	}
	health := &fakeHealthReader{scores: healthScores(now, 50, 60, 72)}
	notifier := &fakeNotifier{}
	eng := NewSelfReviewEngine(repo, &fakeNudgeReader{}, health, notifier, zap.NewNop())

	got, err := eng.Run(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, 1, notifier.sent)
	assert.True(t, got.NoteSent)
	assert.NotEmpty(t, notifier.last)
}
