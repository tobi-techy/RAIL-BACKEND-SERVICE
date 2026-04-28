package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

const mfaExpiry = 5 * time.Minute

type AdaptiveMFAService struct {
	repo   *repositories.SecurityFeaturesRepository
	logger *zap.Logger
}

func NewAdaptiveMFAService(repo *repositories.SecurityFeaturesRepository, logger *zap.Logger) *AdaptiveMFAService {
	return &AdaptiveMFAService{repo: repo, logger: logger}
}

type ChallengeResponse struct {
	ChallengeID   uuid.UUID                `json:"challenge_id"`
	ChallengeType entities.MFAChallengeType `json:"challenge_type"`
	ExpiresAt     time.Time                `json:"expires_at"`
}

func (s *AdaptiveMFAService) RequiresMFA(riskAction entities.TxRiskAction) bool {
	return riskAction == entities.TxRiskActionStepUpAuth
}

func (s *AdaptiveMFAService) CreateChallenge(ctx context.Context, userID uuid.UUID, challengeType entities.MFAChallengeType) (*ChallengeResponse, string, error) {
	code, err := generateOTP(6)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate OTP: %w", err)
	}

	hash := sha256.Sum256([]byte(code))
	codeHash := hex.EncodeToString(hash[:])

	now := time.Now()
	challenge := &entities.MFAChallenge{
		ID:            uuid.New(),
		UserID:        userID,
		ChallengeType: challengeType,
		CodeHash:      codeHash,
		ExpiresAt:     now.Add(mfaExpiry),
		Verified:      false,
		Attempts:      0,
		CreatedAt:     now,
	}

	if err := s.repo.CreateMFAChallenge(ctx, challenge); err != nil {
		return nil, "", fmt.Errorf("failed to create challenge: %w", err)
	}

	s.logger.Info("MFA challenge created",
		zap.String("user_id", userID.String()),
		zap.String("type", string(challengeType)))

	return &ChallengeResponse{
		ChallengeID:   challenge.ID,
		ChallengeType: challengeType,
		ExpiresAt:     challenge.ExpiresAt,
	}, code, nil
}

func (s *AdaptiveMFAService) VerifyChallenge(ctx context.Context, userID uuid.UUID, challengeType entities.MFAChallengeType, code string) (bool, error) {
	challenge, err := s.repo.GetActiveMFAChallenge(ctx, userID, challengeType)
	if err != nil {
		return false, fmt.Errorf("failed to get challenge: %w", err)
	}
	if challenge == nil {
		return false, fmt.Errorf("no active challenge found")
	}
	if challenge.Attempts >= 3 {
		return false, fmt.Errorf("too many attempts")
	}

	hash := sha256.Sum256([]byte(code))
	codeHash := hex.EncodeToString(hash[:])

	if codeHash != challenge.CodeHash {
		s.repo.IncrementMFAAttempts(ctx, challenge.ID)
		return false, nil
	}

	if err := s.repo.VerifyMFAChallenge(ctx, challenge.ID); err != nil {
		return false, fmt.Errorf("failed to verify challenge: %w", err)
	}

	return true, nil
}

func generateOTP(length int) (string, error) {
	const digits = "0123456789"
	code := make([]byte, length)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			return "", err
		}
		code[i] = digits[n.Int64()]
	}
	return string(code), nil
}
