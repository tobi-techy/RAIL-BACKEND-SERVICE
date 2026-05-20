package ai

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type realtimeUserProfileFake struct {
	profile *entities.UserProfile
}

func (f realtimeUserProfileFake) GetCountry(_ context.Context, _ uuid.UUID) (string, error) {
	if f.profile == nil || f.profile.Country == nil {
		return "", nil
	}
	return *f.profile.Country, nil
}

func (f realtimeUserProfileFake) GetEmail(_ context.Context, _ uuid.UUID) (string, error) {
	if f.profile == nil {
		return "", nil
	}
	return f.profile.Email, nil
}

func (f realtimeUserProfileFake) GetProfile(_ context.Context, _ uuid.UUID) (*entities.UserProfile, error) {
	return f.profile, nil
}

func TestBuildRealtimeGreetingUsesNameAndBalanceContext(t *testing.T) {
	firstName := "Tobi"
	userID := uuid.New()
	o := &Orchestrator{
		userProfile:    realtimeUserProfileFake{profile: &entities.UserProfile{FirstName: &firstName, Email: "tobi@example.com"}},
		aggregateStats: govAggregateStatsFake{spend: decimal.NewFromInt(412), stash: decimal.NewFromInt(735)},
	}

	greeting := o.BuildRealtimeGreeting(context.Background(), userID)

	require.Equal(t, "Hey Tobi. Miriam here. I have your Rail numbers in front of me. What money move are we making?", greeting)
}

func TestBuildRealtimeGreetingDoesNotClaimNumbersWithoutBalanceContext(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{}

	greeting := o.BuildRealtimeGreeting(context.Background(), userID)

	require.Equal(t, "Hey. Miriam here. I have Rail open. What money move are we making?", greeting)
}

func TestBuildRealtimeInstructionsIncludesPremiumVoiceMode(t *testing.T) {
	instructions := (&Orchestrator{}).BuildRealtimeInstructions(context.Background(), uuid.New())

	require.Contains(t, instructions, "MIRIAM VOICE MODE")
	require.Contains(t, instructions, "paid, live money operator")
	require.Contains(t, instructions, "Default to 1-2 spoken sentences")
	require.Contains(t, instructions, "Never guess account data")
	require.Contains(t, instructions, "Never end with \"Is there anything else I can help with?\"")
}
