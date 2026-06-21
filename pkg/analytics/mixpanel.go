package analytics

import (
	"context"
	"time"

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
	mp := mixpanel.NewApiClient(token, mixpanel.EuResidency())
	return &Client{mp: mp, logger: logger}
}

// Track sends an event for a user.
func (c *Client) Track(ctx context.Context, userID, event string, props map[string]any) {
	if c.mp == nil {
		return
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.mp.Track(bgCtx, []*mixpanel.Event{
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
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.mp.PeopleSet(bgCtx, []*mixpanel.PeopleProperties{
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
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.mp.PeopleIncrement(bgCtx, userID, props); err != nil {
			c.logger.Error("mixpanel increment failed", zap.String("user_id", userID), zap.Error(err))
		}
	}()
}

// Import sends historical events with explicit timestamps (for backfill).
// Uses the /import endpoint which accepts events with $time set in the past.
func (c *Client) Import(ctx context.Context, events []*mixpanel.Event) error {
	if c.mp == nil {
		return nil
	}
	_, err := c.mp.Import(ctx, events, mixpanel.ImportOptions{Strict: false})
	return err
}

// NewEventWithTime creates an event with a historical timestamp.
func (c *Client) NewEventWithTime(event, userID string, ts time.Time, props map[string]any) *mixpanel.Event {
	if props == nil {
		props = map[string]any{}
	}
	props["time"] = ts.Unix()
	return c.mp.NewEvent(event, userID, props)
}

// PeopleSetBatch sets profile properties for multiple users.
func (c *Client) PeopleSetBatch(ctx context.Context, profiles []*mixpanel.PeopleProperties) error {
	if c.mp == nil {
		return nil
	}
	return c.mp.PeopleSet(ctx, profiles)
}

// TrackRevenue tracks a revenue event via $transaction append.
func (c *Client) TrackRevenue(ctx context.Context, userID string, amount float64, props map[string]any) {
	if c.mp == nil {
		return
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		txn := map[string]any{"$amount": amount}
		for k, v := range props {
			txn[k] = v
		}
		if err := c.mp.PeopleAppendListProperty(bgCtx, userID, map[string]any{
			"$transactions": txn,
		}); err != nil {
			c.logger.Error("mixpanel revenue failed", zap.String("user_id", userID), zap.Error(err))
		}
	}()
}
