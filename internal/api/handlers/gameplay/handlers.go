package gameplay

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	gameplayservice "github.com/rail-service/rail_service/internal/domain/services/gameplay"
	subscriptionsvc "github.com/rail-service/rail_service/internal/domain/services/subscription"
	"go.uber.org/zap"
)

// HeatmapRepo provides deposit dates for the activity heatmap
type HeatmapRepo interface {
	GetDepositDates(ctx context.Context, userID uuid.UUID, since time.Time) ([]time.Time, error)
}

// Handlers handles gameplay API endpoints
type Handlers struct {
	xpSvc          *gameplayservice.XPService
	streakSvc      *gameplayservice.StreakService
	challengeSvc   *gameplayservice.ChallengeService
	achievementSvc *gameplayservice.AchievementService
	subSvc         *subscriptionsvc.Service
	ringsSvc       *gameplayservice.RingsService
	boostsSvc      *gameplayservice.BoostService
	pointsSvc      *gameplayservice.PointsService
	graceDaySvc    *gameplayservice.GraceDayService
	recapSvc       *gameplayservice.RecapService
	repo           HeatmapRepo
	logger         *zap.Logger
}

func NewHandlers(
	xpSvc *gameplayservice.XPService,
	streakSvc *gameplayservice.StreakService,
	challengeSvc *gameplayservice.ChallengeService,
	achievementSvc *gameplayservice.AchievementService,
	subSvc *subscriptionsvc.Service,
	logger *zap.Logger,
) *Handlers {
	return &Handlers{xpSvc: xpSvc, streakSvc: streakSvc, challengeSvc: challengeSvc, achievementSvc: achievementSvc, subSvc: subSvc, logger: logger}
}

func (h *Handlers) SetHeatmapRepo(r HeatmapRepo) { h.repo = r }

// SetRingsService wires the rings service
func (h *Handlers) SetRingsService(s *gameplayservice.RingsService) { h.ringsSvc = s }

// SetBoostService wires the boost service
func (h *Handlers) SetBoostService(s *gameplayservice.BoostService) { h.boostsSvc = s }

// SetPointsService wires the points service
func (h *Handlers) SetPointsService(s *gameplayservice.PointsService) { h.pointsSvc = s }

// SetGraceDayService wires the grace day service
func (h *Handlers) SetGraceDayService(s *gameplayservice.GraceDayService) { h.graceDaySvc = s }

// SetRecapService wires the recap service
func (h *Handlers) SetRecapService(s *gameplayservice.RecapService) { h.recapSvc = s }

func (h *Handlers) getUserID(c *gin.Context) (uuid.UUID, bool) {
	val, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return uuid.Nil, false
	}
	uid, ok := val.(uuid.UUID)
	if !ok {
		str, strOk := val.(string)
		if !strOk {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid user"})
			return uuid.Nil, false
		}
		parsed, err := uuid.Parse(str)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid user"})
			return uuid.Nil, false
		}
		return parsed, true
	}
	return uid, true
}

// GetProfile returns combined gameplay profile (level, XP, streaks, challenges, badges)
func (h *Handlers) GetProfile(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	xp, _ := h.xpSvc.GetUserXP(ctx, userID)
	streaks, _ := h.streakSvc.GetUserStreaks(ctx, userID)
	challenges, _ := h.challengeSvc.GetActiveChallenges(ctx, userID)
	allAch, earnedAch, _ := h.achievementSvc.GetUserAchievements(ctx, userID)

	level, title := entities.LevelForXP(xp.TotalXP)
	nextLevelXP := nextLevelThreshold(xp.TotalXP)
	progressPct := float64(0)
	if nextLevelXP > 0 {
		currentLevelXP := currentLevelThreshold(xp.TotalXP)
		progressPct = float64(xp.TotalXP-currentLevelXP) / float64(nextLevelXP-currentLevelXP) * 100
	}

	c.JSON(http.StatusOK, gin.H{
		"level":               level,
		"level_title":         title,
		"total_xp":            xp.TotalXP,
		"xp_progress_pct":     progressPct,
		"next_level_xp":       nextLevelXP,
		"streaks":             streaks,
		"active_challenges":   challenges,
		"achievements_earned": len(earnedAch),
		"achievements_total":  len(allAch),
		"rail_points":         h.getPointBalance(ctx, userID),
		"today_rings":         h.getTodayRings(ctx, userID),
		"active_boost":        h.getActiveBoost(ctx, userID),
		"grace_days":          h.getGraceDayCount(ctx, userID),
	})
}

// helpers for GetProfile — return nil-safe values
func (h *Handlers) getPointBalance(ctx context.Context, userID uuid.UUID) int64 {
	if h.pointsSvc == nil {
		return 0
	}
	rp, err := h.pointsSvc.GetBalance(ctx, userID)
	if err != nil || rp == nil {
		return 0
	}
	return rp.Balance
}

func (h *Handlers) getTodayRings(ctx context.Context, userID uuid.UUID) *entities.DailyRing {
	if h.ringsSvc == nil {
		return nil
	}
	ring, _ := h.ringsSvc.GetTodayRings(ctx, userID)
	return ring
}

func (h *Handlers) getActiveBoost(ctx context.Context, userID uuid.UUID) *entities.UserBoost {
	if h.boostsSvc == nil {
		return nil
	}
	ub, _ := h.boostsSvc.GetActiveBoost(ctx, userID)
	return ub
}

func (h *Handlers) getGraceDayCount(ctx context.Context, userID uuid.UUID) int {
	if h.graceDaySvc == nil {
		return 0
	}
	gd, err := h.graceDaySvc.GetStatus(ctx, userID)
	if err != nil || gd == nil {
		return 0
	}
	return gd.Remaining
}

// GetStreaks returns all streaks for the user
func (h *Handlers) GetStreaks(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	streaks, err := h.streakSvc.GetUserStreaks(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get streaks"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"streaks": streaks})
}

// GetActivityHeatmap returns deposit dates for the last 90 days
func (h *Handlers) GetActivityHeatmap(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	since := time.Now().AddDate(0, -3, 0)
	dates, err := h.repo.GetDepositDates(c.Request.Context(), userID, since)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get activity"})
		return
	}
	// Convert to date strings
	dateStrs := make([]string, len(dates))
	for i, d := range dates {
		dateStrs[i] = d.Format("2006-01-02")
	}
	c.JSON(http.StatusOK, gin.H{"dates": dateStrs})
}

// GetXP returns XP and level info
func (h *Handlers) GetXP(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	xp, err := h.xpSvc.GetUserXP(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get xp"})
		return
	}
	level, title := entities.LevelForXP(xp.TotalXP)
	c.JSON(http.StatusOK, gin.H{
		"total_xp":      xp.TotalXP,
		"level":         level,
		"level_title":   title,
		"next_level_xp": nextLevelThreshold(xp.TotalXP),
	})
}

// GetXPHistory returns paginated XP events
func (h *Handlers) GetXPHistory(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	events, err := h.xpSvc.GetXPHistory(c.Request.Context(), userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get xp history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

// GetChallenges returns active challenges with progress
func (h *Handlers) GetChallenges(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	challenges, err := h.challengeSvc.GetActiveChallenges(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get challenges"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenges": challenges})
}

// GetAchievements returns all achievements with locked/unlocked status
func (h *Handlers) GetAchievements(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	all, earned, err := h.achievementSvc.GetUserAchievements(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get achievements"})
		return
	}
	earnedMap := make(map[uuid.UUID]bool)
	for _, e := range earned {
		earnedMap[e.AchievementID] = true
	}
	type achResponse struct {
		*entities.Achievement
		Unlocked bool `json:"unlocked"`
	}
	resp := make([]achResponse, 0, len(all))
	for _, a := range all {
		resp = append(resp, achResponse{Achievement: a, Unlocked: earnedMap[a.ID]})
	}
	c.JSON(http.StatusOK, gin.H{"achievements": resp})
}

// GetLeaderboard returns leaderboard (Pro only, placeholder for Phase 4)
func (h *Handlers) GetLeaderboard(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"leaderboard": []interface{}{}, "message": "Coming soon"})
}

// GetSubscription returns current subscription status
func (h *Handlers) GetSubscription(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	sub, err := h.subSvc.GetSubscription(c.Request.Context(), userID)
	if err != nil || sub == nil {
		c.JSON(http.StatusOK, gin.H{"subscription": nil, "is_pro": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscription": sub, "is_pro": sub.IsActive()})
}

// Subscribe creates a new Pro subscription
func (h *Handlers) Subscribe(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Plan string `json:"plan"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Plan == "" {
		body.Plan = "pro_monthly"
	}
	sub, err := h.subSvc.Subscribe(c.Request.Context(), userID, body.Plan)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"subscription": sub})
}

// CancelSubscription cancels the Pro subscription
func (h *Handlers) CancelSubscription(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	if err := h.subSvc.Cancel(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Subscription cancelled. Access continues until period end."})
}

// helpers

func nextLevelThreshold(totalXP int64) int64 {
	for _, t := range entities.LevelThresholds {
		if totalXP < t.XP {
			return t.XP
		}
	}
	return 0 // max level
}

func currentLevelThreshold(totalXP int64) int64 {
	var current int64
	for _, t := range entities.LevelThresholds {
		if totalXP >= t.XP {
			current = t.XP
		}
	}
	return current
}

// --- Rings ---

// GetRings returns today's ring progress and this week's rings
func (h *Handlers) GetRings(c *gin.Context) {
	if h.ringsSvc == nil {
		c.JSON(http.StatusOK, gin.H{"today": nil, "week": []interface{}{}})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	today, _ := h.ringsSvc.GetTodayRings(ctx, userID)
	week, _ := h.ringsSvc.GetWeekRings(ctx, userID)
	c.JSON(http.StatusOK, gin.H{"today": today, "week": week})
}

// --- Boosts ---

// GetBoosts returns available boosts and the user's active boost
func (h *Handlers) GetBoosts(c *gin.Context) {
	if h.boostsSvc == nil {
		c.JSON(http.StatusOK, gin.H{"available": []interface{}{}, "active": nil})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	available, _ := h.boostsSvc.GetAvailableBoosts(ctx)
	active, _ := h.boostsSvc.GetActiveBoost(ctx, userID)
	c.JSON(http.StatusOK, gin.H{"available": available, "active": active})
}

// ActivateBoost activates a boost for the user
func (h *Handlers) ActivateBoost(c *gin.Context) {
	if h.boostsSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "boosts not available"})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	var body struct {
		BoostID string `json:"boost_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "boost_id required"})
		return
	}
	boostID, err := uuid.Parse(body.BoostID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid boost_id"})
		return
	}
	ub, err := h.boostsSvc.ActivateBoost(c.Request.Context(), userID, boostID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"boost": ub})
}

// GetBoostHistory returns the user's boost history
func (h *Handlers) GetBoostHistory(c *gin.Context) {
	if h.boostsSvc == nil {
		c.JSON(http.StatusOK, gin.H{"history": []interface{}{}})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	history, err := h.boostsSvc.GetHistory(c.Request.Context(), userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get boost history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": history})
}

// --- Rail Points ---

// GetRailPoints returns the user's point balance and recent history
func (h *Handlers) GetRailPoints(c *gin.Context) {
	if h.pointsSvc == nil {
		c.JSON(http.StatusOK, gin.H{"balance": nil, "history": []interface{}{}})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	balance, _ := h.pointsSvc.GetBalance(ctx, userID)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	history, _ := h.pointsSvc.GetHistory(ctx, userID, limit, offset)
	c.JSON(http.StatusOK, gin.H{"balance": balance, "history": history})
}

// --- Grace Days ---

// GetGraceDays returns the user's grace day status
func (h *Handlers) GetGraceDays(c *gin.Context) {
	if h.graceDaySvc == nil {
		c.JSON(http.StatusOK, gin.H{"grace_days": nil})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	gd, _ := h.graceDaySvc.GetStatus(c.Request.Context(), userID)
	c.JSON(http.StatusOK, gin.H{"grace_days": gd})
}

// PurchaseGraceDay buys a grace day with rail points
func (h *Handlers) PurchaseGraceDay(c *gin.Context) {
	if h.graceDaySvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "grace days not available"})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	if err := h.graceDaySvc.Purchase(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request. Please check your input."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Grace Day purchased!", "cost": entities.GraceDayPointCost})
}

// --- Weekly Recap ---

// GetWeeklyRecap returns the latest weekly recap
func (h *Handlers) GetWeeklyRecap(c *gin.Context) {
	if h.recapSvc == nil {
		c.JSON(http.StatusOK, gin.H{"recap": nil})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	recap, err := h.recapSvc.GetLatest(c.Request.Context(), userID)
	if err != nil || recap == nil {
		c.JSON(http.StatusOK, gin.H{"recap": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"recap": recap})
}

// GetWeeklyRecapHistory returns recent weekly recaps
func (h *Handlers) GetWeeklyRecapHistory(c *gin.Context) {
	if h.recapSvc == nil {
		c.JSON(http.StatusOK, gin.H{"recaps": []interface{}{}})
		return
	}
	userID, ok := h.getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "12"))
	recaps, err := h.recapSvc.GetHistory(c.Request.Context(), userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get recaps"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"recaps": recaps})
}
