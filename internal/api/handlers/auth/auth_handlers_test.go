package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
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

// TestVerificationSendError maps send failures onto the client's response. The
// important case is the permanent, recipient-level rejection: it is the person's
// own address, retrying cannot fix it, and the default "please try again" would
// keep them on a loop — the same lie the chat copy used to tell.
func TestVerificationSendError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		containsIn string
	}{
		{
			name: "permanently refused recipient is reported precisely",
			err: fmt.Errorf("failed to send verification code: %w", &entities.EmailDeliveryError{
				Provider:  "unosend",
				Recipient: "blocked@example.com",
				Reason:    entities.EmailReasonRecipientSuppressed,
				Permanent: true,
			}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "EMAIL_UNDELIVERABLE",
			containsIn: "different email address",
		},
		{
			name:       "per-minute rate limit stays a 429",
			err:        errors.New("too many verification code send attempts. Please try again after 1m0s"),
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "TOO_MANY_REQUESTS",
			containsIn: "Please wait",
		},
		{
			name:       "a provider blip stays vague",
			err:        errors.New("resend: status 500"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "VERIFICATION_SEND_FAILED",
			containsIn: "try again",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, message := verificationSendError(tt.err)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if !strings.Contains(message, tt.containsIn) {
				t.Errorf("message %q does not contain %q", message, tt.containsIn)
			}
		})
	}
}
