package waitlist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"go.uber.org/zap"
)

type Service struct {
	repo   repositories.WaitlistRepository
	logger *zap.Logger
}

func NewService(repo repositories.WaitlistRepository, logger *zap.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

type SignupRequest struct {
	Email        string `json:"email"`
	FullName     string `json:"full_name"`
	ReferralCode string `json:"referral_code,omitempty"`
	Source       string `json:"source,omitempty"`
}

type SignupResponse struct {
	Position     int    `json:"position"`
	ReferralCode string `json:"referral_code"`
	TotalAhead   int    `json:"total_ahead"`
}

func (s *Service) Signup(ctx context.Context, req SignupRequest) (*SignupResponse, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || !strings.Contains(email, "@") || req.FullName == "" {
		return nil, fmt.Errorf("valid email and full_name are required")
	}

	// Check if already on waitlist
	existing, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("check existing: %w", err)
	}
	if existing != nil {
		return &SignupResponse{
			Position:     existing.Position,
			ReferralCode: existing.ReferralCode,
			TotalAhead:   existing.Position - 1,
		}, nil
	}

	// Resolve referrer
	var referredBy *uuid.UUID
	if req.ReferralCode != "" {
		referrer, err := s.repo.GetByReferralCode(ctx, req.ReferralCode)
		if err == nil && referrer != nil {
			referredBy = &referrer.ID
		}
	}

	source := req.Source
	if source == "" {
		source = "website"
	}

	user := &entities.WaitlistUser{
		Email:        email,
		FullName:     req.FullName,
		ReferralCode: generateReferralCode(),
		ReferredBy:   referredBy,
		Source:       source,
	}

	if err := s.repo.Create(ctx, user); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			// Race condition: re-fetch
			existing, refetchErr := s.repo.GetByEmail(ctx, email)
			if refetchErr != nil {
				return nil, fmt.Errorf("race condition re-fetch failed: %w", refetchErr)
			}
			if existing != nil {
				return &SignupResponse{Position: existing.Position, ReferralCode: existing.ReferralCode, TotalAhead: existing.Position - 1}, nil
			}
		}
		return nil, fmt.Errorf("create waitlist user: %w", err)
	}

	s.logger.Info("waitlist signup", zap.String("email", email), zap.Int("position", user.Position))

	return &SignupResponse{
		Position:     user.Position,
		ReferralCode: user.ReferralCode,
		TotalAhead:   user.Position - 1,
	}, nil
}

func (s *Service) List(ctx context.Context, status *entities.WaitlistStatus, limit, offset int) ([]entities.WaitlistUser, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.List(ctx, status, limit, offset)
}

func (s *Service) MarkConverted(ctx context.Context, email string, userID uuid.UUID) error {
	return s.repo.MarkConverted(ctx, email, userID)
}

func (s *Service) Count(ctx context.Context) (int, error) {
	return s.repo.Count(ctx)
}

func generateReferralCode() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return "RAIL-" + strings.ToUpper(hex.EncodeToString(b))
}
