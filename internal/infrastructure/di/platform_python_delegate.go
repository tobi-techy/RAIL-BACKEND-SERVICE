package di

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/pkg/metrics"
	"go.uber.org/zap"
)

// pythonDelegated reports whether this adapter is wired to the Python agent.
func (a *orchestratorAdapter) pythonDelegated() bool {
	return a.python != nil
}

// pythonRole derives the JWT role claim Python's RBAC expects: "verified" grants
// the money tools, "user" is read/plan only.
//
// This keyed on KYC approval alone, which meant a chat-onboarded account — email
// verified, Tier 1, kyc_status non_kyc — could not move money through Miriam at
// all, so a chat signup could never be the equal of an app signup. A proven
// email on an active account is the identity floor. KYC approval remains an
// additional grant so existing approved users are unaffected.
//
// Note this widens who may move money. The real controls are the per-tier limits
// (enforced Go-side), the ledger's own ceilings in Python's hands/limits.py, and
// the confirm_id step-up — not this claim.
func pythonRole(kycStatus string, emailVerified, isActive bool) string {
	if !isActive {
		return "user"
	}
	if emailVerified || kycStatus == string(entities.KYCStatusApproved) {
		return "verified"
	}
	return "user"
}

// pythonTokenClaims loads the email + KYC-derived role used to mint the JWT
// Python's RBAC expects. Falls back to the base "user" role on lookup failure,
// which fails closed: an unknown caller never gets the money role.
func (a *orchestratorAdapter) pythonTokenClaims(ctx context.Context, uid uuid.UUID) (email, role string) {
	role = "user"
	if a.userRepo == nil {
		// Unwired. Answer with no email and the read-only role rather than
		// dereferencing a nil repository.
		a.logger.Warn("python delegation: no user repository wired")
		return "", role
	}
	u, err := a.userRepo.GetByID(ctx, uid)
	if err != nil {
		a.logger.Warn("python delegation: user lookup failed", zap.Error(err))
		return "", role
	}
	if u == nil {
		return "", role
	}
	return u.Email, pythonRole(u.KYCStatus, u.EmailVerified, u.IsActive)
}

// costCeilingMessage returns the monthly-AI-limit reply when the user is over
// the ceiling, mirroring the Go path. The bool reports whether the ceiling hit.
func (a *orchestratorAdapter) costCeilingMessage(ctx context.Context, uid uuid.UUID) (*platform.PlatformReply, bool) {
	if a.orchestrator == nil {
		return nil, false
	}
	if !a.orchestrator.IsUserOverCostCeiling(ctx, uid) {
		return nil, false
	}
	nextMonth := time.Now().AddDate(0, 1, 0)
	resetDate := time.Date(nextMonth.Year(), nextMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	daysUntil := int(resetDate.Sub(time.Now()).Hours() / 24)
	return &platform.PlatformReply{
		Text: fmt.Sprintf("You've hit your monthly AI limit. Miriam will be back on %s (%d days).",
			resetDate.Format("Jan 2"), daysUntil),
	}, true
}

// mapPythonChatReply projects a Python chat response onto a PlatformReply,
// carrying any interactive poll through so the processor renders it natively.
// Reactions, wrapper bubbles, and shares ride along too, all validated: the
// reaction must be on the tapback whitelist, the share URL must pass the host
// allowlist, and extra bubbles are capped.
//
// A confirm_id is deliberately NOT mapped here: the caller has to stage it
// against the thread first (see stageConfirmIfAsked), because a reply that asks
// a question without a stored id would render a Confirm poll nobody can settle.
func mapPythonChatReply(resp *ai.PythonChatResponse) *platform.PlatformReply {
	reply := &platform.PlatformReply{Text: resp.Response}
	if resp.Poll != nil && len(resp.Poll.Options) > 0 {
		title := strings.TrimSpace(resp.Poll.Title)
		if title == "" {
			title = strings.TrimSpace(resp.Response)
		}
		if title != "" {
			reply.Poll = &platform.PollRequest{Title: title, Options: resp.Poll.Options}
		}
		// Ensure a poll is never bare: if the response text is the same as the
		// title, the processor will still send it as a lead-in bubble before the
		// poll (pollWords now always carries leadIn when text present).
		if strings.TrimSpace(reply.Text) == "" && title != "" {
			reply.Text = title
		}
	}
	if emoji := strings.TrimSpace(resp.Reaction); emoji != "" && platform.ValidReaction(emoji) {
		reply.Reaction = emoji
	}
	for i, m := range resp.Messages {
		if i >= platform.MaxExtraMessages {
			break
		}
		if text := strings.TrimSpace(m); text != "" {
			reply.ExtraTexts = append(reply.ExtraTexts, text)
		}
	}
	if resp.Share != nil {
		if u := strings.TrimSpace(resp.Share.URL); u != "" && shareHostAllowed(u) {
			reply.Share = &platform.ShareRequest{
				Kind:  strings.TrimSpace(resp.Share.Kind),
				Title: strings.TrimSpace(resp.Share.Title),
				URL:   u,
			}
		}
	}
	return reply
}

// shareAllowlist stores the decoded MIRIAM_SHARE_ALLOWED_HOSTS allowlist (read
// once). An empty allowlist admits nothing — a share URL must be on the list to
// reach a user on any path, guest or linked.
var (
	shareAllowlistOnce sync.Once
	shareAllowlist     map[string]bool
)

// shareHostAllowed reports whether a share URL's host is on the allowlist. The
// var is the same one the guest onboarder uses, so both delivery paths agree.
func shareHostAllowed(raw string) bool {
	shareAllowlistOnce.Do(func() {
		shareAllowlist = map[string]bool{}
		for _, h := range strings.Split(os.Getenv("MIRIAM_SHARE_ALLOWED_HOSTS"), ",") {
			h = strings.ToLower(strings.TrimSpace(h))
			if h == "" {
				continue
			}
			shareAllowlist[strings.TrimPrefix(h, "www.")] = true
		}
	})
	if len(shareAllowlist) == 0 {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return shareAllowlist[strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")]
}

// HandlePlatformPollVote implements platform.PollVoteOrchestrator: a poll vote
// with no pending action is forwarded to the Python agent when delegated so
// conversational onboarding can consume answer selections. handled=false keeps
// the legacy stray-vote drop behavior (for non-delegated setups and any
// response that did not originate from the onboarding brain).
func (a *orchestratorAdapter) HandlePlatformPollVote(ctx context.Context, userID, platformIdentityID, threadID string, plat entities.Platform, optionText, pollTitle string) (*platform.PlatformReply, bool, error) {
	if !a.pythonDelegated() {
		return nil, false, nil
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, false, fmt.Errorf("parse user id: %w", err)
	}
	if reply, over := a.costCeilingMessage(ctx, uid); over {
		return reply, true, nil
	}
	pid, _ := uuid.Parse(platformIdentityID)
	if _, _, err := a.convRepo.GetOrCreatePlatformConversation(ctx, uid, plat.String(), threadID, pid); err != nil {
		return nil, false, fmt.Errorf("resolve platform conversation: %w", err)
	}
	email, role := a.pythonTokenClaims(ctx, uid)
	pyConv := fmt.Sprintf("platform:%s:%s", plat.String(), threadID)

	resp, err := a.python.ChatPollVote(ctx, uid, email, role, pyConv, optionText, pollTitle)
	if err != nil {
		a.logger.Warn("python poll vote failed",
			zap.String("user_id", uid.String()),
			zap.String("thread_id", threadID),
			zap.String("option_text", optionText),
			zap.Error(err))
		return nil, false, err
	}
	if voteReplyIsDroppable(resp) {
		return nil, false, nil
	}
	return mapPythonChatReply(resp), true, nil
}

// voteReplyIsDroppable reports whether a Python vote response should be
// silently swallowed: an onboarding turn always carries its marker, but when
// the marker is absent and the payload is empty the tap resolved to a decline
// with nothing to surface. Any surviving content is worth delivering so a poll
// tap never reads as a dead tap.
func voteReplyIsDroppable(resp *ai.PythonChatResponse) bool {
	return resp.Onboarding == nil && isEmptyVoteReply(resp)
}

// isEmptyVoteReply reports whether a Python chat response carries nothing
// deliverable (a content-only check; marker presence is the caller's concern).
// A reaction only counts as content when it would survive the tapback
// whitelist in mapPythonChatReply — off-whitelist emojis are stripped there
// and must not prevent the response from being dropped.
func isEmptyVoteReply(resp *ai.PythonChatResponse) bool {
	return strings.TrimSpace(resp.Response) == "" &&
		resp.Poll == nil &&
		len(resp.Messages) == 0 &&
		!isDeliverableReaction(resp.Reaction) &&
		resp.Share == nil
}

// isDeliverableReaction reports whether a reaction would survive
// mapPythonChatReply and actually be surfaced as a tapback.
func isDeliverableReaction(emoji string) bool {
	trimmed := strings.TrimSpace(emoji)
	return trimmed != "" && platform.ValidReaction(trimmed)
}

// HandlePlatformDocument implements platform.DocumentOrchestrator: a linked
// statement scan is handed to the Python agent so onboarding can ground its
// plan on it immediately. handled=false tells the processor to fall back to the
// durable EnqueueLinked pipeline.
// WantsStatementScan gates the processor's synchronous statement-scan fast
// path so linked statements only pay the blocking 25s scan when this adapter
// will actually consume it (python-delegated). All other setups keep the cheap
// durable-pipeline-only path.
func (a *orchestratorAdapter) WantsStatementScan(_ context.Context, _ uuid.UUID) bool {
	return a.pythonDelegated()
}

func (a *orchestratorAdapter) HandlePlatformDocument(ctx context.Context, userID, platformIdentityID, threadID string, plat entities.Platform, scan platform.StatementScan) (*platform.PlatformReply, bool, error) {
	if !a.pythonDelegated() {
		return nil, false, nil
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, false, fmt.Errorf("parse user id: %w", err)
	}
	if reply, over := a.costCeilingMessage(ctx, uid); over {
		return reply, true, nil
	}
	pid, _ := uuid.Parse(platformIdentityID)
	if _, _, err := a.convRepo.GetOrCreatePlatformConversation(ctx, uid, plat.String(), threadID, pid); err != nil {
		return nil, false, fmt.Errorf("resolve platform conversation: %w", err)
	}
	email, role := a.pythonTokenClaims(ctx, uid)
	pyConv := fmt.Sprintf("platform:%s:%s", plat.String(), threadID)

	doc := ai.PythonChatDocument{Name: "bank_statement.pdf", MIME: "application/pdf", Summary: scan.Summary}
	message := "I sent my bank statement."
	if summary := strings.TrimSpace(scan.Summary); summary != "" {
		message += "\n\n[statement scan]\n" + summary
	}
	resp, err := a.python.ChatWithDocument(ctx, uid, email, role, pyConv, message, doc)
	if err != nil {
		a.logger.Warn("python document chat failed",
			zap.String("user_id", uid.String()),
			zap.String("thread_id", threadID),
			zap.Error(err))
		return nil, false, err
	}
	reply := mapPythonChatReply(resp)
	if reply == nil || (strings.TrimSpace(reply.Text) == "" && reply.Poll == nil && len(reply.ExtraTexts) == 0) {
		return nil, false, nil
	}
	return reply, true, nil
}

// handlePlatformMessagePython is the delegated path for messaging when the
// Python agent is the brain. Flow:
//  1. Anything the user sends is forwarded to Python for normal agentic
//     conversation. A 6-digit reply is nothing special any more.
//  2. When Python asks for confirmation it returns a confirm_id that its ledger
//     issued. Go remembers it against the thread and renders a Confirm/Cancel
//     poll; the tap comes back to ConfirmPlatformAction, which names the id.
//
// Go never holds the money path: it relays the id, and the ledger decides.
func (a *orchestratorAdapter) handlePlatformMessagePython(ctx context.Context, userID, platformIdentityID, message, threadID string, plat entities.Platform) (*platform.PlatformReply, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}

	// Cost-ceiling guard before hitting the LLM, mirrors the Go path.
	if reply, over := a.costCeilingMessage(ctx, uid); over {
		return reply, nil
	}

	// Thread → stable conversation id (keys the staged confirm).
	pid, _ := uuid.Parse(platformIdentityID)
	cid, _, err := a.convRepo.GetOrCreatePlatformConversation(ctx, uid, plat.String(), threadID, pid)
	if err != nil {
		return nil, fmt.Errorf("resolve platform conversation: %w", err)
	}

	// Python-side conversation id is namespaced per platform+thread so agent
	// memory never leaks across channels.
	pyConv := fmt.Sprintf("platform:%s:%s", plat.String(), threadID)

	// User profile for the minted token (email + KYC-derived role).
	email, role := a.pythonTokenClaims(ctx, uid)

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

	reply := mapPythonChatReply(resp)
	a.stageConfirmIfAsked(ctx, cid, resp, reply)
	return reply, nil
}

// stageConfirmIfAsked remembers a challenge Python just raised and turns the
// reply into a Confirm/Cancel poll.
//
// The poll resolves by thread, so the id has to be stored before the poll is
// rendered; the tap then arrives at ConfirmPlatformAction, which reads it back.
// With no poll to render the reply is left alone: an answer that merely mentions
// a confirm_id (a refusal, say) should not ask the user to tap anything.
//
// One confirm_id per thread: if a challenge is already open, the new one is
// rejected — the reply tells the user to finish or cancel the first instead of
// superseding it.
func (a *orchestratorAdapter) stageConfirmIfAsked(ctx context.Context, cid uuid.UUID, resp *ai.PythonChatResponse, reply *platform.PlatformReply) {
	confirmID := strings.TrimSpace(resp.ConfirmID)
	if confirmID == "" {
		return
	}
	if a.confirmStore == nil {
		// Without a store a Confirm poll could never be settled, so don't draw
		// one: the text reply still carries the verdict.
		a.logger.Warn("python asked for a confirmation with no confirm store wired",
			zap.String("conversation", cid.String()),
			zap.String("confirm_id", confirmID))
		return
	}
	// PutIfAbsent rejects a second live challenge on the same thread instead
	// of silently superseding the first.
	staged, err := a.confirmStore.PutIfAbsent(ctx, cid, confirmID)
	if err != nil {
		a.logger.Warn("could not stage python confirmation",
			zap.String("conversation", cid.String()),
			zap.String("confirm_id", confirmID),
			zap.Error(err))
		return
	}
	if !staged {
		// A challenge is already open on this thread — do not overwrite it.
		// Tell the user to settle the first one.
		reply.Confirm = nil
		reply.Text = "You already have a pending confirmation. Finish or cancel that one first."
		return
	}
	summary := buildConfirmSummary(resp, reply.Text)
	reply.Confirm = &platform.ConfirmRequest{Summary: summary}
}

// buildConfirmSummary prefers the structured decision/receipt data Python
// returned, falling back to the prose reply when it is absent. A money summary
// should read "Send ₦X to NAME from spendable?" even when Voice prose is terse.
func buildConfirmSummary(resp *ai.PythonChatResponse, prose string) string {
	// Helper: pull a string field from a generic map.
	field := func(m map[string]interface{}, k string) string {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
		return ""
	}

	// Prefer the decision object: it carries the policy verdict with amount
	// and counterparty before the movement.
	if resp.Decision != nil {
		amount := field(resp.Decision, "amount")
		to := field(resp.Decision, "to")
		if to == "" {
			to = field(resp.Decision, "recipient")
		}
		if amount != "" {
			if to != "" {
				return fmt.Sprintf("Send %s to %s?", amount, to)
			}
			return fmt.Sprintf("Send %s?", amount)
		}
	}

	// Fall back to the receipt object: it carries the same after the fact.
	if resp.Receipt != nil {
		amount := field(resp.Receipt, "amount")
		to := field(resp.Receipt, "to")
		if to == "" {
			to = field(resp.Receipt, "recipient")
		}
		if amount != "" {
			if to != "" {
				return fmt.Sprintf("Send %s to %s?", amount, to)
			}
			return fmt.Sprintf("Send %s?", amount)
		}
	}

	// Last resort: the prose the agent generated.
	if s := strings.TrimSpace(prose); s != "" {
		return s
	}
	return "Shall I go ahead?"
}

// settlePythonConfirm sends the user's answer to Python by confirm_id and
// returns the reply it writes.
//
// `yes=false` is a decline: Python closes the challenge and writes a receipt
// saying nothing moved. Either way the id is dropped from the thread here, so a
// second tap cannot be sent at a challenge that is already settled.
func (a *orchestratorAdapter) settlePythonConfirm(ctx context.Context, uid uuid.UUID, cid uuid.UUID, threadID string, plat entities.Platform, confirmID string, yes bool) (*platform.PlatformReply, error) {
	a.confirmStore.Delete(ctx, cid)
	email, role := a.pythonTokenClaims(ctx, uid)
	pyConv := fmt.Sprintf("platform:%s:%s", plat.String(), threadID)

	resp, err := a.python.ChatConfirm(ctx, uid, email, role, pyConv, confirmID, yes)
	if err != nil {
		a.logger.Warn("python confirm failed",
			zap.String("user_id", uid.String()),
			zap.String("confirm_id", confirmID),
			zap.Bool("yes", yes),
			zap.Error(err))
		if metrics.Business != nil {
			metrics.Business.ConfirmSettleFail.Inc()
		}
		return &platform.PlatformReply{
			Text: "I hit an error carrying that out — give me a second and ask me to finish.",
		}, nil
	}
	reply := mapPythonChatReply(resp)
	if yes {
		reply.Effect = platform.EffectCelebration
	}
	// A settled challenge should not raise a fresh one silently: stage it, so
	// the next step still has a tap to make.
	a.stageConfirmIfAsked(ctx, cid, resp, reply)
	return reply, nil
}

// pendingPythonConfirm reports the open confirmation staged for a thread.
func (a *orchestratorAdapter) pendingPythonConfirm(ctx context.Context, cid uuid.UUID) (string, bool) {
	if !a.pythonDelegated() || a.confirmStore == nil {
		return "", false
	}
	return a.confirmStore.Peek(ctx, cid)
}

// HasPendingPythonConfirm implements platform.PythonConfirmOrchestrator so the
// processor can tell a Python money confirm from a Go-native approve and
// suppress typed YES/NO on poll-less channels.
func (a *orchestratorAdapter) HasPendingPythonConfirm(ctx context.Context, userID, platformIdentityID, threadID string, plat entities.Platform) bool {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false
	}
	pid, _ := uuid.Parse(platformIdentityID)
	cid, _, err := a.convRepo.GetOrCreatePlatformConversation(ctx, uid, plat.String(), threadID, pid)
	if err != nil {
		return false
	}
	_, ok := a.pendingPythonConfirm(ctx, cid)
	return ok
}
