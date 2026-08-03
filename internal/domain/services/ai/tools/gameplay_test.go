package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubGameplay struct {
	challenges []*entities.UserChallenge
}

func (s stubGameplay) GetUserStreaks(context.Context, uuid.UUID) ([]*entities.UserStreak, error) {
	return nil, nil
}

func (s stubGameplay) GetActiveChallenges(context.Context, uuid.UUID) ([]*entities.UserChallenge, error) {
	return s.challenges, nil
}

func (s stubGameplay) GetUserAchievements(context.Context, uuid.UUID) ([]*entities.Achievement, []*entities.UserAchievement, error) {
	return nil, nil, nil
}

func TestGetActiveChallenges_SkipsNilChallenge(t *testing.T) {
	r := NewRegistry()
	RegisterGameplayTools(r)
	tool := r.Get("get_active_challenges")
	require.NotNil(t, tool)

	deps := &core.Dependencies{
		Gameplay: stubGameplay{
			challenges: []*entities.UserChallenge{
				{ID: uuid.New(), Status: entities.ChallengeStatusActive},
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
		},
	}

	res, err := tool.Execute(context.Background(), uuid.New(), nil, deps)
	require.NoError(t, err)
	require.Empty(t, res.Error)
	challenges, ok := res.Data["challenges"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, challenges, 1)
	assert.Equal(t, "Save $50", challenges[0]["title"])
	assert.Equal(t, "40", challenges[0]["remaining"])
}

func TestGetActiveChallenges_AllNilChallenges(t *testing.T) {
	r := NewRegistry()
	RegisterGameplayTools(r)
	tool := r.Get("get_active_challenges")
	require.NotNil(t, tool)

	deps := &core.Dependencies{
		Gameplay: stubGameplay{
			challenges: []*entities.UserChallenge{
				{ID: uuid.New(), Status: entities.ChallengeStatusActive},
				{ID: uuid.New(), Status: entities.ChallengeStatusActive},
			},
		},
	}

	res, err := tool.Execute(context.Background(), uuid.New(), nil, deps)
	require.NoError(t, err)
	require.Empty(t, res.Error)
	challenges, ok := res.Data["challenges"].([]map[string]interface{})
	require.True(t, ok)
	assert.Empty(t, challenges)
}
