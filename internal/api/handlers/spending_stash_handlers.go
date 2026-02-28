package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

var errSpendingDependencyUnavailable = errors.New("spending dependency unavailable")

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
func (h *SpendingStashHandlers) GetSpendingStash(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	var wg sync.WaitGroup
	var (
		balances       *entities.AllocationBalances
		allocationMode *entities.SmartAllocationMode
		cards          []*entities.BridgeCard
		roundupSummary *entities.RoundupSummary
		cardTxns       []*entities.BridgeCardTransaction
		userLimits     *entities.UserLimitsResponse

		balancesErr       error
		allocationModeErr error
		cardsErr          error
		roundupSummaryErr error
		cardTxnsErr       error
		userLimitsErr     error
	)

	wg.Add(4)
	go func() {
		defer wg.Done()
		if h.allocationService == nil {
			balancesErr = errSpendingDependencyUnavailable
			return
		}
		balances, balancesErr = h.allocationService.GetBalances(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		if h.allocationService == nil {
			allocationModeErr = errSpendingDependencyUnavailable
			return
		}
		allocationMode, allocationModeErr = h.allocationService.GetMode(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		if h.cardService == nil {
			cardsErr = errSpendingDependencyUnavailable
			return
		}
		cards, cardsErr = h.cardService.GetUserCards(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		if h.cardService == nil {
			cardTxnsErr = errSpendingDependencyUnavailable
			return
		}
		cardTxns, cardTxnsErr = h.cardService.GetUserTransactions(ctx, userID, limit+1, 0)
	}()
	if h.roundupService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			roundupSummary, roundupSummaryErr = h.roundupService.GetSummary(ctx, userID)
		}()
	}
	if h.limitsService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			userLimits, userLimitsErr = h.limitsService.GetUserLimits(ctx, userID)
		}()
	}
	wg.Wait()

	if errors.Is(balancesErr, errSpendingDependencyUnavailable) || errors.Is(cardTxnsErr, errSpendingDependencyUnavailable) {
		h.logger.Error("Spending stash dependencies unavailable", zap.String("user_id", userID.String()))
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{
			Code:    "SERVICE_UNAVAILABLE",
			Message: "Spending service temporarily unavailable",
		})
		return
	}

	if balancesErr != nil || cardTxnsErr != nil {
		h.logger.Error("Failed to load spending stash core data",
			zap.String("user_id", userID.String()),
			zap.NamedError("balances_error", balancesErr),
			zap.NamedError("transactions_error", cardTxnsErr),
		)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "SPENDING_STASH_ERROR",
			Message: "Failed to retrieve spending stash data",
		})
		return
	}

	if allocationModeErr != nil {
		h.logger.Warn("Failed to load allocation mode for spending stash", zap.String("user_id", userID.String()), zap.Error(allocationModeErr))
	}
	if cardsErr != nil {
		h.logger.Warn("Failed to load cards for spending stash", zap.String("user_id", userID.String()), zap.Error(cardsErr))
	}
	if roundupSummaryErr != nil {
		h.logger.Warn("Failed to load roundup summary for spending stash", zap.String("user_id", userID.String()), zap.Error(roundupSummaryErr))
	}
	if userLimitsErr != nil {
		h.logger.Warn("Failed to load limits for spending stash", zap.String("user_id", userID.String()), zap.Error(userLimitsErr))
	}

	c.JSON(http.StatusOK, h.buildResponse(balances, allocationMode, cards, roundupSummary, cardTxns, userLimits, limit))
}

func (h *SpendingStashHandlers) buildResponse(
	balances *entities.AllocationBalances,
	allocationMode *entities.SmartAllocationMode,
	cards []*entities.BridgeCard,
	roundupSummary *entities.RoundupSummary,
	cardTxns []*entities.BridgeCardTransaction,
	userLimits *entities.UserLimitsResponse,
	limit int,
) *SpendingStashResponse {
	now := time.Now().UTC()

	resp := &SpendingStashResponse{
		Balance: BalanceInfo{
			SpendingBalance:          "0.00",
			SpendingBalanceFormatted: "$0.00",
			Available:                "0.00",
			Pending:                  "0.00",
			PendingFormatted:         "$0.00",
			Currency:                 "USD",
			LastUpdated:              now,
		},
		Allocation: SpendingAllocationInfo{
			Active:                 false,
			SpendingRatio:          0.70,
			StashRatio:             0.30,
			TotalReceived:          "0.00",
			TotalReceivedFormatted: "$0.00",
			SpendingAllocated:      "0.00",
			StashAllocated:         "0.00",
			Unallocated:            "0.00",
		},
		TopCategories:         []CategorySummary{},
		PendingAuthorizations: []PendingAuthorization{},
		RecentTransactions:    TransactionListResponse{Items: []TransactionSummary{}},
		Limits: SpendingLimits{
			Daily:              LimitDetail{Limit: "0.00", Used: "0.00", Remaining: "0.00"},
			Monthly:            LimitDetail{Limit: "0.00", Used: "0.00", Remaining: "0.00"},
			PerTransaction:     "0.00",
			MinimumTransaction: "0.00",
		},
		Links: SpendingLinks{
			Self:           "/api/v1/account/spending-stash",
			Transactions:   "/api/v1/cards/transactions",
			EditLimits:     "/api/v1/limits",
			EditAllocation: "/api/v1/allocation",
			FreezeCard:     "/api/v1/cards/{id}/freeze",
		},
	}

	// Balance
	if balances != nil {
		resp.Balance.SpendingBalance = balances.SpendingBalance.StringFixed(2)
		resp.Balance.SpendingBalanceFormatted = formatCurrencyFromDecimal(balances.SpendingBalance, "USD", false)
		resp.Balance.Available = balances.SpendingRemaining.StringFixed(2)
		resp.Balance.LastUpdated = balances.UpdatedAt
		resp.Allocation.Active = balances.ModeActive
		resp.Allocation.SpendingAllocated = balances.SpendingBalance.StringFixed(2)
		resp.Allocation.StashAllocated = balances.StashBalance.StringFixed(2)
		resp.Allocation.Unallocated = balances.USDCBalance.StringFixed(2)
		total := balances.SpendingBalance.Add(balances.StashBalance).Add(balances.USDCBalance)
		resp.Allocation.TotalReceived = total.StringFixed(2)
		resp.Allocation.TotalReceivedFormatted = formatCurrencyFromDecimal(total, "USD", false)
	}

	// Allocation mode
	if allocationMode != nil {
		resp.Allocation.Active = allocationMode.Active
		spendRatio, _ := allocationMode.RatioSpending.Float64()
		stashRatio, _ := allocationMode.RatioStash.Float64()
		resp.Allocation.SpendingRatio = spendRatio
		resp.Allocation.StashRatio = stashRatio
		if allocationMode.ResumedAt != nil {
			ts := allocationMode.ResumedAt.Format(time.RFC3339)
			resp.Allocation.LastAllocationAt = &ts
		}
	}

	// Card
	if len(cards) > 0 {
		for _, c := range cards {
			if c.Status == entities.CardStatusActive || c.Status == entities.CardStatusFrozen {
				resp.Card = &CardSummary{
					ID:        c.ID.String(),
					Type:      string(c.Type),
					Network:   "visa",
					Status:    string(c.Status),
					LastFour:  c.Last4,
					IsFrozen:  c.Status == entities.CardStatusFrozen,
					CreatedAt: c.CreatedAt.Format(time.RFC3339),
				}
				break
			}
		}
	}

	// Transactions - separate pending authorizations from completed
	if len(cardTxns) > 0 {
		hasMore := len(cardTxns) > limit
		txnsToShow := cardTxns
		if hasMore {
			txnsToShow = cardTxns[:limit]
		}

		var pending decimal.Decimal
		for _, tx := range txnsToShow {
			merchantName := "Card transaction"
			category := "Other"
			if tx.MerchantName != nil {
				trimmed := strings.TrimSpace(*tx.MerchantName)
				if trimmed != "" {
					merchantName = trimmed
				}
			}
			if tx.MerchantCategory != nil {
				category = normalizeCategory(*tx.MerchantCategory)
			}

			if tx.Status == "pending" || tx.Type == "authorization" {
				pendingAmount := tx.Amount.Abs()
				resp.PendingAuthorizations = append(resp.PendingAuthorizations, PendingAuthorization{
					ID:              tx.ID.String(),
					MerchantName:    merchantName,
					Amount:          pendingAmount.StringFixed(2),
					AmountFormatted: formatCurrencyFromDecimal(pendingAmount, tx.Currency, false),
					Currency:        tx.Currency,
					AuthorizedAt:    tx.CreatedAt.Format(time.RFC3339),
					ExpiresAt:       tx.CreatedAt.Add(72 * time.Hour).Format(time.RFC3339),
					Category:        category,
				})
				pending = pending.Add(pendingAmount)
			} else {
				amount := tx.Amount.Abs().Neg()
				direction := "debit"
				if tx.Type == "refund" || tx.Status == "reversed" {
					amount = tx.Amount.Abs()
					direction = "credit"
				}

				resp.RecentTransactions.Items = append(resp.RecentTransactions.Items, TransactionSummary{
					ID:              tx.ID.String(),
					Type:            "card",
					Amount:          amount.StringFixed(2),
					AmountFormatted: formatCurrencyFromDecimal(amount, tx.Currency, true),
					Direction:       direction,
					Currency:        tx.Currency,
					Description:     merchantName,
					Merchant: &MerchantInfo{
						Name:     merchantName,
						Category: category,
					},
					Status:            tx.Status,
					CreatedAt:         tx.CreatedAt.Format(time.RFC3339),
					PendingSettlement: tx.Status == "authorized",
				})
			}
		}
		resp.Balance.Pending = pending.StringFixed(2)
		resp.Balance.PendingFormatted = formatCurrencyFromDecimal(pending, resp.Balance.Currency, false)
		resp.RecentTransactions.HasMore = hasMore
		if hasMore && len(txnsToShow) > 0 {
			cursor := txnsToShow[len(txnsToShow)-1].ID.String()
			resp.RecentTransactions.NextCursor = &cursor
		}
	}

	// Spending summary & categories
	resp.SpendingSummary, resp.TopCategories = h.calculateSpendingMetrics(cardTxns)

	// Round-ups
	if roundupSummary != nil && roundupSummary.Settings != nil && roundupSummary.Settings.Enabled {
		multiplier := 1
		if roundupSummary.Settings.Multiplier.IsPositive() {
			multiplier = int(roundupSummary.Settings.Multiplier.IntPart())
		}
		resp.RoundUps = &RoundUpsSummary{
			IsEnabled:        true,
			Multiplier:       multiplier,
			TotalAccumulated: roundupSummary.TotalCollected.StringFixed(2),
			TransactionCount: roundupSummary.TransactionCount,
		}
	}

	// Limits
	if userLimits != nil && userLimits.Withdrawal.Daily.Limit != "" {
		dailyResetsAt := userLimits.Withdrawal.Daily.ResetsAt.Format(time.RFC3339)
		monthlyResetsAt := userLimits.Withdrawal.Monthly.ResetsAt.Format(time.RFC3339)
		resp.Limits = SpendingLimits{
			Daily: LimitDetail{
				Limit:       userLimits.Withdrawal.Daily.Limit,
				Used:        userLimits.Withdrawal.Daily.Used,
				Remaining:   userLimits.Withdrawal.Daily.Remaining,
				UsedPercent: calculateUsedPercent(userLimits.Withdrawal.Daily.Used, userLimits.Withdrawal.Daily.Limit),
				ResetsAt:    &dailyResetsAt,
			},
			Monthly: LimitDetail{
				Limit:       userLimits.Withdrawal.Monthly.Limit,
				Used:        userLimits.Withdrawal.Monthly.Used,
				Remaining:   userLimits.Withdrawal.Monthly.Remaining,
				UsedPercent: calculateUsedPercent(userLimits.Withdrawal.Monthly.Used, userLimits.Withdrawal.Monthly.Limit),
				ResetsAt:    &monthlyResetsAt,
			},
			PerTransaction:     userLimits.Withdrawal.Daily.Remaining,
			MinimumTransaction: userLimits.Withdrawal.Minimum,
			DailyTransactionsRemaining: estimateDailyTransactionsRemaining(
				userLimits.Withdrawal.Daily.Remaining,
				userLimits.Withdrawal.Minimum,
			),
		}
	}

	return resp
}

func (h *SpendingStashHandlers) calculateSpendingMetrics(txns []*entities.BridgeCardTransaction) (*SpendingSummary, []CategorySummary) {
	if len(txns) == 0 {
		return &SpendingSummary{
			ThisMonthTotal:          "0.00",
			ThisMonthTotalFormatted: "$0.00",
			DailyAverage:            "0.00",
			DailyAverageFormatted:   "$0.00",
			Trend:                   "stable",
		}, []CategorySummary{}
	}

	now := time.Now()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := thisMonthStart.AddDate(0, -1, 0)

	var thisMonthTotal, lastMonthTotal decimal.Decimal
	categoryTotals := make(map[string]decimal.Decimal)
	count := 0

	for _, tx := range txns {
		if tx.Status == "pending" || tx.Type == "authorization" {
			continue
		}
		amount := tx.Amount.Abs()
		category := "Other"
		if tx.MerchantCategory != nil {
			category = *tx.MerchantCategory
		}

		if !tx.CreatedAt.Before(thisMonthStart) {
			thisMonthTotal = thisMonthTotal.Add(amount)
			categoryTotals[category] = categoryTotals[category].Add(amount)
			count++
		} else if !tx.CreatedAt.Before(lastMonthStart) {
			lastMonthTotal = lastMonthTotal.Add(amount)
		}
	}

	summary := &SpendingSummary{
		ThisMonthTotal:          thisMonthTotal.StringFixed(2),
		ThisMonthTotalFormatted: formatCurrencyFromDecimal(thisMonthTotal, "USD", false),
		TransactionCount:        count,
		Trend:                   "stable",
	}

	if daysInMonth := now.Day(); daysInMonth > 0 && !thisMonthTotal.IsZero() {
		summary.DailyAverage = thisMonthTotal.Div(decimal.NewFromInt(int64(daysInMonth))).StringFixed(2)
	} else {
		summary.DailyAverage = "0.00"
	}
	dailyAverageDecimal, _ := decimal.NewFromString(summary.DailyAverage)
	summary.DailyAverageFormatted = formatCurrencyFromDecimal(dailyAverageDecimal, "USD", false)

	if !lastMonthTotal.IsZero() {
		change := thisMonthTotal.Sub(lastMonthTotal).Div(lastMonthTotal).Mul(decimal.NewFromInt(100))
		pct, _ := change.Float64()
		summary.TrendChangePercent = pct
		if pct > 5 {
			summary.Trend = "up"
		} else if pct < -5 {
			summary.Trend = "down"
		}
	}

	// Top categories
	categories := make([]CategorySummary, 0, len(categoryTotals))
	for name, amount := range categoryTotals {
		pct := float64(0)
		if !thisMonthTotal.IsZero() {
			pct, _ = amount.Div(thisMonthTotal).Mul(decimal.NewFromInt(100)).Float64()
		}
		categories = append(categories, CategorySummary{
			Name:            name,
			Amount:          amount.StringFixed(2),
			AmountFormatted: formatCurrencyFromDecimal(amount, "USD", false),
			Percent:         pct,
		})
	}
	// Sort descending by amount
	for i := 0; i < len(categories); i++ {
		for j := i + 1; j < len(categories); j++ {
			amtI, _ := decimal.NewFromString(categories[i].Amount)
			amtJ, _ := decimal.NewFromString(categories[j].Amount)
			if amtJ.GreaterThan(amtI) {
				categories[i], categories[j] = categories[j], categories[i]
			}
		}
	}
	if len(categories) > 5 {
		categories = categories[:5]
	}

	return summary, categories
}

func estimateDailyTransactionsRemaining(dailyRemaining, minimumTxn string) int {
	remaining, err := decimal.NewFromString(dailyRemaining)
	if err != nil || !remaining.IsPositive() {
		return 0
	}

	minimum, err := decimal.NewFromString(minimumTxn)
	if err != nil || !minimum.IsPositive() {
		return 0
	}

	return int(remaining.Div(minimum).Floor().IntPart())
}

func calculateUsedPercent(used, limit string) float64 {
	usedDecimal, err := decimal.NewFromString(used)
	if err != nil || usedDecimal.IsNegative() {
		return 0
	}
	limitDecimal, err := decimal.NewFromString(limit)
	if err != nil || !limitDecimal.IsPositive() {
		return 0
	}
	pct, _ := usedDecimal.Div(limitDecimal).Mul(decimal.NewFromInt(100)).Float64()
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func formatCurrencyFromDecimal(amount decimal.Decimal, currency string, includeSign bool) string {
	normalizedCurrency := strings.ToUpper(strings.TrimSpace(currency))
	symbol := normalizedCurrency + " "
	if normalizedCurrency == "" || normalizedCurrency == "USD" {
		symbol = "$"
	}

	value := amount
	prefix := ""
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

func normalizeCategory(category string) string {
	cleaned := strings.TrimSpace(category)
	if cleaned == "" {
		return "Other"
	}
	return cleaned
}
