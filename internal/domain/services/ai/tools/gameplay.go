package tools

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// RegisterGameplayTools registers tools for streaks, challenges, and achievements.
func RegisterGameplayTools(r *Registry) {
	r.Register(NewTool(
		"get_savings_streaks",
		"Get the user's savings streaks (deposit streak, no-spend days, stash growth, roundup streaks). Call when user asks 'what's my streak', 'how long have I been saving', 'am I on a streak', or mentions streaks.",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Gameplay == nil {
				return &core.ToolResult{Error: "gameplay service is not available"}, nil
			}
			streaks, err := deps.Gameplay.GetUserStreaks(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: "failed to get streaks: " + err.Error()}, nil
			}
			if len(streaks) == 0 {
				return &core.ToolResult{Data: map[string]interface{}{
					"message": "No active streaks yet. Make a deposit or save into Stash to start one!",
				}}, nil
			}
			streakList := make([]map[string]interface{}, 0, len(streaks))
			for _, s := range streaks {
				streakList = append(streakList, map[string]interface{}{
					"type":    string(s.StreakType),
					"current": s.CurrentCount,
					"longest": s.LongestCount,
				})
			}
			return &core.ToolResult{Data: map[string]interface{}{"streaks": streakList}}, nil
		},
	))

	r.Register(NewTool(
		"get_active_challenges",
		"Get the user's active challenges and their progress. Call when user asks 'what are my challenges', 'what challenges am I doing', 'challenge progress', or mentions challenges.",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Gameplay == nil {
				return &core.ToolResult{Error: "gameplay service is not available"}, nil
			}
			challenges, err := deps.Gameplay.GetActiveChallenges(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: "failed to get challenges: " + err.Error()}, nil
			}
			if len(challenges) == 0 {
				return &core.ToolResult{Data: map[string]interface{}{
					"message": "No active challenges right now. Check back next week for new ones!",
				}}, nil
			}
			challengeList := make([]map[string]interface{}, 0, len(challenges))
			for _, c := range challenges {
				entry := map[string]interface{}{
					"title":       c.Challenge.Title,
					"description": c.Challenge.Description,
					"progress":    c.Progress.String(),
					"target":      c.Challenge.TargetValue.String(),
					"xp_reward":   c.Challenge.XPReward,
					"status":      string(c.Status),
				}
				if c.Challenge.TargetValue.GreaterThan(c.Progress) {
					entry["remaining"] = c.Challenge.TargetValue.Sub(c.Progress).String()
				}
				challengeList = append(challengeList, entry)
			}
			return &core.ToolResult{Data: map[string]interface{}{"challenges": challengeList}}, nil
		},
	))

	r.Register(NewTool(
		"get_achievements",
		"Get the user's earned achievements and unearned achievement list. Call when user asks 'what have I achieved', 'my achievements', 'badges', or 'what milestones have I hit'.",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Gameplay == nil {
				return &core.ToolResult{Error: "gameplay service is not available"}, nil
			}
			all, earned, err := deps.Gameplay.GetUserAchievements(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: "failed to get achievements: " + err.Error()}, nil
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
			return &core.ToolResult{Data: map[string]interface{}{
				"earned":   earnedList,
				"unearned": unearnedList,
				"total":    len(earnedList),
				"possible": len(all),
			}}, nil
		},
	))
}
