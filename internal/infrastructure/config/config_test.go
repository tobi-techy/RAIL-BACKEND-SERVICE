package config

import (
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
