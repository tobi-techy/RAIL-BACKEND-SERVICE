package growthengine

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

var filePathRegex = regexp.MustCompile(`(?:/(?:[\w\-.]+/)*[\w\-.]+(?::\d+)?)|(?:[A-Z]:\\(?:[\w\-. ]+\\)*[\w\-. ]+(?::\d+)?)`)

type Repository interface {
	TrackEvent(ctx context.Context, event *entities.UserEvent) error
	ApplyEventToLifecycle(ctx context.Context, event *entities.UserEvent) error
	MarkConversions(ctx context.Context, userID uuid.UUID, eventName entities.GrowthEventName) error
	ListGrowthCandidates(ctx context.Context, limit int) ([]entities.GrowthUserSnapshot, error)
	UpsertSegment(ctx context.Context, userID uuid.UUID, stage entities.UserLifecycleStage, segment entities.GrowthSegment, score int, assignedAt time.Time) error
	ListActiveCampaignsBySegment(ctx context.Context, segment entities.GrowthSegment) ([]entities.GrowthCampaign, error)
	HasRecentDelivery(ctx context.Context, userID, campaignID uuid.UUID, cooldown time.Duration) (bool, error)
	CreateDelivery(ctx context.Context, delivery *entities.CampaignDelivery) error
	UpdateDeliveryStatus(ctx context.Context, deliveryID uuid.UUID, status entities.GrowthDeliveryStatus, errMessage string, sentAt *time.Time) error
	ListManualWhatsAppLeads(ctx context.Context, limit int) ([]entities.WhatsAppGrowthLead, error)
}

type EmailSender interface {
	SendCustomEmail(ctx context.Context, to, subject, htmlContent, textContent string) error
	SendCustomEmailFrom(ctx context.Context, to, subject, htmlContent, textContent, fromEmail, fromName, replyTo string) error
}

type BatchEmailSender interface {
	SendBatchEmails(ctx context.Context, emails []BatchEmailItem) error
}

// BatchEmailItem represents a single email in a batch.
type BatchEmailItem struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	Text    string   `json:"text,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

type Config struct {
	Limit int
	Now   func() time.Time
}

type Service struct {
	repo       Repository
	email      EmailSender
	batchEmail BatchEmailSender
	push       PushSender
	cfg        Config
	logger     *zap.Logger
}

func NewService(repo Repository, email EmailSender, push PushSender, cfg Config, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 1000
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: repo, email: email, push: push, cfg: cfg, logger: logger}
}

// SetBatchEmailSender enables batch email sending for growth campaigns.
func (s *Service) SetBatchEmailSender(b BatchEmailSender) {
	s.batchEmail = b
}

func (s *Service) Track(ctx context.Context, userID uuid.UUID, eventName entities.GrowthEventName, metadata map[string]any) error {
	if s.repo == nil {
		return nil
	}
	event := &entities.UserEvent{
		ID:        uuid.New(),
		UserID:    userID,
		EventName: normalizeEventName(eventName),
		Metadata:  metadata,
		CreatedAt: s.cfg.Now(),
	}
	if err := s.repo.TrackEvent(ctx, event); err != nil {
		return err
	}
	if err := s.repo.ApplyEventToLifecycle(ctx, event); err != nil {
		return err
	}
	return s.repo.MarkConversions(ctx, userID, event.EventName)
}

func (s *Service) RunSegmentation(ctx context.Context) (int, int, error) {
	if s.repo == nil {
		return 0, 0, nil
	}
	now := s.cfg.Now()
	users, err := s.repo.ListGrowthCandidates(ctx, s.cfg.Limit)
	if err != nil {
		return 0, 0, err
	}

	type pendingEmail struct {
		deliveryID uuid.UUID
		item       BatchEmailItem
	}

	segmented, queued := 0, 0
	var emailBatch []pendingEmail

	flushBatch := func() {
		if len(emailBatch) == 0 || s.batchEmail == nil {
			return
		}
		items := make([]BatchEmailItem, len(emailBatch))
		for i, pe := range emailBatch {
			items[i] = pe.item
		}
		if err := s.batchEmail.SendBatchEmails(ctx, items); err != nil {
			s.logger.Warn("batch email send failed, marking deliveries failed", zap.Int("count", len(items)), zap.Error(err))
			for _, pe := range emailBatch {
				_ = s.repo.UpdateDeliveryStatus(ctx, pe.deliveryID, entities.GrowthDeliveryFailed, err.Error(), nil)
			}
		} else {
			sentAt := s.cfg.Now()
			for _, pe := range emailBatch {
				_ = s.repo.UpdateDeliveryStatus(ctx, pe.deliveryID, entities.GrowthDeliverySent, "", &sentAt)
			}
		}
		emailBatch = emailBatch[:0]
	}

	for _, user := range users {
		select {
		case <-ctx.Done():
			flushBatch()
			return segmented, queued, ctx.Err()
		default:
		}

		segment, stage, score := SegmentUser(user, now)
		if err := s.repo.UpsertSegment(ctx, user.UserID, stage, segment, score, now); err != nil {
			s.logger.Error("UpsertSegment failed, skipping user", zap.String("user_id", user.UserID.String()), zap.Error(err))
			continue
		}
		segmented++
		if segment == entities.SegmentActive {
			continue
		}

		campaigns, err := s.repo.ListActiveCampaignsBySegment(ctx, segment)
		if err != nil {
			flushBatch()
			return segmented, queued, err
		}
		for _, campaign := range campaigns {
			cooldown := time.Duration(campaign.CooldownDays) * 24 * time.Hour
			recent, err := s.repo.HasRecentDelivery(ctx, user.UserID, campaign.ID, cooldown)
			if err != nil {
				flushBatch()
				return segmented, queued, err
			}
			if recent {
				continue
			}
			// For email campaigns with batch sender available, queue into batch
			if campaign.Channel == entities.GrowthChannelEmail && s.batchEmail != nil {
				subject, body := renderCampaign(campaign, user)
				delivery := &entities.CampaignDelivery{
					ID:         uuid.New(),
					UserID:     user.UserID,
					CampaignID: campaign.ID,
					Segment:    campaign.Segment,
					Channel:    campaign.Channel,
					Status:     entities.GrowthDeliveryQueued,
					RenderedTo: renderRecipient(user, campaign.Channel),
					Subject:    subject,
					Body:       body,
					CreatedAt:  s.cfg.Now(),
				}
				if err := s.repo.CreateDelivery(ctx, delivery); err != nil {
					s.logger.Debug("growth campaign delivery skipped (duplicate or error)", zap.String("user_id", user.UserID.String()), zap.Error(err))
					continue
				}
				htmlBody := renderEmailHTML(subject, body)
				from := formatFrom(campaign)
				emailBatch = append(emailBatch, pendingEmail{
					deliveryID: delivery.ID,
					item: BatchEmailItem{
						From:    from,
						To:      []string{user.Email},
						Subject: subject,
						HTML:    htmlBody,
						Text:    body,
						ReplyTo: campaign.ReplyTo,
					},
				})
				if len(emailBatch) >= 50 {
					flushBatch()
				}
				queued++
				continue
			}
			if err := s.deliver(ctx, user, campaign); err != nil {
				s.logger.Warn("growth campaign delivery failed",
					zap.String("user_id", user.UserID.String()),
					zap.String("campaign_id", campaign.ID.String()),
					zap.String("segment", string(segment)),
					zap.Error(err))
				continue
			}
			queued++
		}
	}

	flushBatch()
	return segmented, queued, nil
}

func (s *Service) ListManualWhatsAppLeads(ctx context.Context, limit int) ([]entities.WhatsAppGrowthLead, error) {
	if s.repo == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	return s.repo.ListManualWhatsAppLeads(ctx, limit)
}

func SegmentUser(user entities.GrowthUserSnapshot, now time.Time) (entities.GrowthSegment, entities.UserLifecycleStage, int) {
	lastActive := latestTime(user.LastActiveAt, user.LastLoginAt, user.MiriamLastUsedAt)
	hasKYC := strings.EqualFold(user.KYCStatus, "approved") || strings.EqualFold(user.KYCStatus, "active")
	kycStarted := user.KYCStartedAt != nil || hasKYC || strings.EqualFold(user.KYCStatus, "processing")

	if user.FirstDepositAt != nil {
		if lastActive != nil && now.Sub(*lastActive) >= 14*24*time.Hour {
			return entities.SegmentFullyChurned, entities.StageChurned, 90
		}
		if user.MiriamLastUsedAt != nil && lastActive != nil && now.Sub(*lastActive) >= 7*24*time.Hour {
			return entities.SegmentMiriamUserInactive, entities.StageDormant, 75
		}
		if lastActive != nil && now.Sub(*lastActive) >= 7*24*time.Hour {
			return entities.SegmentInactive7Days, entities.StageDormant, 65
		}
		return entities.SegmentActive, entities.StageActivated, 0
	}

	if hasKYC {
		kycDoneAt := firstNonNilTime(user.KYCCompletedAt, user.LastActiveAt, &user.CreatedAt)
		if kycDoneAt != nil && now.Sub(*kycDoneAt) >= 48*time.Hour {
			return entities.SegmentKYCNoDeposit, entities.StageKYCCompleted, 70
		}
		return entities.SegmentActive, entities.StageKYCCompleted, 0
	}

	if kycStarted {
		kycStartedAt := firstNonNilTime(user.KYCStartedAt, &user.CreatedAt)
		if kycStartedAt != nil && now.Sub(*kycStartedAt) >= 2*time.Hour {
			return entities.SegmentKYCAbandoned, entities.StageKYCStarted, 80
		}
		return entities.SegmentActive, entities.StageKYCStarted, 0
	}

	if now.Sub(user.CreatedAt) >= 24*time.Hour {
		return entities.SegmentSignupNoKYC, entities.StageSignedUp, 60
	}
	return entities.SegmentActive, entities.StageSignedUp, 0
}

func (s *Service) deliver(ctx context.Context, user entities.GrowthUserSnapshot, campaign entities.GrowthCampaign) error {
	subject, body := renderCampaign(campaign, user)
	delivery := &entities.CampaignDelivery{
		ID:         uuid.New(),
		UserID:     user.UserID,
		CampaignID: campaign.ID,
		Segment:    campaign.Segment,
		Channel:    campaign.Channel,
		Status:     entities.GrowthDeliveryQueued,
		RenderedTo: renderRecipient(user, campaign.Channel),
		Subject:    subject,
		Body:       body,
		CreatedAt:  s.cfg.Now(),
	}
	if err := s.repo.CreateDelivery(ctx, delivery); err != nil {
		return err
	}

	var sendErr error
	switch campaign.Channel {
	case entities.GrowthChannelEmail:
		sendErr = s.sendEmail(ctx, user.Email, campaign, subject, body)
	case entities.GrowthChannelPush:
		if s.push == nil {
			sendErr = fmt.Errorf("push sender not configured")
		} else {
			sendErr = s.push.SendToUser(ctx, user.UserID, subject, body, map[string]interface{}{
				"type":        "growth_campaign",
				"campaign_id": campaign.ID.String(),
				"segment":     string(campaign.Segment),
			})
		}
	case entities.GrowthChannelManualWhatsApp:
		return nil
	case entities.GrowthChannelInApp:
		if s.push == nil {
			sendErr = fmt.Errorf("in-app delivery requires notification push/persist path")
		} else {
			sendErr = s.push.SendToUser(ctx, user.UserID, subject, body, map[string]interface{}{
				"type":        "growth_campaign",
				"campaign_id": campaign.ID.String(),
				"segment":     string(campaign.Segment),
				"in_app":      true,
			})
		}
	default:
		sendErr = fmt.Errorf("unsupported growth channel: %s", campaign.Channel)
	}

	if sendErr != nil {
		if updateErr := s.repo.UpdateDeliveryStatus(ctx, delivery.ID, entities.GrowthDeliveryFailed, sanitizeErrorMessage(sendErr.Error()), nil); updateErr != nil {
			s.logger.Error("failed to update delivery status", zap.String("delivery_id", delivery.ID.String()), zap.Error(updateErr))
		}
		return sendErr
	}
	sentAt := s.cfg.Now()
	return s.repo.UpdateDeliveryStatus(ctx, delivery.ID, entities.GrowthDeliverySent, "", &sentAt)
}

func (s *Service) sendEmail(ctx context.Context, to string, campaign entities.GrowthCampaign, subject, body string) error {
	if s.email == nil {
		return fmt.Errorf("email sender not configured")
	}
	if strings.TrimSpace(to) == "" {
		return fmt.Errorf("user email is empty")
	}

	htmlBody := renderEmailHTML(subject, body)
	if campaign.FromEmail != "" || campaign.ReplyTo != "" {
		return s.email.SendCustomEmailFrom(ctx, to, subject, htmlBody, body, campaign.FromEmail, campaign.FromName, campaign.ReplyTo)
	}
	return s.email.SendCustomEmail(ctx, to, subject, htmlBody, body)
}

func formatFrom(campaign entities.GrowthCampaign) string {
	email := strings.TrimSpace(campaign.FromEmail)
	if email == "" {
		email = "miriam@userail.money"
	}
	name := strings.TrimSpace(campaign.FromName)
	if name != "" {
		return fmt.Sprintf("%s <%s>", name, email)
	}
	return email
}

func renderCampaign(campaign entities.GrowthCampaign, user entities.GrowthUserSnapshot) (string, string) {
	name := strings.TrimSpace(user.FirstName)
	if name == "" {
		name = "there"
	}
	replacements := map[string]string{
		"{{name}}":       name,
		"{{first_name}}": name,
		"{{cta}}":        campaign.CTA,
	}
	subject := campaign.Subject
	body := campaign.Body
	for key, value := range replacements {
		subject = strings.ReplaceAll(subject, key, value)
		body = strings.ReplaceAll(body, key, value)
	}
	return subject, body
}

func renderEmailHTML(subject, body string) string {
	paragraphs := strings.Split(body, "\n\n")
	rendered := strings.Builder{}
	for _, paragraph := range paragraphs {
		trimmed := strings.TrimSpace(paragraph)
		if trimmed == "" {
			continue
		}
		rendered.WriteString(fmt.Sprintf(`<p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 16px 0;line-height:1.6;">%s</p>`, html.EscapeString(trimmed)))
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f5f5f7;-webkit-font-smoothing:antialiased;">
<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:20px 16px;">
<tr><td align="center">
<table cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;width:100%%;max-width:560px;">
<tr><td style="padding:32px 24px 8px 24px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0;letter-spacing:-0.5px;">Rail Money</p>
</td></tr>
<tr><td style="padding:16px 24px 28px 24px;">
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:650;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">%s</p>
  %s
</td></tr>
</table>
</td></tr></table>
</body></html>`, html.EscapeString(subject), rendered.String())
}

func renderRecipient(user entities.GrowthUserSnapshot, channel entities.GrowthCampaignChannel) string {
	switch channel {
	case entities.GrowthChannelEmail:
		return user.Email
	case entities.GrowthChannelManualWhatsApp:
		if user.Phone != nil {
			return *user.Phone
		}
	}
	return user.UserID.String()
}

func normalizeEventName(eventName entities.GrowthEventName) entities.GrowthEventName {
	return entities.GrowthEventName(strings.ToLower(strings.TrimSpace(string(eventName))))
}

func latestTime(values ...*time.Time) *time.Time {
	var latest *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		if latest == nil || value.After(*latest) {
			v := *value
			latest = &v
		}
	}
	return latest
}

func firstNonNilTime(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func sanitizeErrorMessage(msg string) string {
	if strings.Contains(msg, "://") || strings.Contains(msg, "password") {
		return "internal error"
	}
	if strings.Contains(msg, "goroutine") {
		return "internal error"
	}
	// Redact file paths (e.g., /app/service.go:42, /etc/config.yaml)
	msg = filePathRegex.ReplaceAllString(msg, "[PATH]")
	if len(msg) > 256 {
		msg = msg[:256]
	}
	return msg
}
