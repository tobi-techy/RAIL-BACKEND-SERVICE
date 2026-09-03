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

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

const (
	resendAPIBaseURL  = "https://api.resend.com"
	unosendAPIBaseURL = "https://api.unosend.co"
	emailSendTimeout  = 30 * time.Second
)

// Shared email template helpers
func renderBaseTemplate(contentHTML string) string {
	return `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#fbfaf9;-webkit-font-smoothing:antialiased;">
<table width="100%" cellpadding="0" cellspacing="0" style="background-color:#fbfaf9;padding:32px 16px;">
<tr><td align="center">
<table cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;width:100%;max-width:480px;">
<tr><td style="padding:40px 32px 32px;">
` + contentHTML + `
</td></tr>
</table>
</td></tr></table>
</body></html>`
}

func renderHeader() string {
	return `<p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,sans-serif;font-size:20px;font-weight:700;color:#343433;margin:0 0 24px 0;letter-spacing:-0.3px;">Rail</p>`
}

func renderHeading(text string) string {
	return fmt.Sprintf(`<p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,sans-serif;font-size:20px;font-weight:700;color:#343433;margin:0 0 8px 0;letter-spacing:-0.3px;">%s</p>`, text)
}

func renderBody(text string) string {
	return fmt.Sprintf(`<p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:15px;color:#343433;margin:0 0 20px 0;line-height:1.5;">%s</p>`, text)
}

func renderSmallBody(text string) string {
	return fmt.Sprintf(`<p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:13px;color:#848281;margin:0 0 0 0;line-height:1.5;">%s</p>`, text)
}

func renderCodeBox(code string) string {
	return fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:12px;"><tr><td style="padding:20px 24px;text-align:center;">
  <p style="font-family:-apple-system,SF Mono,SF Pro Text,monospace;font-size:32px;font-weight:700;color:#343433;margin:0;letter-spacing:6px;">%s</p>
</td></tr></table>`, code)
}

func renderCTAButton(url, label string) string {
	return fmt.Sprintf(`<table cellpadding="0" cellspacing="0" style="margin:0 0 20px 0;">
<tr><td style="background-color:#121212;border-radius:32px;padding:14px 28px;">
  <a href="%s" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:15px;font-weight:600;color:#ffffff;text-decoration:none;display:inline-block;">%s</a>
</td></tr></table>`, url, label)
}

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
	sesClient  *ses.Client
}

// NewEmailService creates a new email service
func NewEmailService(logger *zap.Logger, config EmailServiceConfig) (*EmailService, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	if provider == "" {
		return nil, fmt.Errorf("email provider is required")
	}

	if provider != "resend" && provider != "unosend" && provider != "ses" {
		return nil, fmt.Errorf("unsupported email provider: %s (supported: ses, resend, unosend)", provider)
	}

	if strings.TrimSpace(config.FromEmail) == "" {
		return nil, fmt.Errorf("email from address is required")
	}

	svc := &EmailService{logger: logger, config: config}

	if provider == "ses" {
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"))
		if err != nil {
			return nil, fmt.Errorf("ses: load aws config: %w", err)
		}
		svc.sesClient = ses.NewFromConfig(awsCfg)
		return svc, nil
	}

	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("email api key is required")
	}

	svc.httpClient = &http.Client{
		Timeout: emailSendTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}
	return svc, nil
}

// sendEmail routes to the configured provider
func (e *EmailService) sendEmail(ctx context.Context, to, subject, htmlContent, textContent string) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, emailSendTimeout)
	defer cancel()

	switch strings.ToLower(e.config.Provider) {
	case "ses":
		return e.sendViaSES(ctxWithTimeout, to, subject, htmlContent, textContent)
	case "resend":
		return e.sendViaResend(ctxWithTimeout, to, subject, htmlContent, textContent)
	default:
		return e.sendViaUnosend(ctxWithTimeout, to, subject, htmlContent, textContent)
	}
}

func (e *EmailService) sendViaSES(ctx context.Context, to, subject, htmlContent, textContent string) error {
	from := strings.TrimSpace(e.config.FromEmail)
	if e.config.FromName != "" {
		from = fmt.Sprintf("%s <%s>", e.config.FromName, from)
	}

	input := &ses.SendEmailInput{
		Source: aws.String(from),
		Destination: &types.Destination{
			ToAddresses: []string{to},
		},
		Message: &types.Message{
			Subject: &types.Content{Data: aws.String(subject), Charset: aws.String("UTF-8")},
			Body: &types.Body{
				Html: &types.Content{Data: aws.String(htmlContent), Charset: aws.String("UTF-8")},
			},
		},
	}
	if textContent != "" {
		input.Message.Body.Text = &types.Content{Data: aws.String(textContent), Charset: aws.String("UTF-8")}
	}
	if e.config.ReplyTo != "" {
		input.ReplyToAddresses = []string{e.config.ReplyTo}
	}

	_, err := e.sesClient.SendEmail(ctx, input)
	if err != nil {
		e.logger.Error("SES send failed", zap.String("to", to), zap.String("subject", subject), zap.Error(err))
		return fmt.Errorf("ses: send failed: %w", err)
	}

	e.logger.Info("Email sent", zap.String("provider", "ses"), zap.String("to", to), zap.String("subject", subject))
	return nil
}

func (e *EmailService) sendViaResend(ctx context.Context, to, subject, htmlContent, textContent string) error {
	from := strings.TrimSpace(e.config.FromEmail)
	if e.config.FromName != "" {
		from = fmt.Sprintf("%s <%s>", e.config.FromName, from)
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
	if e.config.ReplyTo != "" {
		payload["reply_to"] = e.config.ReplyTo
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIBaseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("resend: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.config.APIKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("resend: send failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		e.logger.Error("Resend returned error",
			zap.String("to", to), zap.Int("status", resp.StatusCode), zap.String("body", string(respBody)))
		return fmt.Errorf("resend: status %d", resp.StatusCode)
	}

	e.logger.Info("Email sent", zap.String("provider", "resend"), zap.String("to", to), zap.String("subject", subject))
	return nil
}

// BatchEmail represents a single email in a batch send request.
type BatchEmail struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	Text    string   `json:"text,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

// SendBatchEmails sends up to 100 emails in a single batch API call.
// Satisfies growthengine.BatchEmailSender interface.
func (e *EmailService) SendBatchEmails(ctx context.Context, emails []BatchEmail) error {
	if len(emails) == 0 {
		return nil
	}
	if len(emails) > 100 {
		return fmt.Errorf("batch: max 100 emails per batch, got %d", len(emails))
	}

	provider := strings.ToLower(strings.TrimSpace(e.config.Provider))

	var batchURL string
	var body []byte
	var err error

	switch provider {
	case "resend":
		batchURL = resendAPIBaseURL + "/emails/batch"
		body, err = json.Marshal(emails)
	case "unosend":
		batchURL = unosendAPIBaseURL + "/emails/batch"
		body, err = json.Marshal(map[string]any{"emails": emails})
	default:
		return fmt.Errorf("batch: unsupported provider %s", provider)
	}

	if err != nil {
		return fmt.Errorf("batch: marshal emails: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("batch: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.config.APIKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("batch: send failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		e.logger.Error("Batch email returned error",
			zap.String("provider", provider),
			zap.Int("count", len(emails)),
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(respBody)))
		return fmt.Errorf("batch (%s): status %d", provider, resp.StatusCode)
	}

	e.logger.Info("Batch email sent", zap.String("provider", provider), zap.Int("count", len(emails)))
	return nil
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
		"from":     from,
		"to":       []string{to},
		"subject":  subject,
		"html":     htmlContent,
		"priority": "high",
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

// SendReportEmail sends an HTML report email.
func (e *EmailService) SendReportEmail(ctx context.Context, to, subject, htmlBody string) error {
	return e.sendEmail(ctx, to, subject, htmlBody, "")
}

// SendVerificationEmail sends a verification code email
func (e *EmailService) SendVerificationEmail(ctx context.Context, email, code string) error {
	e.logger.Info("Sending verification email",
		zap.String("email", email))

	subject := "Your Rail verification code"

	innerHTML := renderHeader() +
		renderBody("Here's your verification code. Enter it in the app to continue.") +
		renderCodeBox(html.EscapeString(code)) +
		`<p style="margin:20px 0 0 0;"></p>` +
		renderSmallBody("This code expires in 10 minutes. If you didn't request this, you can safely ignore this email.")

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("Your Rail verification code is: %s\n\nThis code expires in 10 minutes.\nIf you didn't request this, ignore this email.\n\n— Rail", code)

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// SendPasswordResetEmail sends a password reset OTP email
func (e *EmailService) SendPasswordResetEmail(ctx context.Context, email, code string) error {
	e.logger.Info("Sending password reset email", zap.String("email", email))

	subject := "Reset your Rail password"

	innerHTML := renderHeader() +
		renderHeading("Reset your password") +
		renderBody("Enter this code in the app to reset your password.") +
		renderCodeBox(html.EscapeString(code)) +
		`<p style="margin:20px 0 0 0;"></p>` +
		renderSmallBody("This code expires in 10 minutes. If you didn't request a password reset, you can safely ignore this email.")

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("Your Rail password reset code is: %s\n\nThis code expires in 10 minutes.\nIf you didn't request this, ignore this email.\n\n— Rail", code)

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
			extra += fmt.Sprintf(`<p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:0 0 4px 0;line-height:1.5;">%s</p>`, html.EscapeString(reason))
		}
		if extra != "" {
			extra = fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:12px;"><tr><td style="padding:20px 24px;">%s</td></tr></table>`, extra)
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

	innerHTML := renderHeader() +
		renderHeading(html.EscapeString(heading)) +
		renderBody(html.EscapeString(body)) +
		extra

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("%s\n\n%s\n\n— Rail", heading, body)

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// SendWelcomeEmail sends a welcome email to a new user
func (e *EmailService) SendWelcomeEmail(ctx context.Context, email string) error {
	e.logger.Info("Sending welcome email", zap.String("email", email))

	subject := "Welcome to Rail"

	stepsHTML := `<table width="100%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:12px;margin:0 0 4px 0;">
<tr><td style="padding:20px 24px;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;font-weight:600;color:#343433;margin:0 0 12px 0;">What happens next</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:0 0 8px 0;line-height:1.5;">1. Complete identity verification</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:0 0 8px 0;line-height:1.5;">2. Fund your account</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:0;line-height:1.5;">3. Your money starts working</p>
</td></tr>
</table>`

	innerHTML := renderHeader() +
		renderHeading("You're in.") +
		renderBody("Your Rail account is set up. From here, every deposit automatically splits 70/30 between spending and investing — no decisions required.") +
		stepsHTML

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := "Welcome to Rail.\n\nYour account is set up. Every deposit automatically splits 70/30 between spending and investing.\n\nNext steps:\n1. Complete identity verification\n2. Fund your account\n3. Your money starts working\n\n— Rail"

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// SendCustomEmail delivers an email composed outside of the predefined templates
func (e *EmailService) SendCustomEmail(ctx context.Context, to, subject, htmlContent, textContent string) error {
	return e.sendEmail(ctx, to, subject, htmlContent, textContent)
}

// SendCustomEmailFrom delivers an email with a campaign-specific sender and reply-to.
func (e *EmailService) SendCustomEmailFrom(ctx context.Context, to, subject, htmlContent, textContent, fromEmail, fromName, replyTo string) error {
	cfg := e.config
	if strings.TrimSpace(fromEmail) != "" {
		if strings.ContainsAny(fromEmail, "\r\n") {
			return fmt.Errorf("invalid fromEmail: contains newline characters")
		}
		cfg.FromEmail = strings.TrimSpace(fromEmail)
	}
	if strings.TrimSpace(fromName) != "" {
		if strings.ContainsAny(fromName, "\r\n") {
			return fmt.Errorf("invalid fromName: contains newline characters")
		}
		cfg.FromName = strings.TrimSpace(fromName)
	}
	if strings.TrimSpace(replyTo) != "" {
		if strings.ContainsAny(replyTo, "\r\n") {
			return fmt.Errorf("invalid replyTo: contains newline characters")
		}
		cfg.ReplyTo = strings.TrimSpace(replyTo)
	}

	scoped := *e
	scoped.config = cfg
	return scoped.sendEmail(ctx, to, subject, htmlContent, textContent)
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

	userAgent := strings.TrimSpace(details.UserAgent)
	if userAgent == "" {
		userAgent = "Unknown"
	}

	safeIP := html.EscapeString(strings.TrimSpace(details.IP))
	safeLocation := html.EscapeString(location)
	safeUserAgent := html.EscapeString(userAgent)
	loginTime := details.LoginAt.UTC().Format(time.RFC1123)

	subject := "New login to your Rail account"

	detailsHTML := fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:12px;margin:0 0 20px 0;">
<tr><td style="padding:20px 24px;">
  <table width="100%%" cellpadding="0" cellspacing="0">
    <tr><td style="padding:4px 0;"><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:13px;color:#848281;margin:0;">IP Address</p><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:2px 0 12px 0;">%s</p></td></tr>
    <tr><td style="padding:4px 0;"><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:13px;color:#848281;margin:0;">Location</p><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:2px 0 12px 0;">%s</p></td></tr>
    <tr><td style="padding:4px 0;"><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:13px;color:#848281;margin:0;">Device</p><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:2px 0 12px 0;">%s</p></td></tr>
    <tr><td style="padding:4px 0;"><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:13px;color:#848281;margin:0;">Time (UTC)</p><p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:2px 0 0 0;">%s</p></td></tr>
  </table>
</td></tr></table>`, safeIP, safeLocation, safeUserAgent, loginTime)

	innerHTML := renderHeader() +
		renderBody("We detected a new login to your account. If this was you, no action is needed.") +
		detailsHTML +
		renderSmallBody("If this wasn't you, reset your password immediately and contact support.")

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf(`
New login detected on your Rail account.

IP Address: %s
Location: %s
Device: %s
Time (UTC): %s

If this wasn't you, please reset your password immediately and contact support.
`, strings.TrimSpace(details.IP), location, userAgent, loginTime)

	e.logger.Info("Sending login alert email",
		zap.String("email", email),
		zap.String("ip", strings.TrimSpace(details.IP)))

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// KYC Email Templates are now handled inline by SendKYCStatusEmail

// SendP2PInviteEmail sends an invite to a non-Rail user to claim money
func (e *EmailService) SendP2PInviteEmail(ctx context.Context, toEmail, senderName string, amount string, claimURL string) error {
	subject := fmt.Sprintf("%s sent you money on Rail", senderName)

	innerHTML := renderHeader() +
		renderHeading("You've got money waiting.") +
		renderBody(fmt.Sprintf("%s sent you <strong>%s</strong>. Download Rail to claim it.", html.EscapeString(senderName), html.EscapeString(amount))) +
		renderCTAButton(claimURL, "Claim Your Money") +
		renderSmallBody("This link expires in 14 days. After that, the money returns to the sender.")

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("%s sent you %s on Rail.\n\nClaim it here: %s\n\nThis link expires in 14 days.\n\n— Rail", senderName, amount, claimURL)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// SendP2PReceivedEmail notifies a user they received money
func (e *EmailService) SendP2PReceivedEmail(ctx context.Context, toEmail, senderName, amount string, note *string) error {
	subject := fmt.Sprintf("%s sent you %s", senderName, amount)

	noteHTML := ""
	noteText := ""
	if note != nil && *note != "" {
		noteHTML = fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:8px;margin:0 0 20px 0;"><tr><td style="padding:16px 20px;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;margin:0;line-height:1.5;font-style:italic;">"%s"</p>
</td></tr></table>`, html.EscapeString(*note))
		noteText = fmt.Sprintf("\n\nNote: \"%s\"", *note)
	}

	innerHTML := renderHeader() +
		renderHeading("Money received.") +
		renderBody(fmt.Sprintf("%s sent you <strong>%s</strong>. It's already in your spending balance.", html.EscapeString(senderName), html.EscapeString(amount))) +
		noteHTML

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("%s sent you %s. It's in your spending balance.%s\n\n— Rail", senderName, amount, noteText)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// SendP2PClaimedEmail notifies sender that their transfer was claimed
func (e *EmailService) SendP2PClaimedEmail(ctx context.Context, toEmail, recipientName, amount string) error {
	subject := fmt.Sprintf("%s claimed your %s", recipientName, amount)

	innerHTML := renderHeader() +
		renderHeading("Transfer complete.") +
		renderBody(fmt.Sprintf("%s joined Rail and claimed the <strong>%s</strong> you sent.", html.EscapeString(recipientName), html.EscapeString(amount)))

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("%s joined Rail and claimed the %s you sent.\n\n— Rail", recipientName, amount)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// SendP2PExpiredEmail notifies sender that their transfer expired
func (e *EmailService) SendP2PExpiredEmail(ctx context.Context, toEmail, identifier, amount string) error {
	subject := fmt.Sprintf("Your %s transfer expired", amount)

	innerHTML := renderHeader() +
		renderHeading("Transfer expired.") +
		renderBody(fmt.Sprintf("Your <strong>%s</strong> transfer to %s wasn't claimed within 14 days. The funds have been returned to your spending balance.", html.EscapeString(amount), html.EscapeString(identifier)))

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("Your %s transfer to %s wasn't claimed within 14 days. The funds have been returned to your spending balance.\n\n— Rail", amount, identifier)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// DepositEmailDetails contains details for a deposit confirmation email.
type DepositEmailDetails struct {
	Amount    string
	Currency  string
	Method    string // e.g. "SOL • USDC", "Bank Transfer"
	Reference string
	Date      time.Time
}

// SendDepositConfirmationEmail sends a clean deposit confirmation email.
func (e *EmailService) SendDepositConfirmationEmail(ctx context.Context, toEmail, firstName string, details DepositEmailDetails) error {
	subject := "Deposit confirmed"
	dateStr := details.Date.Format("01/02/2006 - 03:04 PM UTC")

	helloTxt := ""
	if firstName != "" {
		helloTxt = "Hello " + html.EscapeString(firstName) + ","
	}

	detailsHTML := fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:12px;margin:0 0 4px 0;">
<tr><td style="padding:4px 24px;">
  <table width="100%%" cellpadding="0" cellspacing="0">
    <tr><td style="padding:14px 0;border-bottom:1px solid #e0ddd8;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Method</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;font-weight:600;color:#343433;">%s</td>
    </tr></table></td></tr>
    <tr><td style="padding:14px 0;border-bottom:1px solid #e0ddd8;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Amount received</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;font-weight:600;color:#343433;">%s%s</td>
    </tr></table></td></tr>
    <tr><td style="padding:14px 0;border-bottom:1px solid #e0ddd8;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Reference</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;">%s</td>
    </tr></table></td></tr>
    <tr><td style="padding:14px 0;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Date &amp; time</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;">%s</td>
    </tr></table></td></tr>
  </table>
</td></tr></table>`,
		html.EscapeString(details.Method),
		html.EscapeString(details.Currency),
		html.EscapeString(details.Amount),
		html.EscapeString(details.Reference),
		html.EscapeString(dateStr),
	)

	innerHTML := renderHeader() +
		renderBody(helloTxt) +
		renderHeading("Deposit confirmed") +
		renderBody(fmt.Sprintf("Your deposit of %s%s has been confirmed.", html.EscapeString(details.Currency), html.EscapeString(details.Amount))) +
		detailsHTML +
		`<p style="margin:20px 0 0 0;"></p>` +
		renderSmallBody(`If you didn't initiate this transaction, please contact support immediately at <a href="mailto:support@rail.money" style="color:#ff3e00;text-decoration:underline;">support@rail.money</a>.`)

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("Hello %s,\n\nDeposit confirmed\n\nYour deposit of %s%s has been confirmed.\n\nMethod: %s\nAmount: %s%s\nReference: %s\nDate: %s\n\nIf you didn't initiate this, contact support@rail.money\n\n— Rail", firstName, details.Currency, details.Amount, details.Method, details.Currency, details.Amount, details.Reference, dateStr)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// WithdrawalEmailDetails contains details for a withdrawal confirmation email.
type WithdrawalEmailDetails struct {
	AmountTendered string
	AmountReceived string
	Currency       string
	BankName       string
	AccountName    string
	AccountNumber  string
	Reference      string
	Date           time.Time
}

// SendWithdrawalConfirmationEmail sends a clean withdrawal confirmation email.
func (e *EmailService) SendWithdrawalConfirmationEmail(ctx context.Context, toEmail, firstName string, details WithdrawalEmailDetails) error {
	subject := "Withdrawal successful"
	dateStr := details.Date.Format("01/02/2006 - 03:04 PM UTC")

	helloTxt := ""
	if firstName != "" {
		helloTxt = "Hello " + html.EscapeString(firstName) + ","
	}

	detailsHTML := fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:12px;margin:0 0 4px 0;">
<tr><td style="padding:4px 24px;">
  <table width="100%%" cellpadding="0" cellspacing="0">
    <tr><td style="padding:14px 0;border-bottom:1px solid #e0ddd8;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Amount sent</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;font-weight:600;color:#343433;">%s%s</td>
    </tr></table></td></tr>
    <tr><td style="padding:14px 0;border-bottom:1px solid #e0ddd8;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Amount received</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;font-weight:600;color:#343433;">%s%s</td>
    </tr></table></td></tr>
    <tr><td style="padding:14px 0;border-bottom:1px solid #e0ddd8;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Bank</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;">%s</td>
    </tr></table></td></tr>
    <tr><td style="padding:14px 0;border-bottom:1px solid #e0ddd8;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Account</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;">%s • %s</td>
    </tr></table></td></tr>
    <tr><td style="padding:14px 0;border-bottom:1px solid #e0ddd8;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Reference</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;">%s</td>
    </tr></table></td></tr>
    <tr><td style="padding:14px 0;"><table width="100%%"><tr>
      <td style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#848281;">Date &amp; time</td>
      <td align="right" style="font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:14px;color:#343433;">%s</td>
    </tr></table></td></tr>
  </table>
</td></tr></table>`,
		html.EscapeString(details.Currency),
		html.EscapeString(details.AmountTendered),
		html.EscapeString(details.Currency),
		html.EscapeString(details.AmountReceived),
		html.EscapeString(details.BankName),
		html.EscapeString(details.AccountName),
		html.EscapeString(details.AccountNumber),
		html.EscapeString(details.Reference),
		html.EscapeString(dateStr),
	)

	innerHTML := renderHeader() +
		renderBody(helloTxt) +
		renderHeading("Withdrawal successful") +
		renderBody("Your withdrawal has been processed. Here are the details:") +
		detailsHTML +
		`<p style="margin:20px 0 0 0;"></p>` +
		renderSmallBody(`If you didn't initiate this transaction, please contact support immediately at <a href="mailto:support@rail.money" style="color:#ff3e00;text-decoration:underline;">support@rail.money</a>.`)

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("Hello %s,\n\nWithdrawal successful\n\nYour withdrawal has been processed.\n\nAmount sent: %s%s\nAmount received: %s%s\nBank: %s\nAccount: %s (%s)\nReference: %s\nDate: %s\n\nIf you didn't initiate this, contact support@rail.money\n\n— Rail",
		firstName, details.Currency, details.AmountTendered, details.Currency, details.AmountReceived, details.BankName, details.AccountName, details.AccountNumber, details.Reference, dateStr)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}

// SendTransactionConfirmation sends an email with a one-tap confirmation link
// for a fund-moving action initiated from a chat platform.
func (e *EmailService) SendTransactionConfirmation(ctx context.Context, toEmail, description, confirmURL string) error {
	subject := "Confirm your transfer"

	innerHTML := renderHeader() +
		renderHeading("Confirm your transfer") +
		renderBody("Miriam is waiting to complete a transfer for you:") +
		`<p style="margin:16px 0;padding:16px;background-color:#f2f0ed;border-radius:8px;font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:15px;color:#343433;">` + html.EscapeString(description) + `</p>` +
		`<p style="text-align:center;margin:24px 0;"><a href="` + html.EscapeString(confirmURL) + `" style="display:inline-block;padding:14px 32px;background-color:#ff3e00;color:#ffffff;text-decoration:none;border-radius:8px;font-family:-apple-system,SF Pro Text,Helvetica Neue,sans-serif;font-size:16px;font-weight:600;">Confirm transfer</a></p>` +
		renderSmallBody("This link expires in 10 minutes. If you didn't request this, you can safely ignore this email.")

	htmlContent := renderBaseTemplate(innerHTML)
	textContent := fmt.Sprintf("Miriam is waiting to complete a transfer for you:\n\n%s\n\nTap here to confirm: %s\n\nThis link expires in 10 minutes.\nIf you didn't request this, ignore this email.\n\n— Rail", description, confirmURL)

	return e.sendEmail(ctx, toEmail, subject, htmlContent, textContent)
}
