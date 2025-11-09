package quota

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Manager struct {
	quotas map[uuid.UUID]*UserQuota
	mu     sync.RWMutex
	logger *zap.Logger
}

type UserQuota struct {
	UserID           uuid.UUID
	StorageBytes     int64
	StorageLimit     int64
	ComputeTokens    int64
	ComputeLimit     int64
	MonthlyCost      float64
	MonthlyCostLimit float64
	ResetAt          time.Time
}

type CostEstimate struct {
	StorageCost  float64
	ComputeCost  float64
	TotalCost    float64
	EstimatedAt  time.Time
}

const (
	StorageCostPerGB    = 0.10
	ComputeCostPer1KTok = 0.02
)

func NewManager(logger *zap.Logger) *Manager {
	return &Manager{
		quotas: make(map[uuid.UUID]*UserQuota),
		logger: logger,
	}
}

func (m *Manager) InitializeQuota(ctx context.Context, userID uuid.UUID, tier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	limits := m.getTierLimits(tier)
	m.quotas[userID] = &UserQuota{
		UserID:           userID,
		StorageLimit:     limits.storage,
		ComputeLimit:     limits.compute,
		MonthlyCostLimit: limits.cost,
		ResetAt:          time.Now().AddDate(0, 1, 0),
	}

	m.logger.Info("quota initialized", zap.String("user", userID.String()), zap.String("tier", tier))
	return nil
}

func (m *Manager) CheckStorageQuota(ctx context.Context, userID uuid.UUID, bytes int64) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	quota, exists := m.quotas[userID]
	if !exists {
		return fmt.Errorf("quota not found")
	}

	if quota.StorageBytes+bytes > quota.StorageLimit {
		return fmt.Errorf("storage quota exceeded: %d/%d bytes", quota.StorageBytes, quota.StorageLimit)
	}

	return nil
}

func (m *Manager) CheckComputeQuota(ctx context.Context, userID uuid.UUID, tokens int64) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	quota, exists := m.quotas[userID]
	if !exists {
		return fmt.Errorf("quota not found")
	}

	if quota.ComputeTokens+tokens > quota.ComputeLimit {
		return fmt.Errorf("compute quota exceeded: %d/%d tokens", quota.ComputeTokens, quota.ComputeLimit)
	}

	return nil
}

func (m *Manager) RecordStorage(ctx context.Context, userID uuid.UUID, bytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	quota, exists := m.quotas[userID]
	if !exists {
		return fmt.Errorf("quota not found")
	}

	quota.StorageBytes += bytes
	cost := float64(bytes) / (1024 * 1024 * 1024) * StorageCostPerGB
	quota.MonthlyCost += cost

	if quota.MonthlyCost > quota.MonthlyCostLimit {
		m.logger.Warn("cost limit exceeded", zap.String("user", userID.String()), zap.Float64("cost", quota.MonthlyCost))
	}

	return nil
}

func (m *Manager) RecordCompute(ctx context.Context, userID uuid.UUID, tokens int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	quota, exists := m.quotas[userID]
	if !exists {
		return fmt.Errorf("quota not found")
	}

	quota.ComputeTokens += tokens
	cost := float64(tokens) / 1000 * ComputeCostPer1KTok
	quota.MonthlyCost += cost

	if quota.MonthlyCost > quota.MonthlyCostLimit {
		m.logger.Warn("cost limit exceeded", zap.String("user", userID.String()), zap.Float64("cost", quota.MonthlyCost))
	}

	return nil
}

func (m *Manager) GetQuota(ctx context.Context, userID uuid.UUID) (*UserQuota, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	quota, exists := m.quotas[userID]
	if !exists {
		return nil, fmt.Errorf("quota not found")
	}

	return quota, nil
}

func (m *Manager) EstimateCost(ctx context.Context, storageBytes int64, computeTokens int64) *CostEstimate {
	storageCost := float64(storageBytes) / (1024 * 1024 * 1024) * StorageCostPerGB
	computeCost := float64(computeTokens) / 1000 * ComputeCostPer1KTok

	return &CostEstimate{
		StorageCost: storageCost,
		ComputeCost: computeCost,
		TotalCost:   storageCost + computeCost,
		EstimatedAt: time.Now(),
	}
}

func (m *Manager) ResetMonthlyQuotas(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for userID, quota := range m.quotas {
		if now.After(quota.ResetAt) {
			quota.ComputeTokens = 0
			quota.MonthlyCost = 0
			quota.ResetAt = now.AddDate(0, 1, 0)
			m.logger.Info("quota reset", zap.String("user", userID.String()))
		}
	}

	return nil
}

func (m *Manager) getTierLimits(tier string) struct {
	storage int64
	compute int64
	cost    float64
} {
	switch tier {
	case "free":
		return struct {
			storage int64
			compute int64
			cost    float64
		}{
			storage: 1 * 1024 * 1024 * 1024,
			compute: 100000,
			cost:    10.0,
		}
	case "premium":
		return struct {
			storage int64
			compute int64
			cost    float64
		}{
			storage: 100 * 1024 * 1024 * 1024,
			compute: 10000000,
			cost:    1000.0,
		}
	default:
		return struct {
			storage int64
			compute int64
			cost    float64
		}{
			storage: 1 * 1024 * 1024 * 1024,
			compute: 100000,
			cost:    10.0,
		}
	}
}
