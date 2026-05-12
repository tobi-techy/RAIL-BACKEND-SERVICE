package superteam

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	defaultBaseURL = "https://superteam.fun"
	defaultTimeout = 15 * time.Second
)

// Config holds Superteam Earn API configuration.
type Config struct {
	APIKey  string
	BaseURL string
	Timeout time.Duration
}

// Client wraps the Superteam Earn agent API.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
}

func NewClient(cfg Config, logger *zap.Logger) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		logger:     logger,
	}
}

// Listing represents a Superteam Earn listing from the API.
type Listing struct {
	ID               string          `json:"id"`
	Slug             string          `json:"slug"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	Type             string          `json:"type"` // bounty, project, hackathon
	Skills           []SkillEntry    `json:"skills"`
	Token            string          `json:"token"`
	RewardAmount     *float64        `json:"rewardAmount"`
	USDValue         *float64        `json:"usdValue"`
	Deadline         *string         `json:"deadline"`
	Sponsor          *SponsorInfo    `json:"sponsor"`
	URL              string          `json:"url"`
	AgentAccess      string          `json:"agentAccess"`
	CompensationType string          `json:"compensationType"`
	Status           string          `json:"status"`
	Region           string          `json:"region"`
	Raw              json.RawMessage `json:"-"` // full raw JSON
}

type SkillEntry struct {
	Skills string `json:"skills"`
}

type SponsorInfo struct {
	Name string `json:"name"`
	Logo string `json:"logo"`
}

// ListingsResponse is the API response for listings.
type ListingsResponse []Listing

// FetchListings fetches live agent-eligible listings from Superteam Earn.
// Supports type filter: bounty, project, hackathon, or empty for all.
func (c *Client) FetchListings(ctx context.Context, listingType string, take int) ([]Listing, error) {
	if take <= 0 {
		take = 50
	}

	url := fmt.Sprintf("%s/api/agents/listings/live?take=%d", c.cfg.BaseURL, take)
	if listingType != "" {
		url += "&type=" + listingType
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch listings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("superteam API returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5MB max
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var listings []Listing
	if err := json.Unmarshal(body, &listings); err != nil {
		return nil, fmt.Errorf("decode listings: %w", err)
	}

	// Store raw JSON on each listing
	var rawListings []json.RawMessage
	_ = json.Unmarshal(body, &rawListings)
	for i := range listings {
		if i < len(rawListings) {
			listings[i].Raw = rawListings[i]
		}
	}

	c.logger.Info("Fetched Superteam Earn listings", zap.Int("count", len(listings)), zap.String("type", listingType))
	return listings, nil
}

// FetchListingDetails fetches details for a specific listing by slug.
func (c *Client) FetchListingDetails(ctx context.Context, slug string) (*Listing, error) {
	url := fmt.Sprintf("%s/api/agents/listings/details/%s", c.cfg.BaseURL, slug)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch listing details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("superteam API returned %d: %s", resp.StatusCode, string(body))
	}

	var listing Listing
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return nil, fmt.Errorf("decode listing: %w", err)
	}
	return &listing, nil
}
