package growthmail

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSendDueSendsFirstDepositEducation(t *testing.T) {
	now := time.Date(2026, 5, 13, 11, 0, 0, 0, time.UTC)
	userID := uuid.New()
	repo := &fakeGrowthRepo{
		candidates: []entities.GrowthMailCandidate{{
			UserID:    userID,
			Email:     "user@example.com",
			FirstName: "Tobi",
			KYCStatus: "approved",
			CreatedAt: now.Add(-25 * time.Hour),
		}},
	}
	email := &fakeGrowthEmail{}
	svc := NewService(repo, email, Config{BaseURL: "https://app.userail.money"}, zap.NewNop())

	sent, failed, err := svc.SendDue(context.Background(), now)

	require.NoError(t, err)
	require.Equal(t, 1, sent)
	require.Equal(t, 0, failed)
	require.Len(t, email.sent, 1)
	require.Equal(t, "Your first Rail move", email.sent[0].subject)
	require.Equal(t, entities.GrowthMailFirstDeposit, repo.recorded[0].Campaign)
	require.Equal(t, entities.GrowthMailStatusSent, repo.recorded[0].Status)
}

func TestSendDueSkipsPreviouslySentCampaignAndFallsThroughToWeekly(t *testing.T) {
	now := time.Date(2026, 5, 12, 11, 0, 0, 0, time.UTC) // Tuesday
	userID := uuid.New()
	lastLogin := now.Add(-7 * 24 * time.Hour)
	repo := &fakeGrowthRepo{
		alreadySent: map[string]bool{
			string(entities.GrowthMailFirstSplit): true,
		},
		candidates: []entities.GrowthMailCandidate{{
			UserID:       userID,
			Email:        "funded@example.com",
			KYCStatus:    "approved",
			CreatedAt:    now.Add(-14 * 24 * time.Hour),
			LastLoginAt:  &lastLogin,
			DepositCount: 1,
		}},
	}
	email := &fakeGrowthEmail{}
	svc := NewService(repo, email, Config{BaseURL: "https://app.userail.money"}, zap.NewNop())

	sent, failed, err := svc.SendDue(context.Background(), now)

	require.NoError(t, err)
	require.Equal(t, 1, sent)
	require.Equal(t, 0, failed)
	require.Len(t, repo.recorded, 1)
	require.Equal(t, entities.GrowthMailWeeklyExplore, repo.recorded[0].Campaign)
}

func TestSendDueDoesNotSendWithoutMarketingOptInCandidates(t *testing.T) {
	repo := &fakeGrowthRepo{}
	email := &fakeGrowthEmail{}
	svc := NewService(repo, email, Config{}, zap.NewNop())

	sent, failed, err := svc.SendDue(context.Background(), time.Now().UTC())

	require.NoError(t, err)
	require.Equal(t, 0, sent)
	require.Equal(t, 0, failed)
	require.Empty(t, email.sent)
}

type fakeGrowthRepo struct {
	candidates  []entities.GrowthMailCandidate
	alreadySent map[string]bool
	recorded    []*entities.GrowthMailEvent
}

func (r *fakeGrowthRepo) ListCandidates(ctx context.Context, limit int) ([]entities.GrowthMailCandidate, error) {
	return r.candidates, nil
}

func (r *fakeGrowthRepo) HasSuccessfulSend(ctx context.Context, userID uuid.UUID, campaignKey string) (bool, error) {
	return r.alreadySent[campaignKey], nil
}

func (r *fakeGrowthRepo) ClaimSend(ctx context.Context, event *entities.GrowthMailEvent) (bool, error) {
	if r.alreadySent[event.CampaignKey] {
		return false, nil
	}
	event.Status = entities.GrowthMailStatusSending
	return true, nil
}

func (r *fakeGrowthRepo) RecordSend(ctx context.Context, event *entities.GrowthMailEvent) error {
	r.recorded = append(r.recorded, event)
	return nil
}

type fakeGrowthEmail struct {
	sent []struct {
		to      string
		subject string
		html    string
		text    string
	}
}

func (e *fakeGrowthEmail) SendCustomEmail(ctx context.Context, to, subject, htmlContent, textContent string) error {
	e.sent = append(e.sent, struct {
		to      string
		subject string
		html    string
		text    string
	}{to: to, subject: subject, html: htmlContent, text: textContent})
	return nil
}
