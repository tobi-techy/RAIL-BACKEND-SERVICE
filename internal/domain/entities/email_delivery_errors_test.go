package entities

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// These pin the classification contract the OTP path depends on. It has to
// survive fmt.Errorf wrapping, because that is how it travels from the email
// adapter through the verification service to the onboarding copy.
func TestEmailDeliveryError_Classification(t *testing.T) {
	permanent := &EmailDeliveryError{
		Provider:   "unosend",
		Recipient:  "blocked@example.com",
		StatusCode: 400,
		Reason:     EmailReasonRecipientSuppressed,
		Permanent:  true,
	}
	transient := &EmailDeliveryError{
		Provider:   "unosend",
		Recipient:  "person@example.com",
		StatusCode: 500,
	}

	if !errors.Is(permanent, ErrEmailRecipientUndeliverable) {
		t.Error("a permanent rejection must match ErrEmailRecipientUndeliverable")
	}
	if errors.Is(permanent, ErrEmailDeliveryTransient) {
		t.Error("a permanent rejection must not match the transient sentinel")
	}
	if !errors.Is(transient, ErrEmailDeliveryTransient) {
		t.Error("a transient failure must match ErrEmailDeliveryTransient")
	}
	if errors.Is(transient, ErrEmailRecipientUndeliverable) {
		t.Error("a transient failure must not match the permanent sentinel")
	}

	if !IsPermanentEmailDeliveryError(fmt.Errorf("failed to send verification code: %w", permanent)) {
		t.Error("permanence must survive wrapping, or the onboarding copy never fires")
	}
	if IsPermanentEmailDeliveryError(fmt.Errorf("failed to send verification code: %w", transient)) {
		t.Error("a wrapped transient failure must not read as permanent")
	}

	// Non-email failures and nil must stay false: the callers use this to decide
	// whether to refund OTP budget and to stop retrying.
	if IsPermanentEmailDeliveryError(errors.New("resend: status 500")) {
		t.Error("an unclassified error must not read as permanent")
	}
	if IsPermanentEmailDeliveryError(nil) {
		t.Error("nil must not read as permanent")
	}
}

func TestEmailDeliveryError_ErrorString(t *testing.T) {
	err := &EmailDeliveryError{
		Provider:   "unosend",
		Recipient:  "blocked@example.com",
		StatusCode: 400,
		Reason:     EmailReasonRecipientSuppressed,
		Permanent:  true,
	}
	got := err.Error()
	for _, want := range []string{"permanent", "unosend", "blocked@example.com", "400", EmailReasonRecipientSuppressed} {
		if !strings.Contains(got, want) {
			t.Errorf("error string %q is missing %q", got, want)
		}
	}

	// A missing reason must not render as an empty field.
	noReason := &EmailDeliveryError{Provider: "resend", StatusCode: 429}
	if !strings.Contains(noReason.Error(), "unspecified") {
		t.Errorf("expected a placeholder reason, got %q", noReason.Error())
	}
}
