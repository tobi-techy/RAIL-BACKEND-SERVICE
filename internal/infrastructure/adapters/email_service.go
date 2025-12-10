package adapters

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"io"
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
		err = smtp.SendMail(addr, auth, e.config.FromEmail, []string{to}, msg.Bytes())
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
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: e.config.SMTPHost})
	if err != nil {
		return err
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

func isNonProductionEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "", "dev", "development", "local", "staging", "test", "testing":
		return true
	default:
		return false
	}
}

// SendVerificationEmail sends an email verification message
func (e *EmailService) SendVerificationEmail(ctx context.Context, email, verificationToken string) error {
	e.logger.Info("Sending verification email",
		zap.String("email", email),
		zap.String("token", verificationToken))

	verificationURL := fmt.Sprintf("%s/verify-email?token=%s", e.config.BaseURL, verificationToken)

	subject := "Verify Your Email Address - Stack Service"

	htmlContent := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<head>
			<title>Email Verification</title>
		</head>
		<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
			<div style="background-color: #f8f9fa; padding: 30px; border-radius: 8px; text-align: center;">
				<h1 style="color: #333; margin-bottom: 20px;">Welcome to Stack Service!</h1>
				<p style="color: #666; font-size: 16px; line-height: 1.5; margin-bottom: 30px;">
					Thank you for joining Stack Service. To complete your registration and secure your account,
					please verify your email address by clicking the button below.
				</p>
				<a href="%s" 
				   style="display: inline-block; background-color: #007bff; color: white; padding: 15px 30px; 
				          text-decoration: none; border-radius: 5px; font-weight: bold; margin-bottom: 20px;">
					Verify Email Address
				</a>
				<p style="color: #888; font-size: 14px; margin-top: 30px;">
					If you cannot click the button, copy and paste this link into your browser:<br>
					<a href="%s" style="color: #007bff; word-break: break-all;">%s</a>
				</p>
				<p style="color: #888; font-size: 12px; margin-top: 20px;">
					This link will expire in 24 hours. If you did not create an account with Stack Service, 
					please ignore this email.
				</p>
			</div>
		</body>
		</html>
	`, verificationURL, verificationURL, verificationURL)

	textContent := fmt.Sprintf(`
Welcome to Stack Service!

Thank you for joining Stack Service. To complete your registration and secure your account,
please verify your email address by visiting the following link:

%s

This link will expire in 24 hours. If you did not create an account with Stack Service,
please ignore this email.

Best regards,
The Stack Service Team
	`, verificationURL)

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// SendKYCStatusEmail sends a KYC status update email
func (e *EmailService) SendKYCStatusEmail(ctx context.Context, email string, status entities.KYCStatus, rejectionReasons []string) error {
	e.logger.Info("Sending KYC status email",
		zap.String("email", email),
		zap.String("status", string(status)),
		zap.Strings("rejection_reasons", rejectionReasons))

	var subject, htmlContent, textContent string

	switch status {
	case entities.KYCStatusApproved:
		subject = "✅ KYC Verification Approved - Stack Service"
		htmlContent = e.buildKYCApprovedHTML()
		textContent = e.buildKYCApprovedText()

	case entities.KYCStatusRejected:
		subject = "❌ KYC Verification Requires Additional Information - Stack Service"
		htmlContent = e.buildKYCRejectedHTML(rejectionReasons)
		textContent = e.buildKYCRejectedText(rejectionReasons)

	case entities.KYCStatusProcessing:
		subject = "⏳ KYC Verification In Progress - Stack Service"
		htmlContent = e.buildKYCProcessingHTML()
		textContent = e.buildKYCProcessingText()

	default:
		subject = "KYC Status Update - Stack Service"
		htmlContent = e.buildKYCGenericHTML(string(status))
		textContent = e.buildKYCGenericText(string(status))
	}

	return e.sendEmail(ctx, email, subject, htmlContent, textContent)
}

// SendWelcomeEmail sends a welcome email to a new user
func (e *EmailService) SendWelcomeEmail(ctx context.Context, email string) error {
	e.logger.Info("Sending welcome email",
		zap.String("email", email))

	subject := "🎉 Welcome to Stack Service!"

	htmlContent := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<head><title>Welcome to Stack Service</title></head>
		<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
			<div style="background-color: #f8f9fa; padding: 30px; border-radius: 8px; text-align: center;">
				<h1 style="color: #333; margin-bottom: 20px;">Welcome to Stack Service! 🎉</h1>
				<p style="color: #666; font-size: 16px; line-height: 1.5; margin-bottom: 30px;">
					Thank you for joining Stack Service! We're excited to have you on board.
					Your account has been successfully created and verified.
				</p>
				<div style="background-color: white; padding: 20px; border-radius: 8px; margin: 20px 0;">
					<h3 style="color: #333; margin-bottom: 15px;">Next Steps:</h3>
					<ul style="text-align: left; color: #666; line-height: 1.8;">
						<li>Complete your KYC verification</li>
						<li>Set up your digital wallets</li>
						<li>Start exploring our platform</li>
					</ul>
				</div>
				<a href="%s/dashboard" 
				   style="display: inline-block; background-color: #28a745; color: white; padding: 15px 30px; 
				          text-decoration: none; border-radius: 5px; font-weight: bold; margin: 20px 0;">
					Get Started
				</a>
				<p style="color: #888; font-size: 12px; margin-top: 30px;">
					If you have any questions, feel free to contact our support team.
				</p>
			</div>
		</body>
		</html>
	`, e.config.BaseURL)

	textContent := fmt.Sprintf(`
Welcome to Stack Service!

Thank you for joining Stack Service! We're excited to have you on board.
Your account has been successfully created and verified.

Next Steps:
- Complete your KYC verification
- Set up your digital wallets  
- Start exploring our platform

Get started by visiting: %s/dashboard

If you have any questions, feel free to contact our support team.

Best regards,
The Stack Service Team
	`, e.config.BaseURL)

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
	safeForwarded := html.EscapeString(forwarded)
	safeLocation := html.EscapeString(location)
	safeUserAgent := html.EscapeString(userAgent)
	loginTime := details.LoginAt.UTC().Format(time.RFC1123)

	subject := "New Login Detected - Stack Service"

	htmlContent := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<head><title>New Login Detected</title></head>
		<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
			<div style="background-color: #f8f9fa; padding: 24px; border-radius: 8px; border: 1px solid #e9ecef;">
				<h2 style="color: #333; margin-bottom: 16px;">We noticed a new login to your account</h2>
				<p style="color: #555; line-height: 1.6;">If this was you, you're all set. If not, secure your account right away.</p>
				<div style="background-color: white; border-radius: 8px; padding: 16px; margin: 20px 0; border: 1px solid #dee2e6;">
					<p style="margin: 4px 0; color: #333;"><strong>IP Address:</strong> %s</p>
					<p style="margin: 4px 0; color: #333;"><strong>Forwarded For:</strong> %s</p>
					<p style="margin: 4px 0; color: #333;"><strong>Location:</strong> %s</p>
					<p style="margin: 4px 0; color: #333;"><strong>Device:</strong> %s</p>
					<p style="margin: 4px 0; color: #333;"><strong>Time (UTC):</strong> %s</p>
				</div>
				<p style="color: #555; line-height: 1.6;">If you did not perform this login, please reset your password immediately and contact support.</p>
			</div>
		</body>
		</html>
	`, safeIP, safeForwarded, safeLocation, safeUserAgent, loginTime)

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

// KYC Email Templates

func (e *EmailService) buildKYCApprovedHTML() string {
	return fmt.Sprintf(`
	<!DOCTYPE html>
	<html>
	<head><title>KYC Approved</title></head>
	<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
		<div style="background-color: #d4edda; padding: 30px; border-radius: 8px; text-align: center; border: 1px solid #c3e6cb;">
			<h1 style="color: #155724; margin-bottom: 20px;">✅ Verification Complete!</h1>
			<p style="color: #155724; font-size: 16px; line-height: 1.5; margin-bottom: 30px;">
				Congratulations! Your identity verification has been successfully approved.
				You can now proceed to create your digital wallets and access all platform features.
			</p>
			<a href="%s/wallets/create" 
			   style="display: inline-block; background-color: #28a745; color: white; padding: 15px 30px; 
			          text-decoration: none; border-radius: 5px; font-weight: bold;">
				Create Your Wallets
			</a>
		</div>
	</body>
	</html>
	`, e.config.BaseURL)
}

func (e *EmailService) buildKYCApprovedText() string {
	return fmt.Sprintf(`
Verification Complete!

Congratulations! Your identity verification has been successfully approved.
You can now proceed to create your digital wallets and access all platform features.

Create your wallets: %s/wallets/create

Best regards,
The Stack Service Team
	`, e.config.BaseURL)
}

func (e *EmailService) buildKYCRejectedHTML(rejectionReasons []string) string {
	reasons := ""
	for _, reason := range rejectionReasons {
		reasons += fmt.Sprintf("<li style='margin-bottom: 8px;'>%s</li>", reason)
	}

	return fmt.Sprintf(`
	<!DOCTYPE html>
	<html>
	<head><title>KYC Additional Information Required</title></head>
	<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
		<div style="background-color: #fff3cd; padding: 30px; border-radius: 8px; border: 1px solid #ffeaa7;">
			<h1 style="color: #856404; margin-bottom: 20px;">Additional Information Required</h1>
			<p style="color: #856404; font-size: 16px; line-height: 1.5; margin-bottom: 20px;">
				We need additional information to complete your identity verification.
				Please review the following items and resubmit your documents:
			</p>
			<ul style="color: #856404; margin: 20px 0; padding-left: 20px;">%s</ul>
			<a href="%s/kyc/resubmit" 
			   style="display: inline-block; background-color: #ffc107; color: #212529; padding: 15px 30px; 
			          text-decoration: none; border-radius: 5px; font-weight: bold; margin-top: 20px;">
				Resubmit Documents
			</a>
		</div>
	</body>
	</html>
	`, reasons, e.config.BaseURL)
}

func (e *EmailService) buildKYCRejectedText(rejectionReasons []string) string {
	reasons := ""
	for i, reason := range rejectionReasons {
		reasons += fmt.Sprintf("%d. %s\n", i+1, reason)
	}

	return fmt.Sprintf(`
Additional Information Required

We need additional information to complete your identity verification.
Please review the following items and resubmit your documents:

%s
Resubmit documents: %s/kyc/resubmit

Best regards,
The Stack Service Team
	`, reasons, e.config.BaseURL)
}

func (e *EmailService) buildKYCProcessingHTML() string {
	return `
	<!DOCTYPE html>
	<html>
	<head><title>KYC Processing</title></head>
	<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
		<div style="background-color: #cce5ff; padding: 30px; border-radius: 8px; text-align: center; border: 1px solid #99d6ff;">
			<h1 style="color: #004085; margin-bottom: 20px;">⏳ Verification In Progress</h1>
			<p style="color: #004085; font-size: 16px; line-height: 1.5; margin-bottom: 20px;">
				Your identity verification documents are currently being reviewed by our team.
				You will receive an update within 24-48 hours.
			</p>
			<p style="color: #004085; font-size: 14px;">
				Thank you for your patience!
			</p>
		</div>
	</body>
	</html>
	`
}

func (e *EmailService) buildKYCProcessingText() string {
	return `
Verification In Progress

Your identity verification documents are currently being reviewed by our team.
You will receive an update within 24-48 hours.

Thank you for your patience!

Best regards,
The Stack Service Team
	`
}

func (e *EmailService) buildKYCGenericHTML(status string) string {
	return fmt.Sprintf(`
	<!DOCTYPE html>
	<html>
	<head><title>KYC Status Update</title></head>
	<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
		<div style="background-color: #f8f9fa; padding: 30px; border-radius: 8px; border: 1px solid #dee2e6;">
			<h1 style="color: #495057; margin-bottom: 20px;">KYC Status Update</h1>
			<p style="color: #495057; font-size: 16px; line-height: 1.5;">
				Your KYC verification status has been updated to: <strong>%s</strong>
			</p>
		</div>
	</body>
	</html>
	`, status)
}

func (e *EmailService) buildKYCGenericText(status string) string {
	return fmt.Sprintf(`
KYC Status Update

Your KYC verification status has been updated to: %s

Best regards,
The Stack Service Team
	`, status)
}
