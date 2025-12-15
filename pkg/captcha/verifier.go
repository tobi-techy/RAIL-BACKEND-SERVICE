package captcha

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Provider represents the CAPTCHA provider type
type Provider string

const (
	ProviderRecaptcha Provider = "recaptcha"
	ProviderHCaptcha  Provider = "hcaptcha"
)

// Config holds CAPTCHA configuration
type Config struct {
	Enabled        bool     `mapstructure:"enabled"`
	Provider       Provider `mapstructure:"provider"`
	SecretKey      string   `mapstructure:"secret_key"`
	ScoreThreshold float64  `mapstructure:"score_threshold"` // For reCAPTCHA v3
}

// Verifier verifies CAPTCHA tokens
type Verifier struct {
	config     Config
	verifyURL  string
	httpClient *http.Client
}

// Response represents the CAPTCHA verification response
type Response struct {
	Success     bool      `json:"success"`
	ChallengeTS time.Time `json:"challenge_ts"`
	Hostname    string    `json:"hostname"`
	ErrorCodes  []string  `json:"error-codes,omitempty"`
	Score       float64   `json:"score,omitempty"`  // reCAPTCHA v3 only
	Action      string    `json:"action,omitempty"` // reCAPTCHA v3 only
}

// NewVerifier creates a new CAPTCHA verifier
func NewVerifier(config Config) *Verifier {
	verifyURL := "https://www.google.com/recaptcha/api/siteverify"
	if config.Provider == ProviderHCaptcha {
		verifyURL = "https://hcaptcha.com/siteverify"
	}

	if config.ScoreThreshold == 0 {
		config.ScoreThreshold = 0.5 // Default threshold for reCAPTCHA v3
	}

	return &Verifier{
		config:    config,
		verifyURL: verifyURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Verify verifies a CAPTCHA token
func (v *Verifier) Verify(ctx context.Context, token string, remoteIP string) (bool, error) {
	if !v.config.Enabled {
		return true, nil
	}

	if v.config.SecretKey == "" {
		return true, nil // Skip verification if not configured
	}

	resp, err := v.httpClient.PostForm(v.verifyURL, url.Values{
		"secret":   {v.config.SecretKey},
		"response": {token},
		"remoteip": {remoteIP},
	})
	if err != nil {
		return false, fmt.Errorf("captcha verification request failed: %w", err)
	}
	defer resp.Body.Close()

	var result Response
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode captcha response: %w", err)
	}

	if !result.Success {
		return false, nil
	}

	// For reCAPTCHA v3, also check score
	if v.config.Provider == ProviderRecaptcha && result.Score > 0 {
		if result.Score < v.config.ScoreThreshold {
			return false, nil
		}
	}

	return true, nil
}

// IsEnabled returns whether CAPTCHA verification is enabled
func (v *Verifier) IsEnabled() bool {
	return v.config.Enabled && v.config.SecretKey != ""
}
