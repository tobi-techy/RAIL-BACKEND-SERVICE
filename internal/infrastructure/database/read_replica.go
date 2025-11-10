package database

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

type ReplicaPool struct {
	primary  *sql.DB
	replicas []*sql.DB
	mu       sync.RWMutex
}

func NewReplicaPool(primary *sql.DB, replicaURLs []string) (*ReplicaPool, error) {
	pool := &ReplicaPool{
		primary:  primary,
		replicas: make([]*sql.DB, 0, len(replicaURLs)),
	}

	for _, url := range replicaURLs {
		replica, err := sql.Open("postgres", url)
		if err != nil {
			return nil, fmt.Errorf("failed to open replica connection: %w", err)
		}

		replica.SetMaxOpenConns(25)
		replica.SetMaxIdleConns(5)
		replica.SetConnMaxLifetime(5 * time.Minute)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := replica.PingContext(ctx); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to ping replica: %w", err)
		}
		cancel()

		pool.replicas = append(pool.replicas, replica)
	}

	return pool, nil
}

func (rp *ReplicaPool) Primary() *sql.DB {
	return rp.primary
}

func (rp *ReplicaPool) Replica() *sql.DB {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	if len(rp.replicas) == 0 {
		return rp.primary
	}

	idx := rand.Intn(len(rp.replicas))
	return rp.replicas[idx]
}

func (rp *ReplicaPool) Close() error {
	var errs []error

	for _, replica := range rp.replicas {
		if err := replica.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to close replicas: %v", errs)
	}

	return nil
}

func (rp *ReplicaPool) HealthCheck(ctx context.Context) error {
	if err := rp.primary.PingContext(ctx); err != nil {
		return fmt.Errorf("primary health check failed: %w", err)
	}

	for i, replica := range rp.replicas {
		if err := replica.PingContext(ctx); err != nil {
			return fmt.Errorf("replica %d health check failed: %w", i, err)
		}
	}

	return nil
}
