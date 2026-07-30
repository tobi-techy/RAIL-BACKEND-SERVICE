package ai

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// InMemoryAnomalyStore holds the latest anomaly results per user in memory.
// Used for eval and lightweight deployments; production should use RedisAnomalyStore.
type InMemoryAnomalyStore struct {
	mu   sync.RWMutex
	data map[string]anomalyStoreEntry
}

type anomalyStoreEntry struct {
	results   []AnomalyResult
	expiresAt time.Time
}

func NewInMemoryAnomalyStore() *InMemoryAnomalyStore {
	return &InMemoryAnomalyStore{
		data: make(map[string]anomalyStoreEntry),
	}
}

func (s *InMemoryAnomalyStore) Set(_ context.Context, userID uuid.UUID, results []AnomalyResult, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]AnomalyResult, len(results))
	copy(cp, results)
	s.data[userID.String()] = anomalyStoreEntry{
		results:   cp,
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (s *InMemoryAnomalyStore) Get(_ context.Context, userID uuid.UUID) ([]AnomalyResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.data[userID.String()]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, nil
	}
	cp := make([]AnomalyResult, len(entry.results))
	copy(cp, entry.results)
	return cp, nil
}

// AnomalyCache is the subset of Redis needed by RedisAnomalyStore.
// Matches cache.RedisClient Set/Get without importing infrastructure into domain.
type AnomalyCache interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string, dest interface{}) error
}

// RedisAnomalyStore persists anomaly results so multi-instance deployments share
// the same detections Miriam references in chat.
type RedisAnomalyStore struct {
	cache AnomalyCache
}

func NewRedisAnomalyStore(cache AnomalyCache) *RedisAnomalyStore {
	return &RedisAnomalyStore{cache: cache}
}

func anomalyRedisKey(userID uuid.UUID) string {
	return "miriam:anomalies:" + userID.String()
}

type redisAnomalyPayload struct {
	Results []AnomalyResult `json:"results"`
}

func (s *RedisAnomalyStore) Set(ctx context.Context, userID uuid.UUID, results []AnomalyResult, ttl time.Duration) error {
	if s == nil || s.cache == nil {
		return nil
	}
	cp := make([]AnomalyResult, len(results))
	copy(cp, results)
	return s.cache.Set(ctx, anomalyRedisKey(userID), redisAnomalyPayload{Results: cp}, ttl)
}

func (s *RedisAnomalyStore) Get(ctx context.Context, userID uuid.UUID) ([]AnomalyResult, error) {
	if s == nil || s.cache == nil {
		return nil, nil
	}
	var payload redisAnomalyPayload
	if err := s.cache.Get(ctx, anomalyRedisKey(userID), &payload); err != nil {
		// Treat miss as empty, not an error — callers already handle nil.
		return nil, nil
	}
	return payload.Results, nil
}
