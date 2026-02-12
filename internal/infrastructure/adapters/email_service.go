package adapters

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

const (
	resendAPIBaseURL        = "https://api.resend.com"
	resendSandboxFromSender = "onboarding@resend.dev"
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
	// SMTP settings (for mailpit, smtp providers)
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPUseTLS   bool
}

// EmailService implements the email service interface
type EmailService struct {
	logger     *zap.Logger
	config     EmailServiceConfig
	client     *sendgrid.Client
	httpClient *http.Client
}

// NewEmailService creates a new email service
func NewEmailService(logger *zap.Logger, config EmailServiceConfig) (*EmailService, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	if provider == "" {
		return nil, fmt.Errorf("email provider is required")
	}

	if strings.TrimSpace(config.FromEmail) == "" {
		return nil, fmt.Errorf("email from address is required")
	}

	var (
		client     *sendgrid.Client
		httpClient *http.Client
	)

	switch provider {
	case "sendgrid":
		if strings.TrimSpace(config.APIKey) == "" {
			return nil, fmt.Errorf("sendgrid api key is required")
		}
		client = sendgrid.NewSendClient(config.APIKey)
	case "resend":
		if strings.TrimSpace(config.APIKey) == "" {
			return nil, fmt.Errorf("resend api key is required")
		}
		httpClient = &http.Client{Timeout: 30 * time.Second}
	case "mailpit", "smtp":
		if config.SMTPHost == "" {
			return nil, fmt.Errorf("smtp host is required for %s provider", provider)
		}
		if config.SMTPPort == 0 {
			config.SMTPPort = 1025 // default mailpit port
		}
	case "mailtrap":
		if strings.TrimSpace(config.APIKey) == "" {
			return nil, fmt.Errorf("mailtrap api key is required")
		}
		httpClient = &http.Client{Timeout: 15 * time.Second}
	default:
		return nil, fmt.Errorf("unsupported email provider: %s", provider)
	}

	return &EmailService{
		logger:     logger,
		config:     config,
		client:     client,
		httpClient: httpClient,
	}, nil
}

// sendEmail is a helper method to send emails via the configured provider
func (e *EmailService) sendEmail(ctx context.Context, to, subject, htmlContent, textContent string) error {
	provider := strings.ToLower(e.config.Provider)

	// Add timeout to context
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	switch provider {
	case "resend":
		return e.sendViaResend(ctxWithTimeout, to, subject, htmlContent, textContent)
	case "sendgrid":
		return e.sendViaSendgrid(ctxWithTimeout, to, subject, htmlContent, textContent)
	case "mailtrap":
		return e.sendViaMailtrap(ctxWithTimeout, to, subject, htmlContent, textContent)
	case "mailpit", "smtp":
		return e.sendViaSMTP(ctxWithTimeout, to, subject, htmlContent, textContent)
	default:
		return fmt.Errorf("unsupported email provider: %s", provider)
	}
}

func (e *EmailService) sendViaSendgrid(ctx context.Context, to, subject, htmlContent, textContent string) error {
	if e.client == nil {
		return fmt.Errorf("sendgrid client not configured")
	}

	from := mail.NewEmail(e.config.FromName, e.config.FromEmail)
	toEmail := mail.NewEmail("", to)
	message := mail.NewSingleEmail(from, subject, toEmail, textContent, htmlContent)

	if strings.TrimSpace(e.config.ReplyTo) != "" {
		message.SetReplyTo(mail.NewEmail(e.config.FromName, e.config.ReplyTo))
	}

	response, err := e.client.SendWithContext(ctx, message)
	if err != nil {
		e.logger.Error("Failed to send email",
			zap.String("provider", "sendgrid"),
			zap.String("to", to),
			zap.String("subject", subject),
			zap.Error(err))
		return fmt.Errorf("failed to send email: %w", err)
	}

	if response.StatusCode >= 400 {
		e.logger.Error("Email service returned error",
			zap.String("provider", "sendgrid"),
			zap.String("to", to),
			zap.String("subject", subject),
			zap.Int("status_code", response.StatusCode),
			zap.String("response_body", response.Body))
		return fmt.Errorf("email service error: status %d, body: %s", response.StatusCode, response.Body)
	}

	e.logger.Info("Email sent successfully",
		zap.String("provider", "sendgrid"),
		zap.String("to", to),
		zap.String("subject", subject),
		zap.Int("status_code", response.StatusCode))

	return nil
}

func (e *EmailService) sendViaResend(ctx context.Context, to, subject, htmlContent, textContent string) error {
	if e.httpClient == nil {
		return fmt.Errorf("resend client not configured")
	}

	fromEmail := strings.TrimSpace(e.config.FromEmail)
	if fromEmail == "" {
		return fmt.Errorf("resend from email is required")
	}

	from := fromEmail
	if strings.TrimSpace(e.config.FromName) != "" {
		from = fmt.Sprintf("%s <%s>", e.config.FromName, fromEmail)
	}

	if isNonProductionEnv(e.config.Environment) {
		domainParts := strings.SplitN(fromEmail, "@", 2)
		if len(domainParts) != 2 || strings.TrimSpace(domainParts[1]) == "" {
			return fmt.Errorf("invalid resend from address: %s", fromEmail)
		}

		domain := strings.ToLower(strings.TrimSpace(domainParts[1]))
		if domain != "resend.dev" {
			originalFrom := from
			fromEmail = resendSandboxFromSender
			if strings.TrimSpace(e.config.FromName) != "" {
				from = fmt.Sprintf("%s <%s>", e.config.FromName, resendSandboxFromSender)
			} else {
				from = resendSandboxFromSender
			}

			e.logger.Warn("Overriding Resend sender address for non-production environment",
				zap.String("original_from", originalFrom),
				zap.String("overridden_from", from),
				zap.String("environment", e.config.Environment))
		}
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
		return fmt.Errorf("failed to marshal resend payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIBaseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create resend request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.config.APIKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.logger.Error("Failed to send email via Resend",
			zap.String("provider", "resend"),
			zap.String("to", to),
			zap.String("subject", subject),
			zap.Error(err))
		return fmt.Errorf("resend send request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		logFields := []zap.Field{
			zap.String("provider", "resend"),
			zap.String("to", to),
			zap.String("subject", subject),
			zap.Int("status_code", resp.StatusCode),
			zap.String("environment", e.config.Environment),
			zap.String("response_body", string(respBody)),
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			e.logger.Error("Resend authentication failed", logFields...)
		} else {
			e.logger.Error("Resend returned error", logFields...)
		}

		return fmt.Errorf("resend email error: status %d", resp.StatusCode)
	}

	e.logger.Info("Email sent successfully",
		zap.String("provider", "resend"),
		zap.String("to", to),
		zap.String("subject", subject),
		zap.Int("status_code", resp.StatusCode))

	return nil
}


func (e *EmailService) sendViaMailtrap(ctx context.Context, to, subject, htmlContent, textContent string) error {
	payload := map[string]interface{}{
		"from":     map[string]string{"email": e.config.FromEmail, "name": e.config.FromName},
		"to":       []map[string]string{{"email": to}},
		"subject":  subject,
		"html":     htmlContent,
		"text":     textContent,
		"category": "Rail",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal mailtrap payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sandbox.api.mailtrap.io/api/send/"+e.config.SMTPUsername, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create mailtrap request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.config.APIKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		e.logger.Error("Mailtrap API request failed", zap.String("to", to), zap.Error(err))
		return fmt.Errorf("mailtrap api failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		e.logger.Error("Mailtrap API error", zap.Int("status", resp.StatusCode), zap.String("body", string(respBody)))
		return fmt.Errorf("mailtrap api error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	e.logger.Info("Email sent successfully", zap.String("provider", "mailtrap"), zap.String("to", to), zap.String("subject", subject))
	return nil
}
func (e *EmailService) sendViaSMTP(_ context.Context, to, subject, htmlContent, textContent string) error {
	from := e.config.FromEmail
	if e.config.FromName != "" {
		from = fmt.Sprintf("%s <%s>", e.config.FromName, e.config.FromEmail)
	}

	// Build MIME message
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	if e.config.ReplyTo != "" {
		msg.WriteString(fmt.Sprintf("Reply-To: %s\r\n", e.config.ReplyTo))
	}
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(htmlContent)

	addr := fmt.Sprintf("%s:%d", e.config.SMTPHost, e.config.SMTPPort)

	var auth smtp.Auth
	if e.config.SMTPUsername != "" {
		auth = smtp.PlainAuth("", e.config.SMTPUsername, e.config.SMTPPassword, e.config.SMTPHost)
	}

	var err error
	if e.config.SMTPUseTLS {
		err = e.sendSMTPWithTLS(addr, auth, e.config.FromEmail, to, msg.Bytes())
	} else {
		err = e.sendSMTPWithSTARTTLS(addr, auth, e.config.FromEmail, to, msg.Bytes())
	}

	if err != nil {
		e.logger.Error("Failed to send email via SMTP",
			zap.String("provider", e.config.Provider),
			zap.String("to", to),
			zap.String("host", e.config.SMTPHost),
			zap.Error(err))
		return fmt.Errorf("smtp send failed: %w", err)
	}

	e.logger.Info("Email sent successfully",
		zap.String("provider", e.config.Provider),
		zap.String("to", to),
		zap.String("subject", subject))

	return nil
}

func (e *EmailService) sendSMTPWithTLS(addr string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{ServerName: e.config.SMTPHost})
	if err != nil {
		return fmt.Errorf("tls dial failed: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, e.config.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()

	if auth != nil {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(msg)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	return client.Quit()
}

func (e *EmailService) sendSMTPWithSTARTTLS(addr string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("smtp dial failed: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, e.config.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err = client.StartTLS(&tls.Config{ServerName: e.config.SMTPHost}); err != nil {
			return fmt.Errorf("starttls failed: %w", err)
		}
	}

	if auth != nil {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(msg)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	return client.Quit()
}

func isNonProductionEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "", "dev", "development", "local", "staging", "test", "testing":
		return true
	default:
		return false
	}
}

// SendVerificationEmail sends a verification code email
func (e *EmailService) SendVerificationEmail(ctx context.Context, email, code string) error {
	e.logger.Info("Sending verification email",
		zap.String("email", email))

	subject := "Your Rail verification code"

	htmlContent := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background-color:#f5f5f7;-webkit-font-smoothing:antialiased;">
<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:40px 20px;">
<tr><td align="center">
<table width="480" cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;">
<tr><td style="padding:40px 40px 0 40px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0 0 8px 0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:32px 40px;">
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 24px 0;line-height:1.5;">Here's your verification code. Enter it in the app to continue.</p>
  <table width="100%%" cellpadding="0" cellspacing="0"><tr><td align="center" style="background-color:#f5f5f7;border-radius:12px;padding:24px;">
    <p style="font-family:-apple-system,SF Pro Display,SF Pro Rounded,Helvetica Neue,monospace;font-size:36px;font-weight:700;color:#1d1d1f;margin:0;letter-spacing:8px;">%s</p>
  </td></tr></table>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:13px;color:#86868b;margin:24px 0 0 0;line-height:1.5;">This code expires in 10 minutes. If you didn't request this, you can safely ignore this email.</p>
</td></tr>
<tr><td style="padding:0 40px 40px 40px;border-top:1px solid #f5f5f7;">
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
<table width="100%%%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:40px 20px;">
<tr><td align="center">
<table width="480" cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;">
<tr><td style="padding:40px 40px 0 40px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:32px 40px;">
  <p style="font-family:-apple-system,SF Pro Display,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:22px;font-weight:600;color:#1d1d1f;margin:0 0 16px 0;letter-spacing:-0.3px;">%s</p>
  <p style="font-family:-apple-system,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:15px;color:#1d1d1f;margin:0 0 24px 0;line-height:1.5;">%s</p>%s
</td></tr>
<tr><td style="padding:0 40px 40px 40px;border-top:1px solid #f5f5f7;">
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
<table width="100%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:40px 20px;">
<tr><td align="center">
<table width="480" cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;">
<tr><td style="padding:40px 40px 0 40px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0 0 8px 0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:32px 40px;">
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
<tr><td style="padding:0 40px 40px 40px;border-top:1px solid #f5f5f7;">
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
<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f5f5f7;padding:40px 20px;">
<tr><td align="center">
<table width="480" cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:16px;overflow:hidden;">
<tr><td style="padding:40px 40px 0 40px;">
  <p style="font-family:-apple-system,SF Pro Display,SF Pro Text,Helvetica Neue,Helvetica,Arial,sans-serif;font-size:28px;font-weight:700;color:#1d1d1f;margin:0 0 8px 0;letter-spacing:-0.5px;">Rail</p>
</td></tr>
<tr><td style="padding:32px 40px;">
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
<tr><td style="padding:0 40px 40px 40px;border-top:1px solid #f5f5f7;">
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
