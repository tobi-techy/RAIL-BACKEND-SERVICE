package deposit_autosweep

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	"github.com/stretchr/testify/require"
)

func TestIsTerminalSweepError(t *testing.T) {
	require.True(t, isTerminalSweepError(newTerminalSweepError("unsupported source chain: %s", "DOGE")))
	require.True(t, isTerminalSweepError(fmt.Errorf("create chainrails intent: %w", &chainrails.APIError{
		StatusCode: http.StatusBadRequest,
		Body:       "invalid recipient",
	})))
	require.False(t, isTerminalSweepError(fmt.Errorf("create chainrails intent: %w", &chainrails.APIError{
		StatusCode: http.StatusTooManyRequests,
		Body:       "rate limited",
	})))
	require.False(t, isTerminalSweepError(fmt.Errorf("create chainrails intent: %w", &chainrails.APIError{
		StatusCode: http.StatusInternalServerError,
		Body:       "server error",
	})))
	require.False(t, isTerminalSweepError(fmt.Errorf("network unavailable")))
}
