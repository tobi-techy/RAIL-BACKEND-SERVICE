package ai

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ttlEntry is a cache entry with expiration.
type ttlEntry[T any] struct {
	value     T
	expiresAt time.Time
}

// contextCache holds TTL-cached context data per user.
type contextCache struct {
	moneyState sync.Map // uuid.UUID -> ttlEntry[*entities.MiriamMoneyState]
	nairaCtx   sync.Map // uuid.UUID -> ttlEntry[string]
}

var globalContextCache = &contextCache{}

const (
	moneyStateTTL = 5 * time.Minute
	nairaCtxTTL   = 2 * time.Minute
)

// GetMoneyState returns cached state or nil if expired/missing.
func (c *contextCache) GetMoneyState(userID uuid.UUID) *entities.MiriamMoneyState {
	v, ok := c.moneyState.Load(userID)
	if !ok {
		return nil
	}
	entry := v.(ttlEntry[*entities.MiriamMoneyState])
	if time.Now().After(entry.expiresAt) {
		c.moneyState.Delete(userID)
		return nil
	}
	return entry.value
}

// SetMoneyState stores state with TTL.
func (c *contextCache) SetMoneyState(userID uuid.UUID, state *entities.MiriamMoneyState) {
	c.moneyState.Store(userID, ttlEntry[*entities.MiriamMoneyState]{
		value:     state,
		expiresAt: time.Now().Add(moneyStateTTL),
	})
}

// GetNairaCtx returns cached naira context string or empty if expired/missing.
func (c *contextCache) GetNairaCtx(userID uuid.UUID) (string, bool) {
	v, ok := c.nairaCtx.Load(userID)
	if !ok {
		return "", false
	}
	entry := v.(ttlEntry[string])
	if time.Now().After(entry.expiresAt) {
		c.nairaCtx.Delete(userID)
		return "", false
	}
	return entry.value, true
}

// SetNairaCtx stores naira context with TTL.
func (c *contextCache) SetNairaCtx(userID uuid.UUID, ctx string) {
	c.nairaCtx.Store(userID, ttlEntry[string]{
		value:     ctx,
		expiresAt: time.Now().Add(nairaCtxTTL),
	})
}
