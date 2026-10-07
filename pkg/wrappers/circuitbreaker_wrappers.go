package wrappers

import (
	"context"
	"fmt"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/pkg/circuitbreaker"
	"go.uber.org/zap"
)

// BridgeClient wraps Bridge client with circuit breaker
type BridgeClient struct {
	client *bridge.Client
	cb     *circuitbreaker.CircuitBreaker
	logger *zap.Logger
}

// NewBridgeClient creates a new Bridge client with circuit breaker
func NewBridgeClient(client *bridge.Client, logger *zap.Logger) *BridgeClient {
	cb := circuitbreaker.New(circuitbreaker.Config{
		MaxRequests:      10,
		Interval:         time.Minute,
		Timeout:          time.Second * 30,
		FailureThreshold: 5,
		SuccessThreshold: 3,
		OnStateChange: func(from, to circuitbreaker.State) {
			logger.Info("Bridge circuit breaker state changed",
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	})

	return &BridgeClient{
		client: client,
		cb:     cb,
		logger: logger,
	}
}

// Generic wrapper for any client method
func (b *BridgeClient) CallWithCircuitBreaker(ctx context.Context, methodName string, fn func() error) error {
	cbErr := b.cb.Call(fn)

	if cbErr != nil {
		b.logger.Error("Circuit breaker prevented Bridge API call",
			zap.String("method", methodName),
			zap.Error(cbErr),
		)
		return fmt.Errorf("circuit breaker open: %w", cbErr)
	}

	return nil
}
