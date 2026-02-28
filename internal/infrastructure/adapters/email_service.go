package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

const (
	unosendAPIBaseURL = "https://www.unosend.co/api/v1"
	emailSendTimeout  = 8 * time.Second
)

// LoginAlertDetails represents metadata associated with a login notification email
type LoginAlertDetails struct {
	IP           string
	ForwardedFor string
	Location     string
	UserAgent    string
	LoginAt      time.Time
}

// EmailServiceConfig holds email service configuration
type EmailServiceConfig struct {
	Provider    string
	APIKey      string
	FromEmail   string
	FromName    string
	Environment string // "development", "staging", "production"
	BaseURL     string // For verification links
	ReplyTo     string
}

// EmailService implements the email service interface
type EmailService struct {
	logger     *zap.Logger
	config     EmailServiceConfig
	httpClient *http.Client
}

// NewEmailService creates a new email service
func NewEmailService(logger *zap.Logger, config EmailServiceConfig) (*EmailService, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	if provider == "" {
		return nil, fmt.Errorf("email provider is required")
	}

	if provider != "unosend" {
		return nil, fmt.Errorf("unsupported email provider: %s (only unosend is supported)", provider)
	}

	if strings.TrimSpace(config.FromEmail) == "" {
		return nil, fmt.Errorf("email from address is required")
	}

	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("unosend api key is required")
	}

	httpClient := &http.Client{
		Timeout: emailSendTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   3 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
		},
	}

	return &EmailService{
		logger:     logger,
		config:     config,
		httpClient: httpClient,
	}, nil
}

// sendEmail is a helper method to send emails via Unosend
func (e *EmailService) sendEmail(ctx context.Context, to, subject, htmlContent, textContent string) error {
	// Add timeout to context
	ctxWithTimeout, cancel := context.WithTimeout(ctx, emailSendTimeout)
	defer cancel()

	return e.sendViaUnosend(ctxWithTimeout, to, subject, htmlContent, textContent)
}

func (e *EmailService) sendViaUnosend(ctx context.Context, to, subject, htmlContent, textContent string) error {
	if e.httpClient == nil {
		return fmt.Errorf("unosend client not configured")
	}

	fromEmail := strings.TrimSpace(e.config.FromEmail)
	if fromEmail == "" {
		return fmt.Errorf("unosend from email is required")
	}

	from := fromEmail
	if strings.TrimSpace(e.config.FromName) != "" {
		from = fmt.Sprintf("%s <%s>", e.config.FromName, fromEmail)
	}

	payload := map[string]any{
		"from":    from,
		"to":      []string{to},
		"subject": subject,
		"html":    htmlContent,
	}

	if textContent != "" {
		payload["text"] = textContent
	}
	if strings.TrimSpace(e.config.ReplyTo) != "" {
		payload["reply_to"] = e.config.ReplyTo
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal unosend payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, unosendAPIBaseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create unosend request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.config.APIKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.logger.Error("Failed to send email via Unosend",
			zap.String("provider", "unosend"),
			zap.String("to", to),
			zap.String("subject", subject),
			zap.Error(err))
		return fmt.Errorf("unosend send request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		logFields := []zap.Field{
			zap.String("provider", "unosend"),
			zap.String("to", to),
			zap.String("subject", subject),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", string(respBody)),
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			e.logger.Error("Unosend authentication failed", logFields...)
		} else {
			e.logger.Error("Unosend returned error", logFields...)
		}

		return fmt.Errorf("unosend email error: status %d", resp.StatusCode)
	}

	e.logger.Info("Email sent successfully",
		zap.String("provider", "unosend"),
		zap.String("to", to),
		zap.String("subject", subject),
		zap.Int("status_code", resp.StatusCode))

	return nil
}

// SendVerificationEmail sends a verification code email
func (e *EmailService) SendVerificationEmail(ctx context.Context, email, code string) error {
	e.logger.Info("Sending verification email",
		zap.String("email", email))

	subject := "Your Rail verification code"

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f5f5f7;-webkit-font-smoothing:antialiased;">
<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:20px 16px;">
<tr><td align="center">
<table cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;width:100%%;max-width:480px;">
<tr><td style="padding:32px 24px 0 24px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0 0 8px 0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:24px 24px;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 24px 0;line-height:1.5;">Here's your verification code. Enter it in the app to continue.</p>
  <table width="100%%" cellpadding="0" cellspacing="0"><tr><td align="center" style="background-color:#f5f5f7;border-radius:12px;padding:24px;">
    <p style="font-family:-apple-system,SF Pro Display,SF Pro Rounded,Helvetica Neue,monospace;font-size:32px;font-weight:700;color:#1d1d1f;margin:0;letter-spacing:6px;">%s</p>
  </td></tr></table>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:24px 0 0 0;line-height:1.5;">This code expires in 10 minutes. If you didn't request this, you can safely ignore this email.</p>
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, html.EscapeString(code))

	textContent := fmt.Sprintf("Your Rail verification code is: %s\n\nThis code expires in 10 minutes.\nIf you didn't request this, ignore this email.\n\n— Rail", code)

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// SendKYCStatusEmail sends a KYC status update email
func (e *EmailService) SendKYCStatusEmail(ctx context.Context, email string, status entities.KYCStatus, rejectionReasons []string) error {
	e.logger.Info("Sending KYC status email",
		zap.String("email", email),
		zap.String("status", string(status)),
		zap.Strings("rejection_reasons", rejectionReasons))

	var subject, heading, body, extra string

	switch status {
	case entities.KYCStatusApproved:
		subject = "Identity verified"
		heading = "You're verified."
		body = "Your identity verification is complete. You can now fund your account and start using Rail."
	case entities.KYCStatusRejected:
		subject = "Verification needs attention"
		heading = "We need a bit more."
		body = "We couldn't complete your verification. Please review the details below and resubmit."
		for _, reason := range rejectionReasons {
			extra += fmt.Sprintf(`<p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#424245;margin:0 0 8px 0;line-height:1.5;">%s</p>`, html.EscapeString(reason))
		}
		if extra != "" {
			extra = fmt.Sprintf(`<table width="100%%%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;border-radius:12px;"><tr><td style="padding:20px 24px;">%s</td></tr></table>`, extra)
		}
	case entities.KYCStatusProcessing:
		subject = "Verification in progress"
		heading = "We're on it."
		body = "Your documents are being reviewed. You'll hear from us within 24-48 hours."
	default:
		subject = "Verification update"
		heading = "Status update"
		body = fmt.Sprintf("Your verification status has been updated to: %s", string(status))
	}

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f5f5f7;-webkit-font-smoothing:antialiased;">
<table width="100%%%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:20px 16px;">
<tr><td align="center">
<table cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;width:100%%;max-width:480px;">
<tr><td style="padding:32px 24px 0 24px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:24px 24px;">
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:600;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">%s</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 24px 0;line-height:1.5;">%s</p>%s
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, html.EscapeString(heading), html.EscapeString(body), extra)

	textContent := fmt.Sprintf("%s\n\n%s\n\n— Rail", heading, body)

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// SendWelcomeEmail sends a welcome email to a new user
func (e *EmailService) SendWelcomeEmail(ctx context.Context, email string) error {
	e.logger.Info("Sending welcome email", zap.String("email", email))

	subject := "Welcome to Rail"

	htmlContent := `<!DOCTYPE html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f5f5f7;-webkit-font-smoothing:antialiased;">
<table width="100%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:20px 16px;">
<tr><td align="center">
<table cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;width:100%%;max-width:480px;">
<tr><td style="padding:32px 24px 0 24px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0 0 8px 0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:24px 24px;">
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:600;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">You're in.</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 24px 0;line-height:1.5;">Your Rail account is set up. From here, every deposit automatically splits 70/30 between spending and investing — no decisions required.</p>
  <table width="100%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;border-radius:12px;">
    <tr><td style="padding:20px 24px;">
      <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;font-weight:600;color:#1d1d1f;margin:0 0 12px 0;">What happens next</p>
      <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#424245;margin:0 0 8px 0;line-height:1.5;">1. Complete identity verification</p>
      <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#424245;margin:0 0 8px 0;line-height:1.5;">2. Fund your account</p>
      <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#424245;margin:0;line-height:1.5;">3. Your money starts working</p>
    </td></tr>
  </table>
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`

	textContent := "Welcome to Rail.\n\nYour account is set up. Every deposit automatically splits 70/30 between spending and investing.\n\nNext steps:\n1. Complete identity verification\n2. Fund your account\n3. Your money starts working\n\n— Rail"

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// SendCustomEmail delivers an email composed outside of the predefined templates
func (e *EmailService) SendCustomEmail(ctx context.Context, to, subject, htmlContent, textContent string) error {
	return e.sendEmail(ctx, to, subject, htmlContent, textContent)
}

// SendLoginAlertEmail notifies the user about a successful login attempt
func (e *EmailService) SendLoginAlertEmail(ctx context.Context, email string, details LoginAlertDetails) error {
	if details.LoginAt.IsZero() {
		details.LoginAt = time.Now().UTC()
	}

	location := strings.TrimSpace(details.Location)
	if location == "" {
		location = "Unknown"
	}

	forwarded := strings.TrimSpace(details.ForwardedFor)
	if forwarded == "" {
		forwarded = "N/A"
	}

	userAgent := strings.TrimSpace(details.UserAgent)
	if userAgent == "" {
		userAgent = "Unknown"
	}

	safeIP := html.EscapeString(strings.TrimSpace(details.IP))
	safeLocation := html.EscapeString(location)
	safeUserAgent := html.EscapeString(userAgent)
	loginTime := details.LoginAt.UTC().Format(time.RFC1123)

	subject := "New login to your Rail account"

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f5f5f7;-webkit-font-smoothing:antialiased;">
<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:20px 16px;">
<tr><td align="center">
<table cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;width:100%%;max-width:480px;">
<tr><td style="padding:32px 24px 0 24px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0 0 8px 0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:24px 24px;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 24px 0;line-height:1.5;">We detected a new login to your account. If this was you, no action is needed.</p>
  <table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;border-radius:12px;">
    <tr><td style="padding:20px 24px;">
      <table width="100%%" cellpadding="0" cellspacing="0">
        <tr><td style="padding:4px 0;"><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:0;">IP Address</p><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#1d1d1f;margin:2px 0 12px 0;">%s</p></td></tr>
        <tr><td style="padding:4px 0;"><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:0;">Location</p><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#1d1d1f;margin:2px 0 12px 0;">%s</p></td></tr>
        <tr><td style="padding:4px 0;"><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:0;">Device</p><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#1d1d1f;margin:2px 0 12px 0;">%s</p></td></tr>
        <tr><td style="padding:4px 0;"><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:0;">Time (UTC)</p><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#1d1d1f;margin:2px 0 0 0;">%s</p></td></tr>
      </table>
    </td></tr>
  </table>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:24px 0 0 0;line-height:1.5;">If this wasn't you, reset your password immediately and contact support.</p>
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, safeIP, safeLocation, safeUserAgent, loginTime)

	textContent := fmt.Sprintf(`
New login detected on your Stack Service account.

IP Address: %s
Forwarded For: %s
Location: %s
Device: %s
Time (UTC): %s

If this wasn't you, please reset your password immediately and contact support.
`, strings.TrimSpace(details.IP), forwarded, location, userAgent, loginTime)

	e.logger.Info("Sending login alert email",
		zap.String("email", email),
		zap.String("ip", strings.TrimSpace(details.IP)))

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// KYC Email Templates are now handled inline by SendKYCStatusEmail


// SendP2PInviteEmail sends an invite to a non-Rail user to claim money
func (e *EmailService) SendP2PInviteEmail(ctx context.Context, toEmail, senderName string, amount string, claimURL string) error {
	subject := fmt.Sprintf("%s sent you money on Rail", senderName)

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
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:600;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">You've got money waiting.</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 24px 0;line-height:1.5;">%s sent you <strong>%s</strong>. Download Rail to claim it.</p>
  <table cellpadding="0" cellspacing="0" style="margin:0 0 24px 0;">
    <tr><td style="background-color:#1d1d1f;border-radius:12px;padding:14px 28px;">
      <a href="%s" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;font-weight:600;color:#ffffff;text-decoration:none;">Claim Your Money</a>
    </td></tr>
  </table>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:0;line-height:1.5;">This link expires in 14 days. After that, the money returns to the sender.</p>
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, html.EscapeString(senderName), html.EscapeString(amount), claimURL)

	textContent := fmt.Sprintf("%s sent you %s on Rail.\n\nClaim it here: %s\n\nThis link expires in 14 days.\n\n— Rail", senderName, amount, claimURL)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// SendP2PReceivedEmail notifies a user they received money
func (e *EmailService) SendP2PReceivedEmail(ctx context.Context, toEmail, senderName, amount string, note *string) error {
	subject := fmt.Sprintf("%s sent you %s", senderName, amount)

	noteHTML := ""
	noteText := ""
	if note != nil && *note != "" {
		noteHTML = fmt.Sprintf(`<p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:14px;color:#424245;margin:16px 0 0 0;padding:16px;background-color:#f5f5f7;border-radius:8px;line-height:1.5;">"%s"</p>`, html.EscapeString(*note))
		noteText = fmt.Sprintf("\n\nNote: \"%s\"", *note)
	}

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
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:600;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">Money received.</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0;line-height:1.5;">%s sent you <strong>%s</strong>. It's already in your spending balance.</p>%s
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, html.EscapeString(senderName), html.EscapeString(amount), noteHTML)

	textContent := fmt.Sprintf("%s sent you %s. It's in your spending balance.%s\n\n— Rail", senderName, amount, noteText)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// SendP2PClaimedEmail notifies sender that their transfer was claimed
func (e *EmailService) SendP2PClaimedEmail(ctx context.Context, toEmail, recipientName, amount string) error {
	subject := fmt.Sprintf("%s claimed your %s", recipientName, amount)

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
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:600;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">Transfer complete.</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0;line-height:1.5;">%s joined Rail and claimed the <strong>%s</strong> you sent.</p>
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, html.EscapeString(recipientName), html.EscapeString(amount))

	textContent := fmt.Sprintf("%s joined Rail and claimed the %s you sent.\n\n— Rail", recipientName, amount)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// SendP2PExpiredEmail notifies sender that their transfer expired
func (e *EmailService) SendP2PExpiredEmail(ctx context.Context, toEmail, identifier, amount string) error {
	subject := fmt.Sprintf("Your %s transfer expired", amount)

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
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:600;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">Transfer expired.</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0;line-height:1.5;">Your <strong>%s</strong> transfer to %s wasn't claimed within 14 days. The funds have been returned to your spending balance.</p>
</td></tr>
<tr><td style="padding:0 24px 32px 24px;border-top:1px solid #f5f5f7;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:12px;color:#86868b;margin:20px 0 0 0;line-height:1.5;">Rail — Your money, working from the moment it arrives.</p>
</td></tr>
</table>
</td></tr></table>
</body></html>`, html.EscapeString(amount), html.EscapeString(identifier))

	textContent := fmt.Sprintf("Your %s transfer to %s wasn't claimed within 14 days. The funds have been returned to your spending balance.\n\n— Rail", amount, identifier)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}
