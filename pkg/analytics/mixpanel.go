package analytics

import (
	"context"

	"github.com/mixpanel/mixpanel-go"
	"go.uber.org/zap"
)

// Client wraps Mixpanel for Rail event tracking.
type Client struct {
	mp     *mixpanel.ApiClient
	logger *zap.Logger
}

// New creates a Mixpanel analytics client.
func New(token string, logger *zap.Logger) *Client {
	mp := mixpanel.NewApiClient(token)
	return &Client{mp: mp, logger: logger}
}

// Track sends an event for a user.
func (c *Client) Track(ctx context.Context, userID, event string, props map[string]any) {
	if c.mp == nil {
		return
	}
	go func() {
		if err := c.mp.Track(ctx, []*mixpanel.Event{
			c.mp.NewEvent(event, userID, props),
		}); err != nil {
			c.logger.Error("mixpanel track failed", zap.String("event", event), zap.Error(err))
		}
	}()
}

// Identify sets user profile properties.
func (c *Client) Identify(ctx context.Context, userID string, props map[string]any) {
	if c.mp == nil {
		return
	}
	go func() {
		if err := c.mp.PeopleSet(ctx, []*mixpanel.PeopleProperties{
			mixpanel.NewPeopleProperties(userID, props),
		}); err != nil {
			c.logger.Error("mixpanel identify failed", zap.String("user_id", userID), zap.Error(err))
		}
	}()
}

// Increment increments numeric profile properties.
func (c *Client) Increment(ctx context.Context, userID string, props map[string]int) {
	if c.mp == nil {
		return
	}
	go func() {
		if err := c.mp.PeopleIncrement(ctx, userID, props); err != nil {
			c.logger.Error("mixpanel increment failed", zap.String("user_id", userID), zap.Error(err))
		}
	}()
}

// TrackRevenue tracks a revenue event via $transaction append.
func (c *Client) TrackRevenue(ctx context.Context, userID string, amount float64, props map[string]any) {
	if c.mp == nil {
		return
	}
	go func() {
		txn := map[string]any{"$amount": amount}
		for k, v := range props {
			txn[k] = v
		}
		if err := c.mp.PeopleAppendListProperty(ctx, userID, map[string]any{
			"$transactions": txn,
		}); err != nil {
			c.logger.Error("mixpanel revenue failed", zap.String("user_id", userID), zap.Error(err))
		}
	}()
}
