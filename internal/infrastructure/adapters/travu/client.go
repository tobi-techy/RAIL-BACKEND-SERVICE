package travu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	defaultBaseURL    = "https://api.travu.africa/api/v1"
	sandboxBaseURL    = "https://api.travu.africa/test/api/v1"
	defaultTimeout    = 25 * time.Second
	defaultMaxRetries = 2
	maxResponseSize   = 2 << 20 // 2MB
)

// Config holds Travu API configuration.
type Config struct {
	SecretKey  string
	BaseURL    string
	Sandbox    bool
	AgentEmail string
	Timeout    time.Duration
	MaxRetries int
}

// Client wraps the Travu Bus + Flight aggregation API.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient builds a Travu client. SecretKey is required.
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("travu: SecretKey is required")
	}
	if cfg.BaseURL == "" {
		if cfg.Sandbox {
			cfg.BaseURL = sandboxBaseURL
		} else {
			cfg.BaseURL = defaultBaseURL
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		logger:     logger,
	}, nil
}

// AgentEmail returns the configured Travu account email used on bus bookings.
func (c *Client) AgentEmail() string { return c.cfg.AgentEmail }

// --- Bus ---

// CheckTrip returns available bus trips for a route and date.
func (c *Client) CheckTrip(ctx context.Context, req CheckTripRequest) ([]Trip, error) {
	body, err := c.postRaw(ctx, "/check_trip", req)
	if err != nil {
		return nil, fmt.Errorf("travu check_trip: %w", err)
	}
	trips, err := decodeTrips(body)
	if err != nil {
		return nil, fmt.Errorf("travu check_trip decode: %w", err)
	}
	return trips, nil
}

// BookTrip reserves bus seats and returns the confirmed booking receipt.
func (c *Client) BookTrip(ctx context.Context, req BookTripRequest) (*OrderReceipt, error) {
	if req.AgentEmail == "" {
		req.AgentEmail = c.cfg.AgentEmail
	}
	var receipt OrderReceipt
	if err := c.post(ctx, "/book_trip", req, &receipt); err != nil {
		return nil, fmt.Errorf("travu book_trip: %w", err)
	}
	if !receipt.Confirmed() {
		return &receipt, &StatusError{Message: "booking not confirmed", Info: receipt.OrderStatus}
	}
	return &receipt, nil
}

// --- Flight ---

// SearchFlight returns available flight options for an itinerary.
func (c *Client) SearchFlight(ctx context.Context, req SearchFlightRequest) ([]Trip, error) {
	body, err := c.postRaw(ctx, "/search_flight", req)
	if err != nil {
		return nil, fmt.Errorf("travu search_flight: %w", err)
	}
	trips, err := decodeTrips(body)
	if err != nil {
		return nil, fmt.Errorf("travu search_flight decode: %w", err)
	}
	return trips, nil
}

// SelectFlight retrieves the freshest priced itinerary before booking.
func (c *Client) SelectFlight(ctx context.Context, req SelectFlightRequest) (*OrderReceipt, error) {
	var receipt OrderReceipt
	if err := c.post(ctx, "/flight_select", req, &receipt); err != nil {
		return nil, fmt.Errorf("travu flight_select: %w", err)
	}
	return &receipt, nil
}

// TentativeFlightBooking places a tentative booking and returns booking_id+PNR.
func (c *Client) TentativeFlightBooking(ctx context.Context, passengers []FlightPassenger) (*OrderReceipt, error) {
	if len(passengers) == 0 {
		return nil, fmt.Errorf("travu flight_booking: at least one passenger required")
	}
	var receipt OrderReceipt
	if err := c.post(ctx, "/flight_booking", passengers, &receipt); err != nil {
		return nil, fmt.Errorf("travu flight_booking: %w", err)
	}
	if !receipt.Confirmed() {
		return &receipt, &StatusError{Message: "tentative booking not confirmed", Info: receipt.OrderStatus}
	}
	return &receipt, nil
}

// TicketFlight finalizes a tentative flight booking into an issued ticket.
func (c *Client) TicketFlight(ctx context.Context, req TicketFlightRequest) (*OrderReceipt, error) {
	var receipt OrderReceipt
	if err := c.post(ctx, "/flight_ticket", req, &receipt); err != nil {
		return nil, fmt.Errorf("travu flight_ticket: %w", err)
	}
	if !receipt.Confirmed() {
		return &receipt, &StatusError{Message: "ticketing not confirmed", Info: receipt.OrderStatus}
	}
	return &receipt, nil
}

// --- Booking management ---

// GetBookingDetails returns the real-time state of a single booking.
func (c *Client) GetBookingDetails(ctx context.Context, orderID string) (*OrderReceipt, error) {
	var receipt OrderReceipt
	if err := c.post(ctx, "/booking_details", map[string]string{"order_id": orderID}, &receipt); err != nil {
		return nil, fmt.Errorf("travu booking_details: %w", err)
	}
	return &receipt, nil
}

// CancelOrder cancels a booking.
func (c *Client) CancelOrder(ctx context.Context, orderID string) (*OrderReceipt, error) {
	var receipt OrderReceipt
	if err := c.post(ctx, "/cancel_order", map[string]string{"order_id": orderID}, &receipt); err != nil {
		return nil, fmt.Errorf("travu cancel_order: %w", err)
	}
	return &receipt, nil
}

// --- Reference data ---

// GetStates returns the supported bus states.
func (c *Client) GetStates(ctx context.Context) ([]State, error) {
	var resp listEnvelope[State]
	if err := c.get(ctx, "/states", &resp); err != nil {
		return nil, fmt.Errorf("travu states: %w", err)
	}
	return resp.Data, nil
}

// GetAirports returns the supported flight airports.
func (c *Client) GetAirports(ctx context.Context) ([]Airport, error) {
	var resp listEnvelope[Airport]
	if err := c.get(ctx, "/airports", &resp); err != nil {
		return nil, fmt.Errorf("travu airports: %w", err)
	}
	return resp.Data, nil
}

// --- HTTP helpers ---

func (c *Client) post(ctx context.Context, path string, body, dest interface{}) error {
	raw, err := c.postRaw(ctx, path, body)
	if err != nil {
		return err
	}
	if dest != nil {
		if err := json.Unmarshal(raw, dest); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}

func (c *Client) postRaw(ctx context.Context, path string, body interface{}) ([]byte, error) {
	return c.do(ctx, http.MethodPost, path, body)
}

func (c *Client) get(ctx context.Context, path string, dest interface{}) error {
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if dest != nil {
		if err := json.Unmarshal(raw, dest); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		payload = b
	}

	if body != nil {
		c.logger.Debug("Travu request",
			zap.String("method", method),
			zap.String("path", path),
			zap.String("body_safe", maskDigits(string(payload))))
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.cfg.SecretKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("travu request failed: %w", err)
			c.logger.Warn("Travu request failed", zap.String("path", path), zap.Int("attempt", attempt+1), zap.Error(err))
			continue
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		// Retry transient failures only. 4xx are returned immediately.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = &APIError{StatusCode: resp.StatusCode, Body: string(respBody), Path: path}
			c.logger.Warn("Travu retryable error", zap.Int("status", resp.StatusCode), zap.Int("attempt", attempt+1))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			c.logger.Warn("Travu API error",
				zap.Int("status", resp.StatusCode),
				zap.String("path", path),
				zap.String("error_detail", extractErrorMessage(respBody)),
				zap.String("body_safe", maskDigits(string(respBody))))
			return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody), Path: path}
		}

		return respBody, nil
	}
	return nil, fmt.Errorf("travu %s %s failed after %d attempts: %w", method, path, c.cfg.MaxRetries+1, lastErr)
}
