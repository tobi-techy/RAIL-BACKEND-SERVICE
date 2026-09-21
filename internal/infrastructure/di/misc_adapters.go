package di

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/domain/services/gameplay"
	"github.com/rail-service/rail_service/internal/domain/services/growthengine"
	"github.com/rail-service/rail_service/internal/domain/services/passcode"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	supermemoryclient "github.com/rail-service/rail_service/internal/infrastructure/supermemory"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// passcodeStepUpAdapter wraps *passcode.Service to satisfy
// aiservice.StepUpVerifier without coupling the AI service package to the
// passcode package.
type passcodeStepUpAdapter struct {
	svc *passcode.Service
}

func (a *passcodeStepUpAdapter) VerifyStepUp(ctx context.Context, userID uuid.UUID, token string) (bool, error) {
	return a.svc.ValidateSession(ctx, userID, token)
}

// gameplayProviderAdapter wraps the gameplay streak/challenge/achievement
// services to satisfy aiservice.GameplayProvider so Miriam can reference
// gameplay data conversationally.
type gameplayProviderAdapter struct {
	streaks      *gameplay.StreakService
	challenges   *gameplay.ChallengeService
	achievements *gameplay.AchievementService
}

func (a *gameplayProviderAdapter) GetUserStreaks(ctx context.Context, userID uuid.UUID) ([]*entities.UserStreak, error) {
	return a.streaks.GetUserStreaks(ctx, userID)
}

func (a *gameplayProviderAdapter) GetActiveChallenges(ctx context.Context, userID uuid.UUID) ([]*entities.UserChallenge, error) {
	return a.challenges.GetActiveChallenges(ctx, userID)
}

func (a *gameplayProviderAdapter) GetUserAchievements(ctx context.Context, userID uuid.UUID) ([]*entities.Achievement, []*entities.UserAchievement, error) {
	return a.achievements.GetUserAchievements(ctx, userID)
}

type growthBatchEmailAdapter struct {
	email *adapters.EmailService
}

func (a *growthBatchEmailAdapter) SendBatchEmails(ctx context.Context, emails []growthengine.BatchEmailItem) error {
	batch := make([]adapters.BatchEmail, len(emails))
	for i, e := range emails {
		batch[i] = adapters.BatchEmail{
			From:    e.From,
			To:      e.To,
			Subject: e.Subject,
			HTML:    e.HTML,
			Text:    e.Text,
			ReplyTo: e.ReplyTo,
		}
	}
	return a.email.SendBatchEmails(ctx, batch)
}

// supermemoryAdapter adapts the supermemory client to the aiservice.SupermemoryClient interface.
type supermemoryAdapter struct {
	client *supermemoryclient.Client
}

func (a *supermemoryAdapter) IngestConversation(ctx context.Context, userID string, messages []aiservice.SupermemoryMessage) error {
	msgs := make([]supermemoryclient.Message, len(messages))
	for i, m := range messages {
		msgs[i] = supermemoryclient.Message{Role: m.Role, Content: m.Content}
	}
	return a.client.IngestConversation(ctx, userID, msgs)
}

func (a *supermemoryAdapter) SearchMemory(ctx context.Context, userID, query string, limit int) ([]aiservice.SupermemoryResult, error) {
	results, err := a.client.SearchMemory(ctx, userID, query, limit)
	if err != nil {
		return nil, err
	}
	return mapSupermemoryResults(results), nil
}

func (a *supermemoryAdapter) SearchMemoryRanked(ctx context.Context, userID, query string, limit int) ([]aiservice.SupermemoryResult, error) {
	results, err := a.client.Search(ctx, userID, query, supermemoryclient.SearchOptions{Limit: limit, Rerank: true})
	if err != nil {
		return nil, err
	}
	return mapSupermemoryResults(results), nil
}

func mapSupermemoryResults(results []supermemoryclient.SearchResult) []aiservice.SupermemoryResult {
	out := make([]aiservice.SupermemoryResult, len(results))
	for i, r := range results {
		res := aiservice.SupermemoryResult{Memory: r.Memory, Similarity: r.Similarity}
		if r.Metadata != nil {
			if tsStr, ok := r.Metadata["event_ts"]; ok {
				if ts, perr := strconv.ParseInt(tsStr, 10, 64); perr == nil {
					res.EventUnix = ts
				}
			}
		}
		if !r.UpdatedAt.IsZero() {
			res.UpdatedUnix = r.UpdatedAt.Unix()
		}
		out[i] = res
	}
	return out
}

// revenueSweepTransferAdapter wraps Circle adapter for revenue sweep transfers from user wallets.
type revenueSweepTransferAdapter struct {
	circle          *circleadapter.Adapter
	treasuryAddress string
}

func (a *revenueSweepTransferAdapter) TransferToTreasury(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	walletID, tokenID, _, _, err := a.circle.FindWalletWithUSDC(ctx, userID.String())
	if err != nil {
		return fmt.Errorf("find user wallet: %w", err)
	}
	tx, err := a.circle.TransferUSDCWithIdempotency(ctx, walletID, tokenID, a.treasuryAddress, amount.StringFixed(2), reference)
	if err != nil {
		return err
	}
	if tx.State == "DENIED" || tx.State == "FAILED" || tx.State == "CANCELLED" {
		return fmt.Errorf("transfer %s: %s", tx.State, tx.ID)
	}
	return nil
}

// platformVoiceAdapter adapts the ElevenLabs REST client to platform.VoiceTranscoder.
type platformVoiceAdapter struct {
	rest *ai.ElevenLabsREST
}

func (a *platformVoiceAdapter) Available() bool { return a.rest.Available() }

func (a *platformVoiceAdapter) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	return a.rest.TextToSpeech(ctx, text)
}

func (a *platformVoiceAdapter) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	return a.rest.SpeechToText(ctx, audio, mime)
}

// orchestratorAdapter wraps aiservice.AgentAdapter to implement platform.Orchestrator.
// It maps a messaging thread to a stable conversation, translates staged pending
// actions into confirm cards (or an in-app authorization hand-off for fund moves),
// and executes confirmations through the orchestrator's authoritative store.
type orchestratorAdapter struct {
	orchestrator *aiservice.AgentAdapter
	convRepo     *repositories.ConversationRepository
	logger       *zap.Logger

	// Python agent delegation (when wired, HandlePlatformMessage forwards to the
	// Python agent and money movements are settled by a confirm_id its ledger
	// issued, surfaced as a Confirm/Cancel poll).
	python       *ai.PythonAgentClient
	confirmStore *ai.ConfirmStore
	userRepo     *repositories.UserRepository // for email + KYC-derived role
}

func (a *orchestratorAdapter) HandlePlatformMessage(ctx context.Context, userID, platformIdentityID, message, threadID string, plat entities.Platform) (*platform.PlatformReply, error) {
	// Python-agent delegation path: MIRIAM's LLM brain owns the conversation.
	// Must return before touching a.orchestrator — that field is nil when
	// Python is enabled without a Cencori-backed Go AI system.
	if a.pythonDelegated() {
		return a.handlePlatformMessagePython(ctx, userID, platformIdentityID, message, threadID, plat)
	}

	// Fail closed, never sideways: texted chat only ever comes from MIRIAM
	// (Python). If delegation is not wired, an apology is sent instead of an
	// answer from a different brain — a user texting Miriam must never be
	// answered by the in-process Go model.
	return &platform.PlatformReply{
		Text: "I couldn't reach my finance brain just now. Give me a few seconds and ask me again.",
	}, nil
}

// resolveConvID maps a messaging thread to its stable conversation id.
func (a *orchestratorAdapter) resolveConvID(ctx context.Context, uid uuid.UUID, platformIdentityID, threadID string, plat entities.Platform) (uuid.UUID, error) {
	pid, _ := uuid.Parse(platformIdentityID)
	id, _, err := a.convRepo.GetOrCreatePlatformConversation(ctx, uid, plat.String(), threadID, pid)
	return id, err
}

func (a *orchestratorAdapter) ConfirmPlatformAction(ctx context.Context, userID, platformIdentityID, threadID string, plat entities.Platform) (*platform.PlatformReply, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}
	cid, err := a.resolveConvID(ctx, uid, platformIdentityID, threadID, plat)
	if err != nil {
		return nil, fmt.Errorf("resolve conversation: %w", err)
	}

	// Python-delegated confirmation: the tap settles the challenge the ledger
	// issued, named by the id staged for this thread. Go holds no money state.
	if confirmID, ok := a.pendingPythonConfirm(ctx, cid); ok {
		return a.settlePythonConfirm(ctx, uid, cid, threadID, plat, confirmID, true)
	}

	if a.orchestrator == nil {
		return &platform.PlatformReply{Text: "There's nothing waiting on a tap-confirm right now."}, nil
	}

	// Defence in depth: a fund-moving action must never execute from a messaging
	// vote — it should have been sent as an in-app card, never a poll.
	if action, ok := a.orchestrator.PeekPendingAction(ctx, uid, cid); ok && aiservice.IsFundMovingAction(action.Action) {
		return &platform.PlatformReply{Text: "For your security, moving money has to be done with Face ID in the RAIL app."}, nil
	}

	action, err := a.orchestrator.ConfirmAction(ctx, uid, cid)
	if err != nil {
		return nil, err
	}
	return &platform.PlatformReply{
		Text:   "✅ Done — " + actionSuccessSummary(action),
		Effect: platform.EffectCelebration,
	}, nil
}

// HasPendingPlatformAction reports whether the thread's conversation currently
// has a staged pending action. Used to interpret bare YES/NO text replies as
// confirm/cancel on platforms without interactive polls.
func (a *orchestratorAdapter) HasPendingPlatformAction(ctx context.Context, userID, platformIdentityID, threadID string, plat entities.Platform) bool {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false
	}
	cid, err := a.resolveConvID(ctx, uid, platformIdentityID, threadID, plat)
	if err != nil {
		return false
	}
	// A Python challenge is reported as pending too, so the Confirm/Cancel poll
	// and the bare YES/NO fallback both resolve to a settlement rather than to
	// normal chat.
	if _, ok := a.pendingPythonConfirm(ctx, cid); ok {
		return true
	}
	if a.orchestrator == nil {
		return false
	}
	_, ok := a.orchestrator.PeekPendingAction(ctx, uid, cid)
	return ok
}

func (a *orchestratorAdapter) CancelPlatformAction(ctx context.Context, userID, platformIdentityID, threadID string, plat entities.Platform) (*platform.PlatformReply, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}
	cid, err := a.resolveConvID(ctx, uid, platformIdentityID, threadID, plat)
	if err != nil {
		return nil, fmt.Errorf("resolve conversation: %w", err)
	}
	// A declined Python challenge is reported back to the ledger, so it is closed
	// rather than left open for a later tap.
	if confirmID, ok := a.pendingPythonConfirm(ctx, cid); ok {
		return a.settlePythonConfirm(ctx, uid, cid, threadID, plat, confirmID, false)
	}
	if a.orchestrator == nil {
		return &platform.PlatformReply{Text: "No problem — I've cancelled that."}, nil
	}
	if err := a.orchestrator.CancelAction(ctx, uid, cid); err != nil {
		return nil, err
	}
	return &platform.PlatformReply{Text: "No problem — I've cancelled that."}, nil
}

func actionSuccessSummary(action *entities.PendingAction) string {
	if action != nil && action.Description != "" {
		return action.Description
	}
	return "all set"
}
