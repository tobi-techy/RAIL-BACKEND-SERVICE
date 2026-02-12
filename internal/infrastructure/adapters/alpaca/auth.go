package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// TokenManager handles OAuth2 token lifecycle
type TokenManager struct {
	clientID     string
	clientSecret string
	authBaseURL  string
	accessToken  string
	expiresAt    time.Time
	mutex        sync.RWMutex
	logger       *zap.Logger
}

// TokenResponse represents OAuth2 token response
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// NewTokenManager creates a new token manager
func NewTokenManager(clientID, clientSecret, environment string, logger *zap.Logger) *TokenManager {
	authBaseURL := "https://authx.alpaca.markets"
	if environment == "sandbox" {
		authBaseURL = "https://authx.sandbox.alpaca.markets"
	}

	return &TokenManager{
		clientID:     clientID,
		clientSecret: clientSecret,
		authBaseURL:  authBaseURL,
		logger:       logger,
	}
}

// GetValidToken returns a valid access token, refreshing if necessary
func (tm *TokenManager) GetValidToken(ctx context.Context) (string, error) {
	tm.mutex.RLock()
	if tm.accessToken != "" && time.Now().Before(tm.expiresAt.Add(-30*time.Second)) {
		token := tm.accessToken
		tm.mutex.RUnlock()
		return token, nil
	}
	tm.mutex.RUnlock()

	return tm.refreshToken(ctx)
}

// refreshToken obtains a new access token
func (tm *TokenManager) refreshToken(ctx context.Context) (string, error) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// Double-check after acquiring write lock
	if tm.accessToken != "" && time.Now().Before(tm.expiresAt.Add(-30*time.Second)) {
		return tm.accessToken, nil
	}

	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {tm.clientID},
		"client_secret": {tm.clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tm.authBaseURL+"/v1/oauth2/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request failed with status %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	tm.accessToken = tokenResp.AccessToken
	tm.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	tm.logger.Info("OAuth2 token refreshed", zap.Time("expires_at", tm.expiresAt))

	return tm.accessToken, nil
}
