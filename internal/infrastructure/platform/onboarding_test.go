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
	simulated bool
	validCode string
	verifyErr error
}

func (v *fakeVerifier) GenerateAndSendCodeSync(_ context.Context, _, identifier string) (string, bool, error) {
	if v.sendErr != nil {
		return "", false, v.sendErr
	}
	v.sentTo = append(v.sentTo, identifier)
	return "123456", v.simulated, nil
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
	byEmail map[string]*entities.UserProfile
	created []*entities.User
	// vanishAfterEmailLookups simulates an account being deleted mid-flow:
	// once more than this many GetByEmail calls have happened, lookups return
	// not-found. 0 disables the behaviour.
	vanishAfterEmailLookups int
	emailLookups            int
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{
		byPhone: map[string]*entities.UserProfile{},
		byEmail: map[string]*entities.UserProfile{},
	}
}

func (u *fakeUsers) GetByPhone(_ context.Context, phone string) (*entities.UserProfile, error) {
	if p, ok := u.byPhone[phone]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("not found")
}

func (u *fakeUsers) GetByEmail(_ context.Context, email string) (*entities.UserProfile, error) {
	u.emailLookups++
	if u.vanishAfterEmailLookups > 0 && u.emailLookups > u.vanishAfterEmailLookups {
		return nil, fmt.Errorf("not found")
	}
	if p, ok := u.byEmail[email]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("not found")
}

func (u *fakeUsers) CreateUserWithHash(_ context.Context, email string, phone *string, _ string) (*entities.User, error) {
	usr := &entities.User{ID: uuid.New(), Email: email}
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
	lastPhone string
	returnErr error
}

func (p *fakeProvisioner) ProvisionPhoneFirstUser(_ context.Context, _ uuid.UUID, firstName, country, phone string) error {
	p.calls++
	p.lastName = firstName
	p.lastCC = country
	p.lastPhone = phone
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

func stepContact(t *testing.T, ob *ChatOnboarder, sender string, contact SharedContact) string {
	t.Helper()
	reply, err := ob.Handle(context.Background(), OnboardInput{
		Platform: entities.PlatformIMessage,
		SenderID: sender,
		Contact:  &contact,
	})
	if err != nil {
		t.Fatalf("Handle(contact) error: %v", err)
	}
	if reply == nil {
		return ""
	}
	return reply.Text
}

func TestOnboarding_ContactCardShortcut(t *testing.T) {
	ob, _, ver, users, prov, linker := newTestOnboarder()
	sender := "+15551111"

	confirm := stepContact(t, ob, sender, SharedContact{
		FirstName: "Ada",
		Phones:    []string{"+2348012345678"},
		Emails:    []string{"ada-card@example.com"},
		Country:   "Nigeria",
	})
	if !strings.Contains(confirm, "Ada") {
		t.Fatalf("expected name in confirm, got: %q", confirm)
	}
	if !strings.Contains(confirm, "…") {
		t.Fatalf("expected masked phone in confirm, got: %q", confirm)
	}

	otpPrompt := step(t, ob, sender, "yes")
	if len(ver.sentTo) != 1 || ver.sentTo[0] != "+2348012345678" {
		t.Fatalf("expected OTP to card phone, got: %v", ver.sentTo)
	}
	if !strings.Contains(strings.ToLower(otpPrompt), "code") && !strings.Contains(strings.ToLower(otpPrompt), "texted") {
		t.Fatalf("expected OTP prompt, got: %q", otpPrompt)
	}

	consent := step(t, ob, sender, "123456")
	if !strings.Contains(consent, "I agree") {
		t.Fatalf("expected consent after OTP, got: %q", consent)
	}
	done := step(t, ob, sender, "I agree")
	if prov.calls != 1 || prov.lastName != "Ada" || prov.lastCC != "NG" || prov.lastPhone != "+2348012345678" {
		t.Fatalf("provision mismatch: %+v %+v", prov, linker)
	}
	if linker.calls != 1 {
		t.Fatalf("expected auto-link, got %d", linker.calls)
	}
	if len(users.created) != 1 || users.created[0].Email != "ada-card@example.com" {
		t.Fatalf("expected user created from card email, got %+v", users.created)
	}
	if !strings.Contains(strings.ToLower(done), "you're in") {
		t.Fatalf("expected completion, got: %q", done)
	}
}

func TestOnboarding_ContactEmailWaitsForConfirm(t *testing.T) {
	ob, _, ver, users, _, _ := newTestOnboarder()
	sender := "+15551114"
	users.byEmail["existing-card@example.com"] = &entities.UserProfile{
		ID: uuid.New(), Email: "existing-card@example.com", IsActive: true,
	}

	confirm := stepContact(t, ob, sender, SharedContact{
		FirstName: "Ada",
		Phones:    []string{"+2348012345678"},
		Emails:    []string{"existing-card@example.com"},
		Country:   "NG",
	})
	if len(ver.sentTo) != 0 {
		t.Fatalf("must not look up/OTP the card email before confirm, sent to %v", ver.sentTo)
	}
	if !strings.Contains(confirm, "Ada") {
		t.Fatalf("expected phone confirm first, got: %q", confirm)
	}

	askEmailOTP := step(t, ob, sender, "yes")
	if len(ver.sentTo) != 1 || ver.sentTo[0] != "existing-card@example.com" {
		t.Fatalf("expected email OTP after confirm, got: %v", ver.sentTo)
	}
	if !strings.Contains(askEmailOTP, "emailed") {
		t.Fatalf("expected email OTP prompt after confirm, got: %q", askEmailOTP)
	}
}

func TestOnboarding_ContactNameOnlyStillAsksPhone(t *testing.T) {
	ob, _, _, _, _, _ := newTestOnboarder()
	sender := "+15551112"

	reply := stepContact(t, ob, sender, SharedContact{FirstName: "Bola"})
	if !strings.Contains(strings.ToLower(reply), "home") && !strings.Contains(strings.ToLower(reply), "phone") {
		t.Fatalf("expected country or phone follow-up after name-only card, got: %q", reply)
	}
}

func TestOnboarding_ContactRejectFallsBackToTyping(t *testing.T) {
	ob, _, _, _, _, _ := newTestOnboarder()
	sender := "+15551113"

	stepContact(t, ob, sender, SharedContact{
		FirstName: "Chidi",
		Phones:    []string{"+2348099999999"},
		Country:   "NG",
	})
	reply := step(t, ob, sender, "that's not me")
	if !strings.Contains(strings.ToLower(reply), "number") {
		t.Fatalf("expected phone fallback after rejecting card, got: %q", reply)
	}
}

func TestOnboarding_HappyPath(t *testing.T) {
	ob, _, ver, users, prov, linker := newTestOnboarder()
	sender := "+15550001"

	intro := step(t, ob, sender, "hi")
	if !strings.Contains(strings.ToLower(intro), "share your contact") {
		t.Fatalf("intro should invite sharing a contact, got: %q", intro)
	}
	if !strings.Contains(strings.ToLower(intro), "first name") {
		t.Fatalf("intro should offer typing a name, got: %q", intro)
	}

	step(t, ob, sender, "Ada")
	step(t, ob, sender, "Nigeria")
	askPhone := step(t, ob, sender, "ada@example.com")
	if !strings.Contains(strings.ToLower(askPhone), "number") {
		t.Fatalf("expected phone prompt after email, got: %q", askPhone)
	}
	askOTP := step(t, ob, sender, "+2348012345678")
	if len(ver.sentTo) != 1 || ver.sentTo[0] != "+2348012345678" {
		t.Fatalf("expected OTP sent to normalized phone, got: %v", ver.sentTo)
	}
	if !strings.Contains(askOTP, "code") {
		t.Fatalf("expected code prompt, got: %q", askOTP)
	}

	consent := step(t, ob, sender, "123456")
	if !strings.Contains(consent, "I agree") {
		t.Fatalf("expected consent prompt, got: %q", consent)
	}
	if len(users.created) != 1 || users.created[0].Email != "ada@example.com" {
		t.Fatalf("expected user created with email after OTP, got %d", len(users.created))
	}

	done := step(t, ob, sender, "YES")
	if prov.calls != 1 || prov.lastName != "Ada" || prov.lastCC != "NG" || prov.lastPhone != "+2348012345678" {
		t.Fatalf("expected provisioning with Ada/NG/+2348012345678, got calls=%d name=%q cc=%q phone=%q", prov.calls, prov.lastName, prov.lastCC, prov.lastPhone)
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
	users.byPhone["+2348012345678"] = &entities.UserProfile{ID: existingID, IsActive: true}

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Bola")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "bola@example.com")
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
	step(t, ob, sender, "chidi@example.com")
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
	step(t, ob, sender, "dele@example.com")
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
	step(t, ob, sender, "efe@example.com")
	step(t, ob, sender, "+2348012345678")
	step(t, ob, sender, "123456")
	reply := step(t, ob, sender, "yes")

	if !strings.Contains(reply, "I agree") {
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
	proc, sent, _ := newTestProcessor(repo, &fakeOrchestrator{})
	ob, _, _, _, _, _ := newTestOnboarder()
	proc.SetOnboarder(ob)

	raw, err := json.Marshal(InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15559999",
		Text:     "hey",
	})
	if err != nil {
		t.Fatalf("marshal onboarding message: %v", err)
	}
	if err := proc.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected 1 outbound message, got %d", len(*sent))
	}
	if !strings.Contains(strings.ToLower((*sent)[0].Text), "share your contact") {
		t.Fatalf("expected onboarding intro inviting a contact card, got: %q", (*sent)[0].Text)
	}
}

func TestProcessor_HandshakeTokenSkipsOnboarding(t *testing.T) {
	repo := newFakeRepo()
	proc, sent, _ := newTestProcessor(repo, &fakeOrchestrator{})
	ob, _, _, _, _, _ := newTestOnboarder()
	proc.SetOnboarder(ob)

	// A 64-hex token with no active onboarding session must not create a session;
	// it falls through to the (failing) handshake path instead.
	token := strings.Repeat("a", 64)
	raw, err := json.Marshal(InboundMessage{
		Platform: entities.PlatformIMessage,
		UserID:   "+15558888",
		Text:     token,
	})
	if err != nil {
		t.Fatalf("marshal token message: %v", err)
	}
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
	if !strings.Contains(msg, "You're in, Ada") {
		t.Fatalf("expected personalized greeting, got: %q", msg)
	}
	if !strings.Contains(msg, "naira") {
		t.Fatalf("expected country-aware naira line, got: %q", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "what's money actually for") {
		t.Fatalf("expected first-session question, got: %q", msg)
	}
	if !strings.Contains(msg, "https://app.example/join") {
		t.Fatalf("completion should quietly include app link, got: %q", msg)
	}
}

func TestCompletionMessage_CountryVariants(t *testing.T) {
	ob, _, _, _, _, _ := newTestOnboarder()

	if msg := ob.completionMessage("Kofi", "GH"); !strings.Contains(msg, "cedis") {
		t.Fatalf("expected cedi line for GH, got: %q", msg)
	}
	if msg := ob.completionMessage("John", "US"); strings.Contains(msg, "naira") || strings.Contains(msg, "cedis") {
		t.Fatalf("expected generic line for US, got: %q", msg)
	}
	if msg := ob.completionMessage("", ""); !strings.Contains(msg, "You're in.") {
		t.Fatalf("expected fallback greeting for empty name, got: %q", msg)
	}
}

func TestCompletionMessage_EndToEndPrompt(t *testing.T) {
	ob, _, _, _, prov, linker := newTestOnboarder()
	sender := "+15557777"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "ada2@example.com")
	step(t, ob, sender, "+2348012345678")
	done := step(t, ob, sender, "123456")
	if !strings.Contains(done, "I agree") {
		t.Fatalf("expected consent prompt, got: %q", done)
	}
	done = step(t, ob, sender, "I agree")
	if !strings.Contains(strings.ToLower(done), "what's money actually for") {
		t.Fatalf("expected first-session question at completion, got: %q", done)
	}
	if prov.lastCC != "NG" || linker.calls != 1 {
		t.Fatalf("expected provision/link on completion, got prov=%d link=%d", prov.calls, linker.calls)
	}
}

func TestOnboarding_ExistingEmailVerifiesAndLinks(t *testing.T) {
	ob, _, ver, users, prov, linker := newTestOnboarder()
	sender := "+15550008"
	existingID := uuid.New()
	users.byEmail["existing@example.com"] = &entities.UserProfile{ID: existingID, Email: "existing@example.com", IsActive: true}

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Zara")
	step(t, ob, sender, "NG")
	askEmailOTP := step(t, ob, sender, "existing@example.com")
	if !strings.Contains(askEmailOTP, "emailed") {
		t.Fatalf("expected email OTP prompt, got: %q", askEmailOTP)
	}

	askPhone := step(t, ob, sender, "123456")
	if !strings.Contains(strings.ToLower(askPhone), "number") {
		t.Fatalf("expected phone prompt after email OTP, got: %q", askPhone)
	}

	step(t, ob, sender, "+2348099999999")
	consent := step(t, ob, sender, "123456")
	if !strings.Contains(consent, "I agree") {
		t.Fatalf("expected consent prompt, got: %q", consent)
	}

	if len(users.created) != 0 {
		t.Fatalf("should not create a new user when email already exists, created %d", len(users.created))
	}

	step(t, ob, sender, "yes")
	if prov.calls != 1 || linker.calls != 1 {
		t.Fatalf("expected provision+link for existing user, got prov=%d link=%d", prov.calls, linker.calls)
	}
	// Verify it was sent to both email and phone
	if len(ver.sentTo) < 2 {
		t.Fatalf("expected OTPs to both email and phone, got: %v", ver.sentTo)
	}
}

// TestOnboarding_SimulatedOTPDisclosesNoSend pins the honest-delivery contract:
// when the verifier runs in simulated mode (dev, no provider), Miriam must say
// nothing was actually sent instead of claiming an email/SMS went out.
func TestOnboarding_SimulatedOTPDisclosesNoSend(t *testing.T) {
	ob, _, ver, users, _, _ := newTestOnboarder()
	sender := "+15550010"
	existingID := uuid.New()
	users.byEmail["dev@example.com"] = &entities.UserProfile{ID: existingID, Email: "dev@example.com", IsActive: true}
	ver.simulated = true

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Zara")
	step(t, ob, sender, "NG")
	reply := step(t, ob, sender, "dev@example.com")
	if !strings.Contains(strings.ToLower(reply), "nothing was actually sent") {
		t.Fatalf("simulated email OTP must disclose no send, got: %q", reply)
	}

	// Confirm the email code, then the SMS path discloses too.
	askPhone := step(t, ob, sender, ver.validCode)
	if !strings.Contains(strings.ToLower(askPhone), "number") {
		t.Fatalf("expected phone prompt after email code, got: %q", askPhone)
	}
	reply = step(t, ob, sender, "+2348099999998")
	if !strings.Contains(strings.ToLower(reply), "nothing was actually texted") {
		t.Fatalf("simulated SMS OTP must disclose no send, got: %q", reply)
	}
}

// TestOnboarding_OTPSendFailureTellsUser pins that a real send error surfaces
// as a failure message, never as a success claim.
func TestOnboarding_OTPSendFailureTellsUser(t *testing.T) {
	ob, _, ver, users, _, _ := newTestOnboarder()
	sender := "+15550011"
	users.byEmail["fail@example.com"] = &entities.UserProfile{ID: uuid.New(), Email: "fail@example.com", IsActive: true}
	ver.sendErr = fmt.Errorf("resend: status 500")

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Zara")
	step(t, ob, sender, "NG")
	reply := step(t, ob, sender, "fail@example.com")
	lower := strings.ToLower(reply)
	if strings.Contains(lower, "emailed") || strings.Contains(lower, "just sent") {
		t.Fatalf("failed OTP send must not claim success, got: %q", reply)
	}
	if !strings.Contains(lower, "couldn't") && !strings.Contains(lower, "try again") {
		t.Fatalf("failed OTP send should ask the user to retry, got: %q", reply)
	}
}

func TestOnboarding_EmailOTPRetryCapResetsSession(t *testing.T) {
	ob, _, _, users, _, _ := newTestOnboarder()
	sender := "+15550009"
	existingID := uuid.New()
	users.byEmail["locked@example.com"] = &entities.UserProfile{ID: existingID, Email: "locked@example.com", IsActive: true}

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Tomi")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "locked@example.com")

	var last string
	for i := 0; i < maxEmailOTPAttempts; i++ {
		last = step(t, ob, sender, "000000")
	}
	if !strings.Contains(strings.ToLower(last), "link this chat from the rail app") {
		t.Fatalf("expected reset message after max email OTP attempts, got: %q", last)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared after too many wrong email codes")
	}
}

func TestOnboarding_InactiveEmailAccountRejected(t *testing.T) {
	ob, _, _, users, _, _ := newTestOnboarder()
	sender := "+15550010"
	existingID := uuid.New()
	users.byEmail["inactive@example.com"] = &entities.UserProfile{ID: existingID, Email: "inactive@example.com", IsActive: false}

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	step(t, ob, sender, "NG")
	reply := step(t, ob, sender, "inactive@example.com")
	if !strings.Contains(reply, "isn't active") {
		t.Fatalf("expected inactive account message, got: %q", reply)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared after inactive account")
	}
}

func TestOnboarding_EmailAccountVanishesAfterOTPSend(t *testing.T) {
	ob, _, ver, users, _, _ := newTestOnboarder()
	sender := "+15550013"
	users.byEmail["fading@example.com"] = &entities.UserProfile{ID: uuid.New(), Email: "fading@example.com", IsActive: true}
	// First lookup finds the account and triggers the OTP; the second lookup
	// (re-verify right after sending) sees it disappear.
	users.vanishAfterEmailLookups = 1

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ngozi")
	step(t, ob, sender, "NG")
	reply := step(t, ob, sender, "fading@example.com")

	if len(ver.sentTo) != 1 || ver.sentTo[0] != "fading@example.com" {
		t.Fatalf("expected email OTP sent before re-check, got: %v", ver.sentTo)
	}
	if !strings.Contains(reply, "isn't active") {
		t.Fatalf("expected inactive/deleted account message after re-check, got: %q", reply)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared when the account vanishes after the OTP send")
	}
}

func TestOnboarding_EmailAccountDeactivatedAfterEmailOTP(t *testing.T) {
	ob, _, _, users, _, _ := newTestOnboarder()
	sender := "+15550014"
	existingID := uuid.New()
	users.byEmail["frozen@example.com"] = &entities.UserProfile{ID: existingID, Email: "frozen@example.com", IsActive: true}

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Ada")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "frozen@example.com")

	// The account is deactivated between the OTP send and the OTP verification.
	users.byEmail["frozen@example.com"].IsActive = false
	reply := step(t, ob, sender, "123456")

	if !strings.Contains(reply, "isn't active") {
		t.Fatalf("expected inactive account message after email OTP, got: %q", reply)
	}
	if ob.HasSession(context.Background(), entities.PlatformIMessage, sender) {
		t.Fatal("session should be cleared when the account is inactive after email OTP")
	}
}

func TestOnboarding_SkipEmailUsesNoEmail(t *testing.T) {
	ob, _, _, users, _, _ := newTestOnboarder()
	sender := "+15550011"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Kemi")
	step(t, ob, sender, "NG")
	askPhone := step(t, ob, sender, "skip")
	if !strings.Contains(strings.ToLower(askPhone), "number") {
		t.Fatalf("expected phone prompt after skipping email, got: %q", askPhone)
	}
	step(t, ob, sender, "+2348099999999")
	step(t, ob, sender, "123456")
	step(t, ob, sender, "yes")
	if len(users.created) != 1 {
		t.Fatalf("expected one user created when email skipped, got %d", len(users.created))
	}
	got := users.created[0].Email
	if !strings.HasPrefix(got, "phone+") || !strings.HasSuffix(got, "@placeholder.invalid") {
		t.Fatalf("expected opaque placeholder email, got %q", got)
	}
	// The placeholder must not carry the phone number (no PII duplication).
	if strings.Contains(got, "2348099999999") {
		t.Fatalf("placeholder email leaks the phone number: %q", got)
	}
}

func TestOnboarding_NewUserCreatedWithEmail(t *testing.T) {
	ob, _, _, users, _, _ := newTestOnboarder()
	sender := "+15550012"

	step(t, ob, sender, "hi")
	step(t, ob, sender, "Lola")
	step(t, ob, sender, "NG")
	step(t, ob, sender, "lola@example.com")
	step(t, ob, sender, "+2348099999999")
	step(t, ob, sender, "123456")
	step(t, ob, sender, "yes")
	if len(users.created) != 1 || users.created[0].Email != "lola@example.com" {
		t.Fatalf("expected new user with email, got %d", len(users.created))
	}
}

func TestProcessor_ContactInboundStartsOnboarding(t *testing.T) {
	repo := newFakeRepo()
	proc, sent, _ := newTestProcessor(repo, &fakeOrchestrator{})
	ob, _, _, _, _, _ := newTestOnboarder()
	proc.SetOnboarder(ob)

	raw, err := json.Marshal(InboundMessage{
		Platform:  entities.PlatformIMessage,
		UserID:    "+15551212",
		IsContact: true,
		Contact: &SharedContact{
			FirstName: "Ada",
			Phones:    []string{"+2348012345678"},
			Country:   "NG",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected 1 outbound, got %d", len(*sent))
	}
	if !strings.Contains((*sent)[0].Text, "Ada") {
		t.Fatalf("expected contact confirm, got: %q", (*sent)[0].Text)
	}
}

func TestProcessor_UnlinkedYesGoesToOnboarder(t *testing.T) {
	repo := newFakeRepo()
	proc, sent, _ := newTestProcessor(repo, &fakeOrchestrator{})
	ob, _, _, _, _, _ := newTestOnboarder()
	proc.SetOnboarder(ob)

	sender := "+15551313"
	process := func(text string) {
		t.Helper()
		raw, err := json.Marshal(InboundMessage{
			Platform: entities.PlatformIMessage,
			UserID:   sender,
			Text:     text,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := proc.Process(context.Background(), raw); err != nil {
			t.Fatalf("Process(%q): %v", text, err)
		}
	}
	process("hi")
	process("Ada")
	process("NG")
	process("ada-yes@example.com")
	process("+2348012345678")
	process("123456")
	process("yes")
	last := (*sent)[len(*sent)-1]
	if !strings.Contains(strings.ToLower(last.Text), "you're in") {
		t.Fatalf("expected onboarding completion from unlinked yes, got: %q", last.Text)
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"lola@example.com", "lola@example.com"},
		{"  Lola@Example.COM  ", "lola@example.com"},
		{"mailto:lola@example.com", "lola@example.com"},
		{"Lola Ogunsola <lola@example.com>", "lola@example.com"},
		{"skip", ""},
		{"not an email", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeEmail(c.in); got != c.want {
			t.Errorf("normalizeEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
