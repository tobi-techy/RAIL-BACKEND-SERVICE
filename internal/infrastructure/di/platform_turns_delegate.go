package di

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

// This file routes inbound linked chat to the Python brain's POST /v1/turns
// (schema turns.v1) when MIMI_TURNS_ENABLED is on. The legacy POST
// /api/v1/chat path in platform_python_delegate.go stays until the flag flips;
// after MIRIAM PR #51 deploys that path answers 404, so the two must not both
// be live for long. See the PR notes for the rollout order.
//
// The brain only proposes. Go stores what it proposed (deduplicated on
// intent_id / draft_id) and sends the reply text. Nothing here commits a record
// and nothing moves money.

// Replies the turns path sends when it cannot ask the brain.
const (
	turnsUnavailableReply = "I couldn't check that just now. Please send it again in a minute."
	turnsNotSetUpReply    = "I'm not set up for this shop yet. Please ask your distributor to finish connecting you."
	// turnsUnlinkedGuestReply answers a sender Go cannot tie to a shop. The
	// contract forbids calling the brain with a guessed shop.
	turnsUnlinkedGuestReply = "Hi, this is Mimi. This number isn't connected to a shop yet. Ask your distributor for your invite link and I'll be ready."
	// turnsExpiredConfirmReply answers a tap on a legacy Confirm poll after
	// the flip. The turns brain never moves money, so there is nothing to settle.
	turnsExpiredConfirmReply = "That request has expired. Nothing was moved."
)

// turnsRouting holds what the orchestrator needs for the /v1/turns path.
type turnsRouting struct {
	enabled         bool
	client          *ai.MimiTurnsClient
	tenantID        string
	distributorName string
	proposals       ai.ProposalStore
	now             func() time.Time
}

func (t *turnsRouting) on() bool { return t != nil && t.enabled }

// turnsEnabled reports whether inbound chat goes to /v1/turns.
func (a *orchestratorAdapter) turnsEnabled() bool { return a.turns.on() }

// turnsShopIdentity resolves tenant, shop and sender for a linked Rail user.
//
// Go has no shop or tenancy model yet (it arrives with internal/shops and
// internal/tenancy). Until then the pilot runs one tenant, set by
// MIMI_TURNS_TENANT_ID, and each linked Rail account is one shop ("one Mimi per
// shop"): shop_id and sender.id come from Go's own user id, never from the
// message. An unset tenant means the brain is not called at all.
//
// TODO(mimi-tenancy): replace with a lookup of the shop and member rows for
// this platform identity once those tables exist.
func (a *orchestratorAdapter) turnsShopIdentity(uid uuid.UUID) (tenantID, shopID, senderID string, ok bool) {
	tenantID = strings.TrimSpace(a.turns.tenantID)
	if tenantID == "" || uid == uuid.Nil {
		return "", "", "", false
	}
	return tenantID, "shop_" + uid.String(), uid.String(), true
}

// turnsMessageID returns the idempotency key for this turn. The channel
// message id is the contract's key. When the bridge did not send one, a
// deterministic id is derived from the bridge's turn id so a bridge retry of
// the same batch still maps to one turn; with neither, a random id is used and
// the turn is not deduplicated across retries (logged).
func turnsMessageID(in platform.InboundTurn, plat entities.Platform, threadID, text string, logger *zap.Logger) string {
	if id := strings.TrimSpace(in.MsgID); id != "" {
		return id
	}
	if tid := strings.TrimSpace(in.TurnID); tid != "" {
		sum := sha256.Sum256([]byte(plat.String() + "|" + threadID + "|" + in.SenderID + "|" + tid + "|" + text))
		return "go_" + hex.EncodeToString(sum[:12])
	}
	if logger != nil {
		logger.Warn("turns: inbound message has no msg_id or turn_id; using a random message_id (retries will not dedupe)",
			zap.String("thread_id", threadID))
	}
	return "go_" + uuid.NewString()
}

// buildTurnRequest maps one inbound linked message onto turns.v1.
//
// The text is the shop's own words from the inbound message, not the legacy
// rewrite: the legacy path replaces a photo with a receipt OCR summary and
// folds quoted replies into the text, and either would put figures in front of
// the parser that the shop never wrote.
func buildTurnRequest(in platform.InboundTurn, hasInbound bool, fallbackText, tenantID, shopID, senderID, distributorName string, plat entities.Platform, threadID string, logger *zap.Logger) *ai.TurnRequest {
	text := fallbackText
	if hasInbound {
		text = in.Text
	}
	req := &ai.TurnRequest{
		SchemaVersion:   ai.TurnsSchemaVersion,
		TenantID:        tenantID,
		ShopID:          shopID,
		Sender:          ai.TurnSender{ID: senderID, Handle: in.SenderID, Role: "unknown"},
		Channel:         ai.TurnsChannelWhatsApp,
		DistributorName: distributorName,
	}
	req.MessageID = turnsMessageID(in, plat, threadID, text, logger)
	if !in.ReceivedAt.IsZero() {
		req.SentAt = in.ReceivedAt.Format(time.RFC3339)
	}
	switch {
	case hasInbound && strings.TrimSpace(in.ImageB64) != "":
		mime := strings.TrimSpace(in.ImageMime)
		if mime == "" {
			mime = "image/jpeg"
		}
		req.Media = []ai.TurnMedia{{
			ID:      req.MessageID + ":0",
			Kind:    "image",
			MIME:    mime,
			DataB64: in.ImageB64,
			Caption: truncateRunes(strings.TrimSpace(text), 2000),
		}}
		req.Text = truncateRunes(text, 4000)
	case hasInbound && in.IsVoice && strings.TrimSpace(text) != "":
		// A voice note arrives here already transcribed by Go. Send it as the
		// audio item's transcript so the brain marks its intents
		// source=voice_transcript.
		req.Media = []ai.TurnMedia{{
			ID:         req.MessageID + ":0",
			Kind:       "audio",
			Transcript: truncateRunes(text, 4000),
		}}
	default:
		req.Text = truncateRunes(text, 4000)
	}
	return req
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// handlePlatformMessageTurns is the /v1/turns path for a linked sender.
func (a *orchestratorAdapter) handlePlatformMessageTurns(ctx context.Context, userID, message, threadID string, plat entities.Platform) (*platform.PlatformReply, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, err
	}
	if a.turns.client == nil || !a.turns.client.Configured() {
		a.logger.Error("turns: MIMI_TURNS_ENABLED is on but the turns client is not configured (PYTHON_AGENT_URL and RAIL_SERVICE_KEY)")
		return &platform.PlatformReply{Text: turnsUnavailableReply}, nil
	}
	tenantID, shopID, senderID, ok := a.turnsShopIdentity(uid)
	if !ok {
		a.logger.Error("turns: no tenant configured (MIMI_TURNS_TENANT_ID); not calling the brain with a guessed shop",
			zap.String("user_id", uid.String()))
		return &platform.PlatformReply{Text: turnsNotSetUpReply}, nil
	}

	in, hasInbound := platform.InboundTurnFromContext(ctx)
	req := buildTurnRequest(in, hasInbound, message, tenantID, shopID, senderID, a.turns.distributorName, plat, threadID, a.logger)

	resp, err := a.turns.client.Turn(ctx, req, "")
	if err != nil {
		level := a.logger.Warn
		if errors.Is(err, ai.ErrTurnConflict) || errors.Is(err, ai.ErrTurnRejected) {
			level = a.logger.Error
		}
		level("turns: brain call failed",
			zap.String("user_id", uid.String()),
			zap.String("message_id", req.MessageID),
			zap.Bool("has_media", req.HasMedia()),
			zap.Error(err))
		return &platform.PlatformReply{Text: turnsUnavailableReply}, nil
	}

	now := time.Now
	if a.turns.now != nil {
		now = a.turns.now
	}
	outcome := ai.RecordTurnProposals(ctx, a.turns.proposals, a.logger, req, resp, now())
	a.logger.Info("turns: brain answered",
		zap.String("user_id", uid.String()),
		zap.String("message_id", req.MessageID),
		zap.String("turn_id", resp.TurnID),
		zap.String("trace_id", resp.TraceID),
		zap.String("label", resp.Label),
		zap.Bool("replayed", resp.Replayed),
		zap.Int("intents", len(resp.Intents)),
		zap.Int("drafts", len(resp.Drafts)),
		zap.Int("proposals_stored", outcome.Stored),
		zap.Int("proposals_duplicate", outcome.Duplicates),
		zap.Int("proposals_held_money", outcome.HeldMoney),
		zap.Int("proposals_invalid", outcome.Invalid),
		zap.Strings("flags", resp.Flags))

	if resp.Reply == nil || strings.TrimSpace(resp.Reply.Text) == "" {
		// Nothing to say. A nil reply sends nothing.
		return nil, nil
	}
	return &platform.PlatformReply{Text: strings.TrimSpace(resp.Reply.Text)}, nil
}
