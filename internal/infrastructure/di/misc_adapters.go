package di

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/growthengine"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	circleadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

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

// orchestratorAdapter implements platform.Orchestrator by delegating every
// conversation turn to the Python agent (MIRIAM's LLM brain). It maps a
// messaging thread to a stable conversation and settles staged money
// confirmations through the confirm_id the Python ledger issued, surfaced as a
// Confirm/Cancel poll. Go holds no money state and runs no model.
type orchestratorAdapter struct {
	convRepo *repositories.ConversationRepository
	logger   *zap.Logger

	// Python agent delegation. HandlePlatformMessage forwards to the Python
	// agent; money movements are settled by a confirm_id its ledger issued.
	python       *ai.PythonAgentClient
	confirmStore *ai.ConfirmStore
	userRepo     *repositories.UserRepository // for email + KYC-derived role
}

func (a *orchestratorAdapter) HandlePlatformMessage(ctx context.Context, userID, platformIdentityID, message, threadID string, plat entities.Platform) (*platform.PlatformReply, error) {
	// Python-agent delegation path: MIRIAM's LLM brain owns the conversation.
	if a.pythonDelegated() {
		return a.handlePlatformMessagePython(ctx, userID, platformIdentityID, message, threadID, plat)
	}

	// Fail closed, never sideways: texted chat only ever comes from MIRIAM
	// (Python). If delegation is not wired, an apology is sent instead of an
	// answer from a different brain — a user texting Miriam must never be
	// answered by an in-process Go model.
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
	return &platform.PlatformReply{Text: "There's nothing waiting on a tap-confirm right now."}, nil
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
	// A Python challenge is reported as pending, so the Confirm/Cancel poll and
	// the bare YES/NO fallback both resolve to a settlement rather than chat.
	_, ok := a.pendingPythonConfirm(ctx, cid)
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
	return &platform.PlatformReply{Text: "No problem — I've cancelled that."}, nil
}
