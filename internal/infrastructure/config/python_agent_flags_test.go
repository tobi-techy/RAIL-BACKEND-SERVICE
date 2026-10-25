package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func loadWithRequired(t *testing.T) *Config {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("ENCRYPTION_KEY", "test-encryption-key")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/test")
	cfg, err := Load()
	require.NoError(t, err)
	return cfg
}

// Every flag added for the Python refactor defaults off, so deploying Go
// first changes nothing that still works and stops nothing from 5xx-ing.
func TestPythonRefactorFlagsDefaultOff(t *testing.T) {
	cfg := loadWithRequired(t)
	pa := cfg.PythonAgent
	require.False(t, pa.TurnsEnabled)
	require.False(t, pa.NotifyInflowEnabled)
	require.False(t, pa.NotifyDebitEnabled)
	require.False(t, pa.LegacySettleEnabled)
	require.False(t, pa.InflowNotifyActive())
	require.False(t, pa.DebitNotifyActive())
	require.False(t, pa.LegacySettleActive())
	require.Equal(t, 25, pa.TurnsTextTimeoutSeconds)
	require.Equal(t, 150, pa.TurnsMediaTimeoutSeconds)
}

func TestPythonRefactorFlagsFromEnv(t *testing.T) {
	t.Setenv("MIMI_TURNS_ENABLED", "true")
	t.Setenv("MIMI_TURNS_TENANT_ID", " dist_pilot ")
	t.Setenv("MIMI_TURNS_DISTRIBUTOR_NAME", "Ade Distributors")
	t.Setenv("MIMI_TURNS_MEDIA_TIMEOUT_SECONDS", "170")
	t.Setenv("PYTHON_AGENT_NOTIFY_INFLOW_ENABLED", "1")
	t.Setenv("PYTHON_AGENT_LEGACY_SETTLE_ENABLED", "true")
	cfg := loadWithRequired(t)
	pa := cfg.PythonAgent
	require.True(t, pa.TurnsEnabled)
	require.Equal(t, "dist_pilot", pa.TurnsTenantID)
	require.Equal(t, "Ade Distributors", pa.TurnsDistributorName)
	require.Equal(t, 170, pa.TurnsMediaTimeoutSeconds)
	require.True(t, pa.NotifyInflowEnabled)
	// The turns brain has none of the legacy endpoints, so turning turns on
	// forces every legacy call off whatever its own flag says.
	require.False(t, pa.InflowNotifyActive())
	require.False(t, pa.LegacySettleActive())
}

func TestLegacyFlagsWorkWhileTurnsIsOff(t *testing.T) {
	pa := PythonAgentConfig{NotifyInflowEnabled: true, NotifyDebitEnabled: true, LegacySettleEnabled: true}
	require.True(t, pa.InflowNotifyActive())
	require.True(t, pa.DebitNotifyActive())
	require.True(t, pa.LegacySettleActive())
}
