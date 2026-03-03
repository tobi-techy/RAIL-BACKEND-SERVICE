package circle

import (
	"fmt"
	"testing"

	"go.uber.org/zap"
)

func TestCircleEnvironmentConfig(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	tests := []struct {
		name        string
		env         string
		baseURL     string
		wantBaseURL string
	}{
		{"sandbox", "sandbox", "", "https://api-sandbox.circle.com"},
		{"production", "production", "", "https://api.circle.com"},
		{"mainnet", "mainnet", "", "https://api.circle.com"},
		{"empty defaults to production", "", "", "https://api.circle.com"},
		{"explicit base URL", "sandbox", "https://custom.url", "https://custom.url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				APIKey:      "test-key",
				Environment: tt.env,
				BaseURL:     tt.baseURL,
			}

			client := NewClient(config, logger)

			if client.config.BaseURL != tt.wantBaseURL {
				t.Errorf("got baseURL %q, want %q", client.config.BaseURL, tt.wantBaseURL)
			}

			fmt.Printf("Environment: %s, BaseURL: %s\n", tt.env, client.config.BaseURL)
		})
	}
}
