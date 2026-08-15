package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestAssemblyAIVoiceDefaultIsPremiumMiriamVoice(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	setDefaults()

	require.Equal(t, "ivy", viper.GetString("ai.assemblyai.voice"))
}

func TestLoadRegistersCloudflareAICredentials(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	// Provide the required-config env vars so Load's validation passes.
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("ENCRYPTION_KEY", "test-encryption-key")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/test")

	t.Setenv("AI_CLOUDFLARE_ACCOUNT_ID", "acct-123")
	t.Setenv("AI_CLOUDFLARE_API_TOKEN", "tok-456")
	t.Setenv("AI_CLOUDFLARE_GATEWAY_BASE_URL", "https://gateway.example.com/v1/acct/gw")
	t.Setenv("AI_CLOUDFLARE_GATEWAY_API_KEY", "gw-key")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "acct-123", cfg.AI.Cloudflare.AccountID)
	require.Equal(t, "tok-456", cfg.AI.Cloudflare.APIToken)
	require.Equal(t, "https://gateway.example.com/v1/acct/gw", cfg.AI.Cloudflare.Gateway.BaseURL)
	require.Equal(t, "gw-key", cfg.AI.Cloudflare.Gateway.APIKey)
}
