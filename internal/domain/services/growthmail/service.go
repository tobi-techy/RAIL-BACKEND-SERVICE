package growthmail

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

type CandidateRepository interface {
	ListCandidates(ctx context.Context, limit int) ([]entities.GrowthMailCandidate, error)
	HasSuccessfulSend(ctx context.Context, userID uuid.UUID, campaignKey string) (bool, error)
	ClaimSend(ctx context.Context, event *entities.GrowthMailEvent) (bool, error)
	RecordSend(ctx context.Context, event *entities.GrowthMailEvent) error
}

type EmailSender interface {
	SendCustomEmail(ctx context.Context, to, subject, htmlContent, textContent string) error
}

type Config struct {
	BaseURL string
	Limit   int
}

type Service struct {
	repo   CandidateRepository
	email  EmailSender
	cfg    Config
	logger *zap.Logger
}

func NewService(repo CandidateRepository, email EmailSender, cfg Config, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 500
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "https://userail.money"
	}
	return &Service{repo: repo, email: email, cfg: cfg, logger: logger}
}

func (s *Service) SendDue(ctx context.Context, now time.Time) (int, int, error) {
	if s.repo == nil || s.email == nil {
		return 0, 0, nil
	}

	candidates, err := s.repo.ListCandidates(ctx, s.cfg.Limit)
	if err != nil {
		return 0, 0, err
	}

	sent, failed := 0, 0
	for _, c := range candidates {
		select {
		case <-ctx.Done():
			s.logger.Warn("growth mail interrupted", zap.Int("sent", sent), zap.Int("failed", failed), zap.Int("remaining", len(candidates)-sent-failed))
			return sent, failed, ctx.Err()
		default:
		}
		campaign, ok, err := s.nextUnsentCampaign(ctx, c, now)
		if err != nil {
			failed++
			s.logger.Warn("growth mail send check failed", zap.String("user_id", c.UserID.String()), zap.Error(err))
			continue
		}
		if !ok {
			continue
		}

		event := &entities.GrowthMailEvent{
			UserID:      c.UserID,
			CampaignKey: campaign.key,
			Campaign:    campaign.name,
			Subject:     campaign.subject,
			Status:      entities.GrowthMailStatusSending,
		}
		claimed, err := s.repo.ClaimSend(ctx, event)
		if err != nil {
			failed++
			event.Status = entities.GrowthMailStatusFailed
			event.Error = err.Error()
			if recordErr := s.repo.RecordSend(ctx, event); recordErr != nil {
				s.logger.Warn("growth mail claim failure record failed", zap.String("user_id", c.UserID.String()), zap.String("campaign", campaign.key), zap.Error(recordErr))
			}
			s.logger.Warn("growth mail claim failed", zap.String("user_id", c.UserID.String()), zap.String("campaign", campaign.key), zap.Error(err))
			continue
		}
		if !claimed {
			continue
		}

		htmlBody, textBody := renderCampaign(campaign, c, s.cfg.BaseURL)
		sendErr := s.sendCampaignEmail(ctx, c.Email, campaign, htmlBody, textBody)
		event.Status = entities.GrowthMailStatusSent
		if sendErr != nil {
			failed++
			event.Status = entities.GrowthMailStatusFailed
			event.Error = sendErr.Error()
			_ = s.repo.RecordSend(ctx, event)
			s.logger.Warn("growth mail send failed", zap.String("user_id", c.UserID.String()), zap.String("campaign", campaign.key), zap.Error(sendErr))
			continue
		}
		if err := s.repo.RecordSend(ctx, event); err != nil {
			failed++
			s.logger.Warn("growth mail record failed", zap.String("user_id", c.UserID.String()), zap.String("campaign", campaign.key), zap.Error(err))
			continue
		}
		sent++
	}

	return sent, failed, nil
}

func (s *Service) sendCampaignEmail(ctx context.Context, to string, campaign campaign, htmlBody, textBody string) error {
	return s.email.SendCustomEmail(ctx, to, campaign.subject, htmlBody, textBody)
}

func (s *Service) nextUnsentCampaign(ctx context.Context, c entities.GrowthMailCandidate, now time.Time) (campaign, bool, error) {
	for _, campaign := range campaignsForCandidateWithConfig(c, now, s.cfg) {
		alreadySent, err := s.repo.HasSuccessfulSend(ctx, c.UserID, campaign.key)
		if err != nil {
			return campaign, false, err
		}
		if !alreadySent {
			return campaign, true, nil
		}
	}
	return campaign{}, false, nil
}

type campaign struct {
	name       entities.GrowthMailCampaign
	key        string
	subject    string
	heading    string
	body       string
	steps      []string
	ctaLabel   string
	ctaPath    string
	footerHint string
}

func campaignsForCandidate(c entities.GrowthMailCandidate, now time.Time) []campaign {
	return campaignsForCandidateWithConfig(c, now, Config{})
}

func campaignsForCandidateWithConfig(c entities.GrowthMailCandidate, now time.Time, _ Config) []campaign {
	accountAge := now.Sub(c.CreatedAt)
	campaigns := make([]campaign, 0, 2)

	if strings.EqualFold(c.KYCStatus, "approved") && !c.HasDeposit() && accountAge >= 24*time.Hour {
		campaigns = append(campaigns, campaign{
			name:    entities.GrowthMailFirstDeposit,
			key:     string(entities.GrowthMailFirstDeposit),
			subject: "Your first Rail move",
			heading: "Load money once. Rail does the split.",
			body:    "Rail is easiest to understand after the first deposit. Your Spend balance stays ready for daily life while your long-term balance starts building in the background.",
			steps: []string{
				"Open Rail and choose Load money.",
				"Use your account details or supported stablecoin deposit address.",
				"Watch the automatic split show up in Station.",
			},
			ctaLabel:   "Load Money",
			ctaPath:    "/app/load-money",
			footerHint: "No trading, no stock picking, no setup ritual.",
		})
	}

	if c.HasDeposit() {
		firstSplit := campaign{
			name:    entities.GrowthMailFirstSplit,
			key:     string(entities.GrowthMailFirstSplit),
			subject: "What your Rail split is doing",
			heading: "Your money now has two jobs.",
			body:    "Spend is for normal life. Long-term is the part Rail keeps moving without asking you to manage every decision.",
			steps: []string{
				"Check Station for your total, Spend, and long-term balances.",
				"Open Activity to see what changed after funding.",
				"Turn on round-ups if you want everyday spending to add a little more.",
			},
			ctaLabel:   "Open Station",
			ctaPath:    "/app/station",
			footerHint: "The goal is simple: keep spending normally while progress compounds in the background.",
		}
		if accountAge >= 24*time.Hour {
			campaigns = append(campaigns, firstSplit)
		}
	}

	if c.HasDeposit() && shouldSendWeeklyExplore(c, now) {
		year, week := now.ISOWeek()
		campaigns = append(campaigns, campaign{
			name:    entities.GrowthMailWeeklyExplore,
			key:     fmt.Sprintf("%s:%04d-W%02d", entities.GrowthMailWeeklyExplore, year, week),
			subject: "A useful Rail check-in",
			heading: "Three places worth checking this week.",
			body:    "A quick pass through Rail keeps the product familiar without turning money into homework.",
			steps: []string{
				"Station: confirm your split and current balances.",
				"Activity: scan recent money movement.",
				"Miriam: ask for the plain-English read on your week.",
			},
			ctaLabel:   "Explore Rail",
			ctaPath:    "/app/station",
			footerHint: "A 30-second check is enough. Rail should stay quiet until there is something useful to see.",
		})
	}

	return campaigns
}

func shouldSendWeeklyExplore(c entities.GrowthMailCandidate, now time.Time) bool {
	if now.Weekday() != time.Tuesday {
		return false
	}
	if c.LastLoginAt == nil {
		return true
	}
	return now.Sub(*c.LastLoginAt) >= 5*24*time.Hour
}

func renderCampaign(campaign campaign, candidate entities.GrowthMailCandidate, baseURL string) (string, string) {
	name := strings.TrimSpace(candidate.FirstName)
	greeting := "Hey"
	if name != "" {
		greeting = "Hey " + name
	}
	ctaURL := strings.TrimRight(baseURL, "/") + campaign.ctaPath

	stepHTML := strings.Builder{}
	for _, step := range campaign.steps {
		stepHTML.WriteString(fmt.Sprintf(`<p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#424245;margin:0 0 10px 0;line-height:1.5;">%s</p>`, html.EscapeString(step)))
	}

	textSteps := strings.Builder{}
	for i, step := range campaign.steps {
		textSteps.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f5f5f7;-webkit-font-smoothing:antialiased;">
<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:20px 16px;">
<tr><td align="center">
<table cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;width:100%%;max-width:520px;">
<tr><td style="padding:32px 24px 0 24px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:24px 24px;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#86868b;margin:0 0 12px 0;line-height:1.5;">%s,</p>
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:24px;font-weight:650;color:#1d1d1f;margin:0 0 12px 0;letter-spacing:-0.3px;">%s</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 24px 0;line-height:1.5;">%s</p>
  <table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;border-radius:12px;margin:0 0 24px 0;">
    <tr><td style="padding:20px 24px;">%s</td></tr>
  </table>
  <table cellpadding="0" cellspacing="0" style="margin:0 0 20px 0;">
    <tr><td style="background-color:#1d1d1f;border-radius:12px;padding:14px 24px;">
      <a href="%s" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;font-weight:600;color:#ffffff;text-decoration:none;">%s</a>
    </td></tr>
  </table>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:0;line-height:1.5;">%s</p>
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">You're receiving this because growth emails are enabled for your Rail account.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, html.EscapeString(greeting), html.EscapeString(campaign.heading), html.EscapeString(campaign.body), stepHTML.String(), html.EscapeString(ctaURL), html.EscapeString(campaign.ctaLabel), html.EscapeString(campaign.footerHint))

	textBody := fmt.Sprintf("%s,\n\n%s\n\n%s\n\n%s\n%s: %s\n\n%s\n\nYou're receiving this because growth emails are enabled for your Rail account.\n\nRail", greeting, campaign.heading, campaign.body, textSteps.String(), campaign.ctaLabel, ctaURL, campaign.footerHint)
	return htmlBody, textBody
}
