package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func internalKeyConfig(env, jwtSecret, internalKey string) *Config {
	cfg := &Config{Environment: env}
	cfg.JWT.Secret = jwtSecret
	cfg.Security.InternalAPIKey = internalKey
	return cfg
}

func TestValidateInternalAPIKey(t *testing.T) {
	signing := strings.Repeat("s", 40)
	distinct := strings.Repeat("k", 40)

	t.Run("production refuses to boot without the internal key", func(t *testing.T) {
		err := validateInternalAPIKey(internalKeyConfig("production", signing, ""))
		require.ErrorContains(t, err, "SECURITY_INTERNAL_API_KEY is required in production")
	})
	t.Run("production refuses a short key", func(t *testing.T) {
		err := validateInternalAPIKey(internalKeyConfig("production", signing, "short"))
		require.ErrorContains(t, err, "at least 32 characters")
	})
	t.Run("the JWT signing secret is refused as the internal key in every environment", func(t *testing.T) {
		for _, env := range []string{"production", "staging", "development", ""} {
			err := validateInternalAPIKey(internalKeyConfig(env, signing, signing))
			require.ErrorContains(t, err, "must not equal JWT_SECRET", env)
		}
	})
	t.Run("production accepts a distinct long key", func(t *testing.T) {
		require.NoError(t, validateInternalAPIKey(internalKeyConfig("production", signing, distinct)))
	})
	t.Run("development may leave the key unset", func(t *testing.T) {
		require.NoError(t, validateInternalAPIKey(internalKeyConfig("development", signing, "")))
	})
}
