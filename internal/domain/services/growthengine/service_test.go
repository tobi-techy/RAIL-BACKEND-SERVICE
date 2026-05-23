package growthengine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSegmentUserSignupNoKYC(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	user := entities.GrowthUserSnapshot{
		UserID:    uuid.New(),
		CreatedAt: now.Add(-25 * time.Hour),
		KYCStatus: "pending",
	}

	segment, stage, score := SegmentUser(user, now)

	require.Equal(t, entities.SegmentSignupNoKYC, segment)
	require.Equal(t, entities.StageSignedUp, stage)
	require.Equal(t, 60, score)
}

func TestSegmentUserKYCNoDeposit(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	kycDone := now.Add(-49 * time.Hour)
	user := entities.GrowthUserSnapshot{
		UserID:         uuid.New(),
		CreatedAt:      now.Add(-72 * time.Hour),
		KYCStatus:      "approved",
		KYCCompletedAt: &kycDone,
	}

	segment, stage, _ := SegmentUser(user, now)

	require.Equal(t, entities.SegmentKYCNoDeposit, segment)
	require.Equal(t, entities.StageKYCCompleted, stage)
}

func TestRunSegmentationQueuesEmailCampaign(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()
	campaignID := uuid.New()
	repo := &fakeGrowthEngineRepo{
		users: []entities.GrowthUserSnapshot{{
			UserID:    userID,
			Email:     "ryan@example.com",
			FirstName: "Ryan",
			CreatedAt: now.Add(-25 * time.Hour),
			KYCStatus: "pending",
		}},
		campaigns: map[entities.GrowthSegment][]entities.GrowthCampaign{
			entities.SegmentSignupNoKYC: {{
				ID:              campaignID,
				Name:            "Founder signup recovery",
				Segment:         entities.SegmentSignupNoKYC,
				Channel:         entities.GrowthChannelEmail,
				Subject:         "Thanks for signing up for Rail Money",
				Body:            "Hey {{name}},\n\nFinish setup so Miriam can help.",
				CooldownDays:    14,
				ConversionEvent: entities.GrowthEventKYCStarted,
				FromEmail:       "tobilobaomotade@userail.money",
				ReplyTo:         "tobilobaomotade@userail.money",
			}},
		},
	}
	email := &fakeGrowthEngineEmail{}
	svc := NewService(repo, email, nil, Config{Now: func() time.Time { return now }}, zap.NewNop())

	segmented, queued, err := svc.RunSegmentation(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, segmented)
	require.Equal(t, 1, queued)
	require.Len(t, repo.deliveries, 1)
	require.Equal(t, entities.GrowthDeliverySent, repo.statuses[repo.deliveries[0].ID])
	require.Len(t, email.sent, 1)
	require.Equal(t, "ryan@example.com", email.sent[0].to)
	require.Equal(t, "tobilobaomotade@userail.money", email.sent[0].fromEmail)
	require.Contains(t, email.sent[0].text, "Hey Ryan,")
}

func TestTrackMarksConversion(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()
	repo := &fakeGrowthEngineRepo{}
	svc := NewService(repo, nil, nil, Config{Now: func() time.Time { return now }}, zap.NewNop())

	err := svc.Track(context.Background(), userID, entities.GrowthEventKYCStarted, map[string]any{"provider": "bridge"})

	require.NoError(t, err)
	require.Len(t, repo.events, 1)
	require.Equal(t, entities.GrowthEventKYCStarted, repo.events[0].EventName)
	require.Equal(t, userID, repo.convertedUserID)
	require.Equal(t, entities.GrowthEventKYCStarted, repo.convertedEvent)
}

type fakeGrowthEngineRepo struct {
	users           []entities.GrowthUserSnapshot
	campaigns       map[entities.GrowthSegment][]entities.GrowthCampaign
	deliveries      []*entities.CampaignDelivery
	statuses        map[uuid.UUID]entities.GrowthDeliveryStatus
	events          []*entities.UserEvent
	convertedUserID uuid.UUID
	convertedEvent  entities.GrowthEventName
}

func (r *fakeGrowthEngineRepo) TrackEvent(ctx context.Context, event *entities.UserEvent) error {
	r.events = append(r.events, event)
	return nil
}

func (r *fakeGrowthEngineRepo) ApplyEventToLifecycle(ctx context.Context, event *entities.UserEvent) error {
	return nil
}

func (r *fakeGrowthEngineRepo) MarkConversions(ctx context.Context, userID uuid.UUID, eventName entities.GrowthEventName) error {
	r.convertedUserID = userID
	r.convertedEvent = eventName
	return nil
}

func (r *fakeGrowthEngineRepo) ListGrowthCandidates(ctx context.Context, limit int) ([]entities.GrowthUserSnapshot, error) {
	return r.users, nil
}

func (r *fakeGrowthEngineRepo) UpsertSegment(ctx context.Context, userID uuid.UUID, stage entities.UserLifecycleStage, segment entities.GrowthSegment, score int, assignedAt time.Time) error {
	return nil
}

func (r *fakeGrowthEngineRepo) ListActiveCampaignsBySegment(ctx context.Context, segment entities.GrowthSegment) ([]entities.GrowthCampaign, error) {
	return r.campaigns[segment], nil
}

func (r *fakeGrowthEngineRepo) HasRecentDelivery(ctx context.Context, userID, campaignID uuid.UUID, cooldown time.Duration) (bool, error) {
	return false, nil
}

func (r *fakeGrowthEngineRepo) CreateDelivery(ctx context.Context, delivery *entities.CampaignDelivery) error {
	r.deliveries = append(r.deliveries, delivery)
	if r.statuses == nil {
		r.statuses = map[uuid.UUID]entities.GrowthDeliveryStatus{}
	}
	r.statuses[delivery.ID] = delivery.Status
	return nil
}

func (r *fakeGrowthEngineRepo) UpdateDeliveryStatus(ctx context.Context, deliveryID uuid.UUID, status entities.GrowthDeliveryStatus, errMessage string, sentAt *time.Time) error {
	if r.statuses == nil {
		r.statuses = map[uuid.UUID]entities.GrowthDeliveryStatus{}
	}
	r.statuses[deliveryID] = status
	return nil
}

func (r *fakeGrowthEngineRepo) ListManualWhatsAppLeads(ctx context.Context, limit int) ([]entities.WhatsAppGrowthLead, error) {
	return nil, nil
}

type fakeGrowthEngineEmail struct {
	sent []struct {
		to        string
		subject   string
		html      string
		text      string
		fromEmail string
		fromName  string
		replyTo   string
	}
}

func (e *fakeGrowthEngineEmail) SendCustomEmail(ctx context.Context, to, subject, htmlContent, textContent string) error {
	e.sent = append(e.sent, struct {
		to        string
		subject   string
		html      string
		text      string
		fromEmail string
		fromName  string
		replyTo   string
	}{to: to, subject: subject, html: htmlContent, text: textContent})
	return nil
}

func (e *fakeGrowthEngineEmail) SendCustomEmailFrom(ctx context.Context, to, subject, htmlContent, textContent, fromEmail, fromName, replyTo string) error {
	e.sent = append(e.sent, struct {
		to        string
		subject   string
		html      string
		text      string
		fromEmail string
		fromName  string
		replyTo   string
	}{to: to, subject: subject, html: htmlContent, text: textContent, fromEmail: fromEmail, fromName: fromName, replyTo: replyTo})
	return nil
}
