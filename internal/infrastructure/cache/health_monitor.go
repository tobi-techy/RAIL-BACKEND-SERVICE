package cache

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// HealthMonitor periodically pings Redis and fires a callback on every up<->down
// transition, so an outage or Upstash budget suspension pages the team instead
// of surfacing as user-facing 503s. The ping is cheap (one command per tick).
type HealthMonitor struct {
	client   RedisClient
	logger   *zap.Logger
	interval time.Duration
	timeout  time.Duration
	// onChange is called when health flips. up=true means recovered.
	onChange func(up bool, err error)
}

// NewHealthMonitor builds a monitor. interval<=0 defaults to 30s.
func NewHealthMonitor(client RedisClient, logger *zap.Logger, interval time.Duration, onChange func(up bool, err error)) *HealthMonitor {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &HealthMonitor{
		client:   client,
		logger:   logger,
		interval: interval,
		timeout:  3 * time.Second,
		onChange: onChange,
	}
}

// Start runs the monitor until ctx is cancelled.
func (m *HealthMonitor) Start(ctx context.Context) {
	if m == nil || m.client == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		healthy := true // assume healthy at boot; first failure will alert
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, cancel := context.WithTimeout(ctx, m.timeout)
				err := m.client.Ping(pingCtx)
				cancel()
				up := err == nil
				if up == healthy {
					continue // no transition
				}
				healthy = up
				if up {
					m.logger.Info("Redis recovered")
				} else {
					m.logger.Error("Redis health check failed", zap.Error(err))
				}
				if m.onChange != nil {
					m.onChange(up, err)
				}
			}
		}
	}()
}
