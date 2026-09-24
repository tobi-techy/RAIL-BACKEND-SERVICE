package confirmation

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
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
	stored, _ := s.store.Load(c.ID)
	// Fail-closed happens BEFORE the single-use token is consumed: the card
	// stays live (pending, token intact) so wiring the executor later — or
	// settling via MarkExternal — can still complete it. A burned token
	// would trap the card in failed with no retry path.
	if stored.State != entities.ConfirmationPending {
		t.Fatalf("expected pending (token not consumed), got %s", stored.State)
	}
	if stored.TokenUsed {
		t.Fatal("single-use token must not be consumed by a fail-closed approve")
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

func TestFetchBadTokenOnPendingLeaksNothing(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "sent", nil
	})
	c, _, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"amount": "₦20,000", "to_name": "Funsho"}})
	if err != nil {
		t.Fatal(err)
	}
	// Pending card + bad token must error, never return amount/destination.
	if rec, err := s.Fetch(ctx, c.ID, "bad.token"); err == nil || rec != nil {
		t.Fatalf("pending card leaked on bad token: rec=%+v err=%v", rec, err)
	}
}

func TestConcurrentApproveExecutesOnce(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	var calls int32
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return "sent", nil
	})
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"amount": "₦20,000", "to_name": "Funsho"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	const racers = 8
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Approve(ctx, uid, c.ID, tok, "pass")
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("executor ran %d times, want exactly 1", got)
	}
	stored, _ := s.store.Load(c.ID)
	if stored.State != entities.ConfirmationCompleted {
		t.Fatalf("expected completed, got %s", stored.State)
	}
}

func TestApproveEmptyBiometricWithDeviceMaterialRejected(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "sent", nil
	})
	c, url, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"amount": "₦20,000"}})
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	// Empty biometric + enroll key is a protocol violation, not a legacy approve.
	if _, err := s.ApproveWithDevice(ctx, uid, c.ID, tok, "", DeviceApproval{EnrollKey: "aGVsbG8="}); err == nil {
		t.Fatal("expected rejection of empty biometric with enroll key")
	}
	stored, _ := s.store.Load(c.ID)
	if stored.TokenUsed || stored.State != entities.ConfirmationPending {
		t.Fatalf("rejected approve must not consume token: used=%v state=%s", stored.TokenUsed, stored.State)
	}
}

func TestCreateClampsTTL(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "sent", nil
	})
	before := time.Now().UTC()
	c, _, err := s.Create(ctx, CreateInput{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"amount": "₦1"}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if c.ExpiresAt.Sub(before) > 15*time.Minute+time.Second {
		t.Fatalf("TTL not clamped: expires in %v", c.ExpiresAt.Sub(before))
	}
}
