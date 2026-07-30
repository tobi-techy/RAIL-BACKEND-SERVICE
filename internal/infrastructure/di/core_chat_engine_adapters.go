package di

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	aicore "github.com/rail-service/rail_service/internal/domain/services/ai/core"
	obligationservice "github.com/rail-service/rail_service/internal/domain/services/obligation"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	supermemoryclient "github.com/rail-service/rail_service/internal/infrastructure/supermemory"
	"github.com/shopspring/decimal"
)

// --- SupermemoryClient adapter ---

type coreSupermemoryAdapter struct {
	client *supermemoryclient.Client
}

func (a *coreSupermemoryAdapter) IngestConversation(ctx context.Context, userID string, messages []aicore.SupermemoryMessage) error {
	msgs := make([]supermemoryclient.Message, len(messages))
	for i, m := range messages {
		msgs[i] = supermemoryclient.Message{Role: m.Role, Content: m.Content}
	}
	return a.client.IngestConversation(ctx, userID, msgs)
}

func (a *coreSupermemoryAdapter) SearchMemory(ctx context.Context, userID, query string, limit int) ([]aicore.SupermemoryResult, error) {
	results, err := a.client.SearchMemory(ctx, userID, query, limit)
	if err != nil {
		return nil, err
	}
	return mapCoreSupermemoryResults(results), nil
}

func (a *coreSupermemoryAdapter) SearchMemoryRanked(ctx context.Context, userID, query string, limit int) ([]aicore.SupermemoryResult, error) {
	results, err := a.client.Search(ctx, userID, query, supermemoryclient.SearchOptions{Limit: limit, Rerank: true})
	if err != nil {
		return nil, err
	}
	return mapCoreSupermemoryResults(results), nil
}

func mapCoreSupermemoryResults(results []supermemoryclient.SearchResult) []aicore.SupermemoryResult {
	out := make([]aicore.SupermemoryResult, len(results))
	for i, r := range results {
		res := aicore.SupermemoryResult{Memory: r.Memory, Similarity: r.Similarity}
		if r.Metadata != nil {
			if tsStr, ok := r.Metadata["event_ts"]; ok {
				if ts, perr := time.Parse(time.RFC3339, tsStr); perr == nil {
					res.EventUnix = ts.Unix()
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

// --- SavingsGoalStore adapter ---

type coreSavingsGoalStoreAdapter struct {
	inner aiservice.SavingsGoalStore
}

func (a *coreSavingsGoalStoreAdapter) Set(ctx context.Context, userID uuid.UUID, goal *aicore.SavingsGoalData) error {
	return a.inner.Set(ctx, userID, &aiservice.SavingsGoal{
		Name:      goal.Name,
		Target:    goal.Target,
		Deadline:  goal.Deadline,
		CreatedAt: goal.CreatedAt,
	})
}

func (a *coreSavingsGoalStoreAdapter) Get(ctx context.Context, userID uuid.UUID) (*aicore.SavingsGoalData, error) {
	goal, err := a.inner.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if goal == nil {
		return nil, nil
	}
	return &aicore.SavingsGoalData{
		Name:      goal.Name,
		Target:    goal.Target,
		Deadline:  goal.Deadline,
		CreatedAt: goal.CreatedAt,
	}, nil
}

// --- SpendingAnalyzer adapter ---

type coreSpendingAnalyzerAdapter struct {
	inner *spendingsvc.Service
}

func (a *coreSpendingAnalyzerAdapter) GetSummary(ctx context.Context, userID uuid.UUID, start, end time.Time) (interface{}, error) {
	return a.inner.GetSummary(ctx, userID, start, end)
}

func (a *coreSpendingAnalyzerAdapter) GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error) {
	return a.inner.GetMoneyFlow(ctx, userID, start, end)
}

// --- BalanceHistoryProvider adapter ---
// The orchestrator interface uses GetSnapshotsInWindow; core uses GetBalanceHistory.
// We bridge by computing the day window from the days parameter.

type coreBalanceHistoryAdapter struct {
	yieldRepo *repositories.YieldRepository
}

func (a *coreBalanceHistoryAdapter) GetBalanceHistory(ctx context.Context, userID uuid.UUID, days int) (interface{}, error) {
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -days)
	snapshots, err := a.yieldRepo.GetSnapshotsInWindow(ctx, userID, start, end)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, len(snapshots))
	for i, s := range snapshots {
		out[i] = map[string]interface{}{
			"balance":  s.Balance.String(),
			"recorded": s.RecordedAt.Format(time.RFC3339),
		}
	}
	return out, nil
}

// --- PatternAnalyzer adapter ---
// Core uses GetPatterns(ctx, userID) -> interface{}; orchestrator uses multiple methods.
// We aggregate all pattern data into a single map.

type corePatternAnalyzerAdapter struct {
	cardRepo *repositories.CardRepository
}

func (a *corePatternAnalyzerAdapter) GetPatterns(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -90)

	result := map[string]interface{}{}

	byDayOfWeek, err := a.cardRepo.GetSpendingByDayOfWeek(ctx, userID, start, end)
	if err == nil {
		result["by_day_of_week"] = byDayOfWeek
	}

	largest, err := a.cardRepo.GetLargestTransactions(ctx, userID, start, end, 10)
	if err == nil {
		result["largest_transactions"] = largest
	}

	total, count, err := a.cardRepo.GetSpendingTotal(ctx, userID, start, end)
	if err == nil {
		result["total_spending"] = total.String()
		result["transaction_count"] = count
	}

	return result, nil
}

// --- FinancialProfileProvider adapter ---
// Core uses GetFinancialProfile; orchestrator uses GetByUserID.

type coreFinancialProfileAdapter struct {
	financialProfileRepo *repositories.FinancialProfileRepository
}

func (a *coreFinancialProfileAdapter) GetFinancialProfile(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error) {
	return a.financialProfileRepo.GetByUserID(ctx, userID)
}

// --- ActivityDataProvider adapter ---

type coreActivityDataAdapter struct {
	inner *aiservice.ActivityDataProviderImpl
}

func (a *coreActivityDataAdapter) GetContributions(ctx context.Context, userID uuid.UUID, contributionType string, startDate, endDate time.Time) (*aicore.ContributionSummary, error) {
	summary, err := a.inner.GetContributions(ctx, userID, contributionType, startDate, endDate)
	if err != nil {
		return nil, err
	}
	return &aicore.ContributionSummary{
		Deposits: summary.Deposits,
		Roundups: summary.Roundups,
		Cashback: summary.Cashback,
		Total:    summary.Total,
	}, nil
}

func (a *coreActivityDataAdapter) GetStreak(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error) {
	return a.inner.GetStreak(ctx, userID)
}

// --- ObligationCreator adapter ---
// Core accepts map[string]interface{}; orchestrator uses typed AIServiceObligationRequest.

type coreObligationCreatorAdapter struct {
	service *obligationservice.Service
}

func (a *coreObligationCreatorAdapter) CreateObligationFromAI(ctx context.Context, userID uuid.UUID, req map[string]interface{}) (*entities.FinancialObligation, error) {
	creq := aiservice.AIServiceObligationRequest{
		Name:     getStringField(req, "name"),
		Type:     getStringField(req, "type"),
		Currency: getStringField(req, "currency"),
		Cadence:  getStringField(req, "cadence"),
		Priority: getStringField(req, "priority"),
	}
	if v, ok := req["amount"].(string); ok {
		if d, err := decimal.NewFromString(v); err == nil {
			creq.Amount = d
		}
	}
	if v, ok := req["due_day"].(float64); ok {
		dueDay := int(v)
		creq.DueDay = &dueDay
	}
	if v, ok := req["counterparty"].(string); ok {
		creq.Counterparty = &v
	}
	if v, ok := req["metadata"].(map[string]interface{}); ok {
		creq.Metadata = v
	}
	return a.service.Create(ctx, userID, obligationservice.CreateRequest{
		Type:         creq.Type,
		Name:         creq.Name,
		Amount:       creq.Amount,
		Currency:     creq.Currency,
		Cadence:      creq.Cadence,
		DueDay:       creq.DueDay,
		Priority:     creq.Priority,
		Counterparty: creq.Counterparty,
		Metadata:     creq.Metadata,
	})
}

// --- WithdrawalHistoryProvider adapter ---

type coreWithdrawalHistoryAdapter struct {
	withdrawalRepo *repositories.WithdrawalRepository
}

func (a *coreWithdrawalHistoryAdapter) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]entities.Withdrawal, error) {
	withdrawals, err := a.withdrawalRepo.GetByUserID(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]entities.Withdrawal, len(withdrawals))
	for i, w := range withdrawals {
		if w != nil {
			out[i] = *w
		}
	}
	return out, nil
}

// --- BankStatementContextProvider adapter ---

type coreBankStatementContextAdapter struct {
	inner *aiservice.BankStatementContextProvider
}

func (a *coreBankStatementContextAdapter) GetContext(ctx context.Context, userID uuid.UUID) (string, error) {
	return a.inner.BuildContext(ctx, userID), nil
}

// --- helpers ---

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Ensure adapter types satisfy core interfaces at compile time.
var (
	_ aicore.SupermemoryClient            = (*coreSupermemoryAdapter)(nil)
	_ aicore.SavingsGoalStore             = (*coreSavingsGoalStoreAdapter)(nil)
	_ aicore.SpendingAnalyzer             = (*coreSpendingAnalyzerAdapter)(nil)
	_ aicore.BalanceHistoryProvider       = (*coreBalanceHistoryAdapter)(nil)
	_ aicore.PatternAnalyzer              = (*corePatternAnalyzerAdapter)(nil)
	_ aicore.FinancialProfileProvider     = (*coreFinancialProfileAdapter)(nil)
	_ aicore.ActivityDataProvider         = (*coreActivityDataAdapter)(nil)
	_ aicore.ObligationCreator            = (*coreObligationCreatorAdapter)(nil)
	_ aicore.WithdrawalHistoryProvider    = (*coreWithdrawalHistoryAdapter)(nil)
	_ aicore.BankStatementContextProvider = (*coreBankStatementContextAdapter)(nil)
)
