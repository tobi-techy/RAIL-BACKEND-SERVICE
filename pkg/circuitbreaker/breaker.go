package circuitbreaker

import (
	"time"

	"github.com/sony/gobreaker"
)

type Config struct {
	MaxRequests uint32
	Interval    time.Duration
	Timeout     time.Duration
}

func DefaultConfig() Config {
	return Config{
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     60 * time.Second,
	}
}

func New(name string, cfg Config) *gobreaker.CircuitBreaker {
	settings := gobreaker.Settings{
		Name:        name,
		MaxRequests: cfg.MaxRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.Requests >= 3 && failureRatio >= 0.6
		},
	}
	return gobreaker.NewCircuitBreaker(settings)
}
