package di

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

// pythonGuestCompleterAdapter serves the unlinked/guest conversation through the
// Python MIRIAM agent when delegation is wired, so the very first text from an
// unlinked sender is already answered by MIRIAM (Python) instead of the Go
// provider.
//
// Go's deterministic executor still owns every identity effect: the adapter
// only supplies words, and maps two agent markers into guest tool calls the
// executor understands — an onboarding poll becomes send_poll, and a completed
// interview becomes start_signup (the guest now wants their money working, which
// needs the phone/OTP/consent funnel). Cards (money confirmations) are never
// surfaced for a guest: role "guest" blocks money tools in the agent, and even
// if a card slipped through we drop it rather than stage a code no one can
// receive.
//
// A tap on one of Miriam's polls is forwarded as a vote (is_poll_vote + the
// question it answered) so a bare option title reaches the interview as the
// answer it is, not as fresh chat.
//
// Each guest gets a stable synthetic identity with per-sender unique username
// and email (Python's users table enforces uniqueness on both) so interview
// state survives across turns without ever colliding with a real user.
//
// When Python can't answer no Go brain takes over: the adapter returns the
// failure, the guest brain retries once, and the bundling onboarder requeues
// (or, out of redeliveries, answers with its deterministic scripted reply). A
// texted conversation always comes from MIRIAM (Python) — never silently from a
// different brain. A single longer attempt (13s) replaces the default two short
// ones so the Python agent loop fits inside the bridge deadline.
type pythonGuestCompleterAdapter struct {
	python *ai.PythonAgentClient
	logger *zap.Logger
}

// guestTurnTimeout is the whole-turn budget a Python-backed guest completion may
// use. Keeps one agent loop (interview/plan/LLM + HTTP) inside the bridge's 15s
// inbound deadline while leaving headroom for the executor and the response.
const guestTurnTimeout = 13 * time.Second

func (a *pythonGuestCompleterAdapter) CompletionTimeout() time.Duration { return guestTurnTimeout }

func (a *pythonGuestCompleterAdapter) CompleteGuest(ctx context.Context, systemPrompt string, messages []platform.GuestMessage, tools []platform.GuestToolDef) (*platform.GuestResult, error) {
	if a.python == nil {
		return nil, fmt.Errorf("python guest completer has no python backend wired")
	}
	sender, ok := platform.GuestSenderFromContext(ctx)
	if !ok {
		a.logger.Warn("python guest completer used outside an onboarding turn")
		return nil, fmt.Errorf("python guest completer used outside an onboarding turn")
	}
	text := lastGuestUserText(messages)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("python guest completer received no user text")
	}

	uid := guestSyntheticID(sender)
	conversationID := fmt.Sprintf("platform:%s:%s", sender.Platform.String(), sender.ThreadID)
	vote := platform.GuestVoteFromContext(ctx)
	var resp *ai.PythonChatResponse
	var err error
	if vote.IsPollVote {
		// The guest tapped an option rather than typing: send it as a vote with
		// the question it answered so the interview resolves it against that poll
		// instead of treating the option title as a brand-new message (which made
		// Miriam re-ask her own question with a fresh poll).
		resp, err = a.python.ChatAsGuestPollVote(ctx, uid, guestUsername(uid), guestEmail(uid), "guest",
			conversationID, text, vote.PollTitle)
	} else {
		resp, err = a.python.ChatAsGuest(ctx, uid, guestUsername(uid), guestEmail(uid), "guest", conversationID, text)
	}
	if err != nil {
		a.logger.Warn("python guest turn failed; no Go fallback — erroring so the turn is retried or requeued",
			zap.String("sender", sender.SenderID),
			zap.String("platform", sender.Platform.String()),
			zap.Bool("is_poll_vote", vote.IsPollVote),
			zap.Error(err))
		return nil, err
	}

	res := &platform.GuestResult{Text: strings.TrimSpace(resp.Response)}
	if len(resp.Cards) > 0 {
		a.logger.Warn("python guest turn returned confirmation cards; ignoring them",
			zap.String("sender", sender.SenderID),
			zap.Int("cards", len(resp.Cards)))
		resp.RequiresConfirmation = false
	}

	// "Picked from the Go guest brain": the agent recorded the person's name.
	// Surface it as note_detail so the deterministic executor tracks it in
	// guest state (used for a warmer signup and never re-asking).
	if name := strings.TrimSpace(resp.Name); name != "" {
		res.ToolCalls = append(res.ToolCalls, platform.GuestToolCall{
			Name:      "note_detail",
			Arguments: map[string]interface{}{"field": "first_name", "value": name},
		})
	}

	// A completed interview means the guest has made a plan decision. Only when
	// they explicitly consented to setting it up as standing rules (automated)
	// do we hand back into Go's phone/OTP/consent funnel so the rules become
	// real on a real account. A declined setup (draft saved for later) stays
	// conversational — no phone, no account, no push.
	if resp.Onboarding != nil && resp.Onboarding.Completed {
		if resp.Onboarding.Automated {
			res.ToolCalls = append(res.ToolCalls, platform.GuestToolCall{
				Name:      "start_signup",
				Arguments: map[string]interface{}{"reason": "consented to setting up their financial plan"},
			})
		}
		return res, nil
	}

	// An open interview question surfaces as a tappable poll the executor renders.
	if resp.Poll != nil && len(resp.Poll.Options) > 1 {
		opts := resp.Poll.Options
		if len(opts) > 4 {
			opts = opts[:4]
		}
		title := strings.TrimSpace(resp.Poll.Title)
		if title == "" {
			title = strings.TrimSpace(res.Text)
		}
		if title == "" {
			a.logger.Warn("python guest turn returned poll with empty title; dropping poll",
				zap.String("sender", sender.SenderID))
		} else {
			// A poll must never be the entire reply: the person should always see
			// Miriam's words as a message too. The executor renders reply text next
			// to the poll, so guarantee text exists (guest brain otherwise treats an
			// empty text + tools turn as a no-reply and retries/falls back).
			if strings.TrimSpace(res.Text) == "" {
				res.Text = title
			}
			res.ToolCalls = append(res.ToolCalls, platform.GuestToolCall{
				Name: "send_poll",
				Arguments: map[string]interface{}{
					"question": title,
					"options":  opts,
				},
			})
		}
	}

	// Chatty turn: pass through up to two extra short bubbles, one tapback
	// reaction, and one share — all re-validated by the deterministic executor
	// (whitelist, budget, URL allowlist) before anything is sent.
	for i, m := range resp.Messages {
		if i >= platform.MaxExtraMessages {
			break
		}
		text := strings.TrimSpace(m)
		if text == "" {
			continue
		}
		res.ToolCalls = append(res.ToolCalls, platform.GuestToolCall{
			Name:      "send_message",
			Arguments: map[string]interface{}{"text": text},
		})
	}
	if emoji := strings.TrimSpace(resp.Reaction); emoji != "" {
		res.ToolCalls = append(res.ToolCalls, platform.GuestToolCall{
			Name:      "send_reaction",
			Arguments: map[string]interface{}{"emoji": emoji},
		})
	}
	if resp.Share != nil && strings.TrimSpace(resp.Share.URL) != "" {
		res.ToolCalls = append(res.ToolCalls, platform.GuestToolCall{
			Name: "share_artifact",
			Arguments: map[string]interface{}{
				"kind":  resp.Share.Kind,
				"title": resp.Share.Title,
				"url":   resp.Share.URL,
			},
		})
	}
	return res, nil
}

// guestSyntheticID derives a stable UUID for an unlinked sender so Python memory
// and interview state key consistently across turns without any Go user row.
func guestSyntheticID(s platform.GuestSender) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("guest:"+s.Platform.String()+":"+s.SenderID))
}

// guestHandle is a short stable hex fingerprint of the synthetic identity used
// to build unique-per-sender username/email claims.
func guestHandle(id uuid.UUID) string {
	return strings.ReplaceAll(id.String(), "-", "")[:12]
}

func guestUsername(id uuid.UUID) string { return "guest_" + guestHandle(id) }

func guestEmail(id uuid.UUID) string { return "guest_" + guestHandle(id) + "@miriam.invalid" }

func lastGuestUserText(messages []platform.GuestMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}
