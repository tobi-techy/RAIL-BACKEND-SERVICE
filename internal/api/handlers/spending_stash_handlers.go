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

	var wg sync.WaitGroup

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
			return
		}
		balances = b
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
			return
		}
		allocationMode = m
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
			return
		}
		cards = c
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
			return
		}
		roundupSummary = r
	}()

	// Parallel fetch - card transactions
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.cardService == nil {
			return
		}
		txns, err := h.cardService.GetUserTransactions(ctx, userID, 10, 0)
		if err != nil {
			h.logger.Warn("Failed to get card transactions", zap.Error(err), zap.String("user_id", userID.String()))
			return
		}
		cardTxns = txns
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
			return
		}
		userLimits = l
	}()

	wg.Wait()

	// Build response
	response := h.buildResponse(userID, balances, allocationMode, cards, roundupSummary, cardTxns, userLimits)

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
) *SpendingStashResponse {
	response := &SpendingStashResponse{
		SpendingBalance:    "0.00",
		AvailableToSpend:   "0.00",
		PendingAmount:      "0.00",
		Currency:           "USD",
		AllocationInfo:     SpendingAllocationInfo{Active: false, SpendingRatio: "0.70"},
		RecentTransactions: []TransactionSummary{},
		Limits: SpendingLimits{
			Daily:          LimitDetail{Limit: "1000.00", Used: "0.00", Remaining: "1000.00"},
			Monthly:        LimitDetail{Limit: "10000.00", Used: "0.00", Remaining: "10000.00"},
			PerTransaction: "500.00",
		},
		Stats: SpendingStats{
			TotalSpentAllTime: "0.00",
		},
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
	}

	// Populate card info
	if len(cards) > 0 {
		for _, c := range cards {
			if c.Status == entities.CardStatusActive || c.Status == entities.CardStatusFrozen {
				response.Card = &CardSummary{
					ID:        c.ID.String(),
					Type:      string(c.Type),
					Status:    string(c.Status),
					LastFour:  c.Last4,
					IsFrozen:  c.Status == entities.CardStatusFrozen,
					CreatedAt: c.CreatedAt.Format(time.RFC3339),
				}
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

			merchantName := ""
			if tx.MerchantName != nil {
				merchantName = *tx.MerchantName
			}

			transactions = append(transactions, TransactionSummary{
				ID:          tx.ID.String(),
				Type:        "card",
				Amount:      tx.Amount.Neg().StringFixed(2),
				Currency:    tx.Currency,
				Description: merchantName,
				Category:    category,
				Status:      tx.Status,
				CreatedAt:   tx.CreatedAt.Format(time.RFC3339),
			})
		}
		response.RecentTransactions = transactions
		response.Stats.TotalTransactions = len(transactions)
	}

	// Calculate spending summary from transactions
	response.SpendingSummary = h.calculateSpendingSummary(cardTxns)
	response.TopCategories = h.calculateTopCategories(cardTxns)

	// Populate round-ups summary
	if roundupSummary != nil && roundupSummary.Settings != nil && roundupSummary.Settings.Enabled {
		multiplier := 1
		if roundupSummary.Settings.Multiplier.IsPositive() {
			multiplier = int(roundupSummary.Settings.Multiplier.IntPart())
		}
		response.RoundUps = &RoundUpsSummary{
			IsEnabled:        roundupSummary.Settings.Enabled,
			Multiplier:       multiplier,
			TotalAccumulated: roundupSummary.TotalCollected.StringFixed(2),
			TransactionCount: roundupSummary.TransactionCount,
		}
	}

	// Populate limits
	response.Limits = h.buildLimits(userLimits)

	return response
}

// calculateSpendingSummary calculates spending summary from transactions
func (h *SpendingStashHandlers) calculateSpendingSummary(txns []*entities.BridgeCardTransaction) *SpendingSummary {
	summary := &SpendingSummary{
		ThisMonthTotal: "0.00",
		DailyAverage:   "0.00",
		Trend:          "stable",
	}

	if len(txns) == 0 {
		return summary
	}

	now := time.Now()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var thisMonthTotal decimal.Decimal
	for _, tx := range txns {
		if tx.CreatedAt.After(thisMonthStart) || tx.CreatedAt.Equal(thisMonthStart) {
			thisMonthTotal = thisMonthTotal.Add(tx.Amount.Abs())
		}
	}

	summary.ThisMonthTotal = thisMonthTotal.StringFixed(2)
	summary.TransactionCount = len(txns)

	// Calculate daily average
	daysInMonth := now.Day()
	if daysInMonth > 0 && !thisMonthTotal.IsZero() {
		dailyAvg := thisMonthTotal.Div(decimal.NewFromInt(int64(daysInMonth)))
		summary.DailyAverage = dailyAvg.StringFixed(2)
	}

	return summary
}

// calculateTopCategories calculates top spending categories
func (h *SpendingStashHandlers) calculateTopCategories(txns []*entities.BridgeCardTransaction) []CategorySummary {
	if len(txns) == 0 {
		return []CategorySummary{}
	}

	now := time.Now()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var thisMonthTotal decimal.Decimal
	categoryTotals := make(map[string]decimal.Decimal)

	for _, tx := range txns {
		if tx.CreatedAt.After(thisMonthStart) || tx.CreatedAt.Equal(thisMonthStart) {
			amount := tx.Amount.Abs()
			thisMonthTotal = thisMonthTotal.Add(amount)

			category := "Other"
			if tx.MerchantCategory != nil {
				category = *tx.MerchantCategory
			}
			categoryTotals[category] = categoryTotals[category].Add(amount)
		}
	}

	if thisMonthTotal.IsZero() {
		return []CategorySummary{}
	}

	// Build category summaries
	categories := make([]CategorySummary, 0, len(categoryTotals))
	for name, amount := range categoryTotals {
		pct, _ := amount.Div(thisMonthTotal).Mul(decimal.NewFromInt(100)).Float64()
		categories = append(categories, CategorySummary{
			Name:    name,
			Amount:  amount.StringFixed(2),
			Percent: pct,
		})
	}

	// Sort by amount (descending)
	for i := 0; i < len(categories); i++ {
		for j := i + 1; j < len(categories); j++ {
			amtI, _ := decimal.NewFromString(categories[i].Amount)
			amtJ, _ := decimal.NewFromString(categories[j].Amount)
			if amtJ.GreaterThan(amtI) {
				categories[i], categories[j] = categories[j], categories[i]
			}
		}
	}

	// Limit to top 5
	if len(categories) > 5 {
		categories = categories[:5]
	}

	return categories
}

// buildLimits returns spending limits from user limits or defaults
func (h *SpendingStashHandlers) buildLimits(userLimits *entities.UserLimitsResponse) SpendingLimits {
	defaults := SpendingLimits{
		Daily:          LimitDetail{Limit: "1000.00", Used: "0.00", Remaining: "1000.00"},
		Monthly:        LimitDetail{Limit: "10000.00", Used: "0.00", Remaining: "10000.00"},
		PerTransaction: "500.00",
	}

	if userLimits == nil {
		return defaults
	}

	withdrawal := userLimits.Withdrawal
	if withdrawal.Daily.Limit == "" || withdrawal.Monthly.Limit == "" {
		return defaults
	}

	return SpendingLimits{
		Daily: LimitDetail{
			Limit:     withdrawal.Daily.Limit,
			Used:      withdrawal.Daily.Used,
			Remaining: withdrawal.Daily.Remaining,
		},
		Monthly: LimitDetail{
			Limit:     withdrawal.Monthly.Limit,
			Used:      withdrawal.Monthly.Used,
			Remaining: withdrawal.Monthly.Remaining,
		},
		PerTransaction: withdrawal.Minimum,
	}
}
