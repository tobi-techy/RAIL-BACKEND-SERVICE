package di

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	aicore "github.com/rail-service/rail_service/internal/domain/services/ai/core"
	aimemory "github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	"github.com/rail-service/rail_service/internal/domain/services/audit"
	automationsvc "github.com/rail-service/rail_service/internal/domain/services/automation"
	conversationsvc "github.com/rail-service/rail_service/internal/domain/services/conversation"
	investingsvc "github.com/rail-service/rail_service/internal/domain/services/investing"
	knowledgesvc "github.com/rail-service/rail_service/internal/domain/services/knowledge"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	miriamservice "github.com/rail-service/rail_service/internal/domain/services/miriam"
	newsservice "github.com/rail-service/rail_service/internal/domain/services/news"
	obligationsvc "github.com/rail-service/rail_service/internal/domain/services/obligation"
	p2pservice "github.com/rail-service/rail_service/internal/domain/services/p2p"
	"github.com/rail-service/rail_service/internal/domain/services/premium"
	"github.com/rail-service/rail_service/internal/domain/services/sharedgoal"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	subscriptionsvc "github.com/rail-service/rail_service/internal/domain/services/subscription"
	usagesvc "github.com/rail-service/rail_service/internal/domain/services/usage"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	supermemoryclient "github.com/rail-service/rail_service/internal/infrastructure/supermemory"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// buildNewMemoryService creates a unified memory service from container components.
func buildNewMemoryService(c *Container) aicore.MemoryService {
	var vectorStore aimemory.VectorStore
	if c.QdrantStore != nil {
		vectorStore = c.QdrantStore
	} else if c.VectorizeStore != nil {
		vectorStore = c.VectorizeStore
	}
	return &memoryAdapter{
		oldMemory:   c.MemoryService,
		supermemory: c.SupermemoryClient,
		vectorStore: vectorStore,
		logger:      c.ZapLog,
	}
}

// workingMemoryAdapter wraps *memory.WorkingMemoryStore to satisfy core.WorkingMemoryStore.
// The adapter converts between the memory package's concrete types and core's interface types.
type workingMemoryAdapter struct {
	store *aimemory.WorkingMemoryStore
}

func (a *workingMemoryAdapter) Get(ctx context.Context, userID uuid.UUID) aicore.WorkingMemorySnapshot {
	entry := a.store.Get(ctx, userID)
	if entry == nil {
		return nil
	}
	return &workingMemorySnapshot{entry: entry}
}

func (a *workingMemoryAdapter) AppendExchange(ctx context.Context, userID uuid.UUID, userMsg, assistantMsg string) {
	a.store.AppendExchange(ctx, userID, userMsg, assistantMsg)
}

// workingMemorySnapshot adapts *memory.WorkingMemoryEntry to core.WorkingMemorySnapshot.
type workingMemorySnapshot struct {
	entry *aimemory.WorkingMemoryEntry
}

func (s *workingMemorySnapshot) GetSummary() string   { return s.entry.Summary }
func (s *workingMemorySnapshot) GetTopic() string     { return s.entry.Topic }
func (s *workingMemorySnapshot) GetMessageCount() int { return s.entry.MessageCount }

type memoryAdapter struct {
	oldMemory   *aiservice.MemoryService
	supermemory *supermemoryclient.Client
	vectorStore aimemory.VectorStore
	logger      *zap.Logger
}

func (m *memoryAdapter) ProcessExchange(ctx context.Context, userID uuid.UUID, userMsg, assistantMsg string) error {
	if m.oldMemory != nil {
		m.oldMemory.ProcessExchange(userID, userMsg, assistantMsg)
	}
	return nil
}

func (m *memoryAdapter) SearchMemory(ctx context.Context, userID uuid.UUID, query string, limit int) ([]aicore.SupermemoryResult, error) {
	var out []aicore.SupermemoryResult

	if m.supermemory != nil {
		results, err := m.supermemory.SearchMemory(ctx, userID.String(), query, limit)
		if err != nil {
			m.logger.Warn("Supermemory search failed", zap.Error(err))
		} else {
			for _, r := range results {
				out = append(out, aicore.SupermemoryResult{
					Memory:     r.Memory,
					Similarity: r.Similarity,
				})
			}
		}
	}

	if m.vectorStore != nil {
		episodic, err := m.vectorStore.Search(ctx, "episodic", userID, query, limit)
		if err != nil {
			m.logger.Warn("vector memory search failed", zap.Error(err))
		} else {
			for _, r := range episodic {
				out = append(out, aicore.SupermemoryResult{
					Memory:     r.Content,
					Similarity: r.Similarity,
				})
			}
		}
	}

	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memoryAdapter) SearchFacts(ctx context.Context, userID uuid.UUID, query string, limit int) ([]aicore.Fact, error) {
	// Prefer durable SQL facts (source of truth for list_memory / forget).
	if m.oldMemory != nil {
		facts, err := m.oldMemory.GetActiveFacts(ctx, userID)
		if err == nil && len(facts) > 0 {
			out := make([]aicore.Fact, 0, len(facts))
			q := strings.ToLower(strings.TrimSpace(query))
			for _, f := range facts {
				if f == nil {
					continue
				}
				if q != "" {
					hay := strings.ToLower(f.Fact + " " + f.Category)
					if !strings.Contains(hay, q) {
						continue
					}
				}
				out = append(out, aicore.Fact{
					ID:         f.ID.String(),
					Category:   f.Category,
					Fact:       f.Fact,
					Confidence: f.Confidence.InexactFloat64(),
					Source:     "miriam_facts",
				})
				if limit > 0 && len(out) >= limit {
					break
				}
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}

	if m.vectorStore == nil {
		return nil, nil
	}
	results, err := m.vectorStore.Search(ctx, "facts", userID, query, limit)
	if err != nil {
		m.logger.Warn("vector SearchFacts failed", zap.Error(err))
		return nil, nil
	}
	out := make([]aicore.Fact, 0, len(results))
	for _, r := range results {
		out = append(out, aicore.Fact{
			Fact:       r.Content,
			Confidence: r.Similarity,
			Source:     "vector",
		})
	}
	return out, nil
}

func (m *memoryAdapter) ForgetFact(ctx context.Context, userID, factID uuid.UUID) error {
	if m.oldMemory == nil {
		return fmt.Errorf("memory service not available")
	}
	return m.oldMemory.ForgetFact(ctx, userID, factID)
}

func (m *memoryAdapter) SearchEpisodic(ctx context.Context, userID uuid.UUID, query string, limit int) ([]aicore.Episode, error) {
	if m.vectorStore == nil {
		return nil, nil
	}
	results, err := m.vectorStore.Search(ctx, "episodic", userID, query, limit)
	if err != nil {
		m.logger.Warn("vector SearchEpisodic failed", zap.Error(err))
		return nil, nil
	}
	out := make([]aicore.Episode, 0, len(results))
	for _, r := range results {
		out = append(out, aicore.Episode{
			Content:    r.Content,
			Similarity: r.Similarity,
		})
	}
	return out, nil
}

func (m *memoryAdapter) GetToneProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (m *memoryAdapter) GetPersonalityMode(ctx context.Context, userID uuid.UUID) string {
	return ""
}

func (m *memoryAdapter) SetPersonalityMode(ctx context.Context, userID uuid.UUID, mode string) error {
	if m.oldMemory != nil {
		return m.oldMemory.SetPersonalityMode(ctx, userID, mode)
	}
	return nil
}

func (m *memoryAdapter) GetControlLevel(ctx context.Context, userID uuid.UUID) (string, error) {
	if m.oldMemory != nil {
		return m.oldMemory.GetControlLevel(ctx, userID)
	}
	return "", nil
}

func (m *memoryAdapter) SetControlLevel(ctx context.Context, userID uuid.UUID, level string) error {
	if m.oldMemory != nil {
		return m.oldMemory.SetControlLevel(ctx, userID, level)
	}
	return nil
}

func (m *memoryAdapter) GetMemorySummary(ctx context.Context, userID uuid.UUID, message string) (string, error) {
	if m.oldMemory != nil {
		return m.oldMemory.BuildMemoryContextWithSummary(ctx, userID, message), nil
	}
	return "", nil
}

func (m *memoryAdapter) RunDecay(ctx context.Context) error {
	if m.oldMemory != nil {
		return m.oldMemory.RunDecay(ctx)
	}
	return nil
}

func (m *memoryAdapter) SaveTransactionPattern(ctx context.Context, userID uuid.UUID, patternType string, data map[string]interface{}) error {
	if m.oldMemory != nil && patternType != "" {
		confidence, _ := data["confidence"].(float64)
		category, _ := data["category"].(string)
		m.oldMemory.SaveTransactionPattern(ctx, userID, patternType, category, confidence)
	}
	return nil
}

// --- StateService ---

func buildStateService(c *Container) aicore.StateService {
	return &stateService{
		ledger: c.LedgerService,
		logger: c.ZapLog,
	}
}

type stateService struct {
	ledger *ledger.Service
	logger *zap.Logger
}

func (s *stateService) GetState(ctx context.Context, userID uuid.UUID) (*aicore.UserState, error) {
	balances, err := s.ledger.GetUserBalances(ctx, userID)
	if err != nil || balances == nil {
		s.logger.Warn("GetState: failed to get user balances", zap.Error(err))
		return &aicore.UserState{
			Balances: &aicore.Balances{
				Spend:    decimal.Zero,
				Stash:    decimal.Zero,
				Currency: "USD",
			},
		}, nil
	}
	return &aicore.UserState{
		Balances: &aicore.Balances{
			Spend:    balances.SpendingBalance,
			Stash:    balances.StashBalance,
			Currency: "USD",
		},
	}, nil
}

func (s *stateService) RefreshState(ctx context.Context, userID uuid.UUID) error {
	return nil
}

func (s *stateService) InvalidateCache(ctx context.Context, userID uuid.UUID) error {
	return nil
}

// --- ConversationService ---

func buildConversationService(c *Container) aicore.ConversationService {
	if c.ConversationService == nil {
		return &convService{}
	}
	return &convService{realSvc: c.ConversationService}
}

type convService struct {
	realSvc *conversationsvc.Service
}

func (cs *convService) BuildContext(ctx context.Context, convID, userID uuid.UUID) ([]map[string]interface{}, error) {
	if cs.realSvc == nil {
		return nil, nil
	}
	conv, err := cs.realSvc.GetConversation(ctx, convID)
	if err != nil || conv == nil {
		return nil, nil
	}
	messages, err := cs.realSvc.BuildContext(ctx, conv)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		out = append(out, aiMessageToMap(msg))
	}
	return out, nil
}

func (cs *convService) RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string) error {
	if cs.realSvc == nil {
		return nil
	}
	return cs.realSvc.RecordExchange(ctx, convID, userMsg, assistantMsg, tokens, cost, model, nil)
}

func (cs *convService) UpdateTitle(ctx context.Context, convID uuid.UUID, title string) error {
	if cs.realSvc == nil {
		return nil
	}
	return cs.realSvc.UpdateTitle(ctx, convID, title)
}

func (cs *convService) SaveToolMessages(ctx context.Context, convID uuid.UUID, messages []map[string]interface{}) error {
	if cs.realSvc == nil {
		return nil
	}
	aiMsgs := make([]ai.Message, 0, len(messages))
	for _, m := range messages {
		aiMsgs = append(aiMsgs, mapToAIMessage(m))
	}
	return cs.realSvc.SaveToolMessages(ctx, convID, aiMsgs)
}

func aiMessageToMap(msg ai.Message) map[string]interface{} {
	m := map[string]interface{}{
		"role":    msg.Role,
		"content": msg.Content,
	}
	if msg.Name != "" {
		m["name"] = msg.Name
	}
	if msg.ToolCallID != "" {
		m["tool_call_id"] = msg.ToolCallID
	}
	if len(msg.ToolCalls) > 0 {
		tcs := make([]map[string]interface{}, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			tcs[i] = map[string]interface{}{
				"id":        tc.ID,
				"name":      tc.Name,
				"arguments": tc.Arguments,
			}
		}
		m["tool_calls"] = tcs
	}
	return m
}

func mapToAIMessage(m map[string]interface{}) ai.Message {
	msg := ai.Message{}
	if role, ok := m["role"].(string); ok {
		msg.Role = role
	}
	if content, ok := m["content"].(string); ok {
		msg.Content = content
	}
	if name, ok := m["name"].(string); ok {
		msg.Name = name
	}
	if tcID, ok := m["tool_call_id"].(string); ok {
		msg.ToolCallID = tcID
	}
	if tcsRaw, ok := m["tool_calls"].([]interface{}); ok {
		for _, tcRaw := range tcsRaw {
			if tcMap, ok := tcRaw.(map[string]interface{}); ok {
				tc := ai.ToolCall{}
				if id, ok := tcMap["id"].(string); ok {
					tc.ID = id
				}
				if name, ok := tcMap["name"].(string); ok {
					tc.Name = name
				}
				if args, ok := tcMap["arguments"].(map[string]interface{}); ok {
					tc.Arguments = args
				}
				msg.ToolCalls = append(msg.ToolCalls, tc)
			}
		}
	}
	return msg
}

// --- UsageService ---

func buildUsageService(c *Container) aicore.UsageService {
	if c.UsageService == nil {
		return &usageService{}
	}
	return &usageService{realSvc: c.UsageService}
}

type usageService struct {
	realSvc *usagesvc.Service
}

func (u *usageService) TrackInteraction(ctx context.Context, userID uuid.UUID, model string, tokens int) error {
	if u.realSvc == nil {
		return nil
	}
	return u.realSvc.TrackInteraction(ctx, userID, model, tokens)
}

func (u *usageService) IsOverCostCeiling(ctx context.Context, userID uuid.UUID) bool {
	if u.realSvc == nil {
		return false
	}
	over, err := u.realSvc.IsOverCostCeiling(ctx, userID)
	if err != nil {
		return false
	}
	return over
}

// --- CostGuard ---

// buildAgentCostGuard wires the Redis-backed per-user daily/monthly ceiling
// into the core agent. Returns nil when no guard is configured (the agent
// then falls back to the slower Postgres-backed UsageService check alone).
// The *infraai.Guard type satisfies the core.CostGuard interface implicitly
// via Allow/Record method names.
func buildAgentCostGuard(c *Container) aicore.CostGuard {
	if c.AICostGuard == nil {
		return nil
	}
	if !c.AICostGuard.IsEnabled() {
		return nil
	}
	return c.AICostGuard
}

// =======================================================
// REAL PROVIDERS — wrap actual container services
// =======================================================

// --- PortfolioProvider ---

func buildPortfolioProvider(c *Container) aicore.PortfolioProvider {
	if c.PortfolioDataProvider == nil {
		return &stubGeneric{}
	}
	return &portfolioAdapter{
		p:        c.PortfolioDataProvider,
		activity: c.ActivityDataProvider,
		news:     c.NewsService,
		logger:   c.ZapLog,
	}
}

type portfolioAdapter struct {
	p        *aiservice.PortfolioDataProviderImpl
	activity *aiservice.ActivityDataProviderImpl
	news     *newsservice.Service
	logger   *zap.Logger
}

func (a *portfolioAdapter) GetWeeklyStats(ctx context.Context, userID uuid.UUID) (*aicore.PortfolioStats, error) {
	stats, err := a.p.GetWeeklyStats(ctx, userID)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		return nil, nil
	}
	return &aicore.PortfolioStats{
		TotalValue:      stats.TotalValue,
		WeeklyReturn:    stats.WeeklyReturn,
		WeeklyReturnPct: stats.WeeklyReturnPct,
		MonthlyReturn:   stats.MonthlyReturn,
		TotalGainLoss:   stats.TotalGainLoss,
	}, nil
}

func (a *portfolioAdapter) GetTopMovers(ctx context.Context, userID uuid.UUID, limit int) ([]*aicore.Mover, error) {
	movers, err := a.p.GetTopMovers(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*aicore.Mover, 0, len(movers))
	for _, m := range movers {
		if m == nil {
			continue
		}
		out = append(out, &aicore.Mover{
			Symbol:    m.Symbol,
			Name:      m.Name,
			ReturnPct: m.ReturnPct,
		})
	}
	return out, nil
}

func (a *portfolioAdapter) GetAllocations(ctx context.Context, userID uuid.UUID) ([]*aicore.Allocation, error) {
	alloc, err := a.p.GetAllocations(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]*aicore.Allocation, 0, len(alloc))
	for _, al := range alloc {
		if al == nil {
			continue
		}
		out = append(out, &aicore.Allocation{
			Name:   al.BasketName,
			Value:  al.Value,
			Weight: al.Weight,
		})
	}
	return out, nil
}

func (a *portfolioAdapter) GetStreak(ctx context.Context, userID uuid.UUID) (*aicore.StreakInfo, error) {
	if a.activity == nil {
		return nil, nil
	}
	streak, err := a.activity.GetStreak(ctx, userID)
	if err != nil || streak == nil {
		return nil, nil
	}
	lastActive := time.Now()
	if streak.LastInvestmentDate != nil {
		lastActive = *streak.LastInvestmentDate
	}
	return &aicore.StreakInfo{
		Days:       streak.CurrentStreak,
		Longest:    streak.LongestStreak,
		LastActive: lastActive,
	}, nil
}

func (a *portfolioAdapter) GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.news == nil {
		return nil, nil
	}
	newsList, err := a.news.GetWeeklyNews(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(newsList))
	for _, n := range newsList {
		if n == nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"title":   n.Title,
			"summary": n.Summary,
			"source":  n.Source,
			"url":     n.URL,
			"symbols": n.RelatedSymbols,
		})
	}
	return out, nil
}

// --- SpendingProvider ---

func buildSpendingProvider(c *Container) aicore.SpendingProvider {
	return &spendingAdapter{
		ledgerSpendingRepo: c.LedgerSpendingRepo,
		cardRepo:           c.CardRepo,
		ledgerSvc:          c.LedgerService,
		logger:             c.ZapLog,
	}
}

type spendingAdapter struct {
	ledgerSpendingRepo *repositories.LedgerSpendingRepository
	cardRepo           *repositories.CardRepository
	ledgerSvc          *ledger.Service
	logger             *zap.Logger
}

func (a *spendingAdapter) GetSpendingSummary(ctx context.Context, userID uuid.UUID, period string) (map[string]interface{}, error) {
	if a.ledgerSpendingRepo == nil {
		return map[string]interface{}{}, nil
	}
	end := time.Now()
	start := periodStart(end, period)
	total, count, err := a.ledgerSpendingRepo.GetSpendingTotal(ctx, userID, start, end)
	if err != nil {
		return map[string]interface{}{}, nil
	}
	return map[string]interface{}{
		"period":  period,
		"total":   total.String(),
		"count":   count,
		"average": averageString(total, count),
	}, nil
}

func (a *spendingAdapter) GetRecentTransactions(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	return getRecentTransactions(ctx, a.ledgerSpendingRepo, userID, limit)
}

func getRecentTransactions(ctx context.Context, repo *repositories.LedgerSpendingRepository, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	if repo == nil {
		return nil, nil
	}
	end := time.Now()
	start := end.AddDate(0, -3, 0)
	txns, err := repo.GetRecentOutflows(ctx, userID, start, end, limit)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(txns))
	for _, t := range txns {
		out = append(out, map[string]interface{}{
			"amount":   t.Amount.String(),
			"category": t.Category,
			"source":   t.Source,
			"date":     t.Date,
		})
	}
	return out, nil
}

func (a *spendingAdapter) GetMoneyFlow(ctx context.Context, userID uuid.UUID, months int) (map[string]interface{}, error) {
	if a.ledgerSpendingRepo == nil {
		return map[string]interface{}{}, nil
	}
	end := time.Now()
	start := end.AddDate(0, -months, 0)
	flow, err := a.ledgerSpendingRepo.GetMoneyFlow(ctx, userID, start, end)
	if err != nil || flow == nil {
		return map[string]interface{}{}, nil
	}
	return map[string]interface{}{
		"total_deposits":    flow.TotalDeposits.String(),
		"total_withdrawals": flow.TotalWithdrawals.String(),
		"total_card_spend":  flow.TotalCardSpend.String(),
		"deposit_count":     flow.DepositCount,
		"withdrawal_count":  flow.WithdrawalCount,
		"card_spend_count":  flow.CardSpendCount,
	}, nil
}

func (a *spendingAdapter) GetSpendingPatterns(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if a.cardRepo == nil {
		return map[string]interface{}{}, nil
	}
	end := time.Now()
	start := end.AddDate(0, -3, 0)
	byDay, err := a.cardRepo.GetSpendingByDayOfWeek(ctx, userID, start, end)
	if err != nil {
		return map[string]interface{}{}, nil
	}
	large, err2 := a.cardRepo.GetLargestTransactions(ctx, userID, start, end, 5)
	if err2 != nil {
		large = nil
	}
	patterns := make([]map[string]interface{}, 0, len(byDay))
	for _, d := range byDay {
		patterns = append(patterns, map[string]interface{}{
			"day_of_week": d.DayOfWeek,
			"day_name":    d.DayName,
			"total":       d.Total.String(),
			"count":       d.Count,
			"avg_per_tx":  d.AvgPerTx.String(),
		})
	}
	largest := make([]map[string]interface{}, 0, len(large))
	for _, t := range large {
		merchant := ""
		if t.MerchantName != nil {
			merchant = *t.MerchantName
		}
		largest = append(largest, map[string]interface{}{
			"amount":   t.Amount.String(),
			"merchant": merchant,
			"date":     t.CreatedAt,
		})
	}
	return map[string]interface{}{
		"day_of_week_patterns": patterns,
		"largest_transactions": largest,
	}, nil
}

// --- TransactionProvider ---

func buildTransactionProvider(c *Container) aicore.TransactionProvider {
	return &txnAdapter{
		cardRepo:           c.CardRepo,
		depositRepo:        c.DepositRepo,
		withdrawalRepo:     c.WithdrawalRepo,
		receiptRepo:        c.ReceiptRepo,
		yieldRepo:          c.yieldRepo,
		ledgerSpendingRepo: c.LedgerSpendingRepo,
		logger:             c.ZapLog,
	}
}

type txnAdapter struct {
	cardRepo           *repositories.CardRepository
	depositRepo        *repositories.DepositRepository
	withdrawalRepo     *repositories.WithdrawalRepository
	receiptRepo        *repositories.ReceiptRepository
	yieldRepo          *repositories.YieldRepository
	ledgerSpendingRepo *repositories.LedgerSpendingRepository
	logger             *zap.Logger
}

func (a *txnAdapter) GetCardTransactions(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	if a.cardRepo == nil {
		return nil, nil
	}
	txns, err := a.cardRepo.GetTransactionsByUserID(ctx, userID, limit, 0)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(txns))
	for _, t := range txns {
		if t == nil {
			continue
		}
		merchant := ""
		if t.MerchantName != nil {
			merchant = *t.MerchantName
		}
		category := ""
		if t.MerchantCategory != nil {
			category = *t.MerchantCategory
		}
		out = append(out, map[string]interface{}{
			"id":       t.ID.String(),
			"amount":   t.Amount.String(),
			"currency": t.Currency,
			"merchant": merchant,
			"category": category,
			"status":   t.Status,
			"type":     t.Type,
			"date":     t.CreatedAt,
		})
	}
	return out, nil
}

func (a *txnAdapter) GetDepositHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	if a.depositRepo == nil {
		return nil, nil
	}
	deposits, err := a.depositRepo.GetByUserID(ctx, userID, limit, 0)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(deposits))
	for _, d := range deposits {
		if d == nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"id":     d.ID.String(),
			"amount": d.Amount.String(),
			"token":  string(d.Token),
			"status": d.Status,
			"date":   d.CreatedAt,
		})
	}
	return out, nil
}

func (a *txnAdapter) GetWithdrawalHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	if a.withdrawalRepo == nil {
		return nil, nil
	}
	withdrawals, err := a.withdrawalRepo.GetByUserID(ctx, userID, limit, 0)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(withdrawals))
	for _, w := range withdrawals {
		if w == nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"id":       w.ID.String(),
			"amount":   w.Amount.String(),
			"currency": string(w.Currency),
			"status":   string(w.Status),
			"type":     string(w.WithdrawalType),
			"date":     w.CreatedAt,
		})
	}
	return out, nil
}

func (a *txnAdapter) GetIncomeTrend(ctx context.Context, userID uuid.UUID, months int) (map[string]interface{}, error) {
	if a.ledgerSpendingRepo == nil {
		return map[string]interface{}{}, nil
	}
	end := time.Now()
	start := end.AddDate(0, -months, 0)
	flow, err := a.ledgerSpendingRepo.GetMoneyFlow(ctx, userID, start, end)
	if err != nil || flow == nil {
		return map[string]interface{}{}, nil
	}
	return map[string]interface{}{
		"total_deposits":    flow.TotalDeposits.String(),
		"total_withdrawals": flow.TotalWithdrawals.String(),
		"total_card_spend":  flow.TotalCardSpend.String(),
		"months":            months,
	}, nil
}

func (a *txnAdapter) GetReceiptHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	if a.receiptRepo == nil {
		return nil, nil
	}
	receipts, err := a.receiptRepo.GetByUserID(ctx, userID, limit)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(receipts))
	for _, r := range receipts {
		if r == nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"id":       r.ID.String(),
			"merchant": r.Merchant,
			"amount":   r.Amount.String(),
			"currency": r.Currency,
			"category": r.Category,
			"date":     r.ReceiptDate,
		})
	}
	return out, nil
}

func (a *txnAdapter) GetYieldEarned(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if a.yieldRepo == nil {
		return map[string]interface{}{}, nil
	}
	end := time.Now()
	start := end.AddDate(0, -3, 0)
	snapshots, err := a.yieldRepo.GetSnapshotsInWindow(ctx, userID, start, end)
	if err != nil {
		return map[string]interface{}{}, nil
	}
	total := decimal.Zero
	for _, s := range snapshots {
		if s == nil {
			continue
		}
		total = total.Add(s.Balance)
	}
	return map[string]interface{}{
		"total_yield": total.String(),
		"period":      "3 months",
	}, nil
}

func (a *txnAdapter) GetBalanceHistory(ctx context.Context, userID uuid.UUID, days int) ([]map[string]interface{}, error) {
	if a.ledgerSpendingRepo == nil {
		return nil, nil
	}
	end := time.Now()
	start := end.AddDate(0, 0, -days)
	flow, err := a.ledgerSpendingRepo.GetMoneyFlow(ctx, userID, start, end)
	if err != nil || flow == nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, 2)
	if !flow.TotalDeposits.IsZero() {
		out = append(out, map[string]interface{}{
			"type":  "deposit",
			"total": flow.TotalDeposits.String(),
			"count": flow.DepositCount,
		})
	}
	if !flow.TotalWithdrawals.IsZero() {
		out = append(out, map[string]interface{}{
			"type":  "withdrawal",
			"total": flow.TotalWithdrawals.String(),
			"count": flow.WithdrawalCount,
		})
	}
	return out, nil
}

// --- FundsTransferer ---

func buildFundsTransferer(c *Container) aicore.FundsTransferer {
	return &fundsAdapter{
		ledger: c.LedgerService,
		logger: c.ZapLog,
	}
}

type fundsAdapter struct {
	ledger *ledger.Service
	logger *zap.Logger
}

func (f *fundsAdapter) Transfer(ctx context.Context, userID uuid.UUID, from, to string, amount decimal.Decimal) error {
	if f.ledger == nil {
		return fmt.Errorf("funds transfer: ledger service not available")
	}
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}
	key := fmt.Sprintf("agent_transfer_%s_%s_%s_%s", userID.String(), from, to, uuid.New().String())
	switch {
	case from == "spend" && to == "stash":
		return f.ledger.TransferSpendingToStash(ctx, userID, amount, key)
	case from == "stash" && to == "spend":
		return f.ledger.TransferStashToSpending(ctx, userID, amount, key)
	default:
		return fmt.Errorf("unsupported transfer direction: %s -> %s", from, to)
	}
}

func (f *fundsAdapter) Withdraw(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination string) error {
	return fmt.Errorf("withdrawal not wired in agent wiring; use the action engine")
}

func (f *fundsAdapter) SetSavingsGoal(ctx context.Context, userID uuid.UUID, name string, target decimal.Decimal) error {
	return fmt.Errorf("set savings goal not wired in agent wiring; use the action engine")
}

// --- BudgetProvider ---

func buildBudgetProvider(c *Container) aicore.BudgetProvider {
	if c.BudgetRepo == nil {
		return &stubBudget{}
	}
	return &budgetAdapter{b: c.BudgetRepo}
}

type budgetAdapter struct {
	b *repositories.BudgetRepository
}

func (a *budgetAdapter) GetByUserID(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	budget, err := a.b.GetByUserID(ctx, userID)
	if err != nil || budget == nil {
		return map[string]interface{}{}, nil
	}
	return map[string]interface{}{
		"id":       budget.ID.String(),
		"limit":    budget.MonthlyLimit.String(),
		"currency": budget.Currency,
	}, nil
}

func (a *budgetAdapter) Upsert(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error {
	if a.b == nil {
		return fmt.Errorf("budget store unavailable")
	}
	amount, ok := decimalFromArg(data["amount"])
	if !ok {
		amount, ok = decimalFromArg(data["monthly_limit"])
	}
	if !ok || !amount.IsPositive() {
		return fmt.Errorf("budget amount must be a positive number")
	}
	currency := stringOrDefault(data["currency"], "USD")
	return a.b.Upsert(ctx, userID, amount, currency)
}

// --- GoalStore ---

func buildGoalStore(c *Container) aicore.GoalStore {
	return &goalAdapter{
		sharedGoalSvc: c.SharedGoalService,
		ledger:        c.LedgerService,
		logger:        c.ZapLog,
	}
}

type goalAdapter struct {
	sharedGoalSvc *sharedgoal.Service
	ledger        *ledger.Service
	logger        *zap.Logger
}

func (a *goalAdapter) GetByUserID(ctx context.Context, userID uuid.UUID) ([]aicore.Goal, error) {
	if a.sharedGoalSvc == nil {
		return nil, nil
	}
	goals, err := a.sharedGoalSvc.List(ctx, userID)
	if err != nil {
		return nil, nil
	}
	out := make([]aicore.Goal, 0, len(goals))
	for _, g := range goals {
		out = append(out, aicore.Goal{
			ID:      g.ID,
			Name:    g.Name,
			Target:  g.TargetAmount,
			Current: g.CurrentAmount,
		})
	}
	return out, nil
}

func (a *goalAdapter) Create(ctx context.Context, userID uuid.UUID, goal *aicore.Goal) error {
	if a.sharedGoalSvc == nil {
		return fmt.Errorf("savings goal store unavailable")
	}
	if goal == nil || strings.TrimSpace(goal.Name) == "" {
		return fmt.Errorf("goal name is required")
	}
	if !goal.Target.IsPositive() {
		return fmt.Errorf("goal target must be greater than zero")
	}
	_, err := a.sharedGoalSvc.Create(ctx, userID, &sharedgoal.CreateGoalRequest{
		Name:         strings.TrimSpace(goal.Name),
		TargetAmount: goal.Target.String(),
		Currency:     "USD",
		Visibility:   "private",
	})
	return err
}

func (a *goalAdapter) GetWithdrawableStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, nil
	}
	return a.ledger.GetWithdrawableStashBalance(ctx, userID)
}

func (a *goalAdapter) GetUnallocatedStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, nil
	}
	return a.ledger.GetUnallocatedStashBalance(ctx, userID)
}

// --- CurrencyRateProvider ---

func buildCurrencyRateProvider(c *Container) aicore.CurrencyRateProvider {
	if c.ExchangeRateRepo == nil {
		return nil
	}
	return c.ExchangeRateRepo
}

// --- ProfileProvider ---

func buildProfileProvider(c *Container) aicore.ProfileProvider {
	if c.UserRepo == nil {
		return &stubGeneric{}
	}
	return &profileAdapter{
		userRepo: c.UserRepo,
		fpRepo:   c.FinancialProfileRepo,
	}
}

type profileAdapter struct {
	userRepo *repositories.UserRepository
	fpRepo   *repositories.FinancialProfileRepository
}

func (a *profileAdapter) GetFinancialProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if a.fpRepo == nil {
		return map[string]interface{}{}, nil
	}
	profile, err := a.fpRepo.GetByUserID(ctx, userID)
	if err != nil || profile == nil {
		return map[string]interface{}{}, nil
	}
	return map[string]interface{}{
		"monthly_income":         profile.MonthlyIncome.String(),
		"monthly_fixed_costs":    profile.MonthlyFixedCosts.String(),
		"monthly_savings_target": profile.MonthlySavingsTarget.String(),
		"emergency_fund_target":  profile.EmergencyFundTarget.String(),
		"risk_tolerance":         profile.RiskTolerance,
		"income_frequency":       profile.IncomeFrequency,
		"investment_horizon":     profile.InvestmentHorizon,
		"financial_goal":         profile.FinancialGoal,
	}, nil
}
func (a *profileAdapter) UpdateFinancialProfile(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error {
	return fmt.Errorf("financial profile updates not wired in agent wiring; use the action engine")
}
func (a *profileAdapter) GetUserProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	user, err := a.userRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return map[string]interface{}{}, nil
	}
	fullName := ""
	if user.FirstName != nil {
		fullName = *user.FirstName
	}
	if user.LastName != nil {
		if fullName != "" {
			fullName += " "
		}
		fullName += *user.LastName
	}
	return map[string]interface{}{
		"email":   user.Email,
		"country": user.Country,
		"name":    fullName,
		"phone":   user.Phone,
	}, nil
}

// --- AccountChecker ---

func buildAccountChecker(c *Container) aicore.AccountChecker {
	if c.UserRepo == nil {
		return nil
	}
	return &accountCheckAdapter{repo: c.UserRepo}
}

type accountCheckAdapter struct {
	repo *repositories.UserRepository
}

func (a *accountCheckAdapter) CheckAccount(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	user, err := a.repo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return map[string]interface{}{"active": false}, nil
	}
	return map[string]interface{}{
		"active": !user.WithdrawalsFrozen && user.IsActive,
		"frozen": user.WithdrawalsFrozen,
	}, nil
}

// --- MiriamIntelligenceProvider ---

func buildMiriamIntelligenceProvider(c *Container) aicore.MiriamIntelligenceProvider {
	if c.MiriamIntelligenceService == nil {
		return &stubGeneric{}
	}
	return &miriamAdapter{
		m:           c.MiriamIntelligenceService,
		suggestions: c.MiriamMandateSuggestionEngine,
	}
}

type miriamAdapter struct {
	m           *miriamservice.Service
	suggestions *miriamservice.MandateSuggestionEngine
}

func (a *miriamAdapter) GetMoneyState(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	state, err := a.m.GetMoneyState(ctx, userID)
	if err != nil || state == nil {
		return nil, nil
	}
	return map[string]interface{}{
		"confidence_level":        state.ConfidenceLevel,
		"confidence_score":        state.ConfidenceScore,
		"safe_to_spend_daily":     state.SafeToSpendDaily.StringFixed(2),
		"liquidity_runway_days":   state.LiquidityRunwayDays,
		"income_cadence":          state.IncomeCadence,
		"avg_monthly_income":      state.AvgMonthlyIncome.StringFixed(2),
		"upcoming_obligations":    state.UpcomingObligations.StringFixed(2),
		"recurring_spend_monthly": state.RecurringSpendMonthly.StringFixed(2),
		"anomaly_count":           state.AnomalyCount,
		"currency":                "USD",
	}, nil
}

func (a *miriamAdapter) GetMandates(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	mandates, err := a.m.ListMandates(ctx, userID)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(mandates))
	for _, m := range mandates {
		out = append(out, map[string]interface{}{
			"id":          m.ID.String(),
			"action":      m.ActionType,
			"description": m.Name,
			"active":      m.Status,
		})
	}
	return out, nil
}

func (a *miriamAdapter) ListMandateSuggestions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.suggestions == nil {
		return nil, nil
	}
	list, err := a.suggestions.ListSuggestions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(list))
	for _, s := range list {
		out = append(out, map[string]interface{}{
			"id":                   s.ID.String(),
			"name":                 s.Name,
			"action_type":          s.ActionType,
			"reasoning":            s.Reasoning,
			"suggested_max_amount": s.SuggestedMaxAmount.StringFixed(2),
			"suggested_max_day":    s.SuggestedMaxDay.StringFixed(2),
			"confidence":           s.Confidence,
			"status":               s.Status,
		})
	}
	return out, nil
}

func (a *miriamAdapter) AcceptMandateSuggestion(ctx context.Context, userID uuid.UUID, suggestionID uuid.UUID) (map[string]interface{}, error) {
	if a.suggestions == nil {
		return nil, fmt.Errorf("mandate suggestions not available")
	}
	mandate, err := a.suggestions.Accept(ctx, userID, suggestionID)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"status":      "accepted",
		"mandate_id":  mandate.ID.String(),
		"name":        mandate.Name,
		"action_type": mandate.ActionType,
		"max_amount":  mandate.MaxAmountPerAction.StringFixed(2),
		"next_step":   "Mandate is active. Switch to Act (set_control_level full) so I can run it quietly. I'll always text you when money moves.",
	}, nil
}

func (a *miriamAdapter) DismissMandateSuggestion(ctx context.Context, userID uuid.UUID, suggestionID uuid.UUID) error {
	if a.suggestions == nil {
		return fmt.Errorf("mandate suggestions not available")
	}
	return a.suggestions.Dismiss(ctx, userID, suggestionID)
}

func (a *miriamAdapter) GetDecisionReceipts(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	receipts, err := a.m.ListReceipts(ctx, userID, limit)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(receipts))
	for _, r := range receipts {
		out = append(out, map[string]interface{}{
			"id":          r.ID.String(),
			"action":      r.EventType,
			"result":      r.Status,
			"description": r.Reason,
		})
	}
	return out, nil
}

// --- KnowledgeSearcher ---

func buildKnowledgeSearcher(c *Container) aicore.KnowledgeSearcher {
	if c.KnowledgeService == nil {
		return nil
	}
	return &knowledgeAdapter{k: c.KnowledgeService}
}

type knowledgeAdapter struct {
	k *knowledgesvc.Service
}

func (a *knowledgeAdapter) Search(ctx context.Context, userID uuid.UUID, query string) (map[string]interface{}, error) {
	results, err := a.k.Search(ctx, query, 5)
	if err != nil {
		return nil, nil
	}
	return map[string]interface{}{
		"results": results,
		"count":   len(results),
	}, nil
}

// --- Cache ---

func buildCacheClient(c *Container) aicore.CacheClient {
	if c.RedisClient == nil {
		return &stubCache{}
	}
	return &cacheAdapter{redis: c.RedisClient}
}

type cacheAdapter struct {
	redis cache.RedisClient
}

func (a *cacheAdapter) Get(ctx context.Context, key string) (string, error) {
	var dest string
	err := a.redis.Get(ctx, key, &dest)
	if err != nil {
		return "", err
	}
	return dest, nil
}
func (a *cacheAdapter) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return a.redis.Set(ctx, key, value, ttl)
}
func (a *cacheAdapter) Delete(ctx context.Context, key string) error {
	return a.redis.Del(ctx, key)
}

// =======================================================
// REAL PROVIDER BUILDERS — wrap actual container services
// =======================================================

func buildObligationProvider(c *Container) aicore.ObligationProvider {
	if c.FinancialObligationService == nil {
		return &stubListCreate{}
	}
	return &obligationAdapter{svc: c.FinancialObligationService}
}

func buildAutomationProvider(c *Container) aicore.AutomationProvider {
	if c.AutomationService == nil {
		return &stubListCreate{}
	}
	return &automationAdapter{svc: c.AutomationService}
}

func buildSignalProvider(c *Container) aicore.SignalProvider {
	if c.ContextSignalRepo == nil {
		return &stubGeneric{}
	}
	return &signalAdapter{repo: c.ContextSignalRepo}
}

func buildInvestmentProvider(c *Container) aicore.InvestmentProvider {
	if c.InvestingService == nil {
		return &stubGeneric{}
	}
	return &investmentAdapter{svc: c.InvestingService}
}

func buildActionAuditor(c *Container) aicore.ActionAuditor {
	if c.DomainAuditService == nil {
		return &stubGeneric{}
	}
	return &auditorAdapter{svc: c.DomainAuditService, logger: c.ZapLog}
}

func buildWithdrawalInitiator(c *Container) aicore.WithdrawalInitiator {
	if c.WithdrawalService == nil {
		return &stubGeneric{}
	}
	return &withdrawalInitAdapter{svc: c.WithdrawalService, bankRepo: c.BankAccountRepo}
}

func buildGoalProtector(c *Container) aicore.GoalProtector {
	return &goalProtectorAdapter{ledger: c.LedgerService}
}

func buildSubscriptionProvider(c *Container) aicore.SubscriptionProvider {
	if c.SubscriptionService == nil {
		return &stubGeneric{}
	}
	return &subscriptionAdapter{svc: c.SubscriptionService, obligations: c.FinancialObligationService}
}

func buildRecurringExpenseProvider(c *Container) aicore.RecurringExpenseProvider {
	if c.DB == nil {
		return &stubGeneric{}
	}
	db := sqlx.NewDb(c.DB, "postgres")
	return &recurringExpenseAdapter{repo: repositories.NewRecurringExpenseRepository(db)}
}

func buildNairaContextProvider(c *Container) aicore.ContextProvider {
	if c.NairaShieldService == nil && c.ExchangeRateRepo == nil {
		return nil
	}
	return &nairaCtxAdapter{shield: c.NairaShieldService, rates: c.ExchangeRateRepo}
}

func buildBankStatementProvider(c *Container) aicore.ContextProvider {
	if c.BankStatementRepo == nil {
		return nil
	}
	provider := aiservice.NewBankStatementContextProvider(c.BankStatementRepo)
	if provider == nil {
		return nil
	}
	return &bankStatementCtxAdapter{inner: provider}
}

func buildWarrantyProvider(c *Container) aicore.WarrantyProvider {
	if c.DB == nil {
		return &stubGeneric{}
	}
	db := sqlx.NewDb(c.DB, "postgres")
	return &warrantyAdapter{repo: repositories.NewWarrantyRepository(db)}
}

func buildPriceTracker(c *Container) aicore.PriceTrackerProvider {
	if c.DB == nil {
		return nil
	}
	db := sqlx.NewDb(c.DB, "postgres")
	return &priceTrackerAdapter{repo: repositories.NewPriceTrackingRepository(db)}
}

func buildMerchantProvider(c *Container) aicore.MerchantProvider {
	if c.DB == nil {
		return nil
	}
	db := sqlx.NewDb(c.DB, "postgres")
	return &merchantAdapter{repo: repositories.NewMerchantRepository(db)}
}

func buildSavingsSuggestionProvider(c *Container) aicore.SavingsSuggestionProvider {
	if c.ReceiptRepo == nil {
		return &stubGeneric{}
	}
	spendingSvc := spendingsvc.NewService(c.LedgerSpendingRepo)
	return &savingsSuggestionAdapter{inner: aiservice.NewSavingsSuggestionProvider(c.ReceiptRepo, spendingSvc)}
}

func buildReceiptChallengeProvider(c *Container) aicore.ReceiptChallengeProvider {
	if c.ReceiptRepo == nil {
		return &stubGeneric{}
	}
	spendingSvc := spendingsvc.NewService(c.LedgerSpendingRepo)
	return &receiptChallengeAdapter{inner: aiservice.NewReceiptChallengeProvider(c.ReceiptRepo, c.BudgetRepo, spendingSvc)}
}

func buildReportEmailSender(c *Container) aicore.ReportEmailSender {
	if c.EmailService == nil {
		return nil
	}
	return &reportEmailAdapter{svc: c.EmailService}
}

func buildSimulator(c *Container) aicore.Simulator {
	return &simulatorAdapter{}
}

type simulatorAdapter struct{}

func (s *simulatorAdapter) SimulateSavings(ctx context.Context, userID uuid.UUID, monthlyAmount decimal.Decimal, years int) (map[string]interface{}, error) {
	if monthlyAmount.IsZero() || monthlyAmount.IsNegative() {
		return map[string]interface{}{
			"error": "monthly amount must be positive",
		}, nil
	}
	months := years * 12
	totalContributed := monthlyAmount.Mul(decimal.NewFromInt(int64(months)))
	annualRate := decimal.NewFromFloat(0.07)
	monthlyRate := annualRate.Div(decimal.NewFromInt(12))
	total := decimal.Zero
	for i := 0; i < months; i++ {
		total = total.Add(monthlyAmount)
		interest := total.Mul(monthlyRate)
		total = total.Add(interest)
	}
	return map[string]interface{}{
		"monthly_amount":    monthlyAmount.String(),
		"years":             years,
		"total_contributed": totalContributed.StringFixed(2),
		"estimated_total":   total.StringFixed(2),
		"estimated_returns": total.Sub(totalContributed).StringFixed(2),
		"annual_return_pct": "7%",
	}, nil
}

func buildComparativeProvider(c *Container) aicore.ComparativeProvider {
	if c.StationService == nil {
		return &stubGeneric{}
	}
	return &comparativeAdapter{svc: c.StationService}
}

func buildTaxProvider(c *Container) aicore.TaxProvider {
	return &taxAdapter{}
}

type taxAdapter struct{}

func (t *taxAdapter) GetTaxSummary(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return map[string]interface{}{
		"available": false,
		"message":   "Tax reporting is not yet available through Miriam. Download your transaction history from the app for tax purposes.",
	}, nil
}

func (t *taxAdapter) GetTaxCalendar(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return map[string]interface{}{
		"available": false,
		"message":   "Tax calendar is not yet available through Miriam. Check the app for upcoming tax deadlines.",
	}, nil
}

func buildEmergencyWithdrawer(c *Container) aicore.EmergencyWithdrawer {
	if c.WithdrawalService == nil {
		return &stubGeneric{}
	}
	return &emergencyWithdrawAdapter{svc: c.WithdrawalService}
}

func buildFinancialGovernance(c *Container) aicore.FinancialGovernanceProvider {
	if c.MiriamIntelligenceService == nil {
		return &stubGeneric{}
	}
	return &financialGovernanceAdapter{m: c.MiriamIntelligenceService}
}

// =======================================================
// ADAPTER TYPES
// =======================================================

type obligationAdapter struct {
	svc *obligationsvc.Service
}

func (a *obligationAdapter) List(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.svc == nil {
		return nil, nil
	}
	items, err := a.svc.ListActive(ctx, userID)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		var dueDate string
		if item.DueDate != nil {
			dueDate = item.DueDate.Format("2006-01-02")
		}
		out = append(out, map[string]interface{}{
			"id":       item.ID.String(),
			"name":     item.Name,
			"amount":   item.Amount.String(),
			"due_date": dueDate,
			"status":   item.Status,
		})
	}
	return out, nil
}

func (a *obligationAdapter) Create(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error {
	if a.svc == nil {
		return fmt.Errorf("obligation service unavailable")
	}
	name := stringOrDefault(data["name"], "")
	if name == "" {
		return fmt.Errorf("obligation name is required")
	}
	amount, ok := decimalFromArg(data["amount"])
	if !ok || !amount.IsPositive() {
		return fmt.Errorf("a positive amount is required to set a reminder")
	}
	req := obligationsvc.CreateRequest{
		Type:     stringOrDefault(data["type"], entities.ObligationTypeOther),
		Name:     name,
		Amount:   amount,
		Currency: stringOrDefault(data["currency"], "USD"),
		Cadence:  obligationCadence(stringOrDefault(data["frequency"], stringOrDefault(data["cadence"], "monthly"))),
		Priority: stringOrDefault(data["priority"], entities.ObligationPriorityMedium),
		Status:   entities.ObligationStatusActive,
	}
	if dueStr := stringOrDefault(data["due_date"], ""); dueStr != "" {
		if t, perr := time.Parse("2006-01-02", dueStr); perr == nil {
			req.DueDate = &t
		}
	}
	if dd, ok := intFromArg(data["due_day"]); ok && dd >= 1 && dd <= 31 {
		req.DueDay = &dd
	}
	if cp := stringOrDefault(data["counterparty"], ""); cp != "" {
		req.Counterparty = &cp
	}
	if md, ok := data["metadata"].(map[string]interface{}); ok {
		req.Metadata = md
	}
	_, err := a.svc.Create(ctx, userID, req)
	return err
}

func (a *obligationAdapter) FindPaymentMatches(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}

func (a *obligationAdapter) MarkPaid(ctx context.Context, userID, obligationID uuid.UUID) error {
	if a.svc == nil {
		return fmt.Errorf("obligation service unavailable")
	}
	paid := entities.ObligationStatusPaid
	_, err := a.svc.Update(ctx, userID, obligationID, obligationsvc.UpdateRequest{Status: &paid})
	return err
}

type automationAdapter struct {
	svc *automationsvc.Service
}

func (a *automationAdapter) List(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.svc == nil {
		return nil, nil
	}
	items, err := a.svc.List(ctx, userID)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]interface{}{
			"id":      item.ID.String(),
			"name":    item.Name,
			"trigger": item.TriggerType,
			"action":  item.ActionType,
			"enabled": item.IsActive,
		})
	}
	return out, nil
}

func (a *automationAdapter) Create(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error {
	if a.svc == nil {
		return fmt.Errorf("automation service unavailable")
	}
	name := stringOrDefault(data["name"], "")
	if name == "" {
		return fmt.Errorf("automation name is required")
	}
	req := &automationsvc.CreateAutomationRequest{
		Name:              name,
		MaxTriggersPerDay: 1,
	}
	req.TriggerType = stringOrDefault(data["trigger_type"], "")
	req.ActionType = stringOrDefault(data["action_type"], "")
	if tc, ok := data["trigger_config"].(map[string]interface{}); ok {
		req.TriggerConfig = tc
	}
	if ac, ok := data["action_config"].(map[string]interface{}); ok {
		req.ActionConfig = ac
	}

	// Loose-schema fallback: infer a scheduled Spend<->Stash transfer from the
	// tool's {type, schedule, amount, from, to} shape. Transfer automations are
	// still refused by the service unless an in-app passcode consent stamp is
	// present — so money-moving automations set up over chat are safely rejected
	// and the user is told to finish in the app.
	if req.TriggerType == "" {
		req.TriggerType = entities.TriggerSchedule
		if sched := stringOrDefault(data["schedule"], ""); sched != "" {
			if req.TriggerConfig == nil {
				req.TriggerConfig = map[string]interface{}{}
			}
			req.TriggerConfig["schedule"] = sched
		}
	}
	if req.ActionType == "" {
		from := strings.ToLower(stringOrDefault(data["from"], ""))
		to := strings.ToLower(stringOrDefault(data["to"], ""))
		switch {
		case strings.Contains(to, "stash"):
			req.ActionType = entities.ActionTransferToStash
		case strings.Contains(to, "spend") || strings.Contains(from, "stash"):
			req.ActionType = entities.ActionTransferToSpend
		}
		if amt, ok := decimalFromArg(data["amount"]); ok && amt.IsPositive() && req.ActionConfig == nil {
			amtF, _ := amt.Float64()
			req.ActionConfig = map[string]interface{}{
				"amount":      amtF,
				"from_wallet": from,
				"to_wallet":   to,
			}
		}
	}
	if req.ActionType == "" {
		return fmt.Errorf("I couldn't tell what this automation should do — set it up in the app")
	}
	_, err := a.svc.Create(ctx, userID, req)
	return err
}

func (a *automationAdapter) Pause(ctx context.Context, userID uuid.UUID, automationID string) error {
	if a.svc == nil {
		return fmt.Errorf("automation service unavailable")
	}
	id, err := uuid.Parse(automationID)
	if err != nil {
		return fmt.Errorf("invalid automation id: %s", automationID)
	}
	inactive := false
	_, err = a.svc.Update(ctx, userID, id, &automationsvc.UpdateAutomationRequest{IsActive: &inactive})
	return err
}

func (a *automationAdapter) Resume(ctx context.Context, userID uuid.UUID, automationID string) error {
	if a.svc == nil {
		return fmt.Errorf("automation service unavailable")
	}
	id, err := uuid.Parse(automationID)
	if err != nil {
		return fmt.Errorf("invalid automation id: %s", automationID)
	}
	active := true
	_, err = a.svc.Update(ctx, userID, id, &automationsvc.UpdateAutomationRequest{IsActive: &active})
	return err
}

func (a *automationAdapter) Delete(ctx context.Context, userID uuid.UUID, automationID string) error {
	if a.svc == nil {
		return fmt.Errorf("automation service unavailable")
	}
	id, err := uuid.Parse(automationID)
	if err != nil {
		return fmt.Errorf("invalid automation id: %s", automationID)
	}
	return a.svc.Delete(ctx, userID, id)
}

type signalAdapter struct {
	repo *repositories.ContextSignalRepository
}

func (a *signalAdapter) GetActiveByUser(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.repo == nil {
		return nil, nil
	}
	signals, err := a.repo.GetActiveByUser(ctx, userID)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(signals))
	for _, s := range signals {
		out = append(out, map[string]interface{}{
			"type":       s.SignalType,
			"data":       string(s.SignalData),
			"confidence": s.Confidence.String(),
			"is_active":  s.IsActive,
		})
	}
	return out, nil
}

type investmentAdapter struct {
	svc *investingsvc.Service
}

func (a *investmentAdapter) GetProducts(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.svc == nil {
		return nil, nil
	}
	baskets, err := a.svc.ListBaskets(ctx)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(baskets))
	for _, b := range baskets {
		out = append(out, map[string]interface{}{
			"id":          b.ID.String(),
			"name":        b.Name,
			"description": b.Description,
			"risk_level":  b.RiskLevel,
		})
	}
	return out, nil
}

type auditorAdapter struct {
	svc    *audit.Service
	logger *zap.Logger
}

func (a *auditorAdapter) RecordAction(ctx context.Context, userID uuid.UUID, action string, params map[string]interface{}, status string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.Log(ctx, userID, entities.AuditAction(action), "", nil, params)
}

func (a *auditorAdapter) GetActionHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	if a.svc == nil {
		return nil, nil
	}
	logs, _, err := a.svc.GetUserAuditLogs(ctx, userID, limit, 0)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(logs))
	for _, l := range logs {
		var rid string
		if l.ResourceID != nil {
			rid = l.ResourceID.String()
		}
		out = append(out, map[string]interface{}{
			"action":      l.Action,
			"resource":    l.Resource,
			"resource_id": rid,
			"timestamp":   l.CreatedAt.Format(time.RFC3339),
			"metadata":    l.Metadata,
		})
	}
	return out, nil
}

type withdrawalInitAdapter struct {
	svc      *services.WithdrawalService
	bankRepo *repositories.BankAccountRepository
}

func (a *withdrawalInitAdapter) InitiateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, bankAccountID uuid.UUID) error {
	return fmt.Errorf("withdrawal not wired in agent wiring; use the action engine")
}

func (a *withdrawalInitAdapter) GetLinkedBanks(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.bankRepo == nil {
		return nil, nil
	}
	accounts, err := a.bankRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(accounts))
	for _, acct := range accounts {
		if acct == nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"id":             acct.ID.String(),
			"bank_name":      acct.BankName,
			"account_number": acct.AccountNumberLast4,
			"currency":       string(acct.Currency),
			"primary":        acct.IsPrimary,
			"verified":       acct.IsVerified,
		})
	}
	return out, nil
}

type goalProtectorAdapter struct {
	ledger *ledger.Service
}

func (a *goalProtectorAdapter) GetWithdrawableStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, nil
	}
	return a.ledger.GetWithdrawableStashBalance(ctx, userID)
}

func (a *goalProtectorAdapter) GetUnallocatedStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, nil
	}
	return a.ledger.GetUnallocatedStashBalance(ctx, userID)
}

type subscriptionAdapter struct {
	svc         *subscriptionsvc.Service
	obligations *obligationsvc.Service
}

func (a *subscriptionAdapter) ListSubscriptions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.svc == nil {
		return nil, nil
	}
	sub, err := a.svc.GetSubscription(ctx, userID)
	if err != nil || sub == nil {
		return nil, nil
	}
	return []map[string]interface{}{
		{
			"plan":       sub.Plan,
			"status":     string(sub.Status),
			"started":    sub.StartedAt,
			"period_end": sub.CurrentPeriodEnd,
		},
	}, nil
}

func (a *subscriptionAdapter) ProtectSubscription(ctx context.Context, userID, subID uuid.UUID) error {
	if a.obligations == nil {
		return fmt.Errorf("subscription protection unavailable")
	}
	// "Protect" flags the tracked subscription as high priority so it surfaces
	// first and is guarded against accidental cancellation.
	high := entities.ObligationPriorityHigh
	_, err := a.obligations.Update(ctx, userID, subID, obligationsvc.UpdateRequest{Priority: &high})
	return err
}

func (a *subscriptionAdapter) MarkCancelled(ctx context.Context, subID uuid.UUID) error {
	return fmt.Errorf("subscription cancellation not wired in agent wiring; use the action engine")
}

func (a *subscriptionAdapter) IgnoreSubscription(ctx context.Context, subID uuid.UUID) error {
	return fmt.Errorf("subscription ignore not wired in agent wiring; use the action engine")
}

type recurringExpenseAdapter struct {
	repo *repositories.RecurringExpenseRepository
}

func (a *recurringExpenseAdapter) GetRecurring(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.repo == nil {
		return nil, nil
	}
	expenses, err := a.repo.DetectRecurring(ctx, userID)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(expenses))
	for _, e := range expenses {
		out = append(out, map[string]interface{}{
			"merchant":   e.Merchant,
			"frequency":  e.Frequency,
			"avg_amount": e.AvgAmount.String(),
			"total":      e.Total.String(),
			"count":      e.Count,
		})
	}
	return out, nil
}

type nairaCtxAdapter struct {
	shield *premium.NairaShieldService
	rates  *repositories.ExchangeRateRepository
}

func (a *nairaCtxAdapter) GetContext(ctx context.Context, userID uuid.UUID) (string, error) {
	var parts []string
	if a.rates != nil {
		rate, err := a.rates.GetLatestRate(ctx, "USD", "NGN")
		if err == nil && !rate.IsZero() {
			parts = append(parts, fmt.Sprintf("Current USD/NGN exchange rate: ₦%s per $1", rate.StringFixed(2)))
		}
	}
	if a.shield != nil {
		report, err := a.shield.GetShieldReport(ctx, userID)
		if err == nil && report != nil {
			parts = append(parts, fmt.Sprintf("Naira shield protecting ₦%s of value", report.ShieldAmountNGN.StringFixed(2)))
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	result := "NGN Context: "
	for i, p := range parts {
		if i > 0 {
			result += "; "
		}
		result += p
	}
	return result, nil
}

type bankStatementCtxAdapter struct {
	inner *aiservice.BankStatementContextProvider
}

func (a *bankStatementCtxAdapter) GetContext(ctx context.Context, userID uuid.UUID) (string, error) {
	if a.inner == nil {
		return "", nil
	}
	return a.inner.BuildContext(ctx, userID), nil
}

// receiptP2PSplitAdapter executes equal receipt splits via the real P2P service
// (Rail tags get instant transfer requests; email/phone get claim-link invites).
type receiptP2PSplitAdapter struct {
	receipts  *repositories.ReceiptRepository
	p2p       *p2pservice.Service
	claimBase string
	logger    *zap.Logger
}

func buildReceiptProvider(c *Container) aicore.ReceiptProvider {
	if c.ReceiptRepo == nil || c.P2PService == nil {
		return &stubGeneric{}
	}
	base := ""
	if c.Config != nil {
		base = strings.TrimRight(c.Config.Email.BaseURL, "/")
	}
	return &receiptP2PSplitAdapter{
		receipts:  c.ReceiptRepo,
		p2p:       c.P2PService,
		claimBase: base,
		logger:    c.ZapLog,
	}
}

func (a *receiptP2PSplitAdapter) Split(ctx context.Context, userID, receiptID uuid.UUID, participants []string, message string) (map[string]interface{}, error) {
	return a.ExecuteSplit(ctx, userID, receiptID, participants, message)
}

// ExecuteSplit satisfies aiservice.ReceiptSplitter for the stream confirm path.
func (a *receiptP2PSplitAdapter) ExecuteSplit(ctx context.Context, userID, receiptID uuid.UUID, participants []string, message string) (map[string]interface{}, error) {
	if a == nil || a.receipts == nil || a.p2p == nil {
		return nil, fmt.Errorf("receipt split not available")
	}
	receipt, err := a.receipts.GetByID(ctx, userID, receiptID)
	if err != nil || receipt == nil {
		return nil, fmt.Errorf("receipt not found")
	}
	if !receipt.Amount.IsPositive() {
		return nil, fmt.Errorf("receipt has no valid amount to split")
	}
	tags := make([]string, 0, len(participants))
	for _, p := range participants {
		p = strings.TrimPrefix(strings.TrimSpace(p), "@")
		if p != "" {
			tags = append(tags, p)
		}
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("no valid participants")
	}
	if message == "" {
		message = "Receipt split"
	}
	totalPeople := len(tags) + 1
	share := receipt.Amount.Div(decimal.NewFromInt(int64(totalPeople))).Round(2)
	othersTotal := share.Mul(decimal.NewFromInt(int64(len(tags))))
	yourShare := receipt.Amount.Sub(othersTotal)

	splits := make([]map[string]interface{}, 0, len(tags))
	failed := 0
	for _, tag := range tags {
		resp, p2pErr := a.p2p.Send(ctx, userID, &entities.P2PSendRequest{
			Identifier:     tag,
			Amount:         share.StringFixed(2),
			Note:           message,
			IdempotencyKey: fmt.Sprintf("split-%s-%s-%s", receiptID.String(), tag, share.StringFixed(2)),
		})
		entry := map[string]interface{}{
			"identifier": tag,
			"amount":     share.StringFixed(2),
			"status":     "failed",
		}
		if p2pErr != nil {
			failed++
			entry["error"] = p2pErr.Error()
			if a.logger != nil {
				a.logger.Warn("miriam receipt split P2P failed", zap.String("tag", tag), zap.Error(p2pErr))
			}
		} else if resp != nil && resp.Transfer != nil {
			entry["status"] = string(resp.Transfer.Status)
			entry["transfer_id"] = resp.Transfer.ID.String()
			if resp.Transfer.ClaimToken != nil && *resp.Transfer.ClaimToken != "" {
				entry["claim_link"] = claimURL(a.claimBase, *resp.Transfer.ClaimToken)
				entry["invite"] = true
			}
		}
		splits = append(splits, entry)
	}
	out := map[string]interface{}{
		"receipt_id":   receipt.ID.String(),
		"total_amount": receipt.Amount.StringFixed(2),
		"your_share":   yourShare.StringFixed(2),
		"share_each":   share.StringFixed(2),
		"splits":       splits,
		"currency":     "USD",
	}
	if failed > 0 && failed < len(splits) {
		out["partial_success"] = true
		out["failed_count"] = failed
	}
	if failed == len(splits) {
		return out, fmt.Errorf("all split requests failed")
	}
	return out, nil
}

func claimURL(base, token string) string {
	if base == "" {
		return "/claim/" + token
	}
	return strings.TrimRight(base, "/") + "/claim/" + token
}

// p2pAgentAdapter powers send_money / lookup_recipient tools.
type p2pAgentAdapter struct {
	svc       *p2pservice.Service
	claimBase string
}

func buildP2PProvider(c *Container) aicore.P2PProvider {
	if c.P2PService == nil {
		return nil
	}
	base := ""
	if c.Config != nil {
		base = strings.TrimRight(c.Config.Email.BaseURL, "/")
	}
	return &p2pAgentAdapter{svc: c.P2PService, claimBase: base}
}

func (a *p2pAgentAdapter) Lookup(ctx context.Context, identifier string) (map[string]interface{}, error) {
	if a == nil || a.svc == nil {
		return nil, fmt.Errorf("P2P not available")
	}
	res, err := a.svc.LookupRecipient(ctx, identifier)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{
		"found":           res.Found,
		"can_send":        res.CanSend,
		"identifier_type": string(res.IdentifierType),
		"message":         res.Message,
	}
	if res.User != nil {
		u := map[string]interface{}{"id": res.User.ID.String()}
		if res.User.RailTag != nil {
			u["rail_tag"] = *res.User.RailTag
		}
		if res.User.FirstName != nil {
			u["first_name"] = *res.User.FirstName
		}
		out["user"] = u
	}
	if res.Found {
		out["note"] = "Recipient is on Rail — send will transfer after Face ID."
	} else if res.CanSend {
		out["note"] = "Recipient is not on Rail yet — send will reserve funds and create a claim link."
	}
	return out, nil
}

func (a *p2pAgentAdapter) Send(ctx context.Context, userID uuid.UUID, identifier, amount, note, idempotencyKey string) (map[string]interface{}, error) {
	if a == nil || a.svc == nil {
		return nil, fmt.Errorf("P2P not available")
	}
	resp, err := a.svc.Send(ctx, userID, &entities.P2PSendRequest{
		Identifier:     strings.TrimPrefix(strings.TrimSpace(identifier), "@"),
		Amount:         amount,
		Note:           note,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{
		"status":  "sent",
		"message": resp.Message,
		"amount":  amount,
		"to":      identifier,
	}
	if resp.Transfer != nil {
		out["transfer_id"] = resp.Transfer.ID.String()
		out["transfer_status"] = string(resp.Transfer.Status)
		if resp.Transfer.ClaimToken != nil && *resp.Transfer.ClaimToken != "" {
			out["claim_link"] = claimURL(a.claimBase, *resp.Transfer.ClaimToken)
			out["invite"] = true
			out["status"] = "pending_claim"
			out["message"] = "Funds reserved. Share the claim link so they can collect on Rail."
		}
	}
	return out, nil
}

type warrantyAdapter struct {
	repo *repositories.WarrantyRepository
}

func (a *warrantyAdapter) GetItems(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.repo == nil {
		return nil, nil
	}
	items, err := a.repo.GetWarrantyItems(ctx, userID)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]interface{}{
			"item_name":       item.ItemName,
			"merchant":        item.Merchant,
			"price":           item.Price,
			"category":        item.Category,
			"warranty_months": item.WarrantyMonths,
			"expiry_date":     item.ExpiryDate,
			"days_remaining":  item.DaysRemaining,
			"status":          item.Status,
		})
	}
	return out, nil
}

type priceTrackerAdapter struct {
	repo *repositories.PriceTrackingRepository
}

func (a *priceTrackerAdapter) GetChanges(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.repo == nil {
		return nil, nil
	}
	changes, err := a.repo.GetPriceChanges(ctx, userID, 10)
	if err != nil {
		return nil, nil
	}
	out := make([]map[string]interface{}, 0, len(changes))
	for _, c := range changes {
		out = append(out, map[string]interface{}{
			"item_name":      c.ItemName,
			"previous_price": c.PreviousPrice,
			"current_price":  c.CurrentPrice,
			"change_pct":     c.ChangePct,
			"currency":       c.Currency,
		})
	}
	return out, nil
}

type merchantAdapter struct {
	repo *repositories.MerchantRepository
}

func (a *merchantAdapter) GetInsights(ctx context.Context, merchantName string) (map[string]interface{}, error) {
	if a.repo == nil {
		return nil, nil
	}
	return nil, fmt.Errorf("merchant insights require user context; not wired for anonymous lookup")
}

type savingsSuggestionAdapter struct {
	inner aiservice.SavingsSuggestionProvider
}

func (a *savingsSuggestionAdapter) GetSuggestions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.inner == nil {
		return nil, nil
	}
	result, err := a.inner.GetSuggestions(ctx, userID)
	if err != nil || result == nil {
		return nil, nil
	}
	return result.Suggestions, nil
}

type receiptChallengeAdapter struct {
	inner aiservice.ReceiptChallengeProvider
}

func (a *receiptChallengeAdapter) GetChallenges(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	if a.inner == nil {
		return nil, nil
	}
	result, err := a.inner.GetChallenges(ctx, userID)
	if err != nil || result == nil {
		return nil, nil
	}
	return result.CategoryChallenges, nil
}

type reportEmailAdapter struct {
	svc *adapters.EmailService
}

func (a *reportEmailAdapter) SendReport(ctx context.Context, userID uuid.UUID, reportType string, data map[string]interface{}) error {
	return fmt.Errorf("email reporting requires user email resolution; not available in agent wiring")
}

type comparativeAdapter struct {
	svc *station.Service
}

func (a *comparativeAdapter) GetComparativeContext(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if a.svc == nil {
		return map[string]interface{}{}, nil
	}
	balances, _ := a.svc.GetUserBalances(ctx, userID)
	activity, _ := a.svc.GetRecentActivity(ctx, userID, 5)
	allocMode, _ := a.svc.GetAllocationMode(ctx, userID)
	out := map[string]interface{}{
		"allocation_mode": "auto",
	}
	if balances != nil {
		out["spend_balance"] = balances.SpendingBalance.String()
		out["stash_balance"] = balances.StashBalance.String()
		out["invest_balance"] = balances.InvestBalance.String()
		out["total_balance"] = balances.TotalBalance.String()
		out["safe_to_spend"] = balances.SafeToSpend.String()
	}
	if allocMode != nil {
		out["allocation_active"] = allocMode.Active
		out["ratio_spending"] = allocMode.RatioSpending.String()
		out["ratio_stash"] = allocMode.RatioStash.String()
	}
	if len(activity) > 0 {
		items := make([]map[string]interface{}, 0, len(activity))
		for _, act := range activity {
			if act == nil {
				continue
			}
			items = append(items, map[string]interface{}{
				"type":        act.Type,
				"amount":      act.Amount.String(),
				"description": act.Description,
				"date":        act.CreatedAt.Format(time.RFC3339),
			})
		}
		out["recent_activity"] = items
	}
	return out, nil
}

func (a *comparativeAdapter) GetSpendingComparison(ctx context.Context, userID uuid.UUID, period string) (map[string]interface{}, error) {
	if a.svc == nil {
		return map[string]interface{}{}, nil
	}
	balances, _ := a.svc.GetUserBalances(ctx, userID)
	if balances == nil {
		return map[string]interface{}{}, nil
	}
	return map[string]interface{}{
		"spend_balance":   balances.SpendingBalance.String(),
		"stash_balance":   balances.StashBalance.String(),
		"invest_balance":  balances.InvestBalance.String(),
		"total_balance":   balances.TotalBalance.String(),
		"safe_to_spend":   balances.SafeToSpend.String(),
		"comparison_type": period,
	}, nil
}

type emergencyWithdrawAdapter struct {
	svc *services.WithdrawalService
}

func (a *emergencyWithdrawAdapter) EmergencyWithdraw(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	return fmt.Errorf("emergency withdrawal not wired in agent wiring; use the action engine")
}

type financialGovernanceAdapter struct {
	m *miriamservice.Service
}

func (a *financialGovernanceAdapter) GetFinancialHealth(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if a.m == nil {
		return map[string]interface{}{}, nil
	}
	state, err := a.m.GetMoneyState(ctx, userID)
	if err != nil || state == nil {
		return map[string]interface{}{}, nil
	}
	return map[string]interface{}{
		"confidence_score":    state.ConfidenceScore,
		"confidence_level":    state.ConfidenceLevel,
		"safe_to_spend_daily": state.SafeToSpendDaily.String(),
		"runway_days":         state.LiquidityRunwayDays,
	}, nil
}

func (a *financialGovernanceAdapter) GetFinancialPlan(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if a.m == nil {
		return map[string]interface{}{}, nil
	}
	state, err := a.m.GetMoneyState(ctx, userID)
	if err != nil || state == nil {
		return map[string]interface{}{}, nil
	}
	plan := map[string]interface{}{
		"monthly_income":          state.AvgMonthlyIncome.String(),
		"monthly_spend":           state.MonthlySpend.String(),
		"monthly_savings":         state.MonthlySavings.String(),
		"safe_to_spend_daily":     state.SafeToSpendDaily.String(),
		"stash_target":            state.StashTarget.String(),
		"stash_balance":           state.StashBalance.String(),
		"spend_balance":           state.SpendBalance.String(),
		"liquidity_runway_days":   state.LiquidityRunwayDays,
		"recurring_monthly_spend": state.RecurringSpendMonthly.String(),
		"confidence_level":        state.ConfidenceLevel,
		"confidence_score":        state.ConfidenceScore,
	}
	incomeMonth := float64(state.LiquidityRunwayDays) / 30.0
	plan["runway_months"] = fmt.Sprintf("%.1f", incomeMonth)
	return plan, nil
}

func (a *financialGovernanceAdapter) GetCashFlowForecast(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if a.m == nil {
		return map[string]interface{}{}, nil
	}
	state, err := a.m.GetMoneyState(ctx, userID)
	if err != nil || state == nil {
		return map[string]interface{}{}, nil
	}
	netCashFlow := state.AvgMonthlyIncome.Sub(state.RecurringSpendMonthly)
	return map[string]interface{}{
		"avg_monthly_income":      state.AvgMonthlyIncome.String(),
		"recurring_monthly_spend": state.RecurringSpendMonthly.String(),
		"net_monthly_cash_flow":   netCashFlow.String(),
		"liquidity_runway_days":   state.LiquidityRunwayDays,
		"anomaly_count":           state.AnomalyCount,
	}, nil
}

func (a *financialGovernanceAdapter) GetFinancialAudit(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if a.m == nil {
		return map[string]interface{}{}, nil
	}
	state, err := a.m.GetMoneyState(ctx, userID)
	if err != nil || state == nil {
		return map[string]interface{}{}, nil
	}
	upcomingObligations := state.UpcomingObligations
	monthlyIncome := state.AvgMonthlyIncome
	obligationRatio := "0"
	if !monthlyIncome.IsZero() {
		ratio := upcomingObligations.Div(monthlyIncome).Mul(decimal.NewFromInt(100))
		obligationRatio = ratio.StringFixed(1) + "%"
	}
	return map[string]interface{}{
		"confidence_level":     state.ConfidenceLevel,
		"confidence_score":     state.ConfidenceScore,
		"upcoming_obligations": upcomingObligations.String(),
		"monthly_income":       monthlyIncome.String(),
		"obligation_to_income": obligationRatio,
		"anomaly_count":        state.AnomalyCount,
		"calibration_score":    state.CalibrationScore.String(),
	}, nil
}

// --- Stub types (used as fallbacks for nil container services) ---

type stubGeneric struct{}

func (s *stubGeneric) GetWeeklyStats(ctx context.Context, userID uuid.UUID) (*aicore.PortfolioStats, error) {
	return nil, nil
}
func (s *stubGeneric) GetTopMovers(ctx context.Context, userID uuid.UUID, limit int) ([]*aicore.Mover, error) {
	return nil, nil
}
func (s *stubGeneric) GetAllocations(ctx context.Context, userID uuid.UUID) ([]*aicore.Allocation, error) {
	return nil, nil
}
func (s *stubGeneric) GetStreak(ctx context.Context, userID uuid.UUID) (*aicore.StreakInfo, error) {
	return nil, nil
}
func (s *stubGeneric) GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetSpendingSummary(ctx context.Context, userID uuid.UUID, period string) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetRecentTransactions(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetMoneyFlow(ctx context.Context, userID uuid.UUID, months int) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetSpendingPatterns(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetCardTransactions(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetDepositHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetWithdrawalHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetIncomeTrend(ctx context.Context, userID uuid.UUID, months int) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetReceiptHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetYieldEarned(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetBalanceHistory(ctx context.Context, userID uuid.UUID, days int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) Transfer(ctx context.Context, userID uuid.UUID, from, to string, amount decimal.Decimal) error {
	return fmt.Errorf("not wired")
}
func (s *stubGeneric) Withdraw(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination string) error {
	return fmt.Errorf("not wired")
}
func (s *stubGeneric) SetSavingsGoal(ctx context.Context, userID uuid.UUID, name string, target decimal.Decimal) error {
	return fmt.Errorf("not wired")
}
func (s *stubGeneric) GetFinancialProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) UpdateFinancialProfile(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error {
	return nil
}
func (s *stubGeneric) GetUserProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error) {
	return decimal.NewFromInt(1), nil
}
func (s *stubGeneric) GetActiveByUser(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetMoneyState(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetMandates(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetDecisionReceipts(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) ListMandateSuggestions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) AcceptMandateSuggestion(ctx context.Context, userID uuid.UUID, suggestionID uuid.UUID) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not wired")
}
func (s *stubGeneric) DismissMandateSuggestion(ctx context.Context, userID uuid.UUID, suggestionID uuid.UUID) error {
	return fmt.Errorf("not wired")
}
func (s *stubGeneric) GetProducts(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) RecordAction(ctx context.Context, userID uuid.UUID, action string, params map[string]interface{}, status string) error {
	return nil
}
func (s *stubGeneric) GetActionHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) InitiateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, bankAccountID uuid.UUID) error {
	return fmt.Errorf("not wired")
}
func (s *stubGeneric) GetLinkedBanks(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) ListSubscriptions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) ProtectSubscription(ctx context.Context, userID, subID uuid.UUID) error {
	return nil
}
func (s *stubGeneric) MarkCancelled(ctx context.Context, subID uuid.UUID) error      { return nil }
func (s *stubGeneric) IgnoreSubscription(ctx context.Context, subID uuid.UUID) error { return nil }
func (s *stubGeneric) GetRecurring(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetSuggestions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetChallenges(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) Split(ctx context.Context, userID, receiptID uuid.UUID, participants []string, message string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("receipt split not wired")
}
func (s *stubGeneric) GetItems(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetChanges(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetInsights(ctx context.Context, merchantName string) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) SendReport(ctx context.Context, userID uuid.UUID, reportType string, data map[string]interface{}) error {
	return nil
}
func (s *stubGeneric) SimulateSavings(ctx context.Context, userID uuid.UUID, monthlyAmount decimal.Decimal, years int) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetComparativeContext(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetSpendingComparison(ctx context.Context, userID uuid.UUID, period string) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetTaxSummary(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetTaxCalendar(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) EmergencyWithdraw(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	return fmt.Errorf("not wired")
}
func (s *stubGeneric) GetFinancialHealth(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetFinancialPlan(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetCashFlowForecast(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubGeneric) GetFinancialAudit(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}

type stubBudget struct{}

func (s *stubBudget) GetByUserID(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubBudget) Upsert(ctx context.Context, budgetID uuid.UUID, data map[string]interface{}) error {
	return fmt.Errorf("not wired")
}

type stubGoal struct{}

func (s *stubGoal) GetByUserID(ctx context.Context, userID uuid.UUID) ([]aicore.Goal, error) {
	return nil, nil
}
func (s *stubGoal) Create(ctx context.Context, userID uuid.UUID, goal *aicore.Goal) error {
	return fmt.Errorf("not wired")
}
func (s *stubGoal) GetWithdrawableStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (s *stubGoal) GetUnallocatedStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

type stubListCreate struct{}

func (s *stubListCreate) List(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubListCreate) Create(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error {
	return nil
}
func (s *stubListCreate) FindPaymentMatches(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (s *stubListCreate) MarkPaid(ctx context.Context, userID, obligationID uuid.UUID) error {
	return nil
}

func (s *stubListCreate) Pause(ctx context.Context, userID uuid.UUID, automationID string) error {
	return nil
}
func (s *stubListCreate) Resume(ctx context.Context, userID uuid.UUID, automationID string) error {
	return nil
}
func (s *stubListCreate) Delete(ctx context.Context, userID uuid.UUID, automationID string) error {
	return nil
}

type stubCache struct{}

func (c *stubCache) Get(ctx context.Context, key string) (string, error) { return "", nil }
func (c *stubCache) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return nil
}
func (c *stubCache) Delete(ctx context.Context, key string) error { return nil }

// --- Helpers ---

func periodStart(t time.Time, period string) time.Time {
	switch period {
	case "week":
		return t.AddDate(0, 0, -7)
	case "month":
		return t.AddDate(0, -1, 0)
	case "quarter":
		return t.AddDate(0, -3, 0)
	default:
		return t.AddDate(0, -1, 0)
	}
}

func averageString(total decimal.Decimal, count int) string {
	if count == 0 {
		return "0"
	}
	return total.Div(decimal.NewFromInt(int64(count))).StringFixed(2)
}

// --- Argument coercion helpers for the write-path adapters ---

func decimalFromArg(v interface{}) (decimal.Decimal, bool) {
	switch x := v.(type) {
	case decimal.Decimal:
		return x, true
	case float64:
		return decimal.NewFromFloat(x), true
	case int:
		return decimal.NewFromInt(int64(x)), true
	case int64:
		return decimal.NewFromInt(x), true
	case string:
		d, err := decimal.NewFromString(strings.TrimSpace(x))
		return d, err == nil
	default:
		return decimal.Zero, false
	}
}

func intFromArg(v interface{}) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		if n, err := decimal.NewFromString(strings.TrimSpace(x)); err == nil {
			return int(n.IntPart()), true
		}
	}
	return 0, false
}

func stringOrDefault(v interface{}, fallback string) string {
	if s, ok := v.(string); ok {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return fallback
}

// obligationCadence maps the tools' loose frequency vocabulary onto the
// obligation service's validated cadence set.
func obligationCadence(freq string) string {
	switch strings.ToLower(strings.TrimSpace(freq)) {
	case "one_time", "once", "one-time":
		return entities.ObligationCadenceOneTime
	case "weekly":
		return entities.ObligationCadenceWeekly
	case "biweekly", "fortnightly":
		return entities.ObligationCadenceBiweekly
	case "quarterly":
		return entities.ObligationCadenceQuarterly
	case "yearly", "annual", "annually":
		return entities.ObligationCadenceAnnual
	default:
		return entities.ObligationCadenceMonthly
	}
}
