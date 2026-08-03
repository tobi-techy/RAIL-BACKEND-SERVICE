package ai

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock gameplay provider ---

type mockGameplayProvider struct {
	streaks      []*entities.UserStreak
	challenges   []*entities.UserChallenge
	achievements []*entities.Achievement
	earned       []*entities.UserAchievement
	err          error
}

func (m *mockGameplayProvider) GetUserStreaks(_ context.Context, _ uuid.UUID) ([]*entities.UserStreak, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.streaks, nil
}

func (m *mockGameplayProvider) GetActiveChallenges(_ context.Context, _ uuid.UUID) ([]*entities.UserChallenge, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.challenges, nil
}

func (m *mockGameplayProvider) GetUserAchievements(_ context.Context, _ uuid.UUID) ([]*entities.Achievement, []*entities.UserAchievement, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.achievements, m.earned, nil
}

// --- tests ---

func TestExecuteGetSavingsStreaks_ReturnsStreaks(t *testing.T) {
	o := newTestAdapter(t)
	now := time.Now()
	o.gameplayProvider = &mockGameplayProvider{
		streaks: []*entities.UserStreak{
			{StreakType: entities.StreakTypeDeposit, CurrentCount: 5, LongestCount: 12, LastActivityAt: &now},
			{StreakType: entities.StreakTypeStashGrowth, CurrentCount: 3, LongestCount: 7, LastActivityAt: &now},
		},
	}

	result, err := o.executeGetSavingsStreaks(context.Background(), uuid.New())
	require.NoError(t, err)
	streaks, ok := result["streaks"].([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, streaks, 2)
	assert.Equal(t, "Deposit streak", streaks[0]["type"])
	assert.Equal(t, 5, streaks[0]["current"])
	assert.Equal(t, 12, streaks[0]["longest"])
	assert.Equal(t, "Stash growth streak", streaks[1]["type"])
}

func TestExecuteGetSavingsStreaks_NoStreaks(t *testing.T) {
	o := newTestAdapter(t)
	o.gameplayProvider = &mockGameplayProvider{streaks: nil}

	result, err := o.executeGetSavingsStreaks(context.Background(), uuid.New())
	require.NoError(t, err)
	msg, ok := result["message"].(string)
	require.True(t, ok)
	assert.Contains(t, msg, "No active streaks")
}

func TestExecuteGetSavingsStreaks_NoProvider(t *testing.T) {
	o := newTestAdapter(t)

	result, err := o.executeGetSavingsStreaks(context.Background(), uuid.New())
	require.NoError(t, err)
	errMsg, ok := result["error"].(string)
	require.True(t, ok)
	assert.Contains(t, errMsg, "not available")
}

func TestExecuteGetChallenges_ReturnsChallenges(t *testing.T) {
	o := newTestAdapter(t)
	o.gameplayProvider = &mockGameplayProvider{
		challenges: []*entities.UserChallenge{
			{
				ID:       uuid.New(),
				Status:   entities.ChallengeStatusActive,
				Progress: decimal.NewFromFloat(30),
				Challenge: &entities.Challenge{
					Title:       "Save $100 this week",
					Description: "Grow your stash by $100",
					TargetValue: decimal.NewFromFloat(100),
					XPReward:    50,
				},
			},
		},
	}

	result, err := o.executeGetChallenges(context.Background(), uuid.New())
	require.NoError(t, err)
	challenges, ok := result["challenges"].([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, challenges, 1)
	assert.Equal(t, "Save $100 this week", challenges[0]["title"])
	assert.Equal(t, "30.00", challenges[0]["progress"])
	assert.Equal(t, "70.00", challenges[0]["remaining"])
	assert.Equal(t, 50, challenges[0]["xp_reward"])
}

func TestExecuteGetChallenges_NoChallenges(t *testing.T) {
	o := newTestAdapter(t)
	o.gameplayProvider = &mockGameplayProvider{challenges: nil}

	result, err := o.executeGetChallenges(context.Background(), uuid.New())
	require.NoError(t, err)
	msg, ok := result["message"].(string)
	require.True(t, ok)
	assert.Contains(t, msg, "No active challenges")
}

func TestExecuteGetChallenges_SkipsNilChallenge(t *testing.T) {
	o := newTestAdapter(t)
	o.gameplayProvider = &mockGameplayProvider{
		challenges: []*entities.UserChallenge{
			{
				ID:     uuid.New(),
				Status: entities.ChallengeStatusActive,
			},
			{
				ID:       uuid.New(),
				Status:   entities.ChallengeStatusActive,
				Progress: decimal.NewFromFloat(10),
				Challenge: &entities.Challenge{
					Title:       "Save $50",
					Description: "Reach $50 in stash",
					TargetValue: decimal.NewFromFloat(50),
					XPReward:    25,
				},
			},
		},
	}

	result, err := o.executeGetChallenges(context.Background(), uuid.New())
	require.NoError(t, err)
	challenges, ok := result["challenges"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, challenges, 1)
	assert.Equal(t, "Save $50", challenges[0]["title"])
	assert.Equal(t, "40.00", challenges[0]["remaining"])
}

func TestExecuteGetAchievements_ReturnsEarnedAndUnearned(t *testing.T) {
	o := newTestAdapter(t)
	ach1 := &entities.Achievement{ID: uuid.New(), Name: "First Deposit", Description: "Made your first deposit", Rarity: entities.RarityCommon}
	ach2 := &entities.Achievement{ID: uuid.New(), Name: "Stash Master", Description: "Reach $1000 in Stash", Rarity: entities.RarityRare}
	o.gameplayProvider = &mockGameplayProvider{
		achievements: []*entities.Achievement{ach1, ach2},
		earned: []*entities.UserAchievement{
			{AchievementID: ach1.ID, Achievement: ach1, UnlockedAt: time.Now()},
		},
	}

	result, err := o.executeGetAchievements(context.Background(), uuid.New())
	require.NoError(t, err)
	earned, ok := result["earned"].([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, earned, 1)
	assert.Equal(t, "First Deposit", earned[0]["name"])

	unearned, ok := result["unearned"].([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, unearned, 1)
	assert.Equal(t, "Stash Master", unearned[0]["name"])

	assert.Equal(t, 1, result["total"])
	assert.Equal(t, 2, result["possible"])
}

func TestExecuteGetAchievements_NoProvider(t *testing.T) {
	o := newTestAdapter(t)

	result, err := o.executeGetAchievements(context.Background(), uuid.New())
	require.NoError(t, err)
	errMsg, ok := result["error"].(string)
	require.True(t, ok)
	assert.Contains(t, errMsg, "not available")
}

func TestHumanStreakType(t *testing.T) {
	assert.Equal(t, "Deposit streak", humanStreakType(entities.StreakTypeDeposit))
	assert.Equal(t, "No-spend day streak", humanStreakType(entities.StreakTypeNoSpend))
	assert.Equal(t, "Stash growth streak", humanStreakType(entities.StreakTypeStashGrowth))
	assert.Equal(t, "Roundup streak", humanStreakType(entities.StreakTypeRoundup))
	assert.Equal(t, "No panic withdrawal streak", humanStreakType(entities.StreakTypeNoPanicWithdrawal))
}
