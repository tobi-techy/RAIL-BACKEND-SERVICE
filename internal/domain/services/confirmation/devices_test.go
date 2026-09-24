package confirmation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func testDeviceService() *Service {
	s := NewService(Config{TokenSecret: "device-test-secret-1234567890", ConfirmBase: "https://example.com/confirm"}, nil, nil)
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "sent", nil
	})
	return s
}

func p256SPKI(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.StdEncoding.EncodeToString(der)
}

func signFor(t *testing.T, priv *ecdsa.PrivateKey, actionID string, exp int64) string {
	t.Helper()
	digest := sha256.Sum256(SignedMessage(actionID, exp))
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func tokenExp(url string) int64 {
	tok := url[strings.Index(url, "?t=")+3:]
	exp, _, err := splitToken(tok)
	if err != nil {
		panic(err)
	}
	return exp
}

func TestParseDevicePublicKey(t *testing.T) {
	_, good := p256SPKI(t)
	spki, err := ParseDevicePublicKey(good)
	if err != nil {
		t.Fatalf("valid P-256 SPKI refused: %v", err)
	}
	if len(spki) == 0 {
		t.Fatal("empty spki")
	}
	if _, err := ParseDevicePublicKey("!!!not-base64!!!"); err == nil {
		t.Fatal("bad base64 accepted")
	}
	if _, err := ParseDevicePublicKey(base64.StdEncoding.EncodeToString([]byte("nope"))); err == nil {
		t.Fatal("non-PKIX accepted")
	}
}

func TestApproveTokenOnlyWhenUnenrolled(t *testing.T) {
	s := testDeviceService()
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@tobi", "amount": "5"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	out, err := s.ApproveWithDevice(ctx, uid, c.ID, tok, "pass", DeviceApproval{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != entities.ConfirmationCompleted {
		t.Fatalf("state = %s", out.State)
	}
	if out.Assurance != AssuranceTokenOnly {
		t.Fatalf("assurance = %q", out.Assurance)
	}
}

func TestApproveEnrollsTrustOnFirstUse(t *testing.T) {
	s := testDeviceService()
	ctx := context.Background()
	uid := uuid.New()
	_, enrollB64 := p256SPKI(t)
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@tobi", "amount": "5"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	out, err := s.ApproveWithDevice(ctx, uid, c.ID, tok, "pass", DeviceApproval{EnrollKey: enrollB64})
	if err != nil {
		t.Fatal(err)
	}
	if out.Assurance != AssuranceEnrolled {
		t.Fatalf("assurance = %q", out.Assurance)
	}
	keys, err := s.devices.Keys(uid)
	if err != nil || len(keys) != 1 {
		t.Fatalf("expected 1 enrolled key, got %d (%v)", len(keys), err)
	}
}

func TestApproveSecureEnclaveSignature(t *testing.T) {
	s := testDeviceService()
	ctx := context.Background()
	uid := uuid.New()
	priv, enrollB64 := p256SPKI(t)

	// First approval enrolls.
	c1, url1, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveWithDevice(ctx, uid, c1.ID, url1[strings.Index(url1, "?t=")+3:], "pass", DeviceApproval{EnrollKey: enrollB64}); err != nil {
		t.Fatal(err)
	}
	keys, err := s.devices.Keys(uid)
	if err != nil || len(keys) != 1 {
		t.Fatalf("enroll failed: %d keys (%v)", len(keys), err)
	}
	keyID := keys[0].ID.String()

	// Second approval must carry a valid Enclave signature.
	c2, url2, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@b", "amount": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	tok2 := url2[strings.Index(url2, "?t=")+3:]
	exp2 := tokenExp(url2)
	sig := signFor(t, priv, c2.ID.String(), exp2)
	out, err := s.ApproveWithDevice(ctx, uid, c2.ID, tok2, "pass", DeviceApproval{KeyID: keyID, Signature: sig})
	if err != nil {
		t.Fatal(err)
	}
	if out.Assurance != AssuranceSecureEnclave {
		t.Fatalf("assurance = %q", out.Assurance)
	}
}

func TestApproveBadSignatureDoesNotBurnToken(t *testing.T) {
	s := testDeviceService()
	ctx := context.Background()
	uid := uuid.New()
	priv, enrollB64 := p256SPKI(t)
	other, _ := p256SPKI(t)

	c1, url1, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveWithDevice(ctx, uid, c1.ID, url1[strings.Index(url1, "?t=")+3:], "pass", DeviceApproval{EnrollKey: enrollB64}); err != nil {
		t.Fatal(err)
	}
	keys, err := s.devices.Keys(uid)
	if err != nil || len(keys) == 0 {
		t.Fatalf("enroll failed (%v)", err)
	}

	c2, url2, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@b", "amount": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	tok2 := url2[strings.Index(url2, "?t=")+3:]
	wrongSig := signFor(t, other, c2.ID.String(), tokenExp(url2))

	// Missing signature → rejected.
	if _, err := s.ApproveWithDevice(ctx, uid, c2.ID, tok2, "pass", DeviceApproval{}); err == nil {
		t.Fatal("missing signature accepted after enrollment")
	}
	// Wrong-key signature → rejected.
	if _, err := s.ApproveWithDevice(ctx, uid, c2.ID, tok2, "pass", DeviceApproval{KeyID: keys[0].ID.String(), Signature: wrongSig}); err == nil {
		t.Fatal("wrong-key signature accepted")
	}
	// Unknown key id → rejected.
	if _, err := s.ApproveWithDevice(ctx, uid, c2.ID, tok2, "pass", DeviceApproval{KeyID: uuid.New().String(), Signature: wrongSig}); err == nil {
		t.Fatal("unknown key id accepted")
	}
	// Token must NOT be consumed: the real signature still works.
	goodSig := signFor(t, priv, c2.ID.String(), tokenExp(url2))
	out, err := s.ApproveWithDevice(ctx, uid, c2.ID, tok2, "pass", DeviceApproval{KeyID: keys[0].ID.String(), Signature: goodSig})
	if err != nil {
		t.Fatalf("valid retry after failures: %v", err)
	}
	if out.State != entities.ConfirmationCompleted {
		t.Fatalf("state = %s", out.State)
	}
}

func TestStrictModeRejectsTokenOnly(t *testing.T) {
	s := testDeviceService()
	s.SetStrictDeviceSignature(true)
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	if _, err := s.ApproveWithDevice(ctx, uid, c.ID, tok, "pass", DeviceApproval{}); err == nil {
		t.Fatal("strict mode accepted token-only approve")
	}
	// Enrollment itself is also refused under strict (nothing to verify against).
	_, enrollB64 := p256SPKI(t)
	if _, err := s.ApproveWithDevice(ctx, uid, c.ID, tok, "pass", DeviceApproval{EnrollKey: enrollB64}); err == nil {
		t.Fatal("strict mode accepted TOFU enrollment")
	}
}
