package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// P2PRepository interface for spending stash
type P2PRepository interface {
	GetBySender(ctx context.Context, senderID uuid.UUID, limit, offset int) ([]*entities.P2PTransfer, error)
}

// WithdrawalRepository interface for spending stash
type WithdrawalRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
}

// SpendingStashHandlers handles the spending stash screen endpoint
type SpendingStashHandlers struct {
	allocationService *allocation.Service
	cardService       *card.Service
	roundupService    *roundup.Service
	p2pRepo           P2PRepository
	withdrawalRepo    WithdrawalRepository
	logger            *zap.Logger
}

// NewSpendingStashHandlers creates new spending stash handlers
func NewSpendingStashHandlers(
	allocationService *allocation.Service,
	cardService *card.Service,
	roundupService *roundup.Service,
	logger *zap.Logger,
) *SpendingStashHandlers {
	return &SpendingStashHandlers{
		allocationService: allocationService,
		cardService:       cardService,
		roundupService:    roundupService,
		logger:            logger,
	}
}

func (h *SpendingStashHandlers) SetP2PRepo(repo P2PRepository)             { h.p2pRepo = repo }
func (h *SpendingStashHandlers) SetWithdrawalRepo(repo WithdrawalRepository) { h.withdrawalRepo = repo }

// GetSpendingStash handles GET /api/v1/account/spending-stash
func (h *SpendingStashHandlers) GetSpendingStash(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	if h.allocationService == nil || h.cardService == nil {
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{Code: "SERVICE_UNAVAILABLE", Message: "Spending service temporarily unavailable"})
		return
	}

	var (
		wg             sync.WaitGroup
		balances       *entities.AllocationBalances
		cardTxns       []*entities.BridgeCardTransaction
		p2pTransfers   []*entities.P2PTransfer
		withdrawals    []*entities.Withdrawal
		roundupSummary *entities.RoundupSummary
		balancesErr    error
		cardTxnsErr    error
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		balances, balancesErr = h.allocationService.GetBalances(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		// fetch enough for 6-month chart
		cardTxns, cardTxnsErr = h.cardService.GetUserTransactions(ctx, userID, 500, 0)
	}()
	if h.p2pRepo != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p2pTransfers, _ = h.p2pRepo.GetBySender(ctx, userID, 500, 0)
		}()
	}
	if h.withdrawalRepo != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			withdrawals, _ = h.withdrawalRepo.GetByUserID(ctx, userID, 500, 0)
		}()
	}
	if h.roundupService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			roundupSummary, _ = h.roundupService.GetSummary(ctx, userID)
		}()
	}
	wg.Wait()

	if balancesErr != nil || cardTxnsErr != nil {
		h.logger.Error("Failed to load spending stash core data",
			zap.String("user_id", userID.String()),
			zap.NamedError("balances_error", balancesErr),
			zap.NamedError("transactions_error", cardTxnsErr),
		)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "SPENDING_STASH_ERROR", Message: "Failed to retrieve spending data"})
		return
	}

	c.JSON(http.StatusOK, h.buildResponse(balances, cardTxns, p2pTransfers, withdrawals, roundupSummary))
}

func (h *SpendingStashHandlers) buildResponse(
	balances *entities.AllocationBalances,
	cardTxns []*entities.BridgeCardTransaction,
	p2pTransfers []*entities.P2PTransfer,
	withdrawals []*entities.Withdrawal,
	roundupSummary *entities.RoundupSummary,
) *SpendingStashResponse {
	resp := &SpendingStashResponse{
		Balance: SpendingBalanceInfo{
			Available:   "0.00",
			Currency:    "USD",
			LastUpdated: time.Now().UTC(),
		},
		TopCategories: []CategorySummary{},
		MonthlyChart:  []MonthlyChartBar{},
	}

	if balances != nil {
		resp.Balance.Available = balances.SpendingRemaining.StringFixed(2)
		resp.Balance.LastUpdated = balances.UpdatedAt
	}

	resp.SpendingSummary, resp.TopCategories, resp.MonthlyChart = computeSpendingAnalytics(cardTxns, p2pTransfers, withdrawals)

	if roundupSummary != nil && roundupSummary.Settings != nil && roundupSummary.Settings.Enabled {
		resp.RoundUps = &RoundUpsSummary{
			IsEnabled:        true,
			TotalAccumulated: roundupSummary.TotalCollected.StringFixed(2),
			TransactionCount: roundupSummary.TransactionCount,
		}
	}

	return resp
}

func computeSpendingAnalytics(
	cardTxns []*entities.BridgeCardTransaction,
	p2pTransfers []*entities.P2PTransfer,
	withdrawals []*entities.Withdrawal,
) (SpendingSummary, []CategorySummary, []MonthlyChartBar) {
	now := time.Now().UTC()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := thisMonthStart.AddDate(0, -1, 0)
	sixMonthsAgo := thisMonthStart.AddDate(0, -5, 0)

	type monthKey struct{ year int; month time.Month }
	cardByMonth := map[monthKey]decimal.Decimal{}
	p2pByMonth := map[monthKey]decimal.Decimal{}
	wdByMonth := map[monthKey]decimal.Decimal{}

	var thisMonth, lastMonth decimal.Decimal
	categoryTotals := map[string]decimal.Decimal{}
	count := 0

	for _, tx := range cardTxns {
		if tx.Status == "pending" || tx.Type == "authorization" || tx.Type == "refund" || tx.Status == "reversed" {
			continue
		}
		amount := tx.Amount.Abs()
		cat := "Other"
		if tx.MerchantCategory != nil && strings.TrimSpace(*tx.MerchantCategory) != "" {
			cat = strings.TrimSpace(*tx.MerchantCategory)
		}
		if !tx.CreatedAt.Before(sixMonthsAgo) {
			k := monthKey{tx.CreatedAt.Year(), tx.CreatedAt.Month()}
			cardByMonth[k] = cardByMonth[k].Add(amount)
		}
		if !tx.CreatedAt.Before(thisMonthStart) {
			thisMonth = thisMonth.Add(amount)
			categoryTotals[cat] = categoryTotals[cat].Add(amount)
			count++
		} else if !tx.CreatedAt.Before(lastMonthStart) {
			lastMonth = lastMonth.Add(amount)
		}
	}

	for _, p2p := range p2pTransfers {
		if p2p.Status != entities.P2PStatusCompleted && p2p.Status != entities.P2PStatusClaimed {
			continue
		}
		amount := p2p.Amount
		if !p2p.CreatedAt.Before(sixMonthsAgo) {
			k := monthKey{p2p.CreatedAt.Year(), p2p.CreatedAt.Month()}
			p2pByMonth[k] = p2pByMonth[k].Add(amount)
		}
		if !p2p.CreatedAt.Before(thisMonthStart) {
			thisMonth = thisMonth.Add(amount)
			categoryTotals["P2P Transfers"] = categoryTotals["P2P Transfers"].Add(amount)
			count++
		} else if !p2p.CreatedAt.Before(lastMonthStart) {
			lastMonth = lastMonth.Add(amount)
		}
	}

	for _, w := range withdrawals {
		if w.Status != entities.WithdrawalStatusCompleted {
			continue
		}
		amount := w.Amount
		if !w.CreatedAt.Before(sixMonthsAgo) {
			k := monthKey{w.CreatedAt.Year(), w.CreatedAt.Month()}
			wdByMonth[k] = wdByMonth[k].Add(amount)
		}
		cat := ""
		if w.Category != nil {
			cat = strings.TrimSpace(*w.Category)
		}
		if cat == "" {
			cat = "Crypto Withdrawal"
			if w.WithdrawalType == entities.WithdrawalTypeFiat {
				cat = "Fiat Withdrawal"
			}
		}
		if !w.CreatedAt.Before(thisMonthStart) {
			thisMonth = thisMonth.Add(amount)
			categoryTotals[cat] = categoryTotals[cat].Add(amount)
			count++
		} else if !w.CreatedAt.Before(lastMonthStart) {
			lastMonth = lastMonth.Add(amount)
		}
	}

	// Build summary
	summary := SpendingSummary{
		ThisMonthTotal:   thisMonth.StringFixed(2),
		LastMonthTotal:   lastMonth.StringFixed(2),
		TransactionCount: count,
		Trend:            "stable",
	}
	if days := now.Day(); days > 0 && !thisMonth.IsZero() {
		summary.DailyAverage = thisMonth.Div(decimal.NewFromInt(int64(days))).StringFixed(2)
	} else {
		summary.DailyAverage = "0.00"
	}
	if !lastMonth.IsZero() {
		change := thisMonth.Sub(lastMonth).Div(lastMonth).Mul(decimal.NewFromInt(100))
		pct, _ := change.Float64()
		summary.TrendChangePercent = pct
		if pct > 5 {
			summary.Trend = "up"
		} else if pct < -5 {
			summary.Trend = "down"
		}
	}

	// Top 5 categories
	categories := make([]CategorySummary, 0, len(categoryTotals))
	for name, amt := range categoryTotals {
		pct := float64(0)
		if !thisMonth.IsZero() {
			pct, _ = amt.Div(thisMonth).Mul(decimal.NewFromInt(100)).Float64()
		}
		categories = append(categories, CategorySummary{Name: name, Amount: amt.StringFixed(2), Percent: pct})
	}
	for i := 0; i < len(categories); i++ {
		for j := i + 1; j < len(categories); j++ {
			ai, _ := decimal.NewFromString(categories[i].Amount)
			aj, _ := decimal.NewFromString(categories[j].Amount)
			if aj.GreaterThan(ai) {
				categories[i], categories[j] = categories[j], categories[i]
			}
		}
	}
	if len(categories) > 5 {
		categories = categories[:5]
	}

	// 6-month stacked bar chart (oldest → newest)
	monthNames := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	chart := make([]MonthlyChartBar, 6)
	for i := 5; i >= 0; i-- {
		m := now.AddDate(0, -i, 0)
		k := monthKey{m.Year(), m.Month()}
		cardF, _ := cardByMonth[k].Float64()
		p2pF, _ := p2pByMonth[k].Float64()
		wdF, _ := wdByMonth[k].Float64()
		chart[5-i] = MonthlyChartBar{
			Month:       fmt.Sprintf("%s %d", monthNames[m.Month()-1], m.Year()),
			Card:        cardF,
			P2P:         p2pF,
			Withdrawals: wdF,
			Total:       cardF + p2pF + wdF,
		}
	}

	return summary, categories, chart
}

func formatCurrencyFromDecimal(amount decimal.Decimal, currency string, includeSign bool) string {
	symbol := strings.ToUpper(strings.TrimSpace(currency)) + " "
	if currency == "" || strings.ToUpper(currency) == "USD" {
		symbol = "$"
	}
	prefix := ""
	value := amount
	if includeSign {
		if amount.IsNegative() {
			prefix = "-"
			value = amount.Abs()
		} else if amount.IsPositive() {
			prefix = "+"
		}
	}
	return fmt.Sprintf("%s%s%s", prefix, symbol, value.StringFixed(2))
}
