package di

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

// emailOTPSender is the minimal email surface the delegated confirmation flow
// needs. Satisfied by *adapters.EmailService.
type emailOTPSender interface {
	SendCustomEmail(ctx context.Context, to, subject, htmlContent, textContent string) error
}

var sixDigitCode = regexp.MustCompile(`^\d{6}$`)

// pythonDelegated reports whether this adapter is wired to the Python agent.
func (a *orchestratorAdapter) pythonDelegated() bool {
	return a.python != nil && a.otpStore != nil
}

// pythonRole derives the JWT role claim Python's RBAC expects: "verified" for
// KYC-approved users (money tools), otherwise "user" (read/plan only).
func pythonRole(kycStatus string) string {
	if kycStatus == string(entities.KYCStatusApproved) {
		return "verified"
	}
	return "user"
}

// handlePlatformMessagePython is the delegated path for messaging when the
// Python agent is the brain. Flow:
//  1. An inbound 6-digit reply while a confirmation is staged is an OTP attempt.
//  2. Anything else is forwarded to Python for normal agentic conversation.
//  3. When Python asks for confirmation, Go stages an email OTP (never the
//     money path itself) and replies asking for the code.
func (a *orchestratorAdapter) handlePlatformMessagePython(ctx context.Context, userID, platformIdentityID, message, threadID string, plat entities.Platform) (*platform.PlatformReply, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	// Cost-ceiling guard before hitting the LLM, mirrors the Go path.
	if a.orchestrator.IsUserOverCostCeiling(ctx, uid) {
		nextMonth := time.Now().AddDate(0, 1, 0)
		resetDate := time.Date(nextMonth.Year(), nextMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
		daysUntil := int(resetDate.Sub(time.Now()).Hours() / 24)
		msg := fmt.Sprintf("You've hit your monthly AI limit. Miriam will be back on %s (%d days).",
			resetDate.Format("Jan 2"), daysUntil)
		return &platform.PlatformReply{Text: msg}, nil
	}

	// Thread → stable conversation id (keys the OTP record).
	pid, _ := uuid.Parse(platformIdentityID)
	cid, _, err := a.convRepo.GetOrCreatePlatformConversation(ctx, uid, plat.String(), threadID, pid)
	if err != nil {
		return nil, fmt.Errorf("resolve platform conversation: %w", err)
	}

	// Python-side conversation id is namespaced per platform+thread so agent
	// memory never leaks across channels.
	pyConv := fmt.Sprintf("platform:%s:%s", plat.String(), threadID)

	// User profile for the minted token (email + KYC-derived role).
	email := ""
	role := "user"
	if u, uErr := a.userRepo.GetByID(ctx, uid); uErr == nil && u != nil {
		email = u.Email
		role = pythonRole(u.KYCStatus)
	} else if uErr != nil {
		a.logger.Warn("python delegation: user lookup failed", zap.Error(uErr))
	}

	// (1) OTP attempt: 6-digit reply while a confirmation is staged.
	if sixDigitCode.MatchString(message) && a.otpStore.DryPeek(ctx, cid) {
		return a.handleOTPRetry(ctx, uid, email, role, cid, pyConv, message)
	}

	// (2) Forward to the Python agent.
	resp, err := a.python.Chat(ctx, uid, email, role, pyConv, message)
	if err != nil {
		a.logger.Warn("python agent chat failed",
			zap.String("user_id", uid.String()),
			zap.String("conversation", pyConv),
			zap.Error(err))
		return &platform.PlatformReply{
			Text: "I couldn't reach my finance brain just now. Give me a few seconds and ask me again.",
		}, nil
	}

	reply := &platform.PlatformReply{Text: resp.Response}

	// (3) Confirmation requested: stage an email OTP, never execute here.
	if resp.RequiresConfirmation && len(resp.Cards) > 0 {
		card := resp.Cards[0]
		code, otpErr := a.otpStore.Create(ctx, cid, card.Tool, card.Arguments, card.Summary)
		if otpErr != nil {
			a.logger.Warn("could not stage OTP confirmation",
				zap.String("user_id", uid.String()),
				zap.String("tool", card.Tool),
				zap.Error(otpErr))
			return &platform.PlatformReply{
				Text: "I can't complete that right now — something went wrong saving the confirmation. Please try again.",
			}, nil
		}
		if err := a.sendOTPEmail(ctx, email, code); err != nil {
			a.logger.Warn("otp email delivery failed",
				zap.String("user_id", uid.String()),
				zap.Error(err))
		}

		confirmText := card.Summary
		if confirmText == "" {
			confirmText = "that change"
		}
		if reply.Text == "" || reply.Text == resp.Response {
			reply.Text = fmt.Sprintf("I've emailed you a 6-digit code to confirm %s.\n\nReply with the code and I'll take care of it.", confirmText)
		} else {
			reply.Text += fmt.Sprintf("\n\nTo confirm %s, reply with the 6-digit code I just emailed you.", confirmText)
		}
	}

	return reply, nil
}

// handleOTPRetry verifies a submitted code against the staged confirmation and,
// on success, replays the approved action back to Python so it executes (via
// Go's REST) and returns the final response.
func (a *orchestratorAdapter) handleOTPRetry(ctx context.Context, uid uuid.UUID, email, role string, cid uuid.UUID, pyConv, code string) (*platform.PlatformReply, error) {
	action, err := a.otpStore.Verify(ctx, cid, code)
	if err != nil {
		return &platform.PlatformReply{Text: friendlyOTPError(err)}, nil
	}
	if action == nil {
		return &platform.PlatformReply{Text: "That confirmation is no longer valid. Ask me again and I'll set it up fresh."}, nil
	}

	resp, err := a.python.ChatWithApprovedActions(ctx, uid, email, role, pyConv, "complete the confirmed action", []ai.PythonApprovedAction{*action})
	if err != nil {
		a.logger.Warn("python agent approval replay failed",
			zap.String("user_id", uid.String()),
			zap.String("tool", action.Tool),
			zap.Error(err))
		return &platform.PlatformReply{
			Text: "Code verified. I hit an error carrying it out — give me a second and ask me to finish.",
		}, nil
	}
	reply := &platform.PlatformReply{Text: resp.Response, Effect: platform.EffectCelebration}

	// Defensive: if the agent asks for yet another confirmation, fail closed.
	if resp.RequiresConfirmation && len(resp.Cards) > 0 {
		return &platform.PlatformReply{
			Text:   "That step is confirmed and the request was sent. If anything else needs your sign-off, you'll get a new code by email.",
			Effect: platform.EffectCelebration,
		}, nil
	}
	return reply, nil
}

// sendOTPEmail delivers the confirmation code to the user's inbox.
func (a *orchestratorAdapter) sendOTPEmail(ctx context.Context, email, code string) error {
	if a.emailSvc == nil || email == "" {
		return fmt.Errorf("no email channel configured for otp delivery")
	}
	subject := "Your MIRIAM confirmation code"
	text := fmt.Sprintf("Your confirmation code is: %s\n\nThis code expires in 10 minutes. If you didn't request this, you can safely ignore this email.\n\n— MIRIAM", code)
	html := fmt.Sprintf(`<div style="font-family:-apple-system,Segoe UI,sans-serif;max-width:480px;margin:0 auto;padding:24px;">
<p style="font-size:15px;color:#343433;">Here's your confirmation code:</p>
<div style="background:#f2f0ed;border-radius:12px;padding:20px 24px;margin:16px 0;text-align:center;font-size:32px;letter-spacing:6px;font-weight:700;color:#0a0a0a;">%s</div>
<p style="font-size:13px;color:#737370;">It expires in 10 minutes. If you didn't ask for this, you can safely ignore this email.</p>
</div>`, code)
	return a.emailSvc.SendCustomEmail(ctx, email, subject, html, text)
}

// friendlyOTPError maps OtpStore verification errors to a helpful channel reply.
func friendlyOTPError(err error) string {
	if err == nil {
		return "That confirmation is no longer valid. Ask me again and I'll set it up fresh."
	}
	switch err.Error() {
	case "invalid_confirmation: That code has expired. Ask me again and I'll send a fresh one.":
		return "That code has expired. Ask me again and I'll email you a fresh one."
	case "invalid_confirmation: Too many wrong attempts. Ask me again and I'll send a fresh code.":
		return "Too many wrong attempts, so I've cancelled it for safety. Ask me again and I'll send a fresh code."
	default:
		return "That code didn't match. Reply with the code I emailed you, or ask me to resend it."
	}
}

// pythonOTPConfirmShortcut responds to a confirm-vote (YES/NO/poll/tapback) when
// a Python-staged OTP confirmation is pending: a bare vote can't pass step-up,
// so ask for the code.
func (a *orchestratorAdapter) confirmVoteWhenOTPPending(ctx context.Context, uid, cid uuid.UUID) (*platform.PlatformReply, bool) {
	if !a.pythonDelegated() || !a.otpStore.DryPeek(ctx, cid) {
		return nil, false
	}
	return &platform.PlatformReply{
		Text: "Reply with the 6-digit code I emailed you to confirm — that's how I verify it's you.",
	}, true
}

// cancelOTPWhenPresent discards a staged OTP confirmation on cancel.
func (a *orchestratorAdapter) cancelOTPWhenPresent(ctx context.Context, cid uuid.UUID) {
	if a.pythonDelegated() {
		a.otpStore.Delete(ctx, cid)
	}
}