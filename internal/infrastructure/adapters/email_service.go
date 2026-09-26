package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// Supported email providers. The set is closed: an unrecognised provider name
// is a boot error rather than a silent fall-through to Unosend, which is how a
// typo in EMAIL_PROVIDER used to go unnoticed.
const (
	emailProviderLog     = "log"
	emailProviderSES     = "ses"
	emailProviderResend  = "resend"
	emailProviderUnosend = "unosend"
)

func normalizeEmailProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func isSupportedEmailProvider(provider string) bool {
	switch provider {
	case emailProviderLog, emailProviderSES, emailProviderResend, emailProviderUnosend:
		return true
	default:
		return false
	}
}

// emailProviderNeedsAPIKey reports whether a provider authenticates with a
// bearer API key (SES uses AWS credentials instead).
func emailProviderNeedsAPIKey(provider string) bool {
	return provider == emailProviderResend || provider == emailProviderUnosend
}

// emailResponseReason maps a provider rejection body to a reason code, and
// reports whether the rejection is permanent for that recipient.
//
// Only 400/422 can be recipient-level: 401/403 are our credentials, 429 and 5xx
// are the provider's problem. Both are transient from the sender's point of
// view and neither proves anything about the address. The phrase list is
// deliberately small — misreading a transient failure as permanent tells the
// person their address is dead and stops charging their OTP attempts against
// the rate limit, so unrecognised bodies stay transient.
func emailResponseReason(statusCode int, body string) (string, bool) {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusUnprocessableEntity {
		return "", false
	}

	lower := strings.ToLower(body)
	switch {
	case strings.Contains(lower, "suppress"):
		return entities.EmailReasonRecipientSuppressed, true
	case strings.Contains(lower, "unsubscrib"), strings.Contains(lower, "opt-out"), strings.Contains(lower, "opted out"):
		return entities.EmailReasonRecipientUnsubscribed, true
	case strings.Contains(lower, "invalid recipient"),
		strings.Contains(lower, "invalid to address"),
		strings.Contains(lower, "recipient address rejected"),
		strings.Contains(lower, "not a valid email"),
		strings.Contains(lower, "mailbox not found"),
		strings.Contains(lower, "mailbox unavailable"),
		strings.Contains(lower, "no such user"),
		strings.Contains(lower, "user unknown"):
		return entities.EmailReasonRecipientInvalid, true
	default:
		return "", false
	}
}

// deliveryReason extracts the reason code from a delivery error for logging.
func deliveryReason(err error) string {
	var deliveryErr *entities.EmailDeliveryError
	if errors.As(err, &deliveryErr) && deliveryErr.Reason != "" {
		return deliveryErr.Reason
	}
	return "unspecified"
}

// providerTarget is one delivery route: the provider plus the credentials and
// sender identity it must use. The fallback shares the primary's sender
// identity (same verified domain) but carries its own API key, so a fallback
// can never authenticate with the primary's credentials.
type providerTarget struct {
	provider  string
	apiKey    string
	fromEmail string
	fromName  string
	replyTo   string
}

// sender renders the RFC 5322 From header for this target.
func (t providerTarget) sender() (string, error) {
	from := strings.TrimSpace(t.fromEmail)
	if from == "" {
		return "", fmt.Errorf("%s: from email is required", t.provider)
	}
	if t.fromName != "" {
		return fmt.Sprintf("%s <%s>", t.fromName, from), nil
	}
	return from, nil
}

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

	// FallbackProvider is tried once when the primary provider permanently
	// rejects the recipient (suppressed, invalid, blocked). Only a permanent,
	// recipient-level rejection falls through, because it is the one failure
	// that proves nothing was delivered — a transient failure may already be
	// sitting in the inbox, so retrying it elsewhere could double-send.
	FallbackProvider string
	FallbackAPIKey   string
}

// EmailService implements the email service interface
type EmailService struct {
	logger     *zap.Logger
	config     EmailServiceConfig
	httpClient *http.Client
	sesClient  *ses.Client

	// Base URLs are fields rather than constants so tests can point a provider
	// at a local server. Production values are set in NewEmailService.
	resendBaseURL  string
	unosendBaseURL string
}

// NewEmailService creates a new email service
func NewEmailService(logger *zap.Logger, config EmailServiceConfig) (*EmailService, error) {
	provider := normalizeEmailProvider(config.Provider)
	if provider == "" {
		return nil, fmt.Errorf("email provider is required")
	}
	if !isSupportedEmailProvider(provider) {
		return nil, fmt.Errorf("unsupported email provider %q", config.Provider)
	}
	config.Provider = provider
	config.FallbackProvider = normalizeEmailProvider(config.FallbackProvider)

	svc := &EmailService{
		logger:         logger,
		config:         config,
		resendBaseURL:  resendAPIBaseURL,
		unosendBaseURL: unosendAPIBaseURL,
	}

	// "log" is a dev-only sink that writes the rendered email (including any OTP
	// code) to the logger instead of sending, so E2E harnesses can capture codes.
	// It always succeeds, which is why it is fenced off separately below.
	if provider == emailProviderLog {
		if err := validateLogSinkConfig(config); err != nil {
			return nil, err
		}
		return svc, nil
	}

	if strings.TrimSpace(config.FromEmail) == "" {
		return nil, fmt.Errorf("email from address is required")
	}

	if err := svc.resolveSESClient(); err != nil {
		return nil, err
	}

	if emailProviderNeedsAPIKey(config.Provider) && strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("email api key is required")
	}

	if err := svc.resolveFallbackProvider(); err != nil {
		return nil, err
	}

	if svc.config.FallbackProvider != "" {
		logger.Info("email fallback provider configured",
			zap.String("provider", svc.config.Provider),
			zap.String("fallback_provider", svc.config.FallbackProvider))
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

// validateLogSinkConfig enforces that the dev-only "log" sink can never stand in
// for a real provider: it is forbidden in production, and it always reports
// success, so putting a fallback behind it would silence that provider entirely.
func validateLogSinkConfig(config EmailServiceConfig) error {
	if strings.EqualFold(config.Environment, "production") {
		return fmt.Errorf("email provider 'log' is not allowed in production")
	}
	if config.FallbackProvider != "" {
		return fmt.Errorf("email provider 'log' cannot have a fallback provider")
	}
	return nil
}

// resolveSESClient loads AWS credentials when SES serves either role, since SES
// authenticates with them instead of an API key.
func (e *EmailService) resolveSESClient() error {
	if e.config.Provider != emailProviderSES && e.config.FallbackProvider != emailProviderSES {
		return nil
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"))
	if err != nil {
		if e.config.Provider == emailProviderSES {
			return fmt.Errorf("ses: load aws config: %w", err)
		}
		// The fallback is an optimisation, never a boot requirement.
		e.logger.Warn("email fallback provider 'ses' disabled: could not load aws config", zap.Error(err))
		e.config.FallbackProvider = ""
		return nil
	}
	e.sesClient = ses.NewFromConfig(awsCfg)
	return nil
}

// resolveFallbackProvider keeps the fallback usable or drops it. A fallback that
// cannot work is a configuration gap, not a reason to refuse to boot: the primary
// provider still sends everything it can, so the fallback is removed with a
// warning and delivery continues.
func (e *EmailService) resolveFallbackProvider() error {
	switch {
	case e.config.FallbackProvider == "":
	case e.config.FallbackProvider == e.config.Provider:
		e.config.FallbackProvider = ""
	case e.config.FallbackProvider == emailProviderLog:
		return fmt.Errorf("email provider 'log' cannot be used as a fallback provider")
	case e.config.FallbackProvider == emailProviderSES:
		if e.sesClient == nil {
			e.config.FallbackProvider = ""
		}
	case emailProviderNeedsAPIKey(e.config.FallbackProvider) && strings.TrimSpace(e.config.FallbackAPIKey) == "":
		e.logger.Warn("email fallback provider disabled: api key missing",
			zap.String("fallback_provider", e.config.FallbackProvider))
		e.config.FallbackProvider = ""
	}
	return nil
}

// providerTargets returns the delivery order: the primary provider first, then
// the fallback when one is configured and usable.
func (e *EmailService) providerTargets() []providerTarget {
	primary := providerTarget{
		provider:  normalizeEmailProvider(e.config.Provider),
		apiKey:    e.config.APIKey,
		fromEmail: strings.TrimSpace(e.config.FromEmail),
		fromName:  strings.TrimSpace(e.config.FromName),
		replyTo:   strings.TrimSpace(e.config.ReplyTo),
	}

	targets := []providerTarget{primary}
	if fallback := normalizeEmailProvider(e.config.FallbackProvider); fallback != "" {
		targets = append(targets, providerTarget{
			provider:  fallback,
			apiKey:    e.config.FallbackAPIKey,
			fromEmail: primary.fromEmail,
			fromName:  primary.fromName,
			replyTo:   primary.replyTo,
		})
	}
	return targets
}

// sendEmail routes to the configured provider. When the primary permanently
// rejects the recipient (suppressed, invalid, blocked) the send is retried once
// through the fallback provider, because a suppression list is per-provider: a
// hard bounce on one provider says nothing about another. A transient failure
// never falls through — the provider may already have accepted the message, and
// a second copy from a different provider is worse than an honest error.
func (e *EmailService) sendEmail(ctx context.Context, to, subject, htmlContent, textContent string) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, emailSendTimeout)
	defer cancel()

	targets := e.providerTargets()
	var primaryPermanentErr error

	for i, target := range targets {
		err := e.sendViaTarget(ctxWithTimeout, target, to, subject, htmlContent, textContent)
		if err == nil {
			return nil
		}
		if !entities.IsPermanentEmailDeliveryError(err) {
			// The primary provider's transient failure is the answer: nothing may
			// fall through to the fallback, because the message might already be
			// delivered. A *fallback's* failure, however, must not be allowed to
			// overwrite a primary permanent rejection — that rejection is the only
			// thing that tells the person their address is the problem, and losing
			// it would put them back on the unbreakable "try again" loop.
			if primaryPermanentErr == nil {
				return err
			}
			e.logger.Error("email fallback provider failed after a permanent rejection",
				zap.String("to", to),
				zap.String("provider", target.provider),
				zap.Error(err))
			continue
		}
		if primaryPermanentErr == nil {
			primaryPermanentErr = err
		}

		fields := []zap.Field{
			zap.String("to", to),
			zap.String("provider", target.provider),
			zap.String("reason", deliveryReason(err)),
		}
		switch remaining := len(targets) - i - 1; {
		case remaining > 0:
			e.logger.Warn("provider permanently rejected recipient; trying the fallback provider",
				append(fields, zap.String("fallback_provider", targets[i+1].provider))...)
		default:
			e.logger.Error("every configured email provider permanently rejected the recipient", fields...)
		}
	}

	// The primary provider's rejection is what the caller acts on: it names the
	// address's real state. A fallback failure adds noise, not information.
	return primaryPermanentErr
}

// sendViaTarget dispatches one send to one provider.
func (e *EmailService) sendViaTarget(ctx context.Context, target providerTarget, to, subject, htmlContent, textContent string) error {
	switch target.provider {
	case emailProviderLog:
		e.logNewLogProviderEmail(to, subject, htmlContent, textContent)
		return nil
	case emailProviderSES:
		return e.sendViaSES(ctx, target, to, subject, htmlContent, textContent)
	case emailProviderResend:
		return e.sendViaResend(ctx, target, to, subject, htmlContent, textContent)
	case emailProviderUnosend:
		return e.sendViaUnosend(ctx, target, to, subject, htmlContent, textContent)
	default:
		return fmt.Errorf("unsupported email provider %q", target.provider)
	}
}

// logNewLogProviderEmail writes a rendered, sendable email to the logger as a
// structured event, so dev/test harnesses can read OTP codes without a real
// delivery provider. Least-privilege: this sink must never be enabled in
// production (enforced in NewEmailService).
func (e *EmailService) logNewLogProviderEmail(to, subject, htmlContent, textContent string) {
	e.logger.Info("EMAIL_LOG_PROVIDER (dev sink, not sent)",
		zap.String("to", to),
		zap.String("subject", subject),
		zap.String("html", htmlContent),
		zap.String("text", textContent),
	)
}

func (e *EmailService) sendViaSES(ctx context.Context, target providerTarget, to, subject, htmlContent, textContent string) error {
	if e.sesClient == nil {
		return fmt.Errorf("ses client not configured")
	}

	from, err := target.sender()
	if err != nil {
		return err
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
	if target.replyTo != "" {
		input.ReplyToAddresses = []string{target.replyTo}
	}

	if _, err := e.sesClient.SendEmail(ctx, input); err != nil {
		// SES reports a suppressed recipient or an unverified address as
		// MessageRejected (HTTP 400). The exception carries no stable code
		// beyond that, so the text is classified the same way the HTTP
		// providers' response bodies are.
		if reason, permanent := emailResponseReason(http.StatusBadRequest, err.Error()); permanent {
			e.logger.Error("SES permanently rejected the recipient",
				zap.String("to", to), zap.String("reason", reason))
			return &entities.EmailDeliveryError{
				Provider:   emailProviderSES,
				Recipient:  to,
				StatusCode: http.StatusBadRequest,
				Reason:     reason,
				Permanent:  true,
			}
		}
		e.logger.Error("SES send failed", zap.String("to", to), zap.String("subject", subject), zap.Error(err))
		return fmt.Errorf("ses: send failed: %w", err)
	}

	e.logger.Info("Email sent", zap.String("provider", "ses"), zap.String("to", to), zap.String("subject", subject))
	return nil
}

func (e *EmailService) sendViaResend(ctx context.Context, target providerTarget, to, subject, htmlContent, textContent string) error {
	from, err := target.sender()
	if err != nil {
		return err
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
	if target.replyTo != "" {
		payload["reply_to"] = target.replyTo
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("resend: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.resendBaseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("resend: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+target.apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("resend: send failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		e.logger.Error("Resend returned error",
			zap.String("to", to), zap.Int("status", resp.StatusCode), zap.String("body", string(respBody)))
		if reason, permanent := emailResponseReason(resp.StatusCode, string(respBody)); permanent {
			return &entities.EmailDeliveryError{
				Provider:   emailProviderResend,
				Recipient:  to,
				StatusCode: resp.StatusCode,
				Reason:     reason,
				Permanent:  true,
			}
		}
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

	// Batches go to the primary provider only. The fallback exists for a single
	// recipient the provider refuses, and a batch response aggregates per-recipient
	// outcomes, so there is nothing to classify or re-route here.
	primary := e.providerTargets()[0]

	var batchURL string
	var body []byte
	var err error

	switch primary.provider {
	case emailProviderResend:
		batchURL = e.resendBaseURL + "/emails/batch"
		body, err = json.Marshal(emails)
	case emailProviderUnosend:
		batchURL = e.unosendBaseURL + "/emails/batch"
		body, err = json.Marshal(map[string]any{"emails": emails})
	default:
		return fmt.Errorf("batch: unsupported provider %s", primary.provider)
	}

	if err != nil {
		return fmt.Errorf("batch: marshal emails: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("batch: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+primary.apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("batch: send failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		e.logger.Error("Batch email returned error",
			zap.String("provider", primary.provider),
			zap.Int("count", len(emails)),
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(respBody)))
		return fmt.Errorf("batch (%s): status %d", primary.provider, resp.StatusCode)
	}

	e.logger.Info("Batch email sent", zap.String("provider", primary.provider), zap.Int("count", len(emails)))
	return nil
}

func (e *EmailService) sendViaUnosend(ctx context.Context, target providerTarget, to, subject, htmlContent, textContent string) error {
	if e.httpClient == nil {
		return fmt.Errorf("unosend client not configured")
	}

	from, err := target.sender()
	if err != nil {
		return err
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
	if target.replyTo != "" {
		payload["reply_to"] = target.replyTo
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal unosend payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.unosendBaseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create unosend request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+target.apiKey)

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

		if reason, permanent := emailResponseReason(resp.StatusCode, string(respBody)); permanent {
			return &entities.EmailDeliveryError{
				Provider:   emailProviderUnosend,
				Recipient:  to,
				StatusCode: resp.StatusCode,
				Reason:     reason,
				Permanent:  true,
			}
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
