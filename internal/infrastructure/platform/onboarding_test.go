package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// fakeStore is an in-memory OnboardingStateStore that mimics the JSON round-trip
// of the real Redis client.
type fakeStore struct {
	m map[string][]byte
}

func newFakeStore() *fakeStore { return &fakeStore{m: map[string][]byte{}} }

func (s *fakeStore) Set(_ context.Context, key string, value interface{}, _ time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.m[key] = b
	return nil
}

func (s *fakeStore) Get(_ context.Context, key string, dest interface{}) error {
	b, ok := s.m[key]
	if !ok {
		return fmt.Errorf("not found")
	}
	return json.Unmarshal(b, dest)
}

func (s *fakeStore) Del(_ context.Context, key string) error { delete(s.m, key); return nil }
func (s *fakeStore) Exists(_ context.Context, key string) (bool, error) {
	_, ok := s.m[key]
	return ok, nil
}

// fakeVerifier records sent codes and validates against a fixed code.
type fakeVerifier struct {
	sentTo    []string
	sendErr   error
	validCode string
	verifyErr error
}

func (v *fakeVerifier) GenerateAndSendCode(_ context.Context, _, identifier string) (string, error) {
	if v.sendErr != nil {
		return "", v.sendErr
	}
	v.sentTo = append(v.sentTo, identifier)
	return "sent", nil
}

func (v *fakeVerifier) VerifyCode(_ context.Context, _, _, code string) (bool, error) {
	if v.verifyErr != nil {
		return false, v.verifyErr
	}
	return code == v.validCode, nil
}

// fakeUsers backs the OnboardingUserStore.
type fakeUsers struct {
	byPhone map[string]*entities.UserProfile
	created []*entities.User
}

func newFakeUsers() *fakeUsers { return &fakeUsers{byPhone: map[string]*entities.UserProfile{}} }

func (u *fakeUsers) GetByPhone(_ context.Context, phone string) (*entities.UserProfile, error) {
	if p, ok := u.byPhone[phone]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("not found")
}

func (u *fakeUsers) CreateUserWithHash(_ context.Context, _ string, phone *string, _ string) (*entities.User, error) {
	usr := &entities.User{ID: uuid.New()}
	if phone != nil {
		usr.Phone = phone
	}
	u.created = append(u.created, usr)
	return usr, nil
}

type fakeProvisioner struct {
	calls     int
	lastName  string
	lastCC    string
	returnErr error
}

func (p *fakeProvisioner) ProvisionPhoneFirstUser(_ context.Context, _ uuid.UUID, firstName, country string) error {
	p.calls++
	p.lastName = firstName
	p.lastCC = country
	return p.returnErr
}

type fakeLinker struct {
	calls     int
	lastSend  string
	returnErr error
}

func (l *fakeLinker) LinkVerified(_ context.Context, _ uuid.UUID, _ entities.Platform, senderUserID string) (*entities.PlatformIdentity, error) {
	l.calls++
	l.lastSend = senderUserID
	if l.returnErr != nil {
		return nil, l.returnErr
	}
	now := time.Now()
	return &entities.PlatformIdentity{ID: uuid.New(), PlatformUserID: senderUserID, LinkedAt: &now}, nil
}

func newTestOnboarder() (*ChatOnboarder, *fakeStore, *fakeVerifier, *fakeUsers, *fakeProvisioner, *fakeLinker) {
	store := newFakeStore()
	ver := &fakeVerifier{validCode: "123456"}
	users := newFakeUsers()
	prov := &fakeProvisioner{}
	linker := &fakeLinker{}
	ob := NewChatOnboarder(store, ver, users, prov, linker, "https://app.example/join", nil)
	return ob, store, ver, users, prov, linker
}

func step(t *testing.T, ob *ChatOnboarder, sender, text string) string {
	t.Helper()
	reply, err := ob.Handle(context.Background(), OnboardInput{
		Platform: entities.PlatformIMessage,
		SenderID: sender,
		Text:     text,
	})
	if err != nil {
		t.Fatalf("Handle(%q) error: %v", text, err)
	}
	if reply == nil {
		return ""
	}
	return reply.Text
}

func TestOnboarding_HappyPath(t *testing.T) {
	ob, _, ver, users, prov, linker := newTestOnboarder()
	sender := "+15550001"

	intro := step(t, ob, sender, "hi")
	if !strings.Contains(intro, "https://app.example/join") {
		t.Fatalf("intro should include app link, got: %q", intro)
	}
	if !strings.Contains(strings.ToLower(intro), "first name") {
		t.Fatalf("intro should ask for name, got: %q", intro)
	}

	step(t, ob, sender, "Ada")
	step(t, ob, sender, "Nigeria")
	askOTP := step(t, ob, sender, "+2348012345678")
	if len(ver.sentTo) != 1 || ver.sentTo[0] != "+2348012345678" {
		t.Fatalf("expected OTP sent to normalized phone, got: %v", ver.sentTo)
	}
	if !strings.Contains(askOTP, "code") {
		t.Fatalf("expected code prompt, got: %q", askOTP)
	}

	consent := step(t, ob, sender, "123456")
	if !strings.Contains(strings.ToUpper(consent), "YES") {
		t.Fatalf("expected consent prompt, got: %q", consent)
	}
	if len(users.created) != 1 {
		t.Fatalf("expected user created after OTP, got %d", len(users.created))
	}

	done := step(t, ob, sender, "YES")
	if prov.calls != 1 || prov.lastName != "Ada" || prov.lastCC != "NG" {
		t.Fatalf("expected provisioning with Ada/NG, got calls=%d name=%q cc=%q", prov.calls, prov.lastName, prov.lastCC)
	}
	if linker.calls != 1 || linker.lastSend != sender {
		t.Fatalf("expected auto-link to sender, got calls=%d send=%q", linker.calls, linker.lastSend)
	}
	if !strings.Contains(done, "https://app.example/join") {
		t.Fatalf("completion should re-share app link, got: %q", done)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared after completion")
	}
}

func TestOnboarding_ExistingPhoneAutoLinks(t *testing.T) {
	ob, _, _, users, prov, linker := newTestOnboarder()
	sender := "+15550002"
	existingID := uuid.New()
	users.byPhone["+2348012345678"] = &entities.UserProfile{ID: existingID}

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Bola")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "+2348012345678")
	step(t, ob, sender, "123456")
	step(t, ob, sender, "yes")

	if len(users.created) != 0 {
		t.Fatalf("should not create a new user when phone already exists, created %d", len(users.created))
	}
	if prov.calls != 1 || linker.calls != 1 {
		t.Fatalf("expected provision+link for existing user, got prov=%d link=%d", prov.calls, linker.calls)
	}
}

func TestOnboarding_OTPRetryCapResetsSession(t *testing.T) {
	ob, _, _, _, _, _ := newTestOnboarder()
	sender := "+15550003"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Chidi")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "+2348012345678")

	var last string
	for i := 0; i < maxOnboardingOTPAttempts; i++ {
		last = step(t, ob, sender, "000000")
	}
	if !strings.Contains(strings.ToLower(last), "start over") {
		t.Fatalf("expected reset message after max attempts, got: %q", last)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared after too many wrong codes")
	}
}

func TestOnboarding_ConsentRequiredBeforeProvision(t *testing.T) {
	ob, _, _, _, prov, linker := newTestOnboarder()
	sender := "+15550004"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Dele")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "+2348012345678")
	step(t, ob, sender, "123456")

	// A non-affirmative reply must not provision.
	step(t, ob, sender, "not yet")
	if prov.calls != 0 || linker.calls != 0 {
		t.Fatalf("must not provision before consent, prov=%d link=%d", prov.calls, linker.calls)
	}

	step(t, ob, sender, "yes")
	if prov.calls != 1 || linker.calls != 1 {
		t.Fatalf("expected provision after consent, prov=%d link=%d", prov.calls, linker.calls)
	}
}

func TestOnboarding_ProvisionFailureKeepsSession(t *testing.T) {
	ob, _, _, _, prov, _ := newTestOnboarder()
	prov.returnErr = fmt.Errorf("boom")
	sender := "+15550005"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Efe")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "+2348012345678")
	step(t, ob, sender, "123456")
	reply := step(t, ob, sender, "yes")

	if !strings.Contains(strings.ToUpper(reply), "YES") {
		t.Fatalf("expected retry prompt on provision failure, got: %q", reply)
	}
	if !ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should survive a provisioning failure so the user can retry")
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in      string
		country string
		want    string
		ok      bool
	}{
		{"+2348012345678", "NG", "+2348012345678", true},
		{"08012345678", "NG", "+2348012345678", true},
		{"(080) 1234-5678", "NG", "+2348012345678", true},
		{"8012345678", "NG", "+2348012345678", true},
		{"555", "US", "", false},
		{"08012345678", "", "", false},
		{"not a phone", "NG", "", false},
	}
	for _, c := range cases {
		got, ok := normalizePhone(c.in, c.country)
		if ok != c.ok || got != c.want {
			t.Errorf("normalizePhone(%q,%q) = (%q,%v), want (%q,%v)", c.in, c.country, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeCountry(t *testing.T) {
	cases := map[string]string{
		"Nigeria":       "NG",
		"nigeria":       "NG",
		"ng":            "NG",
		"United States": "US",
		"uk":            "GB",
		"gh":            "GH",
		"":              "",
		"somewhere odd": "",
	}
	for in, want := range cases {
		if got := normalizeCountry(in); got != want {
			t.Errorf("normalizeCountry(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsAffirmative(t *testing.T) {
	yes := []string{"yes", "Y", "yeah", "I agree", "sure", "YES!", "yes i agree"}
	no := []string{"no", "nope", "later", "", "maybe"}
	for _, s := range yes {
		if !isAffirmative(s) {
			t.Errorf("isAffirmative(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isAffirmative(s) {
			t.Errorf("isAffirmative(%q) = true, want false", s)
		}
	}
}

func TestProcessor_RoutesUnlinkedSenderToOnboarding(t *testing.T) {
	repo := newFakeRepo()
	proc, sent := newTestProcessor(repo, &fakeOrchestrator{})
	ob, _, _, _, _, _ := newTestOnboarder()
	proc.SetOnboarder(ob)

	raw, _ := json.Marshal(InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15559999",
		Text:     "hey",
	})
	if err := proc.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected 1 outbound message, got %d", len(*sent))
	}
	if !strings.Contains((*sent)[0].Text, "https://app.example/join") {
		t.Fatalf("expected onboarding intro with app link, got: %q", (*sent)[0].Text)
	}
}

func TestProcessor_HandshakeTokenSkipsOnboarding(t *testing.T) {
	repo := newFakeRepo()
	proc, sent := newTestProcessor(repo, &fakeOrchestrator{})
	ob, _, _, _, _, _ := newTestOnboarder()
	proc.SetOnboarder(ob)

	// A 64-hex token with no active onboarding session must not create a session;
	// it falls through to the (failing) handshake path instead.
	token := strings.Repeat("a", 64)
	raw, _ := json.Marshal(InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15558888",
		Text:     token,
	})
	if err := proc.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, "+15558888") {
		t.Fatal("handshake token should not start an onboarding session")
	}
	if len(*sent) != 1 || !strings.Contains(strings.ToLower((*sent)[0].Text), "link code") {
		t.Fatalf("expected handshake failure reply, got: %+v", *sent)
	}
}

func TestCompletionMessage_PersonalizedFirstInsight(t *testing.T) {
	ob, _, _, _, _, _ := newTestOnboarder()

	msg := ob.completionMessage("Ada", "NG")
	if !strings.Contains(msg, "You're all set, Ada!") {
		t.Fatalf("expected personalized greeting, got: %q", msg)
	}
	if !strings.Contains(msg, "stable dollars and convert to naira") {
		t.Fatalf("expected country-aware naira line, got: %q", msg)
	}
	if !strings.Contains(msg, "what are you saving for?") {
		t.Fatalf("expected first-savings prompt, got: %q", msg)
	}
	if !strings.Contains(msg, "https://app.example/join") {
		t.Fatalf("completion should re-share app link, got: %q", msg)
	}
}

func TestCompletionMessage_CountryVariants(t *testing.T) {
	ob, _, _, _, _, _ := newTestOnboarder()

	if msg := ob.completionMessage("Kofi", "GH"); !strings.Contains(msg, "convert to cedis") {
		t.Fatalf("expected cedi line for GH, got: %q", msg)
	}
	if msg := ob.completionMessage("John", "US"); strings.Contains(msg, "convert to") {
		t.Fatalf("expected generic line for US, got: %q", msg)
	}
	if msg := ob.completionMessage("", ""); !strings.Contains(msg, "You're all set!") {
		t.Fatalf("expected fallback greeting for empty name, got: %q", msg)
	}
}

func TestCompletionMessage_EndToEndPrompt(t *testing.T) {
	ob, _, _, _, prov, linker := newTestOnboarder()
	sender := "+15557777"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "+2348012345678")
	done := step(t, ob, sender, "123456")
	if !strings.Contains(strings.ToUpper(done), "YES") {
		t.Fatalf("expected consent prompt, got: %q", done)
	}
	done = step(t, ob, sender, "YES")
	if !strings.Contains(done, "what are you saving for?") {
		t.Fatalf("expected first-savings prompt at completion, got: %q", done)
	}
	if prov.lastCC != "NG" || linker.calls != 1 {
		t.Fatalf("expected provision/link on completion, got prov=%d link=%d", prov.calls, linker.calls)
	}
}
