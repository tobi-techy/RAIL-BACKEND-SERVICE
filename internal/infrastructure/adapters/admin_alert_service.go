package adapters

import (
	"context"
	"fmt"
	"html"
	"runtime/debug"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/withdrawal"
	"go.uber.org/zap"
)

// AdminAlertService sends detailed error emails to the admin/ops address.
type AdminAlertService struct {
	emailService *EmailService
	adminEmail   string
	logger       *zap.Logger
}

// NewAdminAlertService creates a new admin alert service.
// adminEmail is the recipient address for error notifications.
func NewAdminAlertService(emailService *EmailService, adminEmail string, logger *zap.Logger) *AdminAlertService {
	return &AdminAlertService{
		emailService: emailService,
		adminEmail:   adminEmail,
		logger:       logger,
	}
}

// SendErrorAlert sends a detailed HTML email to the admin with the full error context.
// It is safe to call in a goroutine — it has its own timeout and error handling.
func (a *AdminAlertService) SendErrorAlert(ctx context.Context, payload withdrawal.AdminErrorPayload) {
	if a == nil || a.emailService == nil || a.adminEmail == "" {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.logger.Error("admin alert goroutine panicked", zap.Any("panic", r))
			}
		}()

		sendCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		subject := fmt.Sprintf("[Rail Alert] %s failed", payload.Operation)
		if payload.UserID != "" {
			subject = fmt.Sprintf("[Rail Alert] %s failed for user %s", payload.Operation, payload.UserID)
		}

		htmlContent := a.buildHTML(payload)
		textContent := a.buildText(payload)

		if err := a.emailService.SendCustomEmail(sendCtx, a.adminEmail, subject, htmlContent, textContent); err != nil {
			a.logger.Error("failed to send admin error alert email",
				zap.Error(err),
				zap.String("operation", payload.Operation),
				zap.String("user_id", payload.UserID))
		}
	}()
}

func (a *AdminAlertService) buildHTML(p withdrawal.AdminErrorPayload) string {
	errMsg := ""
	if p.Error != nil {
		errMsg = html.EscapeString(p.Error.Error())
	}

	stack := ""
	if len(p.PanicStack) > 0 {
		stack = fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#1a1a1a;border-radius:8px;margin:16px 0;">
<tr><td style="padding:16px;">
  <pre style="font-family:'SF Mono',Consolas,monospace;font-size:12px;color:#e0e0e0;margin:0;white-space:pre-wrap;word-break:break-all;">%s</pre>
</td></tr></table>`, html.EscapeString(string(p.PanicStack)))
	}

	extraRows := ""
	for k, v := range p.ExtraFields {
		extraRows += fmt.Sprintf(`<tr><td style="padding:6px 12px;font-family:-apple-system,SF Pro Text,sans-serif;font-size:13px;color:#848281;font-weight:600;white-space:nowrap;">%s</td><td style="padding:6px 12px;font-family:-apple-system,SF Pro Text,sans-serif;font-size:13px;color:#343433;">%s</td></tr>`,
			html.EscapeString(k), html.EscapeString(v))
	}
	extraTable := ""
	if extraRows != "" {
		extraTable = fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:8px;margin:16px 0;">
%s</table>`, extraRows)
	}

	innerHTML := renderHeader() +
		renderHeading(fmt.Sprintf("%s failed", html.EscapeString(p.Operation))) +
		renderBody(fmt.Sprintf("A <strong>%s</strong> operation failed and needs attention.", html.EscapeString(p.Operation))) +
		fmt.Sprintf(`<table width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f2f0ed;border-radius:8px;margin:16px 0;">
<tr><td style="padding:12px 16px;">
  <p style="font-family:-apple-system,SF Pro Text,sans-serif;font-size:13px;color:#848281;margin:0 0 4px 0;">Error</p>
  <p style="font-family:'SF Mono',monospace;font-size:14px;color:#d32f2f;margin:0;word-break:break-all;">%s</p>
</td></tr></table>`, errMsg) +
		extraTable +
		stack +
		renderSmallBody(fmt.Sprintf("Time: %s UTC", time.Now().UTC().Format("2006-01-02 15:04:05")))

	return renderBaseTemplate(innerHTML)
}

func (a *AdminAlertService) buildText(p withdrawal.AdminErrorPayload) string {
	text := fmt.Sprintf("RAIL ERROR ALERT\n\nOperation: %s\n", p.Operation)
	if p.UserID != "" {
		text += fmt.Sprintf("User ID:   %s\n", p.UserID)
	}
	if p.Error != nil {
		text += fmt.Sprintf("Error:     %s\n", p.Error.Error())
	}
	for k, v := range p.ExtraFields {
		text += fmt.Sprintf("%s: %s\n", k, v)
	}
	if len(p.PanicStack) > 0 {
		text += fmt.Sprintf("\nStack Trace:\n%s\n", string(p.PanicStack))
	}
	text += fmt.Sprintf("\nTime: %s UTC\n", time.Now().UTC().Format("2006-01-02 15:04:05"))
	return text
}

// CaptureStack is a helper to grab the current goroutine stack for passing to AdminErrorPayload.
func CaptureStack() []byte {
	return debug.Stack()
}
