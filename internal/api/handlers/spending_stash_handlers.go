package handlers

import (
	"context"
	"net/http"
	"strconv"
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
	)

	wg.Add(6)
	go func() { defer wg.Done(); balances, _ = h.allocationService.GetBalances(ctx, userID) }()
	go func() { defer wg.Done(); allocationMode, _ = h.allocationService.GetMode(ctx, userID) }()
	go func() { defer wg.Done(); cards, _ = h.cardService.GetUserCards(ctx, userID) }()
	go func() { defer wg.Done(); roundupSummary, _ = h.roundupService.GetSummary(ctx, userID) }()
	go func() { defer wg.Done(); cardTxns, _ = h.cardService.GetUserTransactions(ctx, userID, limit+1, 0) }()
	go func() { defer wg.Done(); userLimits, _ = h.limitsService.GetUserLimits(ctx, userID) }()
	wg.Wait()

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
			Available:   "0.00",
			Pending:     "0.00",
			Currency:    "USD",
			LastUpdated: now,
		},
		Allocation: SpendingAllocationInfo{
			Active:        false,
			SpendingRatio: 0.70,
			StashRatio:    0.30,
			TotalReceived: "0.00",
		},
		TopCategories:         []CategorySummary{},
		PendingAuthorizations: []PendingAuthorization{},
		RecentTransactions:    TransactionListResponse{Items: []TransactionSummary{}},
		Limits: SpendingLimits{
			Daily:          LimitDetail{Limit: "1000.00", Used: "0.00", Remaining: "1000.00"},
			Monthly:        LimitDetail{Limit: "10000.00", Used: "0.00", Remaining: "10000.00"},
			PerTransaction: "500.00",
		},
		Links: SpendingLinks{
			Self:           "/api/v1/account/spending-stash",
			Transactions:   "/api/v1/transactions?type=card",
			EditLimits:     "/api/v1/limits",
			EditAllocation: "/api/v1/allocation",
			FreezeCard:     "/api/v1/card/freeze",
		},
	}

	// Balance
	if balances != nil {
		resp.Balance.Available = balances.SpendingRemaining.StringFixed(2)
		resp.Balance.LastUpdated = balances.UpdatedAt
		resp.Allocation.Active = balances.ModeActive
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
			merchantName := ""
			category := "Other"
			if tx.MerchantName != nil {
				merchantName = *tx.MerchantName
			}
			if tx.MerchantCategory != nil {
				category = *tx.MerchantCategory
			}

			if tx.Status == "pending" || tx.Type == "authorization" {
				resp.PendingAuthorizations = append(resp.PendingAuthorizations, PendingAuthorization{
					ID:           tx.ID.String(),
					MerchantName: merchantName,
					Amount:       tx.Amount.Abs().StringFixed(2),
					Currency:     tx.Currency,
					AuthorizedAt: tx.CreatedAt.Format(time.RFC3339),
					ExpiresAt:    tx.CreatedAt.Add(72 * time.Hour).Format(time.RFC3339),
					Category:     category,
				})
				pending = pending.Add(tx.Amount.Abs())
			} else {
				resp.RecentTransactions.Items = append(resp.RecentTransactions.Items, TransactionSummary{
					ID:          tx.ID.String(),
					Type:        "card",
					Amount:      tx.Amount.Neg().StringFixed(2),
					Currency:    tx.Currency,
					Description: merchantName,
					Merchant: &MerchantInfo{
						Name:         merchantName,
						Category:     category,
						CategoryIcon: categoryIcon(category),
					},
					Status:            tx.Status,
					CreatedAt:         tx.CreatedAt.Format(time.RFC3339),
					PendingSettlement: tx.Status == "authorized",
				})
			}
		}
		resp.Balance.Pending = pending.StringFixed(2)
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
		resp.Limits = SpendingLimits{
			Daily:          LimitDetail{Limit: userLimits.Withdrawal.Daily.Limit, Used: userLimits.Withdrawal.Daily.Used, Remaining: userLimits.Withdrawal.Daily.Remaining},
			Monthly:        LimitDetail{Limit: userLimits.Withdrawal.Monthly.Limit, Used: userLimits.Withdrawal.Monthly.Used, Remaining: userLimits.Withdrawal.Monthly.Remaining},
			PerTransaction: userLimits.Withdrawal.Minimum,
		}
	}

	return resp
}

func (h *SpendingStashHandlers) calculateSpendingMetrics(txns []*entities.BridgeCardTransaction) (*SpendingSummary, []CategorySummary) {
	if len(txns) == 0 {
		return &SpendingSummary{ThisMonthTotal: "0.00", DailyAverage: "0.00", Trend: "stable"}, []CategorySummary{}
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
		ThisMonthTotal:   thisMonthTotal.StringFixed(2),
		TransactionCount: count,
		Trend:            "stable",
	}

	if daysInMonth := now.Day(); daysInMonth > 0 && !thisMonthTotal.IsZero() {
		summary.DailyAverage = thisMonthTotal.Div(decimal.NewFromInt(int64(daysInMonth))).StringFixed(2)
	} else {
		summary.DailyAverage = "0.00"
	}

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
		categories = append(categories, CategorySummary{Name: name, Amount: amount.StringFixed(2), Percent: pct})
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

func categoryIcon(category string) string {
	icons := map[string]string{
		"Food & Drink":    "🍔",
		"Shopping":        "🛍️",
		"Transportation":  "🚗",
		"Entertainment":   "🎬",
		"Travel":          "✈️",
		"Health":          "💊",
		"Utilities":       "💡",
		"Groceries":       "🛒",
	}
	if icon, ok := icons[category]; ok {
		return icon
	}
	return "💳"
}
