package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/services/withdrawal"
)

func TestAdminAlertService_SendErrorAlert_NilService_NoPanic(t *testing.T) {
	var svc *AdminAlertService
	// Should not panic — nil-safe
	svc.SendErrorAlert(context.Background(), withdrawal.AdminErrorPayload{
		UserID:    "user-1",
		Operation: "test",
		Error:     errors.New("test"),
	})
}

func TestAdminAlertService_SendErrorAlert_NilEmailService_NoPanic(t *testing.T) {
	svc := &AdminAlertService{
		emailService: nil,
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}
	svc.SendErrorAlert(context.Background(), withdrawal.AdminErrorPayload{
		UserID:    "user-1",
		Operation: "test",
		Error:     errors.New("test"),
	})
}

func TestAdminAlertService_SendErrorAlert_EmptyAdminEmail_NoPanic(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "",
		logger:       zap.NewNop(),
	}
	svc.SendErrorAlert(context.Background(), withdrawal.AdminErrorPayload{
		UserID:    "user-1",
		Operation: "test",
		Error:     errors.New("test"),
	})
}

func TestAdminAlertService_BuildText_ContainsAllFields(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}

	payload := withdrawal.AdminErrorPayload{
		UserID:    "user-abc-123",
		Operation: "crypto_withdrawal",
		Error:     errors.New("nil pointer dereference"),
		PanicStack: []byte("goroutine 1 [running]:\nmain.main()"),
		ExtraFields: map[string]string{
			"withdrawal_id": "wd-456",
			"amount":        "50.00",
			"currency":      "USDC",
		},
	}

	text := svc.buildText(payload)

	assert.Contains(t, text, "RAIL ERROR ALERT")
	assert.Contains(t, text, "crypto_withdrawal")
	assert.Contains(t, text, "user-abc-123")
	assert.Contains(t, text, "nil pointer dereference")
	assert.Contains(t, text, "goroutine 1 [running]:")
	assert.Contains(t, text, "withdrawal_id: wd-456")
	assert.Contains(t, text, "amount: 50.00")
	assert.Contains(t, text, "currency: USDC")
	assert.Contains(t, text, "Time:")
	assert.Contains(t, text, "UTC")
}

func TestAdminAlertService_BuildText_OmitsEmptyUserID(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}

	payload := withdrawal.AdminErrorPayload{
		Operation: "test_op",
		Error:     errors.New("err"),
	}

	text := svc.buildText(payload)
	assert.NotContains(t, text, "User ID:")
}

func TestAdminAlertService_BuildText_OmitsNilError(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}

	payload := withdrawal.AdminErrorPayload{
		Operation: "test_op",
	}

	text := svc.buildText(payload)
	assert.NotContains(t, text, "Error:")
}

func TestAdminAlertService_BuildText_OmitsEmptyStack(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}

	payload := withdrawal.AdminErrorPayload{
		Operation: "test_op",
		Error:     errors.New("err"),
	}

	text := svc.buildText(payload)
	assert.NotContains(t, text, "Stack Trace:")
}

func TestAdminAlertService_BuildHTML_ContainsAllFields(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}

	payload := withdrawal.AdminErrorPayload{
		UserID:    "user-789",
		Operation: "crypto_withdrawal_stash_yield",
		Error:     errors.New("blend session expired"),
		PanicStack: []byte("goroutine 5 [running]:\nfoo.go:42"),
		ExtraFields: map[string]string{
			"withdrawal_id": "wd-999",
			"amount":        "100.50",
		},
	}

	html := svc.buildHTML(payload)

	assert.Contains(t, html, "crypto_withdrawal_stash_yield failed")
	assert.Contains(t, html, "blend session expired")
	assert.Contains(t, html, "goroutine 5")
	assert.Contains(t, html, "withdrawal_id")
	assert.Contains(t, html, "wd-999")
	assert.Contains(t, html, "100.50")
	assert.Contains(t, html, "pre-wrap")
	assert.Contains(t, html, "<!DOCTYPE html>")
}

func TestAdminAlertService_BuildHTML_HTMLEscapesUserInput(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}

	payload := withdrawal.AdminErrorPayload{
		UserID:    "<script>alert('xss')</script>",
		Operation: "test<script>",
		Error:     errors.New("<img src=x onerror=alert(1)>"),
		ExtraFields: map[string]string{
			"key": "<b>bold</b>",
		},
	}

	html := svc.buildHTML(payload)

	assert.NotContains(t, html, "<script>alert('xss')</script>")
	assert.Contains(t, html, "&lt;script&gt;")
	assert.Contains(t, html, "&lt;img")
	assert.NotContains(t, html, "<img src=x onerror=alert(1)>")
}

func TestAdminAlertService_BuildHTML_HandlesEmptyPayload(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}

	payload := withdrawal.AdminErrorPayload{
		Operation: "minimal_test",
	}

	html := svc.buildHTML(payload)
	assert.Contains(t, html, "minimal_test failed")
	assert.Contains(t, html, "<!DOCTYPE html>")
}

func TestAdminAlertService_BuildText_HandlesEmptyExtraFields(t *testing.T) {
	svc := &AdminAlertService{
		emailService: &EmailService{},
		adminEmail:   "admin@example.com",
		logger:       zap.NewNop(),
	}

	payload := withdrawal.AdminErrorPayload{
		Operation: "test",
		Error:     errors.New("err"),
	}

	text := svc.buildText(payload)
	// Should not crash and should contain the operation
	assert.Contains(t, text, "test")
}

func TestCaptureStack_ReturnsNonEmpty(t *testing.T) {
	stack := CaptureStack()
	require.NotEmpty(t, stack)
	assert.True(t, strings.Contains(string(stack), "goroutine") || strings.Contains(string(stack), "runtime"))
}
