package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// PortfolioActivityHandlers handles portfolio and activity endpoints
type PortfolioActivityHandlers struct {
	portfolioProvider aiservice.PortfolioDataProvider
	activityProvider  aiservice.ActivityDataProvider
	streakRepo        InvestmentStreakRepository
	contributionsRepo UserContributionsRepository
	logger            *logger.Logger
}

// InvestmentStreakRepository interface
type InvestmentStreakRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error)
}

// UserContributionsRepository interface
type UserContributionsRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, contributionType *entities.ContributionType, startDate, endDate *time.Time, limit, offset int) ([]*entities.UserContribution, error)
	GetTotalByType(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (map[entities.ContributionType]string, error)
}

// NewPortfolioActivityHandlers creates new handlers
func NewPortfolioActivityHandlers(
	portfolioProvider aiservice.PortfolioDataProvider,
	activityProvider aiservice.ActivityDataProvider,
	streakRepo InvestmentStreakRepository,
	contributionsRepo UserContributionsRepository,
	logger *logger.Logger,
) *PortfolioActivityHandlers {
	return &PortfolioActivityHandlers{
		portfolioProvider: portfolioProvider,
		activityProvider:  activityProvider,
		streakRepo:        streakRepo,
		contributionsRepo: contributionsRepo,
		logger:            logger,
	}
}

// GetWeeklyStats handles GET /api/v1/portfolio/weekly-stats
func (h *PortfolioActivityHandlers) GetWeeklyStats(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	stats, err := h.portfolioProvider.GetWeeklyStats(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get weekly stats", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get stats"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total_value":       stats.TotalValue.String(),
		"weekly_return":     stats.WeeklyReturn.String(),
		"weekly_return_pct": stats.WeeklyReturnPct.String(),
		"monthly_return":    stats.MonthlyReturn.String(),
		"total_gain_loss":   stats.TotalGainLoss.String(),
	})
}

// GetAllocations handles GET /api/v1/portfolio/allocations
func (h *PortfolioActivityHandlers) GetAllocations(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	allocations, err := h.portfolioProvider.GetAllocations(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get allocations", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get allocations"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"allocations": allocations})
}

// GetTopMovers handles GET /api/v1/portfolio/top-movers
func (h *PortfolioActivityHandlers) GetTopMovers(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	limit := 5
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	movers, err := h.portfolioProvider.GetTopMovers(c.Request.Context(), userID, limit)
	if err != nil {
		h.logger.Error("Failed to get top movers", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get top movers"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"movers": movers})
}

// GetPerformance handles GET /api/v1/portfolio/performance
func (h *PortfolioActivityHandlers) GetPerformance(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	period := c.DefaultQuery("period", "1w")

	stats, err := h.portfolioProvider.GetWeeklyStats(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get performance", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get performance"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"period":     period,
		"return":     stats.WeeklyReturn.String(),
		"return_pct": stats.WeeklyReturnPct.String(),
	})
}

// GetContributions handles GET /api/v1/activity/contributions
func (h *PortfolioActivityHandlers) GetContributions(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	contributionType := c.Query("type")
	period := c.DefaultQuery("period", "1w")

	now := time.Now()
	var startDate time.Time
	switch period {
	case "1m":
		startDate = now.AddDate(0, -1, 0)
	case "3m":
		startDate = now.AddDate(0, -3, 0)
	case "1y":
		startDate = now.AddDate(-1, 0, 0)
	default:
		startDate = now.AddDate(0, 0, -7)
	}

	var contribType *entities.ContributionType
	if contributionType != "" && contributionType != "all" {
		ct := entities.ContributionType(contributionType)
		contribType = &ct
	}

	contributions, err := h.contributionsRepo.GetByUserID(c.Request.Context(), userID, contribType, &startDate, &now, 100, 0)
	if err != nil {
		h.logger.Error("Failed to get contributions", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get contributions"})
		return
	}

	totals, err := h.contributionsRepo.GetTotalByType(c.Request.Context(), userID, startDate, now)
	if err != nil {
		h.logger.Warn("Failed to get contribution totals", "error", err)
		totals = make(map[entities.ContributionType]string)
	}

	c.JSON(http.StatusOK, gin.H{
		"contributions": contributions,
		"totals":        totals,
		"period":        period,
	})
}

// GetStreak handles GET /api/v1/activity/streak
func (h *PortfolioActivityHandlers) GetStreak(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	streak, err := h.streakRepo.GetByUserID(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get streak", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get streak"})
		return
	}

	// Handle nil streak - return safe defaults
	if streak == nil {
		c.JSON(http.StatusOK, gin.H{
			"current_streak":       0,
			"longest_streak":       0,
			"last_investment_date": nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"current_streak":       streak.CurrentStreak,
		"longest_streak":       streak.LongestStreak,
		"last_investment_date": streak.LastInvestmentDate,
	})
}

// GetTimeline handles GET /api/v1/activity/timeline
func (h *PortfolioActivityHandlers) GetTimeline(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	startStr := c.Query("start")
	endStr := c.Query("end")

	var startDate, endDate time.Time
	var parseErr error

	if startStr != "" {
		startDate, parseErr = time.Parse("2006-01-02", startStr)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start date format"})
			return
		}
	} else {
		startDate = time.Now().AddDate(0, 0, -30)
	}

	if endStr != "" {
		endDate, parseErr = time.Parse("2006-01-02", endStr)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end date format"})
			return
		}
	} else {
		endDate = time.Now()
	}

	contributions, err := h.contributionsRepo.GetByUserID(c.Request.Context(), userID, nil, &startDate, &endDate, 100, 0)
	if err != nil {
		h.logger.Error("Failed to get timeline", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get timeline"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"timeline":   contributions,
		"start_date": startDate.Format("2006-01-02"),
		"end_date":   endDate.Format("2006-01-02"),
	})
}
