package platform

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// guestSessionTTL bounds how long a pre-signup conversation lives in Redis.
// It is a conversation now, not a form — people come back hours later.
const guestSessionTTL = 24 * time.Hour

// turnLockTTL caps one in-flight guest turn so a crashed worker never wedges
// a sender. Turns are short (one or two LLM completions).
const turnLockTTL = 30 * time.Second

// maxGuestTurns caps model turns inside one session before Miriam steers to
// signup or wraps up. Bounds cost on conversations that never convert.
const maxGuestTurns = 40

// maxGuestDailyTurns caps model turns per sender per rolling 24h across
// sessions, so a cleared session cannot reset the meter.
const maxGuestDailyTurns = 60

// maxOnboardingOTPAttempts caps wrong SMS OTP entries before the session is reset.
const maxOnboardingOTPAttempts = 5

// maxEmailOTPAttempts caps wrong email OTP entries before the session is reset.
const maxEmailOTPAttempts = 5

// maxTranscriptTurns is how much of the guest conversation is replayed into the
// user's first platform conversation at signup.
const maxTranscriptTurns = 20

// OnboardingStateStore is the subset of the Redis client the onboarder needs.
type OnboardingStateStore interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error)
	Get(ctx context.Context, key string, dest interface{}) error
	Del(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// OnboardingOTPVerifier sends and checks one-time codes. GenerateAndSendCodeSync
// is synchronous and reports real delivery: err means the send failed (tell the
// user), simulated=true means dev mode stored the code without a sender
// (disclose it, never claim a send). Satisfied by the domain VerificationService.
type OnboardingOTPVerifier interface {
	GenerateAndSendCodeSync(ctx context.Context, identifierType, identifier string) (code string, simulated bool, err error)
	VerifyCode(ctx context.Context, identifierType, identifier, code string) (bool, error)
}

// OnboardingUserStore creates and looks up users by phone or email. Satisfied
// by the user repository.
type OnboardingUserStore interface {
	GetByPhone(ctx context.Context, phone string) (*entities.UserProfile, error)
	GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error)
	CreateUserWithHash(ctx context.Context, email string, phone *string, passwordHash string) (*entities.User, error)
}

// OnboardingProvisioner completes Tier 1 setup once the phone is verified.
// For existing users it is a no-op / safe update, so chat onboarding can link
// an already-created account without downgrading KYC or re-creating wallets.
// The verified phone is passed so an existing email-only account can have its
// phone field populated.
// Satisfied by the onboarding service.
type OnboardingProvisioner interface {
	ProvisionPhoneFirstUser(ctx context.Context, userID uuid.UUID, firstName, country, phone string) error
}

// OnboardingLinker binds the verified messaging identity to the user without a
// token handshake. Satisfied by the LinkingService.
type OnboardingLinker interface {
	LinkVerified(ctx context.Context, userID uuid.UUID, platform entities.Platform, senderUserID string) (*entities.PlatformIdentity, error)
}

// GuestMoneyTypeWriter persists the agent's read of the guest's money style so
// the authenticated Miriam calibrates tone from the first turn. Satisfied by
// the Miriam memory repository. Optional.
type GuestMoneyTypeWriter interface {
	SetMoneyType(ctx context.Context, userID uuid.UUID, moneyType string) error
}

// GuestTranscriptWriter replays the pre-signup conversation into the user's
// first platform conversation so the authenticated Miriam continues mid-thread
// instead of starting cold. Satisfied by a DI adapter over the conversation
// repository. Optional.
type GuestTranscriptWriter interface {
	AppendGuestTranscript(ctx context.Context, userID uuid.UUID, identity *entities.PlatformIdentity, threadID string, turns []GuestMessage) error
}

type guestPhase string

const (
	// phaseConverse — agent-led conversation. Identity is only collected when
	// the guest wants something that needs an account.
	phaseConverse guestPhase = "converse"
	// phasePhone — signup demanded, waiting for a phone number.
	phasePhone guestPhase = "awaiting_phone"
	// phaseOTP — SMS code sent, waiting for the 6-digit entry.
	phaseOTP guestPhase = "awaiting_otp"
	// phaseConsent — phone verified, waiting for terms consent.
	phaseConsent guestPhase = "awaiting_consent"
	// phaseEmail — existing-account path, waiting for the account email.
	phaseEmail guestPhase = "awaiting_email"
	// phaseEmailOTP — existing-account path, email code sent.
	phaseEmailOTP guestPhase = "awaiting_email_otp"
)

// guestState is the per-sender pre-signup conversation persisted in Redis.
type guestState struct {
	Phase              guestPhase     `json:"phase"`
	FirstName          string         `json:"first_name,omitempty"`
	Country            string         `json:"country,omitempty"`
	Goal               string         `json:"goal,omitempty"`
	MoneyType          string         `json:"money_type,omitempty"`
	Email              string         `json:"email,omitempty"`
	Phone              string         `json:"phone,omitempty"`
	Turns              []GuestMessage `json:"turns,omitempty"`
	TurnCount          int            `json:"turn_count,omitempty"`
	LastReplyHash      uint64         `json:"last_reply_hash,omitempty"`
	OTPAttempts        int            `json:"otp_attempts,omitempty"`
	EmailOTPAttempts   int            `json:"email_otp_attempts,omitempty"`
	EmailVerified      bool           `json:"email_verified,omitempty"`
	UserID             string         `json:"user_id,omitempty"`
	SignupReason       string         `json:"signup_reason,omitempty"`
	IntroSent          bool           `json:"intro_sent,omitempty"`
	StatementSummary   string         `json:"statement_summary,omitempty"`
	PendingStatementID string         `json:"pending_statement_id,omitempty"`
}

// OnboardInput is a normalized inbound message from an unlinked sender.
type OnboardInput struct {
	Platform  entities.Platform
	SenderID  string
	ThreadID  string
	Text      string
	Contact   *SharedContact
	Statement *StatementAttachment
}

// ChatOnboarder hosts the pre-signup conversation: Miriam talks first (agent-led
// via the guest brain) and collects phone + OTP + consent only when the guest
// wants something that needs an account. Without a completer it preserves
// state and reports the temporary limitation instead of replaying a script.
type ChatOnboarder struct {
	store            OnboardingStateStore
	verifier         OnboardingOTPVerifier
	users            OnboardingUserStore
	provisioner      OnboardingProvisioner
	linker           OnboardingLinker
	appURL           string
	logger           *zap.Logger
	babySteps        BabyStepsSeeder
	brain            *guestBrain
	moneyTypes       GuestMoneyTypeWriter
	transcripts      GuestTranscriptWriter
	statementHandler StatementAttachmentHandler
}

func NewChatOnboarder(
	store OnboardingStateStore,
	verifier OnboardingOTPVerifier,
	users OnboardingUserStore,
	provisioner OnboardingProvisioner,
	linker OnboardingLinker,
	appDownloadURL string,
	logger *zap.Logger,
) *ChatOnboarder {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ChatOnboarder{
		store:       store,
		verifier:    verifier,
		users:       users,
		provisioner: provisioner,
		linker:      linker,
		appURL:      strings.TrimSpace(appDownloadURL),
		logger:      logger,
	}
}

// SetGuestCompleter enables the agent-led guest conversation. When unset, the
// onboarder preserves state and asks the sender to retry instead of emitting a
// scripted onboarding sequence.
func (c *ChatOnboarder) SetGuestCompleter(completer GuestCompleter) {
	if completer == nil {
		return
	}
	c.brain = newGuestBrain(completer, c.logger)
}

// SetGuestHandoff installs the post-signup writers: the guest's money-type read
// and the conversation transcript. Both are fired best-effort at provisioning.
func (c *ChatOnboarder) SetGuestHandoff(moneyTypes GuestMoneyTypeWriter, transcripts GuestTranscriptWriter) {
	c.moneyTypes = moneyTypes
	c.transcripts = transcripts
}

// SetStatementAttachmentHandler enables statement scanning for unlinked
// senders and hands pending documents to the durable pipeline after signup.
func (c *ChatOnboarder) SetStatementAttachmentHandler(handler StatementAttachmentHandler) {
	c.statementHandler = handler
}

// SetBabyStepsSeeder installs the first-login goal seeder. After a successful
// chat-first onboarding, the seeder materializes the 7-step Baby Steps ladder
// for the new user so the goal_progress worker has something to track on the
// next tick. Nil-safe.
func (c *ChatOnboarder) SetBabyStepsSeeder(s BabyStepsSeeder) {
	c.babySteps = s
}

func onboardingKey(platform entities.Platform, senderID string) string {
	return fmt.Sprintf("onboarding:%s:%s", platform, senderID)
}

func turnLockKey(platform entities.Platform, senderID string) string {
	return fmt.Sprintf("onboarding:turn:%s:%s", platform, senderID)
}

func dailyTurnsKey(platform entities.Platform, senderID string) string {
	return fmt.Sprintf("onboarding:daily:%s:%s", platform, senderID)
}

// HasSession reports whether an onboarding conversation is already in progress
// for this sender.
func (c *ChatOnboarder) HasSession(ctx context.Context, platform entities.Platform, senderID string) bool {
	ok, err := c.store.Exists(ctx, onboardingKey(platform, senderID))
	if err != nil {
		c.logger.Warn("onboarding session lookup failed", zap.Error(err))
		return false
	}
	return ok
}

// ClearSession removes an onboarding conversation for a sender. Used by the
// processor when a handshake token completes a link mid-onboarding.
func (c *ChatOnboarder) ClearSession(ctx context.Context, platform entities.Platform, senderID string) error {
	if err := c.store.Del(ctx, onboardingKey(platform, senderID)); err != nil {
		c.logger.Warn("onboarding session clear failed", zap.Error(err))
		return Retryable(fmt.Errorf("clear onboarding session: %w", err))
	}
	return nil
}

// clear removes the onboarding session for a sender, logging (never failing on)
// store errors so an abandoned session is a recoverable annoyance, not a flow break.
func (c *ChatOnboarder) clear(ctx context.Context, key string) {
	if err := c.store.Del(ctx, key); err != nil {
		c.logger.Warn("onboarding session clear failed", zap.Error(err))
	}
}

// Handle advances the guest conversation by one turn and returns the reply to
// send back. A nil reply means nothing should be sent.
func (c *ChatOnboarder) Handle(ctx context.Context, in OnboardInput) (*PlatformReply, error) {
	key := onboardingKey(in.Platform, in.SenderID)

	// Serialize turns per sender: anything that slips past the bridge debounce
	// cannot interleave two state writes. Contention is transient, so requeue.
	locked, err := c.store.SetNX(ctx, turnLockKey(in.Platform, in.SenderID), 1, turnLockTTL)
	if err != nil {
		c.logger.Warn("onboarding turn lock failed", zap.Error(err))
		return nil, Retryable(fmt.Errorf("acquire turn lock: %w", err))
	}
	if !locked {
		return nil, Retryable(fmt.Errorf("guest turn already in flight"))
	}
	// Lock acquired — ensure it's released on every exit path.
	defer func() {
		_ = c.store.Del(ctx, turnLockKey(in.Platform, in.SenderID))
	}()

	var st guestState
	if err := c.store.Get(ctx, key, &st); err != nil || st.Phase == "" {
		st = guestState{Phase: phaseConverse}
	}

	if in.Contact != nil {
		c.mergeContact(&st, in.Contact)
	}

	text := strings.TrimSpace(in.Text)
	if in.Statement != nil && c.statementHandler != nil {
		scan, scanErr := c.statementHandler.ScanGuest(ctx, in.SenderID, *in.Statement)
		if scanErr != nil {
			c.logger.Warn("guest statement scan failed", zap.Error(scanErr))
			return textReply("I couldn't read that statement just now. Give me a moment and send it again?"), nil
		}
		if scan != nil {
			st.StatementSummary = truncate(scan.Summary, 4000)
			st.PendingStatementID = scan.PendingID
			if text == "" {
				text = "[they shared a bank statement]\n" + st.StatementSummary
			} else {
				text = text + "\n[statement scan]\n" + st.StatementSummary
			}
		}
	}
	if in.Contact != nil && text == "" {
		// Give the model (or the fallback) something to react to.
		text = "[they shared their contact card]"
	}

	var reply *PlatformReply
	switch st.Phase {
	case phaseOTP:
		reply, err = c.handleOTP(ctx, key, &st, text)
	case phaseConsent:
		reply, err = c.handleConsent(ctx, key, &st, in, text)
	case phaseEmailOTP:
		reply, err = c.handleEmailOTP(ctx, key, &st, text)
	case phasePhone, phaseEmail, phaseConverse:
		reply, err = c.handleConversational(ctx, key, &st, in, text)
	default:
		reply, err = c.handleConversational(ctx, key, &st, in, text)
	}
	if err != nil {
		return nil, err
	}
	return reply, nil
}

// handleConversational covers every phase where the next move depends on what
// the person said rather than on a code entry: the open conversation, the
// phone ask, and the existing-account email ask.
func (c *ChatOnboarder) handleConversational(ctx context.Context, key string, st *guestState, in OnboardInput, text string) (*PlatformReply, error) {
	// A bare email address in any of these phases is the existing-account path.
	if st.Phase != phaseEmail {
		if email := normalizeEmail(text); email != "" && strings.Contains(text, "@") && len(strings.Fields(text)) <= 2 {
			return c.handleEmail(ctx, key, st, email)
		}
	}

	if st.Phase == phasePhone {
		if phone, ok := extractPhoneFromText(text, st.Country); ok {
			st.Phone = phone
			if st.Country == "" {
				st.Country = inferCountryFromPhone(phone)
			}
			return c.startVerification(ctx, key, st)
		}
	}

	if st.Phase == phaseEmail {
		email := normalizeEmail(text)
		if email == "" {
			return textReply("What's the email on your RAIL account?"), nil
		}
		return c.handleEmail(ctx, key, st, email)
	}

	// Daily abuse cap — checked before spending a model turn.
	if c.brain != nil {
		if over, err := c.overDailyCap(ctx, in); err != nil {
			c.logger.Warn("guest daily cap check failed", zap.Error(err))
		} else if over {
			return textReply("I've hit my chat limit for today. Text me tomorrow and we'll pick right back up."), nil
		}
	}

	if c.brain != nil {
		return c.brainTurn(ctx, key, st, in, text)
	}
	return c.fallbackTurn(ctx, key, st, in, text)
}

// brainTurn runs one agent-led turn: the model writes the reply, the executor
// applies the tool effects.
func (c *ChatOnboarder) brainTurn(ctx context.Context, key string, st *guestState, in OnboardInput, text string) (*PlatformReply, error) {
	if st.TurnCount >= maxGuestTurns {
		// Enough talking without converting — steer to the one useful action.
		st.Phase = phasePhone
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply("I've enjoyed this, but talking only gets us so far. Drop your number (with the country code) and I'll actually get your money working."), nil
	}

	out, err := c.brain.respond(ctx, st, text)
	if err != nil {
		c.logger.Warn("guest brain turn failed; preserving state for retry", zap.Error(err))
		return c.fallbackTurn(ctx, key, st, in, text)
	}

	for _, n := range out.notes {
		c.applyNote(st, n)
	}

	replyText := out.text

	switch {
	case out.end:
		c.recordTurn(st, text, replyText)
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		// Leave a tombstone-free exit: clear so a future text starts warm-fresh.
		c.clear(ctx, key)
		return textReply(replyText), nil

	case out.startSignup:
		st.SignupReason = out.signupReason
		return c.beginSignup(ctx, key, st, text, replyText)
	}

	// Plain conversational turn.
	if hash := hashReply(replyText); hash == st.LastReplyHash && st.LastReplyHash != 0 {
		if alt, rerr := c.brain.regenerateDifferent(ctx, st, text); rerr == nil && alt != "" && hashReply(alt) != st.LastReplyHash {
			replyText = alt
		} else {
			replyText = variedNudge(st)
		}
	}

	c.recordTurn(st, text, replyText)
	st.LastReplyHash = hashReply(replyText)
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}

	reply := &PlatformReply{Text: replyText}
	if out.poll != nil {
		reply.Poll = out.poll
	}
	return reply, nil
}

// beginSignup transitions the conversation into identity verification. OTP and
// consent copy stay deterministic — compliance text is not generated.
func (c *ChatOnboarder) beginSignup(ctx context.Context, key string, st *guestState, userText, replyText string) (*PlatformReply, error) {
	if st.Phone != "" || (st.Email != "" && !st.EmailVerified) {
		c.recordTurn(st, userText, replyText)
		return c.startVerification(ctx, key, st)
	}

	st.Phase = phasePhone
	c.recordTurn(st, userText, replyText)
	st.LastReplyHash = hashReply(replyText)
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	if strings.TrimSpace(replyText) == "" {
		replyText = c.phonePrompt()
	}
	return textReply(replyText), nil
}

// startVerification picks the next proof step. An unverified email that matches
// an existing account must be proven by email OTP before anything links or is
// created — a shared contact card alone can never substitute for ownership.
// Otherwise a phone on file gets the SMS code; without one we ask.
func (c *ChatOnboarder) startVerification(ctx context.Context, key string, st *guestState) (*PlatformReply, error) {
	if st.Email != "" && !st.EmailVerified {
		if existing, err := c.users.GetByEmail(ctx, st.Email); err == nil && existing != nil {
			if !existing.IsActive {
				c.clear(ctx, key)
				return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
			}
			return c.sendEmailOTP(ctx, key, st, existing)
		}
	}
	if st.Phone != "" {
		return c.sendPhoneOTP(ctx, key, st)
	}
	st.Phase = phasePhone
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(c.phonePrompt()), nil
}

// recordTurn appends the exchange to the bounded transcript and ticks the
// counters.
func (c *ChatOnboarder) recordTurn(st *guestState, userText, replyText string) {
	if strings.TrimSpace(userText) != "" {
		st.Turns = append(st.Turns, GuestMessage{Role: "user", Content: truncate(userText, 500)})
	}
	if strings.TrimSpace(replyText) != "" {
		st.Turns = append(st.Turns, GuestMessage{Role: "assistant", Content: truncate(replyText, 500)})
	}
	if len(st.Turns) > maxTranscriptTurns {
		st.Turns = st.Turns[len(st.Turns)-maxTranscriptTurns:]
	}
	st.TurnCount++
}

// applyNote validates and stores one detail the model extracted.
func (c *ChatOnboarder) applyNote(st *guestState, n guestNote) {
	switch n.field {
	case "first_name":
		if name := parseFirstName(n.value); name != "" && !isGreeting(name) {
			st.FirstName = name
		}
	case "country":
		if cc := normalizeCountry(n.value); cc != "" {
			st.Country = cc
		}
	case "goal":
		if g := truncate(strings.TrimSpace(n.value), 200); g != "" {
			st.Goal = g
		}
	case "money_type":
		switch strings.ToLower(strings.TrimSpace(n.value)) {
		case "avoider", "optimizer", "worrier", "dreamer":
			st.MoneyType = strings.ToLower(strings.TrimSpace(n.value))
		}
	case "email":
		if st.Email == "" {
			st.Email = normalizeEmail(n.value)
		}
	}
}

func (c *ChatOnboarder) overDailyCap(ctx context.Context, in OnboardInput) (bool, error) {
	key := dailyTurnsKey(in.Platform, in.SenderID)
	var n int
	if err := c.store.Get(ctx, key, &n); err == nil && n >= maxGuestDailyTurns {
		return true, nil
	}
	if err := c.store.Set(ctx, key, n+1, 24*time.Hour); err != nil {
		return false, err
	}
	return false, nil
}

// fallbackTurn is the no-LLM path. Identity verification remains deterministic,
// but ordinary conversation must not fall back to scripted onboarding copy.
func (c *ChatOnboarder) fallbackTurn(ctx context.Context, key string, st *guestState, in OnboardInput, text string) (*PlatformReply, error) {
	// A phone number is a phone number, whenever it arrives — even as the very
	// first message.
	if st.Phone == "" && st.Phase != phaseOTP {
		if phone, ok := extractPhoneFromText(text, st.Country); ok {
			st.Phone = phone
			if st.Country == "" {
				st.Country = inferCountryFromPhone(phone)
			}
			st.IntroSent = true
			return c.startVerification(ctx, key, st)
		}
	}

	// A contact card answers the questions it answers; never re-ask them.
	if in.Contact != nil {
		st.IntroSent = true
		if st.Phone != "" {
			return c.startVerification(ctx, key, st)
		}
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		if st.FirstName != "" {
			return textReply(fmt.Sprintf("Got it, %s. %s", st.FirstName, c.phonePrompt())), nil
		}
		return textReply("Whose card is that? Tell me your first name, and " + c.phonePrompt()), nil
	}

	// "I already have an account" routes to email-ownership proof.
	if st.Phase == phaseConverse && looksLikeExistingAccount(text) {
		st.Phase = phaseEmail
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply("What's the email on your RAIL account?"), nil
	}

	// Keep obvious identity details useful while the model is unavailable, but
	// do not replay the old scripted introduction.
	if st.FirstName == "" {
		if !isGreeting(text) {
			name := parseNameFromText(text)
			if name != "" && !isGreeting(name) {
				st.FirstName = name
				if st.Country == "" {
					st.Country = countryFromText(text)
				}
				if err := c.save(ctx, key, *st); err != nil {
					return nil, err
				}
				return textReply(fmt.Sprintf("Got it, %s. I'm still reconnecting. If you want to continue setup, send your number with the country code.", name)), nil
			}
		} else {
			// Greeting or empty input without a name yet — ask for it.
			if err := c.save(ctx, key, *st); err != nil {
				return nil, err
			}
			return textReply("Hey! What should I call you?"), nil
		}
	}

	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply("I'm having trouble with my conversation engine right now. Give me a moment and send that again?"), nil
}

// countryFromText finds a country mention inside a longer message ("Ada from
// Nigeria") so the fallback flow doesn't need a dedicated country step.
func countryFromText(text string) string {
	for _, word := range strings.Fields(strings.ToLower(text)) {
		if cc, ok := countryAliases[strings.Trim(word, ".,!?")]; ok {
			return cc
		}
	}
	return ""
}

// --- Identity verification (deterministic; never model-generated) ---

// handleEmail starts or continues the existing-account path: the address is
// looked up, and a live account gets an email OTP to prove ownership.
func (c *ChatOnboarder) handleEmail(ctx context.Context, key string, st *guestState, email string) (*PlatformReply, error) {
	st.Email = email

	existing, err := c.users.GetByEmail(ctx, email)
	if err != nil || existing == nil {
		// No account under that email — keep it for creation and move on.
		reply, rerr := c.startVerification(ctx, key, st)
		if rerr != nil || st.Phone != "" {
			return reply, rerr
		}
		return textReply("No account under that email, so we'll start fresh. " + c.phonePrompt()), nil
	}
	if !existing.IsActive {
		c.clear(ctx, key)
		return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
	}
	return c.sendEmailOTP(ctx, key, st, existing)
}

// sendEmailOTP verifies the account is still live, then sends the ownership
// code synchronously so a provider failure reaches the user instead of dying
// in a background worker after we already claimed success.
func (c *ChatOnboarder) sendEmailOTP(ctx context.Context, key string, st *guestState, existing *entities.UserProfile) (*PlatformReply, error) {
	code, simulated, err := c.verifier.GenerateAndSendCodeSync(ctx, "email", st.Email)
	if err != nil {
		c.logger.Warn("onboarding email OTP send failed", zap.Error(err))
		return textReply(otpSendErrorMessage(err)), nil
	}
	// Re-verify the account still exists and is active now that the OTP is in
	// flight, so a deleted or deactivated account can never receive a code that
	// links a chat to a stale row.
	recheck, rerr := c.users.GetByEmail(ctx, st.Email)
	if rerr != nil || recheck == nil || !recheck.IsActive {
		c.clear(ctx, key)
		return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
	}
	st.Phase = phaseEmailOTP
	st.EmailOTPAttempts = 0
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	if simulated {
		return textReply(fmt.Sprintf("No email provider is configured here, so nothing was actually sent. Test code: %s.", code)), nil
	}
	return textReply(fmt.Sprintf("I found your RAIL account. I just emailed a 6-digit code to %s. Reply with it here to confirm it's you.", st.Email)), nil
}

func (c *ChatOnboarder) handleEmailOTP(ctx context.Context, key string, st *guestState, text string) (*PlatformReply, error) {
	code := digitsOnly(text)
	if len(code) != 6 {
		return textReply("That doesn't look like the 6-digit code. Reply with the code I emailed you."), nil
	}
	ok, err := c.verifier.VerifyCode(ctx, "email", st.Email, code)
	if err != nil || !ok {
		st.EmailOTPAttempts++
		if st.EmailOTPAttempts >= maxEmailOTPAttempts {
			c.clear(ctx, key)
			return textReply("That code didn't match too many times. For security, please link this chat from the RAIL app instead."), nil
		}
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply("That code didn't match. Double-check and try again."), nil
	}
	st.EmailVerified = true
	existing, err := c.users.GetByEmail(ctx, st.Email)
	if err != nil || existing == nil {
		c.logger.Error("verified email account disappeared during onboarding", zap.Error(err))
		c.clear(ctx, key)
		return textReply("Something went wrong on my end. Please try again in a moment."), nil
	}
	if !existing.IsActive {
		c.clear(ctx, key)
		return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
	}
	st.UserID = existing.ID.String()
	if st.Phone != "" {
		return c.sendPhoneOTP(ctx, key, st)
	}
	st.Phase = phasePhone
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply("Email confirmed. " + c.phonePrompt()), nil
}

// looksLikeExistingAccount catches the "I already have an account" intent so
// the fallback flow can route to the email-ownership path.
func looksLikeExistingAccount(text string) bool {
	s := strings.ToLower(text)
	return strings.Contains(s, "already have") && (strings.Contains(s, "account") || strings.Contains(s, "rail")) ||
		strings.Contains(s, "have an account") || strings.Contains(s, "i'm registered") ||
		strings.Contains(s, "i am registered") || strings.Contains(s, "signed up before") ||
		strings.Contains(s, "existing account")
}

func (c *ChatOnboarder) sendPhoneOTP(ctx context.Context, key string, st *guestState) (*PlatformReply, error) {
	code, simulated, err := c.verifier.GenerateAndSendCodeSync(ctx, "phone", st.Phone)
	if err != nil {
		c.logger.Warn("onboarding OTP send failed", zap.Error(err))
		return textReply(otpSendErrorMessage(err)), nil
	}
	st.Phase = phaseOTP
	st.OTPAttempts = 0
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	if simulated {
		return textReply(fmt.Sprintf("No SMS provider is configured here, so nothing was actually texted. Test code: %s.", code)), nil
	}
	return textReply(fmt.Sprintf("Just texted a code to %s. Drop it here.", maskPhone(st.Phone))), nil
}

func (c *ChatOnboarder) handleOTP(ctx context.Context, key string, st *guestState, text string) (*PlatformReply, error) {
	// "That's not me" after a contact-card code send: drop the card details and
	// ask whose number to use instead.
	if isContactReject(text) {
		st.Phone = ""
		st.Email = ""
		st.EmailVerified = false
		st.Phase = phasePhone
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply("Got it. Whose number should I text the code to? Include the country code, like +2348012345678."), nil
	}
	code := digitsOnly(text)
	if len(code) != 6 {
		return textReply("That doesn't look like the 6-digit code. Reply with the code I texted you."), nil
	}
	ok, err := c.verifier.VerifyCode(ctx, "phone", st.Phone, code)
	if err != nil || !ok {
		st.OTPAttempts++
		if st.OTPAttempts >= maxOnboardingOTPAttempts {
			c.clear(ctx, key)
			return textReply("That code didn't match too many times. Text me again when you're ready and we'll start over."), nil
		}
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply("That code didn't match. Double-check and try again."), nil
	}

	if err := c.ensureUser(ctx, st); err != nil {
		c.logger.Error("onboarding user creation failed", zap.Error(err))
		if strings.Contains(err.Error(), "inactive") {
			c.clear(ctx, key)
			return textReply("That phone number belongs to an inactive account. Please reach out to support@userail.money for help."), nil
		}
		return textReply("Something went wrong setting up your account. Mind trying that code again in a moment?"), nil
	}
	st.Phase = phaseConsent
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return c.consentReply(), nil
}

func (c *ChatOnboarder) handleConsent(ctx context.Context, key string, st *guestState, in OnboardInput, text string) (*PlatformReply, error) {
	if !isAffirmative(text) {
		// A question or hesitation instead of consent: answer it with the model
		// when available (the state block tells it to invite, not pressure).
		// Without the model, re-send the consent poll.
		if c.brain != nil && strings.TrimSpace(text) != "" {
			out, err := c.brain.respond(ctx, st, text)
			if err == nil && strings.TrimSpace(out.text) != "" {
				for _, n := range out.notes {
					c.applyNote(st, n)
				}
				c.recordTurn(st, text, out.text)
				if err := c.save(ctx, key, *st); err != nil {
					return nil, err
				}
				return textReply(out.text), nil
			}
		}
		return c.consentReply(), nil
	}
	uid, err := uuid.Parse(st.UserID)
	if err != nil {
		c.logger.Error("onboarding consent with unparseable user id", zap.String("user_id", st.UserID), zap.Error(err))
		return textReply("Something went wrong on my end. Text me again and we'll pick this back up."), nil
	}
	if err := c.provisioner.ProvisionPhoneFirstUser(ctx, uid, st.FirstName, st.Country, st.Phone); err != nil {
		c.logger.Error("phone-first provisioning failed", zap.Error(err), zap.String("user_id", st.UserID))
		return textReply("I couldn't finish setting up your account just now. Tap I agree to try again."), nil
	}
	identity, err := c.linker.LinkVerified(ctx, uid, in.Platform, in.SenderID)
	if err != nil {
		c.logger.Error("phone-first auto-link failed", zap.Error(err), zap.String("user_id", st.UserID))
		return textReply("I couldn't finish linking this chat just now. Tap I agree to try again."), nil
	}
	// Fire the first-login goal seeder for the new user so the goal_progress
	// worker has a 7-step ladder to track on its next tick. Async + recover
	// so a failure here can't fail the onboarding completion.
	SeedBabyStepsOnLink(c.babySteps, uid, c.logger)
	c.fireGuestHandoff(uid, identity, in, st)
	c.clear(ctx, key)
	return &PlatformReply{
		Text:   c.completionMessage(st),
		Effect: EffectCelebration,
	}, nil
}

// fireGuestHandoff carries the guest conversation into the authenticated
// relationship: money-type read to the tone profile, transcript to the first
// platform conversation. Both async best-effort with bounded contexts.
func (c *ChatOnboarder) fireGuestHandoff(uid uuid.UUID, identity *entities.PlatformIdentity, in OnboardInput, st *guestState) {
	if c.moneyTypes != nil && st.MoneyType != "" {
		moneyType := st.MoneyType
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.moneyTypes.SetMoneyType(ctx, uid, moneyType); err != nil {
				c.logger.Warn("money type handoff failed", zap.Stringer("user_id", uid), zap.Error(err))
			}
		}()
	}
	if c.transcripts != nil && identity != nil && len(st.Turns) > 0 {
		turns := make([]GuestMessage, len(st.Turns))
		copy(turns, st.Turns)
		threadID := in.ThreadID
		plat := in.Platform
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := c.transcripts.AppendGuestTranscript(ctx, uid, identity, threadID, turns); err != nil {
				c.logger.Warn("guest transcript handoff failed",
					zap.Stringer("user_id", uid), zap.String("platform", plat.String()), zap.Error(err))
			}
		}()
	}
	if c.statementHandler != nil && st.PendingStatementID != "" {
		pendingID := st.PendingStatementID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := c.statementHandler.CompletePending(ctx, uid, pendingID); err != nil {
				c.logger.Warn("pending guest statement handoff failed",
					zap.Stringer("user_id", uid), zap.String("pending_id", pendingID), zap.Error(err))
			}
		}()
	}
}

// ensureUser finds an existing user by the verified email (already proven) or
// verified phone, or creates a new passwordless one. Idempotent across retries
// via the stored user id.
func (c *ChatOnboarder) ensureUser(ctx context.Context, st *guestState) error {
	if st.UserID != "" {
		return nil
	}

	// Email was already verified by OTP for an existing account.
	if st.EmailVerified && st.Email != "" {
		if existing, err := c.users.GetByEmail(ctx, st.Email); err == nil && existing != nil && existing.IsActive {
			st.UserID = existing.ID.String()
			return nil
		}
	}

	// Phone was just verified by SMS OTP; it may match an existing phone-first
	// account (e.g. user signed up with phone elsewhere).
	if existing, err := c.users.GetByPhone(ctx, st.Phone); err == nil && existing != nil {
		if !existing.IsActive {
			return fmt.Errorf("phone belongs to an inactive account")
		}
		st.UserID = existing.ID.String()
		return nil
	}

	// New account. Use the collected email so we don't create a second
	// phone-first user with a blank email (which violates the unique email
	// constraint once two such users exist).
	email := st.Email
	if email == "" {
		email = placeholderEmail()
	}
	phone := st.Phone
	user, err := c.users.CreateUserWithHash(ctx, email, &phone, "")
	if err != nil {
		// Lost a race or a stale duplicate — try to recover the existing row.
		if existing, gerr := c.users.GetByEmail(ctx, email); gerr == nil && existing != nil && existing.IsActive {
			st.UserID = existing.ID.String()
			return nil
		}
		if existing, gerr := c.users.GetByPhone(ctx, st.Phone); gerr == nil && existing != nil && existing.IsActive {
			st.UserID = existing.ID.String()
			return nil
		}
		return fmt.Errorf("create user: %w", err)
	}
	st.UserID = user.ID.String()
	return nil
}

func (c *ChatOnboarder) mergeContact(st *guestState, contact *SharedContact) {
	if st == nil || contact == nil {
		return
	}
	if st.FirstName == "" {
		if n := contact.FirstNameResolved(); n != "" && !isGreeting(n) {
			st.FirstName = n
		}
	}
	if st.Email == "" {
		st.Email = contact.PrimaryEmail()
	}
	if st.Country == "" {
		if cc := normalizeCountry(contact.Country); cc != "" {
			st.Country = cc
		}
	}
	if st.Phone == "" {
		raw := contact.PrimaryPhone()
		if st.Country == "" {
			if inferred := inferCountryFromPhone(raw); inferred != "" {
				st.Country = inferred
			}
		}
		if phone, ok := normalizePhone(raw, st.Country); ok {
			st.Phone = phone
			if st.Country == "" {
				st.Country = inferCountryFromPhone(phone)
			}
		}
	}
}

func (c *ChatOnboarder) save(ctx context.Context, key string, st guestState) error {
	if err := c.store.Set(ctx, key, st, guestSessionTTL); err != nil {
		c.logger.Warn("onboarding state save failed", zap.Error(err))
		return Retryable(fmt.Errorf("save onboarding state: %w", err))
	}
	return nil
}

// --- Copy ---

func (c *ChatOnboarder) phonePrompt() string {
	return "What's the best number for a quick code? Include the country code, like +2348012345678."
}

func (c *ChatOnboarder) consentMessage() string {
	return "Last thing: RAIL's terms and privacy policy. Tap I agree and I'll finish setting you up."
}

func (c *ChatOnboarder) consentReply() *PlatformReply {
	title := c.consentMessage()
	return &PlatformReply{
		Text: title,
		Poll: &PollRequest{Title: title, Options: []string{"I agree", "Not yet"}},
	}
}

func (c *ChatOnboarder) completionMessage(st *guestState) string {
	who := "You're in"
	if strings.TrimSpace(st.FirstName) != "" {
		who = fmt.Sprintf("You're in, %s", st.FirstName)
	}

	countryLine := "Wallet's spinning up."
	switch strings.ToUpper(strings.TrimSpace(st.Country)) {
	case "NG":
		countryLine = "Wallet's spinning up. I'll keep it in stable dollars until you need naira."
	case "GH":
		countryLine = "Wallet's spinning up. I'll keep it in stable dollars until you need cedis."
	case "KE":
		countryLine = "Wallet's spinning up. I'll keep it in stable dollars until you need shillings."
	}

	next := "What's money actually for, for you, right now? A trip, breathing room, something you want, anything."
	if st.Goal != "" {
		next = fmt.Sprintf("That goal you mentioned (%s) starts with the first deposit. Want me to walk you through funding it?", truncate(st.Goal, 80))
	}

	return fmt.Sprintf("%s. %s\n\n%s", who, countryLine, next)
}

// variedNudge is the last-resort reply when the model repeats itself twice.
// Verbatim repetition is the worst tell that you're talking to a script.
func variedNudge(st *guestState) string {
	variants := []string{
		"Okay, my turn to ask better. What would you want your money to do for you this year?",
		"Let me come at it differently. If your money handled one thing for you, what would it be?",
		"New angle: what's the money thing you keep putting off?",
	}
	if st.FirstName != "" {
		variants[0] = fmt.Sprintf("Okay %s, my turn to ask better. What would you want your money to do for you this year?", st.FirstName)
	}
	return variants[st.TurnCount%len(variants)]
}

func textReply(s string) *PlatformReply {
	return &PlatformReply{Text: s}
}

// --- Parsing helpers ---

var (
	nonDigits     = regexp.MustCompile(`\D`)
	e164Pattern   = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
	nameCleanRe   = regexp.MustCompile(`[^\p{L}'-]`)
	lettersOnlyRe = regexp.MustCompile(`[^\p{L}]`)
	phoneInTextRe = regexp.MustCompile(`\+?[\d][\d\s().-]{6,}\d`)
)

// greetings can never be a name. "Nice to meet you, Hi" was a real bug.
var greetings = map[string]bool{
	"hi": true, "hey": true, "hello": true, "yo": true, "sup": true,
	"hiya": true, "howdy": true, "heyy": true, "heyyy": true, "hii": true,
	"good morning": true, "good afternoon": true, "good evening": true,
	"morning": true, "afternoon": true, "evening": true,
	"hi miriam": true, "hey miriam": true, "hello miriam": true,
	"start": true, "test": true, "testing": true,
}

func isGreeting(text string) bool {
	s := strings.ToLower(strings.TrimSpace(text))
	s = strings.Trim(s, ".!👋")
	return greetings[s]
}

func normalizeEmail(input string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	s = strings.TrimPrefix(s, "mailto:")
	if s == "" || s == "skip" {
		return ""
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return ""
	}
	return addr.Address
}

// placeholderEmail generates an opaque, unique placeholder email for a
// phone-first user who skipped the email step. users.email is NOT NULL UNIQUE,
// so a blank value would collide across phone-first users. It deliberately
// contains no PII (nothing phone-derived), uses the reserved .invalid TLD so no
// mail system attempts delivery, and is unique per creation; retry recovery
// finds the row again via the GetByPhone lookup in ensureUser, not by email.
func placeholderEmail() string {
	return "phone+" + uuid.NewString() + "@placeholder.invalid"
}

// countryDialCodes maps a stored ISO alpha-2 country to its E.164 dial prefix,
// used to complete locally-formatted numbers.
var countryDialCodes = map[string]string{
	"NG": "234",
	"US": "1",
	"GB": "44",
	"GH": "233",
	"KE": "254",
	"ZA": "27",
	"CA": "1",
}

// countryAliases maps common country names/codes to ISO alpha-2.
var countryAliases = map[string]string{
	"nigeria": "NG", "ng": "NG", "nga": "NG", "naija": "NG",
	"united states": "US", "usa": "US", "us": "US", "america": "US", "united states of america": "US",
	"united kingdom": "GB", "uk": "GB", "gb": "GB", "britain": "GB", "great britain": "GB", "england": "GB",
	"ghana": "GH", "gh": "GH",
	"kenya": "KE", "ke": "KE",
	"south africa": "ZA", "za": "ZA",
	"canada": "CA", "ca": "CA",
}

var nameIntroRe = regexp.MustCompile(`(?i)^(?:my name is|i am|i'm|im|it's|its|call me|name'?s)\s+(\S+)`)

// parseNameFromText extracts a first name from natural phrasing ("my name is
// Oluwatobiloba", "it's Ada") as well as a bare name ("Ada", "Ada Lovelace").
func parseNameFromText(text string) string {
	if m := nameIntroRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		return parseFirstName(m[1])
	}
	if len(strings.Fields(text)) <= 3 {
		return parseFirstName(text)
	}
	return ""
}

func parseFirstName(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	name := nameCleanRe.ReplaceAllString(fields[0], "")
	if name == "" {
		return ""
	}
	if len(name) > 40 {
		name = name[:40]
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func normalizeCountry(input string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return ""
	}
	if code, ok := countryAliases[s]; ok {
		return code
	}
	letters := lettersOnlyRe.ReplaceAllString(s, "")
	if len(letters) == 2 {
		return strings.ToUpper(letters)
	}
	return ""
}

func digitsOnly(text string) string {
	return nonDigits.ReplaceAllString(text, "")
}

// extractPhoneFromText pulls a phone number out of a sentence ("it's
// +2349164904178", "my number is 0803 123 4567"). Requires at least 9 digits
// so a stray 6-digit OTP can never be mistaken for a number.
func extractPhoneFromText(text, country string) (string, bool) {
	for _, candidate := range phoneInTextRe.FindAllString(text, -1) {
		if len(digitsOnly(candidate)) < 9 {
			continue
		}
		if phone, ok := normalizePhone(candidate, country); ok {
			return phone, true
		}
	}
	return "", false
}

// normalizePhone coerces user input into E.164, using the collected country to
// complete locally-formatted numbers. Returns false when it can't produce a
// plausible E.164 number.
func normalizePhone(input, country string) (string, bool) {
	raw := strings.TrimSpace(input)
	hasPlus := strings.HasPrefix(raw, "+")
	digits := digitsOnly(raw)
	if digits == "" {
		return "", false
	}

	if hasPlus {
		candidate := "+" + digits
		if e164Pattern.MatchString(candidate) {
			return candidate, true
		}
		return "", false
	}

	dial := countryDialCodes[strings.ToUpper(strings.TrimSpace(country))]
	if dial == "" {
		return "", false
	}
	// Local trunk-prefixed numbers start with 0; drop it before prefixing.
	local := strings.TrimPrefix(digits, "0")
	candidate := "+" + dial + local
	if e164Pattern.MatchString(candidate) {
		return candidate, true
	}
	return "", false
}

func isAffirmative(text string) bool {
	s := strings.ToLower(strings.TrimSpace(text))
	s = strings.Trim(s, ".!")
	switch s {
	case "yes", "y", "yeah", "yep", "yup", "sure", "ok", "okay", "agree", "i agree", "yes i agree", "confirm", "accept", "i accept", "sound right", "that's me", "thats me", "it's me", "its me":
		return true
	}
	return strings.HasPrefix(s, "yes")
}

func otpSendErrorMessage(err error) string {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "too many") {
		return "You've asked for a few codes already. Give it a minute, then text me to try again."
	}
	return "I couldn't send the code just now. Mind trying again in a moment?"
}

func hashReply(text string) uint64 {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	h := fnv.New64a()
	_, _ = h.Write([]byte(normalized))
	return h.Sum64()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
