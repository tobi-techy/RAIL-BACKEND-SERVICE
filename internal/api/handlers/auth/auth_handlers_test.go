package auth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rail-service/rail_service/internal/domain/services/session"
)

func TestIsRefreshSessionConsumedClassifiesOnlyReplayErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "missing refresh session",
			err:  session.ErrRefreshSessionNotFound,
			want: true,
		},
		{
			name: "wrapped rotation conflict",
			err:  fmt.Errorf("rotate refresh token: %w", session.ErrSessionRotationConflict),
			want: true,
		},
		{
			name: "database failure",
			err:  errors.New("failed to rotate session tokens: database unavailable"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRefreshSessionConsumed(tt.err); got != tt.want {
				t.Fatalf("isRefreshSessionConsumed() = %v, want %v", got, tt.want)
			}
		})
	}
}
