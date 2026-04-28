package adapters

import (
	"context"
	"fmt"
	"html"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// EmailSenderAdapter adapts EmailService to the notification.EmailSenderService interface
type EmailSenderAdapter struct {
	emailService *EmailService
}

func NewEmailSenderAdapter(emailService *EmailService) *EmailSenderAdapter {
	return &EmailSenderAdapter{emailService: emailService}
}

func (a *EmailSenderAdapter) SendGenericEmail(ctx context.Context, to, subject, body string) error {
	safeSubject := html.EscapeString(subject)
	safeBody := html.EscapeString(body)

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f5f5f7;-webkit-font-smoothing:antialiased;">
<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:20px 16px;">
<tr><td align="center">
<table cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;width:100%%;max-width:480px;">
<tr><td style="padding:32px 24px 0 24px;">
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:24px 24px;">
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:600;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">%s</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0;line-height:1.5;">%s</p>
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, safeSubject, safeBody)

	return a.emailService.SendCustomEmail(ctx, to, subject, htmlContent, body)
}

// UserRepo is the minimal interface needed for email lookup.
type UserRepo interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// UserEmailLookup resolves a user's email via the user repository.
type UserEmailLookup struct {
	repo UserRepo
}

func NewUserEmailLookup(repo UserRepo) *UserEmailLookup {
	return &UserEmailLookup{repo: repo}
}

func (u *UserEmailLookup) GetEmailByUserID(ctx context.Context, userID uuid.UUID) (string, error) {
	user, err := u.repo.GetByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("lookup user email: %w", err)
	}
	return user.Email, nil
}
