package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// SpendingStashHandlers handles the spending stash screen endpoint
type SpendingStashHandlers struct {
	allocationService *allocation.Service
	cardService       *card.Service
	roundupService    *roundup.Service
	limitsService     *limits.Service
	logger            *zap.Logger
}

// NewSpendingStashHandlers creates new spending stash handlers
func NewSpendingStashHandlers(
	allocationService *allocation.Service,
	cardService *card.Service,
	roundupService *roundup.Service,
	limitsService *limits.Service,
	logger *zap.Logger,
) *SpendingStashHandlers {
	return &SpendingStashHandlers{
		allocationService: allocationService,
		cardService:       cardService,
		roundupService:    roundupService,
		limitsService:     limitsService,
		logger:            logger,
	}
}

// GetSpendingStash handles GET /api/v1/account/spending-stash
// @Summary Get spending stash screen data
// @Description Returns comprehensive spending data for the spending stash screen
// @Tags account
// @Produce json
// @Success 200 {object} SpendingStashResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/account/spending-stash [get]
func (h *SpendingStashHandlers) GetSpendingStash(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		warnings []string
	)

	// Data containers
	var (
		balances       *entities.AllocationBalances
		allocationMode *entities.SmartAllocationMode
		cards          []*entities.BridgeCard
		roundupSummary *entities.RoundupSummary
		cardTxns       []*entities.BridgeCardTransaction
		userLimits     *entities.UserLimitsResponse
	)

	// Parallel fetch - allocation balances
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.allocationService == nil {
			return
		}
		b, err := h.allocationService.GetBalances(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get allocation balances", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "balances_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		balances = b
		mu.Unlock()
	}()

	// Parallel fetch - allocation mode
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.allocationService == nil {
			return
		}
		m, err := h.allocationService.GetMode(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get allocation mode", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "allocation_mode_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		allocationMode = m
		mu.Unlock()
	}()

	// Parallel fetch - cards
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.cardService == nil {
			return
		}
		c, err := h.cardService.GetUserCards(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get user cards", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "card_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		cards = c
		mu.Unlock()
	}()

	// Parallel fetch - round-ups summary
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.roundupService == nil {
			return
		}
		r, err := h.roundupService.GetSummary(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get roundup summary", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "roundups_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		roundupSummary = r
		mu.Unlock()
	}()

	// Parallel fetch - card transactions (for recent transactions)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.cardService == nil {
			return
		}
		txns, err := h.cardService.GetUserTransactions(ctx, userID, 10, 0)
		if err != nil {
			h.logger.Warn("Failed to get card transactions", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "transactions_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		cardTxns = txns
		mu.Unlock()
	}()

	// Parallel fetch - user limits
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.limitsService == nil {
			return
		}
		l, err := h.limitsService.GetUserLimits(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get user limits", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "limits_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		userLimits = l
		mu.Unlock()
	}()

	wg.Wait()

	// Build response
	response := h.buildResponse(userID, balances, allocationMode, cards, roundupSummary, cardTxns, userLimits, warnings)

	c.JSON(http.StatusOK, response)
}

// buildResponse constructs the SpendingStashResponse from fetched data
func (h *SpendingStashHandlers) buildResponse(
	userID uuid.UUID,
	balances *entities.AllocationBalances,
	allocationMode *entities.SmartAllocationMode,
	cards []*entities.BridgeCard,
	roundupSummary *entities.RoundupSummary,
	cardTxns []*entities.BridgeCardTransaction,
	userLimits *entities.UserLimitsResponse,
	warnings []string,
) *SpendingStashResponse {
	response := &SpendingStashResponse{
		SpendingBalance:    "0.00",
		AvailableToSpend:   "0.00",
		PendingAmount:      "0.00",
		AllocationInfo:     SpendingAllocationInfo{Active: false, SpendingRatio: "0.70"},
		RecentTransactions: []TransactionSummary{},
		Analytics:          h.buildDefaultAnalytics(),
		Limits:             h.buildLimits(userLimits),
		Stats: SpendingStats{
			TotalSpentAllTime: "0.00",
			TotalRoundUps:     "0.00",
		},
		Warnings: warnings,
	}

	// Populate balance summary
	if balances != nil {
		response.SpendingBalance = balances.SpendingBalance.StringFixed(2)
		response.AvailableToSpend = balances.SpendingRemaining.StringFixed(2)
		response.AllocationInfo.Active = balances.ModeActive
	}

	// Populate allocation mode info
	if allocationMode != nil {
		response.AllocationInfo.Active = allocationMode.Active
		response.AllocationInfo.SpendingRatio = allocationMode.RatioSpending.StringFixed(2)
		if allocationMode.ResumedAt != nil {
			response.AllocationInfo.LastReceivedAt = allocationMode.ResumedAt.Format(time.RFC3339)
		}
	}

	// Populate card info (use first active card)
	if len(cards) > 0 {
		for _, c := range cards {
			if c.Status == entities.CardStatusActive || c.Status == entities.CardStatusFrozen {
				response.Card = &CardSummary{
					ID:              c.ID.String(),
					Type:            string(c.Type),
					Status:          string(c.Status),
					LastFour:        c.Last4,
					SpendingEnabled: c.Status == entities.CardStatusActive,
					ATMEnabled:      c.Status == entities.CardStatusActive,
					CreatedAt:       c.CreatedAt.Format(time.RFC3339),
				}
				response.Stats.CardCreatedAt = c.CreatedAt.Format(time.RFC3339)
				break
			}
		}
	}

	// Populate recent transactions
	if len(cardTxns) > 0 {
		transactions := make([]TransactionSummary, 0, len(cardTxns))
		for _, tx := range cardTxns {
			category := "Other"
			if tx.MerchantCategory != nil {
				category = *tx.MerchantCategory
			}
			categoryIcon := GetCategoryIcon(category)

			merchantName := ""
			if tx.MerchantName != nil {
				merchantName = *tx.MerchantName
			}

			transactions = append(transactions, TransactionSummary{
				ID:           tx.ID.String(),
				Type:         "card",
				Amount:       tx.Amount.Neg().StringFixed(2), // Card transactions are debits
				Currency:     tx.Currency,
				Description:  merchantName, // Use merchant name as description
				MerchantName: merchantName,
				Category:     category,
				CategoryIcon: categoryIcon,
				Status:       tx.Status,
				CreatedAt:    tx.CreatedAt.Format(time.RFC3339),
			})
		}
		response.RecentTransactions = transactions
		response.Stats.TotalTransactions = len(transactions)

		if len(transactions) > 0 {
			response.Stats.FirstTransactionAt = transactions[len(transactions)-1].CreatedAt
		}
	}

	// Populate round-ups summary
	if roundupSummary != nil && roundupSummary.Settings != nil && roundupSummary.Settings.Enabled {
		multiplier := 1
		if roundupSummary.Settings.Multiplier.IsPositive() {
			multiplier = int(roundupSummary.Settings.Multiplier.IntPart())
		}
		response.RoundUps = &RoundUpsSummary{
			Enabled:          roundupSummary.Settings.Enabled,
			Multiplier:       multiplier,
			TotalAccumulated: roundupSummary.TotalCollected.StringFixed(2),
			PendingAmount:    roundupSummary.PendingAmount.StringFixed(2),
			InvestedAmount:   roundupSummary.TotalInvested.StringFixed(2),
			ThisMonthTotal:   "0.00", // Would need additional query
			TransactionCount: roundupSummary.TransactionCount,
		}
		response.Stats.TotalRoundUps = roundupSummary.TotalCollected.StringFixed(2)
	}

	// Calculate analytics from transactions
	response.Analytics = h.calculateAnalytics(cardTxns)

	return response
}

// calculateAnalytics calculates spending analytics from transactions
func (h *SpendingStashHandlers) calculateAnalytics(txns []*entities.BridgeCardTransaction) SpendingAnalytics {
	analytics := h.buildDefaultAnalytics()

	if len(txns) == 0 {
		return analytics
	}

	now := time.Now()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var thisMonthTotal decimal.Decimal
	var thisMonthCount int
	categoryTotals := make(map[string]decimal.Decimal)
	categoryCounts := make(map[string]int)

	for _, tx := range txns {
		if tx.CreatedAt.After(thisMonthStart) || tx.CreatedAt.Equal(thisMonthStart) {
			thisMonthTotal = thisMonthTotal.Add(tx.Amount.Abs())
			thisMonthCount++

			category := "Other"
			if tx.MerchantCategory != nil {
				category = *tx.MerchantCategory
			}
			categoryTotals[category] = categoryTotals[category].Add(tx.Amount.Abs())
			categoryCounts[category]++
		}
	}

	analytics.TotalSpentThisMonth = thisMonthTotal.StringFixed(2)
	analytics.TransactionsThisMonth = thisMonthCount

	if thisMonthCount > 0 {
		avg := thisMonthTotal.Div(decimal.NewFromInt(int64(thisMonthCount)))
		analytics.AvgTransactionThisMonth = avg.StringFixed(2)
	}

	// Calculate daily average
	daysInMonth := now.Day()
	if daysInMonth > 0 {
		dailyAvg := thisMonthTotal.Div(decimal.NewFromInt(int64(daysInMonth)))
		analytics.DailyAverage = dailyAvg.StringFixed(2)
	}

	// Build top categories
	topCategories := make([]SpendingCategory, 0)
	for cat, amount := range categoryTotals {
		pct := decimal.Zero
		if !thisMonthTotal.IsZero() {
			pct = amount.Div(thisMonthTotal).Mul(decimal.NewFromInt(100))
		}
		icon := GetCategoryIcon(cat)
		topCategories = append(topCategories, SpendingCategory{
			Category:         cat,
			CategoryIcon:     icon,
			Amount:           amount.StringFixed(2),
			Percent:          pct.StringFixed(1) + "%",
			TransactionCount: categoryCounts[cat],
			Trend:            "stable",
		})
	}

	// Sort by amount (simple bubble sort for small list)
	for i := 0; i < len(topCategories); i++ {
		for j := i + 1; j < len(topCategories); j++ {
			amtI, _ := decimal.NewFromString(topCategories[i].Amount)
			amtJ, _ := decimal.NewFromString(topCategories[j].Amount)
			if amtJ.GreaterThan(amtI) {
				topCategories[i], topCategories[j] = topCategories[j], topCategories[i]
			}
		}
	}

	// Limit to top 5
	if len(topCategories) > 5 {
		topCategories = topCategories[:5]
	}
	analytics.TopCategories = topCategories

	return analytics
}

// buildDefaultAnalytics returns default analytics
func (h *SpendingStashHandlers) buildDefaultAnalytics() SpendingAnalytics {
	return SpendingAnalytics{
		TotalSpentThisMonth:     "0.00",
		TransactionsThisMonth:   0,
		AvgTransactionThisMonth: "0.00",
		TotalSpentLastMonth:     "0.00",
		TransactionsLastMonth:   0,
		SpendingChange:          "0.00",
		SpendingChangePct:       "0.00%",
		SpendingTrend:           "stable",
		TopCategories:           []SpendingCategory{},
		DailyAverage:            "0.00",
	}
}

// buildLimits returns spending limits from user limits or defaults
func (h *SpendingStashHandlers) buildLimits(userLimits *entities.UserLimitsResponse) SpendingLimits {
	defaults := SpendingLimits{
		DailySpendLimit:       "1000.00",
		DailySpendUsed:        "0.00",
		DailySpendRemaining:   "1000.00",
		DailyResetAt:          time.Now().Add(24 * time.Hour).Truncate(24 * time.Hour).Format(time.RFC3339),
		MonthlySpendLimit:     "10000.00",
		MonthlySpendUsed:      "0.00",
		MonthlySpendRemaining: "10000.00",
		PerTransactionLimit:   "500.00",
	}

	if userLimits == nil {
		return defaults
	}

	// Withdrawal is a value type, but check for zero values as safety
	withdrawal := userLimits.Withdrawal
	if withdrawal.Daily.Limit == "" || withdrawal.Monthly.Limit == "" {
		return defaults
	}

	return SpendingLimits{
		DailySpendLimit:       withdrawal.Daily.Limit,
		DailySpendUsed:        withdrawal.Daily.Used,
		DailySpendRemaining:   withdrawal.Daily.Remaining,
		DailyResetAt:          withdrawal.Daily.ResetsAt.Format(time.RFC3339),
		MonthlySpendLimit:     withdrawal.Monthly.Limit,
		MonthlySpendUsed:      withdrawal.Monthly.Used,
		MonthlySpendRemaining: withdrawal.Monthly.Remaining,
		PerTransactionLimit:   withdrawal.Minimum,
	}
}
