package confirmation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func testService() *Service {
	s := NewService(Config{TokenSecret: "test-secret-1234567890", ConfirmBase: "https://example.com/confirm"}, nil, nil)
	s.now = time.Now
	return s
}

func TestStateMachine(t *testing.T) {
	legal := [][2]entities.ConfirmationState{
		{entities.ConfirmationPending, entities.ConfirmationAuthenticating},
		{entities.ConfirmationPending, entities.ConfirmationRejected},
		{entities.ConfirmationPending, entities.ConfirmationExpired},
		{entities.ConfirmationAuthenticating, entities.ConfirmationApproved},
		{entities.ConfirmationAuthenticating, entities.ConfirmationRejected},
		{entities.ConfirmationApproved, entities.ConfirmationCompleted},
		{entities.ConfirmationApproved, entities.ConfirmationFailed},
	}
	for _, e := range legal {
		if !entities.ValidConfirmationTransition(e[0], e[1]) {
			t.Fatalf("expected legal %s -> %s", e[0], e[1])
		}
	}
	illegal := [][2]entities.ConfirmationState{
		{entities.ConfirmationPending, entities.ConfirmationApproved},
		{entities.ConfirmationPending, entities.ConfirmationCompleted},
		{entities.ConfirmationCompleted, entities.ConfirmationPending},
		{entities.ConfirmationRejected, entities.ConfirmationPending},
		{entities.ConfirmationFailed, entities.ConfirmationCompleted},
		{entities.ConfirmationExpired, entities.ConfirmationPending},
	}
	for _, e := range illegal {
		if entities.ValidConfirmationTransition(e[0], e[1]) {
			t.Fatalf("expected illegal %s -> %s", e[0], e[1])
		}
	}
}

func TestCreateApproveIdempotent(t *testing.T) {
	s := testService()
	calls := 0
	s.RegisterExecutor(entities.ConfirmationActionInvestBuy, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		calls++
		return "Filled at $180.20", nil
	})
	edits := 0
	s.SetCardEditor(func(ctx context.Context, c *entities.Confirmation) error { edits++; return nil })

	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionInvestBuy, Payload: map[string]any{"symbol": "GOOGL", "quantity": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Buy GOOGL" {
		t.Fatalf("renderer title = %q", c.Title)
	}
	if !strings.Contains(url, c.ID.String()) {
		t.Fatalf("url missing action id: %s", url)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	out, err := s.Approve(ctx, uid, c.ID, tok, "pass")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != entities.ConfirmationCompleted {
		t.Fatalf("state = %s", out.State)
	}
	// Replay: no-op, executor not called again.
	out2, err := s.Approve(ctx, uid, c.ID, tok, "pass")
	if err != nil {
		t.Fatal(err)
	}
	if out2.State != entities.ConfirmationCompleted || calls != 1 {
		t.Fatalf("replay moved state or re-executed: state=%s calls=%d", out2.State, calls)
	}
	if edits == 0 {
		t.Fatal("expected card edits")
	}
}

func TestTokenSingleUseAndReject(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "sent", nil
	})
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"amount": "₦20,000", "to_name": "Funsho"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Send ₦20,000" {
		t.Fatalf("title = %q", c.Title)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	if _, err := s.Reject(ctx, uid, c.ID, tok, "cancel"); err != nil {
		t.Fatal(err)
	}
	// Approve after reject: no-op terminal.
	out, err := s.Approve(ctx, uid, c.ID, tok, "pass")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != entities.ConfirmationRejected {
		t.Fatalf("expected rejected, got %s", out.State)
	}
	// Bad token rejected.
	if _, err := s.Fetch(ctx, c.ID, "bad.token"); err == nil {
		got, ferr := s.Fetch(ctx, c.ID, "bad.token")
		if ferr != nil || got == nil {
			t.Fatal("expected dead-state record on bad token")
		}
	}
}

func TestFailClosedNoExecutor(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionLimitChange, Payload: map[string]any{"scope": "weekly", "to": "₦100,000"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Title, "Raise") {
		t.Fatalf("title = %q", c.Title)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	if _, err := s.Approve(ctx, uid, c.ID, tok, "pass"); err == nil {
		t.Fatal("expected fail-closed with no executor")
	}
	got, ferr := s.Fetch(ctx, c.ID, tok)
	if ferr != nil {
		t.Fatalf("fetch after fail-closed: %v", ferr)
	}
	_ = got
	stored, err := s.store.Load(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != entities.ConfirmationFailed {
		t.Fatalf("expected failed, got %s", stored.State)
	}
}

func TestPreviewLayout(t *testing.T) {
	c := &entities.Confirmation{Title: "Buy GOOGL", Subtitle: "Fractional · market"}
	for _, st := range []entities.ConfirmationState{
		entities.ConfirmationPending, entities.ConfirmationApproved,
		entities.ConfirmationCompleted, entities.ConfirmationRejected,
		entities.ConfirmationFailed, entities.ConfirmationExpired,
	} {
		l := entities.PreviewLayoutFor(c, st, "4:32")
		if l.Caption == "" || l.Image == "" || l.Summary == "" {
			t.Fatalf("state %s produced empty layout: %+v", st, l)
		}
	}
}

func TestResumeAfterCrashBetweenClaimAndExecute(t *testing.T) {
	s := testService()
	calls := 0
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		calls++
		return "sent", nil
	})
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]

	// Simulate a crash after the atomic claim but before execution: the
	// token is spent, the card is still pending, nothing ran.
	if _, err := s.store.Claim(ctx, c.ID, AssuranceTokenOnly); err != nil {
		t.Fatal(err)
	}
	// A retry of the same approve resumes the pipeline instead of
	// no-op-ing (which would strand the card) or re-executing blindly.
	out, err := s.Approve(ctx, uid, c.ID, tok, "pass")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if out.State != entities.ConfirmationCompleted {
		t.Fatalf("resumed state = %s", out.State)
	}
	if calls != 1 {
		t.Fatalf("executor calls = %d, want exactly 1", calls)
	}
}

func TestDoubleClaimSecondLoses(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	c, _, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@a", "amount": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Claim(ctx, c.ID, ""); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := s.store.Claim(ctx, c.ID, ""); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("second claim must lose, got %v", err)
	}
}

type errStore struct{ err error }

func (e errStore) Save(ctx context.Context, c *entities.Confirmation) error {
	return e.err
}

func (e errStore) Load(ctx context.Context, id uuid.UUID) (*entities.Confirmation, error) {
	return nil, e.err
}

func (e errStore) Claim(ctx context.Context, id uuid.UUID, assurance string) (*entities.Confirmation, error) {
	return nil, e.err
}

func TestStoreOutageFailsClosed(t *testing.T) {
	s := NewService(Config{TokenSecret: "outage-test-secret-1234567890", ConfirmBase: "https://example.com/confirm"}, errStore{err: errors.New("db down")}, nil)
	ctx := context.Background()
	uid := uuid.New()
	if _, _, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend}); err == nil {
		t.Fatal("create during outage must fail")
	}
	if _, err := s.Fetch(ctx, uuid.New(), "1.sig"); err == nil {
		t.Fatal("fetch during outage must fail, not 404-masquerade")
	}
	if _, err := s.Approve(ctx, uid, uuid.New(), "1.sig", "pass"); err == nil {
		t.Fatal("approve during outage must fail")
	}
}

func TestFetchBadTokenHidesPendingCard(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	c, _, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend})
	if err != nil {
		t.Fatal(err)
	}
	// Guessing the UUID with a forged token must not leak the live card.
	if _, err := s.Fetch(ctx, c.ID, "9999999999.deadbeef"); err == nil {
		t.Fatal("fetch with bad token on a pending card must fail")
	}
	// Terminal cards render dead, not broken.
	c.State = entities.ConfirmationCompleted
	if err := s.store.Save(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.Fetch(ctx, c.ID, "9999999999.deadbeef")
	if err != nil || !got.IsTerminal() {
		t.Fatalf("terminal fetch with bad token must return the dead record, got %v %v", got, err)
	}
}

func TestCreateTTLClamped(t *testing.T) {
	s := testService()
	ctx := context.Background()
	c, _, err := s.Create(ctx, CreateInput{
		UserID: uuid.New(), Action: entities.ConfirmationActionTransferSend, TTL: time.Hour * 24 * 365,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.ExpiresAt.Sub(c.CreatedAt) > maxCreateTTL+time.Second {
		t.Fatalf("TTL not clamped: %v", c.ExpiresAt.Sub(c.CreatedAt))
	}
}

func TestMarkExternalDoesNotResurrectCompleted(t *testing.T) {
	s := testService()
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "sent", nil
	})
	ctx := context.Background()
	uid := uuid.New()
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	if _, err := s.Approve(ctx, uid, c.ID, tok, "pass"); err != nil {
		t.Fatal(err)
	}
	// A late external mark must not flip completed back to pending.
	out, err := s.MarkExternal(ctx, c.ID, entities.ConfirmationRejected, "late cancel")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != entities.ConfirmationCompleted {
		t.Fatalf("completed card resurrected to %s", out.State)
	}
}
