package execution

import (
	"context"
	"testing"
)

func TestWithEmailLinkVerification(t *testing.T) {
	ctx := WithEmailLinkVerification(context.Background())
	if !IsEmailLinkVerified(ctx) {
		t.Fatal("IsEmailLinkVerified should return true after WithEmailLinkVerification")
	}
}

func TestIsEmailLinkVerified_Default(t *testing.T) {
	if IsEmailLinkVerified(context.Background()) {
		t.Fatal("IsEmailLinkVerified should return false on a plain context")
	}
}

func TestWithEmailLinkVerification_DoesNotSetStepUpToken(t *testing.T) {
	ctx := WithEmailLinkVerification(context.Background())
	// Email link verification should NOT set a step-up token — they are
	// independent verification paths.
	if token := StepUpTokenFromContext(ctx); token != "" {
		t.Fatalf("email link verification should not set step-up token, got %q", token)
	}
}

func TestWithStepUpToken_DoesNotSetEmailLink(t *testing.T) {
	ctx := WithStepUpToken(context.Background(), "my-token")
	if IsEmailLinkVerified(ctx) {
		t.Fatal("step-up token should not set email link verification")
	}
}
