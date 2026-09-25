package confirmation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type fakeTxAssert struct {
	hasCreds  bool
	hasErr    error
	beginErr  error
	finishErr error
	began     int
	finished  int
}

func (f *fakeTxAssert) BeginTransactionAssertion(ctx context.Context, userID uuid.UUID, email string) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	f.began++
	if f.beginErr != nil {
		return nil, nil, f.beginErr
	}
	return &protocol.CredentialAssertion{
		Response: protocol.PublicKeyCredentialRequestOptions{
			Challenge:        protocol.URLEncodedBase64("test-challenge"),
			RelyingPartyID:   "example.com",
			UserVerification: protocol.VerificationRequired,
		},
	}, &webauthn.SessionData{Challenge: "test-challenge"}, nil
}

func (f *fakeTxAssert) FinishTransactionAssertion(ctx context.Context, userID uuid.UUID, email string, session *webauthn.SessionData, response *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error) {
	f.finished++
	if f.finishErr != nil {
		return nil, f.finishErr
	}
	return &webauthn.Credential{}, nil
}

func (f *fakeTxAssert) HasCredentials(ctx context.Context, userID uuid.UUID) (bool, error) {
	return f.hasCreds, f.hasErr
}

func passkeyService(tx *fakeTxAssert) *Service {
	s := NewService(Config{TokenSecret: "passkey-test-secret-1234567890", ConfirmBase: "https://example.com/confirm"}, nil, nil)
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "sent", nil
	})
	s.SetTxAssertion(tx)
	s.SetUserEmailLookup(func(ctx context.Context, userID uuid.UUID) (string, error) {
		return "user@example.com", nil
	})
	return s
}

func TestAssertionOptionsAndApprove(t *testing.T) {
	tx := &fakeTxAssert{hasCreds: true}
	s := passkeyService(tx)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[mustIndex(t, url):]

	opts, err := s.AssertionOptions(ctx, uid, c.ID, tok)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Response.RelyingPartyID != "example.com" {
		t.Fatalf("rp = %q", opts.Response.RelyingPartyID)
	}
	if opts.Response.UserVerification != protocol.VerificationRequired {
		t.Fatal("user verification must be required")
	}

	out, err := s.ApproveWithAssertion(ctx, uid, c.ID, tok, []byte(stubAssertion(t)))
	if err != nil {
		t.Fatal(err)
	}
	if out.State != entities.ConfirmationCompleted {
		t.Fatalf("state = %s", out.State)
	}
	if out.Assurance != AssurancePasskey {
		t.Fatalf("assurance = %q", out.Assurance)
	}
	if tx.began != 1 || tx.finished != 1 {
		t.Fatalf("ceremony calls began=%d finished=%d", tx.began, tx.finished)
	}
	// Session consumed: second approve is a terminal no-op, not a re-execution.
	out2, err := s.ApproveWithAssertion(ctx, uid, c.ID, tok, []byte(stubAssertion(t)))
	if err != nil {
		t.Fatal(err)
	}
	if out2.State != entities.ConfirmationCompleted || tx.finished != 1 {
		t.Fatal("replay must be a no-op")
	}
}

func TestAssertionOptionsTerminal(t *testing.T) {
	tx := &fakeTxAssert{hasCreds: true}
	s := passkeyService(tx)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[mustIndex(t, url):]
	if _, err := s.Reject(ctx, uid, c.ID, tok, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssertionOptions(ctx, uid, c.ID, tok); !errors.Is(err, ErrCardTerminal) {
		t.Fatalf("expected ErrCardTerminal, got %v", err)
	}
}

func TestAssertionOptionsNoPasskey(t *testing.T) {
	tx := &fakeTxAssert{beginErr: errors.New("no passkey enrolled")}
	s := passkeyService(tx)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AssertionOptions(ctx, uid, c.ID, url[mustIndex(t, url):]); !errors.Is(err, ErrNoPasskey) {
		t.Fatalf("expected ErrNoPasskey, got %v", err)
	}
}

func TestApproveAssertionFailureKeepsToken(t *testing.T) {
	tx := &fakeTxAssert{hasCreds: true, finishErr: errors.New("bad signature")}
	s := passkeyService(tx)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[mustIndex(t, url):]
	if _, err := s.AssertionOptions(ctx, uid, c.ID, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveWithAssertion(ctx, uid, c.ID, tok, []byte(stubAssertion(t))); err == nil {
		t.Fatal("bad assertion accepted")
	}
	// Token not burned: fix the backend, retry succeeds.
	tx.finishErr = nil
	out, err := s.ApproveWithAssertion(ctx, uid, c.ID, tok, []byte(stubAssertion(t)))
	if err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if out.State != entities.ConfirmationCompleted {
		t.Fatalf("state = %s", out.State)
	}
}

func TestApproveMissingSession(t *testing.T) {
	tx := &fakeTxAssert{hasCreds: true}
	s := passkeyService(tx)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	// No options fetched: ceremony missing, token intact.
	if _, err := s.ApproveWithAssertion(ctx, uid, c.ID, url[mustIndex(t, url):], []byte(stubAssertion(t))); err == nil {
		t.Fatal("assertion without ceremony accepted")
	}
}

func TestLegacyApproveRequiresPasskeyWhenEnrolled(t *testing.T) {
	tx := &fakeTxAssert{hasCreds: true}
	s := passkeyService(tx)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, uid, c.ID, url[mustIndex(t, url):], "pass"); err == nil {
		t.Fatal("token-only approve accepted despite enrolled passkey")
	}
}

func TestLegacyApproveTokenOnlyPreEnrollment(t *testing.T) {
	tx := &fakeTxAssert{hasCreds: false}
	s := passkeyService(tx)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Approve(ctx, uid, c.ID, url[mustIndex(t, url):], "pass")
	if err != nil {
		t.Fatal(err)
	}
	if out.Assurance != AssuranceTokenOnly {
		t.Fatalf("assurance = %q", out.Assurance)
	}
}

func TestLegacyApproveStrict(t *testing.T) {
	tx := &fakeTxAssert{hasCreds: false}
	s := passkeyService(tx)
	s.SetRequirePasskey(true)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, uid, c.ID, url[mustIndex(t, url):], "pass"); err == nil {
		t.Fatal("strict mode accepted token-only approve")
	}
}

func TestPasskeyRegistered(t *testing.T) {
	ctx := context.Background()
	uid := uuid.New()
	if passkeyService(&fakeTxAssert{hasCreds: true}).PasskeyRegistered(ctx, uid) != true {
		t.Fatal("expected registered")
	}
	if passkeyService(&fakeTxAssert{hasCreds: false}).PasskeyRegistered(ctx, uid) != false {
		t.Fatal("expected unregistered")
	}
	s := NewService(Config{TokenSecret: "x-12345678901234567890", ConfirmBase: "https://e.com/c"}, nil, nil)
	if s.PasskeyRegistered(ctx, uid) != false {
		t.Fatal("nil backend must report unregistered")
	}
}

func mustIndex(t *testing.T, url string) int {
	t.Helper()
	idx := strings.Index(url, "?t=")
	if idx < 0 {
		t.Fatalf("no token in url %s", url)
	}
	return idx + 3
}

// stubAssertion returns a structurally valid assertion body: real rpIdHash,
// flags with user verification, a counter, and well-formed client data. The
// fake ceremony backend ignores the bytes; the real parser must accept them.
func stubAssertion(t *testing.T) []byte {
	t.Helper()
	rpHash := sha256.Sum256([]byte("example.com"))
	authData := append(append(rpHash[:], 0x05), 0, 0, 0, 1)
	clientData := `{"type":"webauthn.get","challenge":"dGVzdA","origin":"https://example.com"}`
	enc := base64.RawURLEncoding.EncodeToString
	body := fmt.Sprintf(
		`{"id":"%s","rawId":"%s","type":"public-key","response":{"clientDataJSON":"%s","authenticatorData":"%s","signature":"%s"}}`,
		enc([]byte("test-cred-id")), enc([]byte("test-cred-id")),
		enc([]byte(clientData)), enc(authData), enc([]byte("sig")),
	)
	return []byte(body)
}
