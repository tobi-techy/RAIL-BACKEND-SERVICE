package di

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

// fakeConfirmRedis is a minimal in-memory cache.RedisClient for the staged
// confirm_id. It honors TTL, so a test can prove an expiry reads as a miss.
type fakeConfirmRedis struct {
	mu   sync.Mutex
	data map[string]confirmEntry
}

type confirmEntry struct {
	value string
	until time.Time
}

func newFakeConfirmRedis() *fakeConfirmRedis {
	return &fakeConfirmRedis{data: map[string]confirmEntry{}}
}

func (f *fakeConfirmRedis) Set(_ context.Context, key string, value interface{}, expiration time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("fakeConfirmRedis.Set: unsupported value type %T", value)
	}
	f.data[key] = confirmEntry{value: text, until: time.Now().Add(expiration)}
	return nil
}

func (f *fakeConfirmRedis) Get(_ context.Context, key string, dest interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.data[key]
	if !ok || time.Now().After(e.until) {
		return errConfirmMiss{}
	}
	if d, isString := dest.(*string); isString {
		*d = e.value
	}
	return nil
}

func (f *fakeConfirmRedis) Del(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	return nil
}

func (f *fakeConfirmRedis) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.data[key]
	return ok && time.Now().Before(e.until), nil
}

func (f *fakeConfirmRedis) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	ok, err := f.Exists(ctx, key)
	if err != nil {
		return false, err
	}
	if ok {
		return false, nil
	}
	return true, f.Set(ctx, key, value, expiration)
}

func (f *fakeConfirmRedis) Incr(context.Context, string) (int64, error) { return 1, nil }
func (f *fakeConfirmRedis) IncrBy(_ context.Context, _ string, v int64) (int64, error) {
	return v, nil
}
func (f *fakeConfirmRedis) Expire(context.Context, string, time.Duration) error { return nil }
func (f *fakeConfirmRedis) Keys(context.Context, string) ([]string, error)      { return nil, nil }
func (f *fakeConfirmRedis) Ping(context.Context) error                          { return nil }
func (f *fakeConfirmRedis) Close() error                                        { return nil }
func (f *fakeConfirmRedis) Client() *redis.Client                               { return nil }

type errConfirmMiss struct{}

func (errConfirmMiss) Error() string { return "redis: nil" }

// TestPythonRole_GrantsMoneyToolsToEmailVerifiedChatAccounts pins the parity
// rule between the two onboarding paths.
//
// pythonRole used to key on KYC approval alone. A chat-onboarded account is
// email-verified but sits at kyc_status non_kyc, so it was handed the read-only
// "user" role and could never move money through Miriam — which meant a chat
// signup could never be the equal of an app signup. A proven address on an
// active account is now the identity floor; KYC approval remains an additional
// grant so existing approved users are unaffected.
func TestPythonRole_GrantsMoneyToolsToEmailVerifiedChatAccounts(t *testing.T) {
	nonKYC := string(entities.KYCStatusNonKYC)
	approved := string(entities.KYCStatusApproved)

	cases := []struct {
		name          string
		kycStatus     string
		emailVerified bool
		isActive      bool
		want          string
	}{
		{"chat signup: email proven at tier 1", nonKYC, true, true, "verified"},
		{"app signup: kyc approved", approved, true, true, "verified"},
		{"kyc approved with an unproven address keeps access", approved, false, true, "verified"},
		{"no proof of identity is read-only", nonKYC, false, true, "user"},
		{"an inactive account never gets money tools", nonKYC, true, false, "user"},
		{"kyc approved but inactive still gets nothing", approved, true, false, "user"},
	}

	for _, tc := range cases {
		got := pythonRole(tc.kycStatus, tc.emailVerified, tc.isActive)
		if got != tc.want {
			t.Errorf("%s: pythonRole(%q, emailVerified=%v, active=%v) = %q, want %q",
				tc.name, tc.kycStatus, tc.emailVerified, tc.isActive, got, tc.want)
		}
	}
}

// TestVoteReplyIsDroppable covers the gate that keeps a declined poll-vote turn
// from being silently swallowed while still surfacing any reply that carries
// real content (even when the Python agent omitted the onboarding marker).
func TestVoteReplyIsDroppable(t *testing.T) {
	// A fully empty response with no onboarding marker: nothing to deliver.
	if !voteReplyIsDroppable(&ai.PythonChatResponse{}) {
		t.Fatal("empty unmarked response must be dropped")
	}
	// Whitespace is not content.
	if !voteReplyIsDroppable(&ai.PythonChatResponse{Response: "   "}) {
		t.Fatal("whitespace-only response must be dropped")
	}
	// Real content is always delivered, even without the onboarding marker.
	for _, resp := range []*ai.PythonChatResponse{
		{Response: "hello"},
		{Messages: []string{"hi"}},
		{Poll: &ai.PythonChatPoll{Title: "q", Options: []string{"a"}}},
		{Share: &ai.PythonChatShare{Kind: "link", Title: "t", URL: "https://x"}},
	} {
		if voteReplyIsDroppable(resp) {
			t.Fatalf("content-bearing unmarked response must not be dropped: %+v", resp)
		}
	}
	// Off-whitelist emoji is stripped by mapPythonChatReply so it is NOT content.
	if !voteReplyIsDroppable(&ai.PythonChatResponse{Reaction: "🍀"}) {
		t.Fatal("off-whitelist emoji must not count as deliverable content")
	}
	// Whitelisted emoji IS content — a tapback survives mapping.
	if voteReplyIsDroppable(&ai.PythonChatResponse{Reaction: "❤️"}) {
		t.Fatal("whitelisted reaction must count as deliverable content")
	}
	// An onboarding-marked turn is never dropped, regardless of content.
	if voteReplyIsDroppable(&ai.PythonChatResponse{Onboarding: &ai.PythonOnboardingStatus{}}) {
		t.Fatal("onboarding-marked turn must never be dropped")
	}
}

// ---------------------------------------------------------------------------
// The confirm_id handshake
//
// The email-OTP step-up is gone. A money turn now carries a confirm_id from
// Python's ledger, Go renders it as a Confirm/Cancel poll, and the tap comes
// back through ConfirmPlatformAction naming that id. These tests pin the two
// halves Go owns: asking (stage the id + draw the poll) and answering (send the
// id back).
// ---------------------------------------------------------------------------

// newDelegatedAdapter builds the adapter as the wiring builds it: an agent plus
// the Redis-backed confirm store. Both are needed — the store is only readable
// while delegation is on, since nothing else could settle what it holds.
func newDelegatedAdapter(t *testing.T) *orchestratorAdapter {
	t.Helper()
	return &orchestratorAdapter{
		confirmStore: ai.NewConfirmStore(newFakeConfirmRedis(), time.Minute, zap.NewNop()),
		python:       ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: "http://localhost:0", JWTSecret: "t"}, zap.NewNop()),
		logger:       zap.NewNop(),
	}
}

func TestStageConfirmIfAsked_StagesTheIDAndDrawsThePoll(t *testing.T) {
	a := newDelegatedAdapter(t)
	cid := uuid.New()
	reply := &platform.PlatformReply{Text: "Rent is 120k in 9 days. That leaves you short."}

	a.stageConfirmIfAsked(context.Background(), cid, &ai.PythonChatResponse{ConfirmID: "confirm_abc"}, reply)

	if reply.Confirm == nil {
		t.Fatal("a challenge must be drawn as a Confirm/Cancel poll")
	}
	if !strings.Contains(reply.Confirm.Summary, "leaves you short") {
		t.Fatalf("the poll must carry the verdict, got %q", reply.Confirm.Summary)
	}
	// And the tap has something to name.
	got, ok := a.pendingPythonConfirm(context.Background(), cid)
	if !ok || got != "confirm_abc" {
		t.Fatalf("want confirm_abc staged, got %q ok=%v", got, ok)
	}
}

func TestStageConfirmIfAsked_IgnoresAReplyWithNoChallenge(t *testing.T) {
	// A refusal or an answer carries no confirm_id, and must not ask for a tap.
	a := newDelegatedAdapter(t)
	cid := uuid.New()
	reply := &platform.PlatformReply{Text: "You have 184,000 liquid."}

	a.stageConfirmIfAsked(context.Background(), cid, &ai.PythonChatResponse{}, reply)

	if reply.Confirm != nil {
		t.Fatal("a reply with no confirm_id must not render a Confirm poll")
	}
	if _, ok := a.pendingPythonConfirm(context.Background(), cid); ok {
		t.Fatal("nothing should be staged")
	}
}

func TestStageConfirmIfAsked_WithoutAStoreItDoesNotAskForATap(t *testing.T) {
	// Drawing a poll nothing can settle is worse than not drawing one: the text
	// reply still carries the verdict.
	a := &orchestratorAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: "http://localhost:0", JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
	}
	reply := &platform.PlatformReply{Text: "Shall I move it?"}

	a.stageConfirmIfAsked(context.Background(), uuid.New(), &ai.PythonChatResponse{ConfirmID: "confirm_abc"}, reply)

	if reply.Confirm != nil {
		t.Fatal("with no store a Confirm poll could never be settled, so none may be drawn")
	}
}

func TestPendingPythonConfirm_FailsClosedWithoutARedisOrAnAgent(t *testing.T) {
	cid := uuid.New()

	// No store wired at all.
	unwired := &orchestratorAdapter{logger: zap.NewNop()}
	if _, ok := unwired.pendingPythonConfirm(context.Background(), cid); ok {
		t.Fatal("an unwired adapter must never report a pending confirm")
	}

	// A store with no Redis behind it: nothing can be read, so nothing is pending.
	store := ai.NewConfirmStore(nil, time.Minute, zap.NewNop())
	a := &orchestratorAdapter{
		confirmStore: store,
		python:       ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: "http://localhost:0", JWTSecret: "t"}, zap.NewNop()),
		logger:       zap.NewNop(),
	}
	if _, ok := a.pendingPythonConfirm(context.Background(), cid); ok {
		t.Fatal("an unreadable confirm must not be treated as pending")
	}
}

func TestPythonDelegatedNeedsOnlyTheAgent(t *testing.T) {
	// It used to require the OTP store as well. Delegation is now a question
	// about the agent alone.
	if (&orchestratorAdapter{}).pythonDelegated() {
		t.Fatal("no agent means no delegation")
	}
	withAgent := &orchestratorAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: "http://x", JWTSecret: "t"}, zap.NewNop()),
	}
	if !withAgent.pythonDelegated() {
		t.Fatal("an agent means delegation")
	}
}

func TestAConfirmTapSendsTheIDAndTheAnswer(t *testing.T) {
	// The answering half: a tap must name the challenge and carry the user's
	// answer, and nothing else in the body may look like an approval.
	srv, bodies := servePythonChatCapturing(t, &ai.PythonChatResponse{
		Response: "Moved it. Spendable is 88,000.",
	})
	a := newDelegatedAdapter(t)
	a.python = ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop())
	cid := uuid.New()
	if err := a.confirmStore.Put(context.Background(), cid, "confirm_abc"); err != nil {
		t.Fatalf("put: %v", err)
	}

	reply, err := a.settlePythonConfirm(
		context.Background(), uuid.New(), cid, "thread-1", entities.PlatformIMessage, "confirm_abc", true)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if reply == nil || !strings.Contains(reply.Text, "Moved it") {
		t.Fatalf("the ledger's reply must reach the user, got %+v", reply)
	}

	if len(*bodies) != 1 {
		t.Fatalf("want one python call, got %d", len(*bodies))
	}
	sent := (*bodies)[0]
	if sent.ConfirmID != "confirm_abc" {
		t.Fatalf("the tap must name the challenge, got %q", sent.ConfirmID)
	}
	if sent.Yes == nil || !*sent.Yes {
		t.Fatalf("a confirm tap must send yes=true, got %v", sent.Yes)
	}
	if sent.Message != "" {
		t.Fatalf("a tap carries no message, got %q", sent.Message)
	}

	// The challenge is spent: a second tap must not find it.
	if _, ok := a.pendingPythonConfirm(context.Background(), cid); ok {
		t.Fatal("a settled challenge must not stay tappable")
	}
}

func TestConfirmDeclinedReportsNoToPython(t *testing.T) {
	srv, bodies := servePythonChatCapturing(t, &ai.PythonChatResponse{
		Response: "No problem, I've left it alone.",
	})
	a := newDelegatedAdapter(t)
	a.python = ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop())
	cid := uuid.New()
	if err := a.confirmStore.Put(context.Background(), cid, "confirm_abc"); err != nil {
		t.Fatalf("put: %v", err)
	}

	if _, err := a.settlePythonConfirm(
		context.Background(), uuid.New(), cid, "thread-1", entities.PlatformIMessage, "confirm_abc", false); err != nil {
		t.Fatalf("settle: %v", err)
	}

	sent := (*bodies)[0]
	if sent.Yes == nil || *sent.Yes {
		t.Fatalf("a decline must send yes=false, got %v", sent.Yes)
	}
}

func TestSettledChallengeThatRaisesAnotherStagesIt(t *testing.T) {
	// A confirm can surface the next step (a smaller amount, say), which must
	// still have a tap behind it rather than arriving as a dead instruction.
	srv, _ := servePythonChatCapturing(t, &ai.PythonChatResponse{
		Response:  "That is over the limit. Shall I do 2,000 instead?",
		ConfirmID: "confirm_second",
	})
	a := newDelegatedAdapter(t)
	a.python = ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop())
	cid := uuid.New()
	if err := a.confirmStore.Put(context.Background(), cid, "confirm_first"); err != nil {
		t.Fatalf("put: %v", err)
	}

	reply, err := a.settlePythonConfirm(
		context.Background(), uuid.New(), cid, "thread-1", entities.PlatformIMessage, "confirm_first", false)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if reply.Confirm == nil {
		t.Fatal("a reply that raises a new challenge must draw a new poll")
	}
	got, ok := a.pendingPythonConfirm(context.Background(), cid)
	if !ok || got != "confirm_second" {
		t.Fatalf("want the new challenge staged, got %q ok=%v", got, ok)
	}
}
