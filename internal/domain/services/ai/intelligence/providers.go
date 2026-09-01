package intelligence

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type KnowledgeSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]entities.KnowledgeSearchResult, error)
}

type GameplayProvider interface {
	GetUserStreaks(ctx context.Context, userID uuid.UUID) ([]*entities.UserStreak, error)
	GetActiveChallenges(ctx context.Context, userID uuid.UUID) ([]*entities.UserChallenge, error)
	GetUserAchievements(ctx context.Context, userID uuid.UUID) ([]*entities.Achievement, []*entities.UserAchievement, error)
}

type ObligationLister interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
}

type BalanceHistoryProvider interface {
	GetSnapshotsInWindow(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]*entities.YieldBalanceSnapshot, error)
}

type MiriamIntelligenceReader interface {
	GetMoneyState(ctx context.Context, userID uuid.UUID) (*entities.MiriamMoneyState, error)
	ListMandates(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutopilotMandate, error)
	ListReceipts(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamDecisionReceipt, error)
}

type ActionHistoryReader interface {
	ListRecentActions(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.ActionAuditEntry, error)
}
