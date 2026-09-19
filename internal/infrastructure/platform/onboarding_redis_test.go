package platform

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
)

// These tests run the onboarding session over a REAL Redis client (miniredis)
// instead of the in-memory fake.
//
// Why that matters: the fake stores Go values as-is, while the production store
// (cache/redisClient) round-trips every session through json.Marshal and
// json.Unmarshal. A wrong `json:"..."` tag on a session field is therefore
// invisible to every other test in this package and fatal in production — the
// field silently reverts to its zero value on the next turn, which for
// EmailBackfill/EmailAttach means re-running the wrong branch of identity
// handling. Everything session-shaped is asserted through the real
// serialisation path here.
//
// Redis only: the database stays faked, because a Postgres server is not
// available in this environment. The SQL itself still needs staging/Docker.

func newRedisStore(t *testing.T) (*miniredis.Miniredis, cache.RedisClient) {
	t.Helper()
	mr := miniredis.RunT(t)
	port, err := strconv.Atoi(mr.Port())
	if err != nil {
		t.Fatalf("parse miniredis port %q: %v", mr.Port(), err)
	}
	client, err := cache.NewRedisClient(
		&config.RedisConfig{Host: mr.Host(), Port: port},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("build redis client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

func newRedisOnboarder(t *testing.T, store cache.RedisClient) (
	*ChatOnboarder, *fakeVerifier, *fakeUsers, *fakeProvisioner, *fakeLinker,
) {
	t.Helper()
	ver := &fakeVerifier{validCode: "123456"}
	users := newFakeUsers()
	prov := &fakeProvisioner{}
	linker := &fakeLinker{}
	ob := NewChatOnboarder(store, ver, users, prov, linker, "https://app.example/join", nil)
	return ob, ver, users, prov, linker
}

// TestRedisSession_EveryFieldSurvivesSerialisation is the guard the fakes could
// never provide: it pins the JSON wire format of the whole session struct.
func TestRedisSession_EveryFieldSurvivesSerialisation(t *testing.T) {
	mr, store := newRedisStore(t)
	ob, _, _, _, _ := newRedisOnboarder(t, store)
	ctx := context.Background()
	key := onboardingKey(entities.PlatformIMessage, "+15559201")

	saved := guestState{
		Phase:              phaseEmailAttach,
		FirstName:          "Ada",
		Country:            "NG",
		Email:              "ada@example.com",
		Phone:              "+2348012345678",
		UserID:             uuid.NewString(),
		EmailVerified:      true,
		EmailAttach:        true,
		EmailBackfill:      true,
		AccountCreated:     true,
		EmailOTPAttempts:   2,
		OTPAttempts:        1,
		TurnCount:          3,
		LastReplyHash:      99,
		IntroSent:          true,
		SignupReason:       "first deposit",
		StatementSummary:   "9 transactions",
		PendingStatementID: "doc-1",
	}
	if err := ob.save(ctx, key, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	// The stored value must actually be JSON carrying the field names, so a
	// dropped or renamed tag fails loudly rather than silently.
	raw, err := mr.Get(key)
	if err != nil {
		t.Fatalf("read raw session: %v", err)
	}
	for _, field := range []string{
		"email_backfill", "email_attach", "account_created", "user_id",
		"email_verified", "first_name", "pending_statement_id",
	} {
		if !strings.Contains(raw, `"`+field+`"`) {
			t.Errorf("session JSON is missing %q — the field would vanish in production: %s", field, raw)
		}
	}

	var loaded guestState
	if err := store.Get(ctx, key, &loaded); err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.EmailBackfill != saved.EmailBackfill ||
		loaded.EmailAttach != saved.EmailAttach ||
		loaded.AccountCreated != saved.AccountCreated {
		t.Fatalf("identity flags did not survive serialisation: %+v", loaded)
	}
	if !reflect.DeepEqual(loaded, saved) {
		t.Fatalf("session did not round-trip.\n got %+v\nwant %+v", loaded, saved)
	}
}

// TestRedisSession_BackfillSurvivesAProcessRestart models the real deployment:
// the next turn may land on a different replica, so the flow must not depend on
// any in-process state.
func TestRedisSession_BackfillSurvivesAProcessRestart(t *testing.T) {
	_, store := newRedisStore(t)
	ctx := context.Background()
	sender := "+15559202"
	first, _, _, _, _ := newRedisOnboarder(t, store)

	if _, err := first.StartEmailBackfill(
		ctx, entities.PlatformIMessage, sender, uuid.New(),
	); err != nil {
		t.Fatalf("StartEmailBackfill: %v", err)
	}

	// A brand-new onboarder: nothing carried over in memory.
	second, _, _, _, _ := newRedisOnboarder(t, store)
	if !second.IsEmailBackfillSession(ctx, entities.PlatformIMessage, sender) {
		t.Fatal("the backfill session must be visible to another process")
	}
	if !second.HasAskedEmailBackfill(ctx, entities.PlatformIMessage, sender) {
		t.Fatal("the asked-once marker must be visible to another process")
	}
	if second.IsEmailBackfillSession(ctx, entities.PlatformIMessage, "+15559999") {
		t.Fatal("a different sender must not be seen as mid-backfill")
	}
}

// TestRedisSession_EmailFirstFlowAcrossRestarts is the closest thing to an
// end-to-end run available without a database: every turn is handled by a brand
// new onboarder, so the entire flow depends only on what survives in Redis.
func TestRedisSession_EmailFirstFlowAcrossRestarts(t *testing.T) {
	_, store := newRedisStore(t)
	ctx := context.Background()
	sender := "+15559205"
	address := "restart@example.com"

	// The fakes stand in for the database and are shared across "processes"; the
	// onboarder itself is rebuilt every turn, so nothing can ride along in memory.
	ver := &fakeVerifier{validCode: "123456"}
	users := newFakeUsers()
	prov := &fakeProvisioner{}
	linker := &fakeLinker{}
	fresh := func() *ChatOnboarder {
		return NewChatOnboarder(store, ver, users, prov, linker, "https://app.example/join", nil)
	}

	for _, turn := range []string{"hi", "Ada", address, "123456", "yes"} {
		if _, err := fresh().Handle(ctx, OnboardInput{
			Platform: entities.PlatformIMessage,
			SenderID: sender,
			Text:     turn,
		}); err != nil {
			t.Fatalf("Handle(%q) on a fresh process: %v", turn, err)
		}
	}

	if len(users.created) != 1 || users.created[0].Email != address {
		t.Fatalf("expected exactly one account anchored on %s, got %+v", address, users.created)
	}
	if len(ver.sentTo) != 1 || ver.sentTo[0] != address {
		t.Fatalf("expected one code to the address, got %v", ver.sentTo)
	}
	if linker.calls != 1 {
		t.Fatalf("expected the handle to be linked once, got %d", linker.calls)
	}
	if fresh().HasSession(ctx, entities.PlatformIMessage, sender) {
		t.Fatal("a completed onboarding must clear its session")
	}
}

// TestRedisSession_MarkersArePerSender pins that the asked-once marker cannot
// leak between senders, which would silently skip the ask for someone else.
func TestRedisSession_MarkersArePerSender(t *testing.T) {
	_, store := newRedisStore(t)
	ctx := context.Background()
	ob, _, _, _, _ := newRedisOnboarder(t, store)

	ob.MarkEmailBackfillAsked(ctx, entities.PlatformIMessage, "+15559203")
	if !ob.HasAskedEmailBackfill(ctx, entities.PlatformIMessage, "+15559203") {
		t.Fatal("marker should be set for the sender it was recorded for")
	}
	if ob.HasAskedEmailBackfill(ctx, entities.PlatformIMessage, "+15559204") {
		t.Fatal("marker must not leak to another sender")
	}
	if ob.HasAskedEmailBackfill(ctx, entities.PlatformWhatsApp, "+15559203") {
		t.Fatal("marker must not leak across platforms")
	}
}
