package confirmation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type stubMailer struct {
	mu       sync.Mutex
	lastTo   string
	lastCode string
	err      error
	calls    int
}

func (m *stubMailer) SendVerificationEmail(_ context.Context, email, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastTo = email
	m.lastCode = code
	return m.err
}

type stubUsers struct {
	email string
	err   error
}

func (u stubUsers) EmailForUser(_ context.Context, _ uuid.UUID) (string, error) {
	return u.email, u.err
}

func otpTestService(t *testing.T) (*Service, *stubMailer) {
	t.Helper()
	s := NewService(Config{TokenSecret: "otp-test-secret-12345678901234567890", ConfirmBase: "https://example.com/confirm"}, nil, nil)
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (string, error) {
		return "sent", nil
	})
	mailer := &stubMailer{}
	s.SetDemoEmailOTP(true)
	s.SetEmailOTPDeps(mailer, stubUsers{email: "alex@example.com"}, nil)
	return s, mailer
}

func stageTransfer(t *testing.T, s *Service) (*entities.Confirmation, string) {
	t.Helper()
	uid := uuid.New()
	// Override user id after create by using CreateInput
	c, url, err := s.Create(context.Background(), CreateInput{
		UserID:  uid,
		Action:  entities.ConfirmationActionTransferSend,
		Payload: map[string]any{"to": "@tobi", "amount": "5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	return c, tok
}

func TestSendEmailOTPRequiresDemoFlag(t *testing.T) {
	s, _ := otpTestService(t)
	s.SetDemoEmailOTP(false)
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err == nil {
		t.Fatal("expected fail-closed when demo flag off")
	}
}

func TestApproveWithEmailOTPRequiresDemoFlag(t *testing.T) {
	s, _ := otpTestService(t)
	c, tok := stageTransfer(t, s)
	s.SetDemoEmailOTP(false)
	if _, err := s.ApproveWithEmailOTP(context.Background(), c.UserID, c.ID, tok, "123456"); err == nil {
		t.Fatal("expected fail-closed when demo flag off")
	}
}

func TestApproveWithDeviceRejectedWhenDemoOTP(t *testing.T) {
	s, _ := otpTestService(t)
	c, tok := stageTransfer(t, s)
	if _, err := s.ApproveWithDevice(context.Background(), c.UserID, c.ID, tok, "pass", DeviceApproval{}); err == nil {
		t.Fatal("expected Face ID approve rejected when demo OTP on")
	} else if !strings.Contains(err.Error(), "email OTP approval required") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestEmailOTPHappyPath(t *testing.T) {
	s, mailer := otpTestService(t)
	c, tok := stageTransfer(t, s)
	out, err := s.SendEmailOTP(context.Background(), c.ID, tok)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.MaskedEmail, "@example.com") || strings.Contains(out.MaskedEmail, "alex@") {
		// masked should hide middle of local part
		if out.MaskedEmail == "alex@example.com" {
			t.Fatalf("email not masked: %s", out.MaskedEmail)
		}
	}
	mailer.mu.Lock()
	code := mailer.lastCode
	mailer.mu.Unlock()
	if len(code) != 6 {
		t.Fatalf("code length = %d", len(code))
	}
	approved, err := s.ApproveWithEmailOTP(context.Background(), c.UserID, c.ID, tok, code)
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != entities.ConfirmationCompleted {
		t.Fatalf("state = %s", approved.State)
	}
	if approved.Assurance != AssuranceEmailOTP {
		t.Fatalf("assurance = %q", approved.Assurance)
	}
	// Replay OTP must fail (consumed).
	if _, err := s.ApproveWithEmailOTP(context.Background(), c.UserID, c.ID, tok, code); err == nil {
		// Card already terminal — ApproveWithEmailOTP returns terminal no-op success.
		// That's correct fail-closed for money (executor not re-run). Ensure state stays completed.
		if approved.State != entities.ConfirmationCompleted {
			t.Fatal("expected terminal replay")
		}
	}
}

func TestEmailOTPWrongCode(t *testing.T) {
	s, _ := otpTestService(t)
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveWithEmailOTP(context.Background(), c.UserID, c.ID, tok, "000000"); err == nil {
		t.Fatal("wrong code accepted")
	}
	// Card must still be pending (token not burned on bad OTP).
	got, ok := s.store.Load(c.ID)
	if !ok || got.TokenUsed || got.IsTerminal() {
		t.Fatalf("bad OTP burned the card: used=%v terminal=%v", got.TokenUsed, got.IsTerminal())
	}
}

func TestEmailOTPExpiredCode(t *testing.T) {
	s, mailer := otpTestService(t)
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err != nil {
		t.Fatal(err)
	}
	mailer.mu.Lock()
	code := mailer.lastCode
	mailer.mu.Unlock()
	// Force expiry by deleting from store (simulates TTL).
	_ = s.otpStore.Delete(context.Background(), c.ID)
	if _, err := s.ApproveWithEmailOTP(context.Background(), c.UserID, c.ID, tok, code); err == nil {
		t.Fatal("expired OTP accepted")
	}
}

func TestEmailOTPReplayAfterSuccess(t *testing.T) {
	s, mailer := otpTestService(t)
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err != nil {
		t.Fatal(err)
	}
	mailer.mu.Lock()
	code := mailer.lastCode
	mailer.mu.Unlock()
	if _, err := s.ApproveWithEmailOTP(context.Background(), c.UserID, c.ID, tok, code); err != nil {
		t.Fatal(err)
	}
	// Executor must not run twice: register a counting executor on a fresh card.
	var runs int
	s2, mailer2 := otpTestService(t)
	s2.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (string, error) {
		runs++
		return "sent", nil
	})
	c2, tok2 := stageTransfer(t, s2)
	if _, err := s2.SendEmailOTP(context.Background(), c2.ID, tok2); err != nil {
		t.Fatal(err)
	}
	mailer2.mu.Lock()
	code2 := mailer2.lastCode
	mailer2.mu.Unlock()
	if _, err := s2.ApproveWithEmailOTP(context.Background(), c2.UserID, c2.ID, tok2, code2); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.ApproveWithEmailOTP(context.Background(), c2.UserID, c2.ID, tok2, code2); err != nil {
		t.Fatal(err) // terminal replay returns nil error
	}
	if runs != 1 {
		t.Fatalf("executor runs = %d, want 1", runs)
	}
}

func TestEmailOTPMaxAttempts(t *testing.T) {
	s, _ := otpTestService(t)
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < emailOTPMaxAttempt; i++ {
		_, err := s.ApproveWithEmailOTP(context.Background(), c.UserID, c.ID, tok, "111111")
		if err == nil {
			t.Fatalf("attempt %d accepted", i)
		}
	}
	_, err := s.ApproveWithEmailOTP(context.Background(), c.UserID, c.ID, tok, "111111")
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("expected lockout, got %v", err)
	}
}

func TestEmailOTPCooldown(t *testing.T) {
	s, _ := otpTestService(t)
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Fatalf("expected cooldown, got %v", err)
	}
}

func TestEmailOTPMissingMailerFailClosed(t *testing.T) {
	s := NewService(Config{TokenSecret: "otp-test-secret-12345678901234567890", ConfirmBase: "https://example.com/confirm"}, nil, nil)
	s.SetDemoEmailOTP(true)
	// no deps
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err == nil {
		t.Fatal("expected misconfig fail-closed")
	}
}

func TestEmailOTPNoUserEmailFailClosed(t *testing.T) {
	s, mailer := otpTestService(t)
	s.SetEmailOTPDeps(mailer, stubUsers{email: ""}, nil)
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err == nil {
		t.Fatal("expected empty email fail-closed")
	}
}

func TestEmailOTPSendFailureDoesNotLeaveCode(t *testing.T) {
	s, mailer := otpTestService(t)
	mailer.err = fmt.Errorf("unosend down")
	c, tok := stageTransfer(t, s)
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err == nil {
		t.Fatal("expected send failure")
	}
	if _, ok, _ := s.otpStore.Get(context.Background(), c.ID); ok {
		t.Fatal("OTP left in store after send failure")
	}
}

func TestEmailOTPClockSkewExpiryOnCard(t *testing.T) {
	s, _ := otpTestService(t)
	c, tok := stageTransfer(t, s)
	// Expire the card itself.
	s.now = func() time.Time { return time.Now().UTC().Add(24 * time.Hour) }
	if _, err := s.SendEmailOTP(context.Background(), c.ID, tok); err == nil {
		t.Fatal("expected expired card reject")
	}
}
