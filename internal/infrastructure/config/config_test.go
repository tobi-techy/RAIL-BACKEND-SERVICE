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

// TestLoad_EmailProviderDecidesWhichKeyIsPrimary pins the pairing that used to
// be wrong: the env block bound Resend's key and then unconditionally overwrote
// it with Unosend's, so `EMAIL_PROVIDER=resend` shipped a deploy that
// authenticated against Resend with the Unosend secret. The key belonging to
// the *other* provider now becomes the fallback, which is what lets an OTP reach
// a recipient the primary provider has suppressed.
func TestLoad_EmailProviderDecidesWhichKeyIsPrimary(t *testing.T) {
	const (
		resendKey  = "re_primary_or_fallback"
		unosendKey = "un_other_provider"
	)

	tests := []struct {
		name             string
		provider         string
		resendKey        string
		unosendKey       string
		fallbackProvider string
		fallbackKey      string
		wantAPIKey       string
		wantFallbackProv string
		wantFallbackKey  string
	}{
		{
			name:             "unosend primary hoists the resend key to the fallback",
			provider:         "unosend",
			resendKey:        resendKey,
			unosendKey:       unosendKey,
			wantAPIKey:       unosendKey,
			wantFallbackProv: "resend",
			wantFallbackKey:  resendKey,
		},
		{
			name:             "resend primary hoists the unosend key to the fallback",
			provider:         "resend",
			resendKey:        resendKey,
			unosendKey:       unosendKey,
			wantAPIKey:       resendKey,
			wantFallbackProv: "unosend",
			wantFallbackKey:  unosendKey,
		},
		{
			name:       "a provider without the other key has no fallback",
			provider:   "unosend",
			unosendKey: unosendKey,
			wantAPIKey: unosendKey,
		},
		{
			name:       "provider name is normalised before it decides anything",
			provider:   " Unosend ",
			unosendKey: unosendKey,
			resendKey:  resendKey,
			wantAPIKey: unosendKey,
			// The provider string itself is normalised in the email adapter, but
			// the fallback decision must already treat it as unosend.
			wantFallbackProv: "resend",
			wantFallbackKey:  resendKey,
		},
		{
			// Naming a fallback provider explicitly must not leave the key that
			// belonged to the derived provider attached to it: a keyed fallback
			// needs both vars, and SES carries no key at all.
			name:             "explicit fallback provider wins and drops the derived key",
			provider:         "unosend",
			unosendKey:       unosendKey,
			resendKey:        resendKey,
			fallbackProvider: "ses",
			wantAPIKey:       unosendKey,
			wantFallbackProv: "ses",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)

			// Required-config env vars so Load's validation passes.
			t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
			t.Setenv("ENCRYPTION_KEY", "test-encryption-key")
			t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/test")

			// Always set all three, empty included, so a key present in the
			// developer's shell cannot change the expected pairing.
			t.Setenv("EMAIL_PROVIDER", tc.provider)
			t.Setenv("RESEND_API_KEY", tc.resendKey)
			t.Setenv("UNOSEND_API_KEY", tc.unosendKey)
			t.Setenv("EMAIL_FALLBACK_PROVIDER", tc.fallbackProvider)
			t.Setenv("EMAIL_FALLBACK_API_KEY", tc.fallbackKey)

			cfg, err := Load()
			require.NoError(t, err)

			require.Equal(t, tc.wantAPIKey, cfg.Email.APIKey)
			require.Equal(t, tc.wantFallbackProv, cfg.Email.FallbackProvider)
			require.Equal(t, tc.wantFallbackKey, cfg.Email.FallbackAPIKey)
		})
	}
}
