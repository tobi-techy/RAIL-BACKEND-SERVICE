package security

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

const coolingPeriod = 24 * time.Hour

type AddressWhitelistService struct {
	repo   *repositories.SecurityFeaturesRepository
	logger *zap.Logger
}

func NewAddressWhitelistService(repo *repositories.SecurityFeaturesRepository, logger *zap.Logger) *AddressWhitelistService {
	return &AddressWhitelistService{repo: repo, logger: logger}
}

func (s *AddressWhitelistService) AddAddress(ctx context.Context, userID uuid.UUID, chain, address, label string) (*entities.WhitelistedAddress, error) {
	existing, _ := s.repo.FindWhitelistedAddress(ctx, userID, chain, address)
	if existing != nil {
		return nil, fmt.Errorf("address already whitelisted")
	}

	now := time.Now()
	coolingUntil := now.Add(coolingPeriod)
	addr := &entities.WhitelistedAddress{
		ID:           uuid.New(),
		UserID:       userID,
		Chain:        chain,
		Address:      address,
		Label:        label,
		Status:       entities.WhitelistStatusPending,
		CoolingUntil: &coolingUntil,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.repo.CreateWhitelistedAddress(ctx, addr); err != nil {
		return nil, fmt.Errorf("failed to add address: %w", err)
	}

	s.logger.Info("Address added to whitelist",
		zap.String("user_id", userID.String()),
		zap.String("chain", chain),
		zap.String("address", address))

	return addr, nil
}

func (s *AddressWhitelistService) GetAddresses(ctx context.Context, userID uuid.UUID) ([]entities.WhitelistedAddress, error) {
	addrs, err := s.repo.GetWhitelistedAddresses(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Auto-activate addresses past cooling period
	now := time.Now()
	for i := range addrs {
		if addrs[i].Status == entities.WhitelistStatusPending && addrs[i].CoolingUntil != nil && now.After(*addrs[i].CoolingUntil) {
			addrs[i].Status = entities.WhitelistStatusActive
		}
	}
	return addrs, nil
}

func (s *AddressWhitelistService) RemoveAddress(ctx context.Context, id, userID uuid.UUID) error {
	return s.repo.RemoveWhitelistedAddress(ctx, id, userID)
}

func (s *AddressWhitelistService) ValidateWithdrawalAddress(ctx context.Context, userID uuid.UUID, chain, address string) error {
	addr, err := s.repo.FindWhitelistedAddress(ctx, userID, chain, address)
	if err != nil {
		return fmt.Errorf("failed to check whitelist: %w", err)
	}
	if addr == nil {
		return fmt.Errorf("address not whitelisted")
	}
	if addr.Status == entities.WhitelistStatusRemoved {
		return fmt.Errorf("address has been removed")
	}
	if addr.CoolingUntil != nil && time.Now().Before(*addr.CoolingUntil) {
		return fmt.Errorf("address is in cooling period until %s", addr.CoolingUntil.Format(time.RFC3339))
	}
	return nil
}
