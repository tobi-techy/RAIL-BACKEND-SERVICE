package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// GameplayProvider reads streaks, challenges, and achievements from the
// gameplay system so Miriam can reference them conversationally.
type GameplayProvider interface {
	GetUserStreaks(ctx context.Context, userID uuid.UUID) ([]*entities.UserStreak, error)
	GetActiveChallenges(ctx context.Context, userID uuid.UUID) ([]*entities.UserChallenge, error)
	GetUserAchievements(ctx context.Context, userID uuid.UUID) ([]*entities.Achievement, []*entities.UserAchievement, error)
}

// Tool names for gameplay tools.
const (
	ToolGetSavingsStreaks = "get_savings_streaks"
	ToolGetChallenges     = "get_active_challenges"
	ToolGetAchievements   = "get_achievements"
)

// SetGameplayProvider wires the gameplay provider.
func (a *AgentAdapter) SetGameplayProvider(g GameplayProvider) {
	a.gameplayProvider = g
}

// GameplayTools returns tool definitions for gameplay-related queries.
func GameplayTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolGetSavingsStreaks,
			Description: "Get the user's savings streaks (deposit streak, no-spend days, stash growth, roundup streaks). Call when user asks 'what's my streak', 'how long have I been saving', 'am I on a streak', or mentions streaks.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        ToolGetChallenges,
			Description: "Get the user's active challenges and their progress. Call when user asks 'what are my challenges', 'what challenges am I doing', 'challenge progress', or mentions challenges.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        ToolGetAchievements,
			Description: "Get the user's earned achievements and unearned achievement list. Call when user asks 'what have I achieved', 'my achievements', 'badges', or 'what milestones have I hit'.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

// executeGetSavingsStreaks returns the user's gameplay streaks formatted for the LLM.
func (o *AgentAdapter) executeGetSavingsStreaks(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.gameplayProvider == nil {
		return map[string]interface{}{"error": "Gameplay service is not available"}, nil
	}
	streaks, err := o.gameplayProvider.GetUserStreaks(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get streaks: %w", err)
	}
	if len(streaks) == 0 {
		return map[string]interface{}{"message": "No active streaks yet. Make a deposit or save into Stash to start one!"}, nil
	}
	streakList := make([]map[string]interface{}, 0, len(streaks))
	for _, s := range streaks {
		streakList = append(streakList, map[string]interface{}{
			"type":          humanStreakType(s.StreakType),
			"current":       s.CurrentCount,
			"longest":       s.LongestCount,
			"last_activity": formatTimePtr(s.LastActivityAt),
		})
	}
	return map[string]interface{}{"streaks": streakList}, nil
}

// executeGetChallenges returns the user's active challenges formatted for the LLM.
func (o *AgentAdapter) executeGetChallenges(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.gameplayProvider == nil {
		return map[string]interface{}{"error": "Gameplay service is not available"}, nil
	}
	challenges, err := o.gameplayProvider.GetActiveChallenges(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get challenges: %w", err)
	}
	if len(challenges) == 0 {
		return map[string]interface{}{"message": "No active challenges right now. Check back next week for new ones!"}, nil
	}
	challengeList := make([]map[string]interface{}, 0, len(challenges))
	for _, c := range challenges {
		if c.Challenge == nil {
			continue
		}
		entry := map[string]interface{}{
			"title":       c.Challenge.Title,
			"description": c.Challenge.Description,
			"progress":    c.Progress.StringFixed(2),
			"target":      c.Challenge.TargetValue.StringFixed(2),
			"xp_reward":   c.Challenge.XPReward,
			"status":      string(c.Status),
		}
		if c.Challenge.TargetValue.GreaterThan(c.Progress) {
			entry["remaining"] = c.Challenge.TargetValue.Sub(c.Progress).StringFixed(2)
		}
		challengeList = append(challengeList, entry)
	}
	return map[string]interface{}{"challenges": challengeList}, nil
}

// executeGetAchievements returns the user's achievements formatted for the LLM.
func (o *AgentAdapter) executeGetAchievements(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.gameplayProvider == nil {
		return map[string]interface{}{"error": "Gameplay service is not available"}, nil
	}
	all, earned, err := o.gameplayProvider.GetUserAchievements(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get achievements: %w", err)
	}
	earnedSet := make(map[uuid.UUID]bool, len(earned))
	earnedList := make([]map[string]interface{}, 0, len(earned))
	for _, ua := range earned {
		earnedSet[ua.AchievementID] = true
		if ua.Achievement != nil {
			earnedList = append(earnedList, map[string]interface{}{
				"name":        ua.Achievement.Name,
				"description": ua.Achievement.Description,
				"rarity":      string(ua.Achievement.Rarity),
				"unlocked_at": ua.UnlockedAt.Format("2006-01-02"),
			})
		}
	}
	unearnedList := make([]map[string]interface{}, 0)
	for _, a := range all {
		if !earnedSet[a.ID] {
			unearnedList = append(unearnedList, map[string]interface{}{
				"name":        a.Name,
				"description": a.Description,
				"rarity":      string(a.Rarity),
			})
		}
	}
	return map[string]interface{}{
		"earned":   earnedList,
		"unearned": unearnedList,
		"total":    len(earnedList),
		"possible": len(all),
	}, nil
}

// humanStreakType converts a StreakType enum to a human-readable label.
func humanStreakType(t entities.StreakType) string {
	switch t {
	case entities.StreakTypeDeposit:
		return "Deposit streak"
	case entities.StreakTypeNoSpend:
		return "No-spend day streak"
	case entities.StreakTypeStashGrowth:
		return "Stash growth streak"
	case entities.StreakTypeRoundup:
		return "Roundup streak"
	case entities.StreakTypeNoPanicWithdrawal:
		return "No panic withdrawal streak"
	case entities.StreakTypeWeeklyGoal:
		return "Weekly goal streak"
	case entities.StreakTypeEmergencyFundGrowth:
		return "Emergency fund growth streak"
	default:
		return strings.ReplaceAll(string(t), "_", " ")
	}
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}
