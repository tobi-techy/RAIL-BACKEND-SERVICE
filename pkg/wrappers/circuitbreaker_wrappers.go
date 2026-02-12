package wrappers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/circle"
	"github.com/rail-service/rail_service/pkg/circuitbreaker"
	"go.uber.org/zap"
)

// CircleClient wraps Circle client with circuit breaker
type CircleClient struct {
	client *circle.Client
	cb     *circuitbreaker.CircuitBreaker
	logger *zap.Logger
}

// NewCircleClient creates a new Circle client with circuit breaker
func NewCircleClient(client *circle.Client, logger *zap.Logger) *CircleClient {
	cb := circuitbreaker.New(circuitbreaker.Config{
		MaxRequests:      10,
		Interval:         time.Minute,
		Timeout:          time.Second * 30,
		FailureThreshold: 5,
		SuccessThreshold: 3,
		OnStateChange: func(from, to circuitbreaker.State) {
			logger.Info("Circle circuit breaker state changed",
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	})

	return &CircleClient{
		client: client,
		cb:     cb,
		logger: logger,
	}
}

// GenerateDepositAddress with circuit breaker protection
func (c *CircleClient) GenerateDepositAddress(ctx context.Context, chain entities.WalletChain, userID uuid.UUID) (interface{}, error) {
	var result interface{}
	var err error

	cbErr := c.cb.Call(func() error {
		result, err = c.client.GenerateDepositAddress(ctx, chain, userID)
		return err
	})

	if cbErr != nil {
		c.logger.Error("Circuit breaker prevented Circle API call", zap.Error(cbErr))
		return nil, fmt.Errorf("circuit breaker open: %w", cbErr)
	}

	return result, err
}

// GetWalletBalances with circuit breaker protection
func (c *CircleClient) GetWalletBalances(ctx context.Context, walletID string, tokenAddress ...string) (interface{}, error) {
	var result interface{}
	var err error

	cbErr := c.cb.Call(func() error {
		result, err = c.client.GetWalletBalances(ctx, walletID, tokenAddress...)
		return err
	})

	if cbErr != nil {
		c.logger.Error("Circuit breaker prevented Circle API call", zap.Error(cbErr))
		return nil, fmt.Errorf("circuit breaker open: %w", cbErr)
	}

	return result, err
}

// AlpacaClient wraps Alpaca client with circuit breaker
type AlpacaClient struct {
	client *alpaca.Client
	cb     *circuitbreaker.CircuitBreaker
	logger *zap.Logger
}

// NewAlpacaClient creates a new Alpaca client with circuit breaker
func NewAlpacaClient(client *alpaca.Client, logger *zap.Logger) *AlpacaClient {
	cb := circuitbreaker.New(circuitbreaker.Config{
		MaxRequests:      10,
		Interval:         time.Minute,
		Timeout:          time.Second * 30,
		FailureThreshold: 5,
		SuccessThreshold: 3,
		OnStateChange: func(from, to circuitbreaker.State) {
			logger.Info("Alpaca circuit breaker state changed",
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	})

	return &AlpacaClient{
		client: client,
		cb:     cb,
		logger: logger,
	}
}

// GetAccount with circuit breaker protection
func (a *AlpacaClient) GetAccount(ctx context.Context, accountID string) (interface{}, error) {
	var result interface{}
	var err error

	cbErr := a.cb.Call(func() error {
		result, err = a.client.GetAccount(ctx, accountID)
		return err
	})

	if cbErr != nil {
		a.logger.Error("Circuit breaker prevented Alpaca API call", zap.Error(cbErr))
		return nil, fmt.Errorf("circuit breaker open: %w", cbErr)
	}

	return result, err
}

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
