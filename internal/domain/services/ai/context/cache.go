package context

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type ttlEntry[T any] struct {
	value     T
	expiresAt time.Time
}

// ContextCache holds TTL-cached context data per user.
type ContextCache struct {
	moneyState sync.Map
	nairaCtx   sync.Map

	rateLineMu   sync.RWMutex
	rateLine     string
	rateLineExp  time.Time
	rateLineSeen bool
}

const (
	moneyStateTTL = 5 * time.Minute
	nairaCtxTTL   = 2 * time.Minute
	rateLineTTL   = 90 * time.Second
)

// NewContextCache creates a new cache instance.
func NewContextCache() *ContextCache {
	return &ContextCache{}
}

// GetRateLine returns the cached live-rate context line.
func (c *ContextCache) GetRateLine() (string, bool) {
	c.rateLineMu.RLock()
	defer c.rateLineMu.RUnlock()
	if !c.rateLineSeen || time.Now().After(c.rateLineExp) {
		return "", false
	}
	return c.rateLine, true
}

// SetRateLine stores the live-rate context line with TTL.
func (c *ContextCache) SetRateLine(line string) {
	c.rateLineMu.Lock()
	defer c.rateLineMu.Unlock()
	c.rateLine = line
	c.rateLineExp = time.Now().Add(rateLineTTL)
	c.rateLineSeen = true
}

// GetMoneyState returns cached state or nil if expired/missing.
func (c *ContextCache) GetMoneyState(userID uuid.UUID) *entities.MiriamMoneyState {
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
func (c *ContextCache) SetMoneyState(userID uuid.UUID, state *entities.MiriamMoneyState) {
	c.moneyState.Store(userID, ttlEntry[*entities.MiriamMoneyState]{
		value:     state,
		expiresAt: time.Now().Add(moneyStateTTL),
	})
}

// GetNairaCtx returns cached naira context string.
func (c *ContextCache) GetNairaCtx(userID uuid.UUID) (string, bool) {
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
func (c *ContextCache) SetNairaCtx(userID uuid.UUID, ctx string) {
	c.nairaCtx.Store(userID, ttlEntry[string]{
		value:     ctx,
		expiresAt: time.Now().Add(nairaCtxTTL),
	})
}
