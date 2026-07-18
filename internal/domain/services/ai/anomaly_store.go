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
	mu    sync.RWMutex
	data  map[string]anomalyStoreEntry
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
