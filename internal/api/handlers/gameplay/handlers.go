package gameplay

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	gameplayservice "github.com/rail-service/rail_service/internal/domain/services/gameplay"
	subscriptionsvc "github.com/rail-service/rail_service/internal/domain/services/subscription"
	"go.uber.org/zap"
)

// Handlers handles gameplay API endpoints
type Handlers struct {
	xpSvc          *gameplayservice.XPService
	streakSvc      *gameplayservice.StreakService
	challengeSvc   *gameplayservice.ChallengeService
	achievementSvc *gameplayservice.AchievementService
	subSvc         *subscriptionsvc.Service
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
		"level":            level,
		"level_title":      title,
		"total_xp":         xp.TotalXP,
		"xp_progress_pct":  progressPct,
		"next_level_xp":    nextLevelXP,
		"streaks":          streaks,
		"active_challenges": challenges,
		"achievements_earned": len(earnedAch),
		"achievements_total":  len(allAch),
	})
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
	sub, err := h.subSvc.Subscribe(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
