package bridge

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsTerminalError(t *testing.T) {
	terminal := func(code int) error {
		// Mirror the production wrap chain: client wraps once, adapter wraps again.
		inner := &ErrorResponse{StatusCode: code, Code: "test", Message: "test failure"}
		return fmt.Errorf("update customer failed: %w", fmt.Errorf("update customer failed: %w", inner))
	}

	require.False(t, IsTerminalError(nil))
	require.False(t, IsTerminalError(errors.New("connection reset")))

	// Deleted/gone customers and rejected payloads must stop retry loops.
	for _, code := range []int{400, 404, 409, 410, 422} {
		require.True(t, IsTerminalError(terminal(code)), "status %d should be terminal", code)
	}

	// Transient conditions must keep retrying.
	require.False(t, IsTerminalError(terminal(408)))
	require.False(t, IsTerminalError(terminal(429)))
	require.False(t, IsTerminalError(terminal(500)))
}
