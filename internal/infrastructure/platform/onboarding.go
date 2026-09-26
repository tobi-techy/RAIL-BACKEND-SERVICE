package platform

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/mail"
	"net/url"
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
	// UpdateEmail attaches a verified email to an existing account (the
	// phone-first case: the row was created with an opaque placeholder because
	// no email had been collected yet). Satisfied by the user repository.
	UpdateEmail(ctx context.Context, userID uuid.UUID, email string) error
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

// GuestMoneyTypeWriter persists the agent's read of the guest's money style and
// money dials so the authenticated Miriam calibrates tone from the first turn.
// Satisfied by the Miriam memory repository. Optional.
type GuestMoneyTypeWriter interface {
	SetMoneyType(ctx context.Context, userID uuid.UUID, moneyType string) error
	SetMoneyDials(ctx context.Context, userID uuid.UUID, dials string) error
}

// GuestTranscriptWriter replays the pre-signup conversation into the user's
// first platform conversation so the authenticated Miriam continues mid-thread
// instead of starting cold. Satisfied by a DI adapter over the conversation
// repository. Optional.
type GuestTranscriptWriter interface {
	AppendGuestTranscript(ctx context.Context, userID uuid.UUID, identity *entities.PlatformIdentity, threadID string, turns []GuestMessage) error
}

// GuestMonoLinker lets a guest link their real bank (Mono) before they have an
// account, so the conversational "aha" financial picture can drive the
// conversation. Satisfied by the mono Service. Optional — without it, the
// guest brain simply can't offer bank linking.
type GuestMonoLinker interface {
	InitiateGuestLinking(ctx context.Context, guestToken, customerName, customerEmail, redirectURL string) (string, error)
	GetGuestSpendingAnalysis(ctx context.Context, guestToken string, days int) (*entities.MonoSpendingAnalysis, error)
	AttachGuestAccountToUser(ctx context.Context, guestToken string, userID uuid.UUID) (*entities.MonoLinkedAccount, error)
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
	// phaseEmailAttach — the account already exists (created at phone
	// verification); we are asking for a real email to attach to it. Replying
	// with an email verifies ownership and links it; skipping keeps the
	// opaque placeholder and finishes onboarding.
	phaseEmailAttach guestPhase = "awaiting_email_attach"
)

// guestState is the per-sender pre-signup conversation persisted in Redis.
type guestState struct {
	Phase            guestPhase     `json:"phase"`
	Platform         string         `json:"platform,omitempty"`
	SenderID         string         `json:"sender_id,omitempty"`
	FirstName        string         `json:"first_name,omitempty"`
	Country          string         `json:"country,omitempty"`
	Goal             string         `json:"goal,omitempty"`
	MoneyType        string         `json:"money_type,omitempty"`
	MoneyDial        string         `json:"money_dial,omitempty"`
	Email            string         `json:"email,omitempty"`
	Phone            string         `json:"phone,omitempty"`
	Turns            []GuestMessage `json:"turns,omitempty"`
	TurnCount        int            `json:"turn_count,omitempty"`
	LastReplyHash    uint64         `json:"last_reply_hash,omitempty"`
	OTPAttempts      int            `json:"otp_attempts,omitempty"`
	EmailOTPAttempts int            `json:"email_otp_attempts,omitempty"`
	EmailVerified    bool           `json:"email_verified,omitempty"`
	// EmailAttach reports that the pending email OTP is proving ownership of an
	// address to attach to the session's account (rather than confirming an
	// existing account's address).
	EmailAttach bool `json:"email_attach,omitempty"`
	// EmailBackfill reports that this session is collecting a real address for an
	// ALREADY-linked account whose row still carries the opaque placeholder
	// (phone+<uuid>@placeholder.invalid). It behaves like EmailAttach except that
	// the account is never handed off: the person is already in their account, so
	// an address belonging to someone else must be refused rather than moving
	// their chat.
	EmailBackfill bool   `json:"email_backfill,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	// AccountCreated reports that THIS chat session created the account row
	// (rather than adopting a pre-existing one). Only a session-created account
	// may be re-pointed or have its email attached automatically: it has no
	// history and no balance, so a bulk email-finish or an existing-account
	// handoff can never strand money.
	AccountCreated     bool   `json:"account_created,omitempty"`
	SignupReason       string `json:"signup_reason,omitempty"`
	IntroSent          bool   `json:"intro_sent,omitempty"`
	StatementSummary   string `json:"statement_summary,omitempty"`
	PendingStatementID string `json:"pending_statement_id,omitempty"`
	// GuestToken is a web-safe token mapping this chat session to a pre-signup
	// Mono linking session (used as Mono's MetaRef and in the redirect URL).
	GuestToken string `json:"guest_token,omitempty"`
	// MonoLinkURL is the pending Mono Connect URL to send the guest.
	MonoLinkURL string `json:"mono_link_url,omitempty"`
	// MonoLinked reports that the guest has connected a bank (set by the
	// guest-scoped Mono complete endpoint).
	MonoLinked bool `json:"mono_linked,omitempty"`
	// MonoSummary is the real spending picture fetched from the linked bank,
	// injected into the state block so the guest brain can deliver the aha.
	MonoSummary string `json:"mono_summary,omitempty"`
}

// OnboardInput is a normalized inbound message from an unlinked sender.
type OnboardInput struct {
	Platform  entities.Platform
	SenderID  string
	ThreadID  string
	Text      string
	Contact   *SharedContact
	Statement *StatementAttachment
	// IsPollVote reports that Text is the option title the guest tapped on an
	// interactive poll Miriam sent, and PollTitle the question that poll asked.
	// The pair is what lets the brain treat a bare option fragment as an answer
	// instead of a new topic.
	IsPollVote bool
	PollTitle  string
	// Redeliverable reports that the caller will retry this message if the turn
	// fails, which lets a transient model failure be requeued silently instead
	// of surfacing an apology. The zero value answers the person immediately,
	// so a caller that doesn't know about redelivery can never cause silence.
	Redeliverable bool
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
	monoLinker       GuestMonoLinker
	shareAllowlist   map[string]bool
	accountLinks     *ChatAccountLinker
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

// SetGuestMonoLinker enables pre-signup bank linking (Mono) so the guest can
// share their real spending and get the conversational aha before signing up.
// Nil-safe.
func (c *ChatOnboarder) SetGuestMonoLinker(l GuestMonoLinker) {
	c.monoLinker = l
}

// SetShareAllowlist enables Miriam's share_artifact tool by listing the hosts a
// shared link may point at. Only hosts on this list are delivered — an empty
// (or unset) allowlist admits nothing, so a wild model URL can never reach the
// user. Hosts are compared case-insensitively with a leading "www." stripped.
func (c *ChatOnboarder) SetShareAllowlist(hosts []string) {
	list := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		list[strings.TrimPrefix(h, "www.")] = true
	}
	c.shareAllowlist = list
}

// shareHostAllowed reports whether a share URL's host passes the allowlist. The
// syntactic http(s) gate already ran in the executor; this is the delivery gate.
func (c *ChatOnboarder) shareHostAllowed(raw string) bool {
	if len(c.shareAllowlist) == 0 {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	return c.shareAllowlist[strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")]
}

// outcomeReply projects a brain outcome onto a PlatformReply, applying the share
// host allowlist so only approved links are ever delivered. Reactions and extra
// bubbles pass through as-is (both were whitelisted/bounded by the executor).
func (c *ChatOnboarder) outcomeReply(out *guestOutcome) *PlatformReply {
	reply := &PlatformReply{Text: out.text}
	if out.poll != nil {
		reply.Poll = out.poll
	}
	if out.reaction != "" {
		reply.Reaction = out.reaction
	}
	if len(out.extraTexts) > 0 {
		reply.ExtraTexts = append([]string(nil), out.extraTexts...)
	}
	if out.share != nil && c.shareHostAllowed(out.share.url) {
		reply.Share = &ShareRequest{Kind: out.share.kind, Title: out.share.title, URL: out.share.url}
	}
	return reply
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

// guestTokenKey maps a guest linking token back to its onboarding session key,
// so the guest-scoped Mono complete endpoint can find and update the session.
func guestTokenKey(token string) string {
	return "onboarding:guest:" + token
}

// MarkGuestMonoLinked flips a guest session's MonoLinked flag once the guest
// completes a bank link. Called by the guest-scoped Mono complete endpoint.
func (c *ChatOnboarder) MarkGuestMonoLinked(ctx context.Context, guestToken string) error {
	if guestToken == "" {
		return fmt.Errorf("guest token is required")
	}
	var key string
	if err := c.store.Get(ctx, guestTokenKey(guestToken), &key); err != nil || key == "" {
		return fmt.Errorf("unknown or expired guest token")
	}
	var st guestState
	if err := c.store.Get(ctx, key, &st); err != nil || st.Phase == "" {
		return fmt.Errorf("onboarding session not found")
	}
	st.MonoLinked = true
	return c.save(ctx, key, st)
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

	// A tap on one of Miriam's polls is a deliberate answer to a question she
	// asked, but all it carries is the option title ("Savings or investments").
	// Ride that through the turn's context so the brain's completer can flag it
	// downstream — without the flag the interview reads the fragment as a fresh
	// topic and answers by re-asking its question with a brand-new poll, which is
	// exactly what a dead tap looks like to the person who tapped.
	ctx = ContextWithGuestVote(ctx, GuestVote{IsPollVote: in.IsPollVote, PollTitle: in.PollTitle})

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
	st.Platform = in.Platform.String()
	st.SenderID = in.SenderID

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
			// Persist the scan before the phase branches below: the identity
			// paths (OTP, consent) can return without saving, which would drop
			// the pending statement id and lose the document after signup.
			if err := c.save(ctx, key, st); err != nil {
				return nil, err
			}
			if st.Phase == phaseConverse || st.Phase == phaseEmail || st.Phase == "" {
				if st.SignupReason == "" {
					st.SignupReason = "bank statement"
				}
				// Route the scan through the brain so the person gets the
				// one-category aha (not a raw summary dump) before the
				// signup ask. Falls back to the deterministic path when the
				// model is unavailable.
				if reply, ok := c.statementAha(ctx, key, &st, text); ok {
					return reply, nil
				}
				return c.beginSignup(ctx, key, &st, text, st.StatementSummary)
			}
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
	if strings.TrimSpace(text) == "" && in.Statement == nil {
		// A sticker or app bubble has nothing to read. The guest brain rejects
		// that as "no user text", and a redelivery of the same empty payload
		// never succeeds — answer once instead of retrying the turn.
		return textReply("I can't open that kind of message. Text me what you need."), nil
	}

	var reply *PlatformReply
	switch st.Phase {
	case phaseOTP:
		reply, err = c.handleOTP(ctx, key, &st, text)
	case phaseConsent:
		reply, err = c.handleConsent(ctx, key, &st, in, text)
	case phaseEmailOTP:
		reply, err = c.handleEmailOTP(ctx, key, &st, in, text)
	case phasePhone, phaseEmail, phaseConverse, phaseEmailAttach:
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
			return c.handleEmail(ctx, key, st, in, email)
		}
	}

	// The account already exists and we are collecting an email for it. An
	// address attaches/links; anything that reads as "no" finishes with the
	// placeholder instead of trapping the person in a loop they cannot exit.
	if st.Phase == phaseEmailAttach {
		if isSkipEmail(text) {
			c.clear(ctx, key)
			return c.completionReply(st), nil
		}
		return textReply(c.emailAttachPrompt()), nil
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
			return textReply(c.emailPrompt()), nil
		}
		return c.handleEmail(ctx, key, st, in, email)
	}

	// Daily abuse cap — read before spending a model turn. The counter is only
	// incremented once a turn actually lands, so provider failures and their
	// redeliveries don't eat the person's allowance.
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
		st.Phase = phaseEmail
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply("I've enjoyed this, but talking only gets us so far. " + c.emailPrompt()), nil
	}

	out, err := c.brain.respond(ctx, st, text)
	if err != nil {
		// Nothing was sent and nothing was saved, so a redelivery is free and
		// invisible to the person: prefer retrying the whole turn over
		// answering them with an apology.
		if in.Redeliverable && isTransientGuestErr(err) {
			c.logger.Warn("guest brain turn failed; requeueing for redelivery", zap.Error(err))
			return nil, Retryable(fmt.Errorf("guest brain turn: %w", err))
		}
		// Out of redeliveries. The deterministic path can still move identity
		// forward (phone, OTP, consent) even with the model down; only its
		// terminal message becomes an apology.
		c.logger.Error("guest brain turn failed; falling back", zap.Error(err))
		return c.fallbackTurn(ctx, key, st, in, text)
	}
	c.bumpDailyCap(ctx, in)

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

	case out.connectBank:
		return c.handleGuestConnectBank(ctx, key, st, text, replyText)

	case out.analysis:
		return c.handleGuestAnalysis(ctx, key, st, text)
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

	out.text = replyText
	return c.outcomeReply(out), nil
}

// handleGuestConnectBank used to send a Mono link. Mono is not available, so
// the picture comes from a statement PDF the guest sends in the thread.
func (c *ChatOnboarder) handleGuestConnectBank(ctx context.Context, key string, st *guestState, userText, replyText string) (*PlatformReply, error) {
	replyText = "Send me a PDF of a recent bank statement. I'll read it here, no account needed."
	c.recordTurn(st, userText, replyText)
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(replyText), nil
}

// handleGuestAnalysis fetches the guest's real spending picture from their
// linked bank, stores it on the session, and re-runs the brain once so the aha
// is grounded in actual numbers rather than a guess.
func (c *ChatOnboarder) handleGuestAnalysis(ctx context.Context, key string, st *guestState, userText string) (*PlatformReply, error) {
	if c.monoLinker == nil || !st.MonoLinked || st.GuestToken == "" {
		replyText := "Send me a PDF of a recent bank statement and I'll show you where the money went."
		c.recordTurn(st, userText, replyText)
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(replyText), nil
	}

	analysis, err := c.monoLinker.GetGuestSpendingAnalysis(ctx, st.GuestToken, 30)
	if err != nil {
		c.logger.Warn("guest mono analysis failed", zap.Error(err))
		replyText := "I couldn't pull your spending just now. Give me a moment and ask again?"
		c.recordTurn(st, userText, replyText)
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(replyText), nil
	}

	st.MonoSummary = summarizeGuestAnalysis(analysis)
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}

	// One more brain turn so the model sees the real numbers in the state block
	// and delivers the aha. The state block marks the summary as present, and the
	// prompt tells it not to re-call the tool.
	out, err := c.brain.respond(ctx, st, "[the linked bank's spending picture is now available — deliver the aha in one or two sentences, then call start_signup so the account and wallet can be set up. Do not ask them to send money.]")
	if err != nil {
		c.logger.Error("guest aha brain turn failed", zap.Error(err))
		replyText := "See? That's where your money actually goes. Want to talk about what to do with it?"
		c.recordTurn(st, userText, replyText)
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(replyText), nil
	}
	replyText := strings.TrimSpace(out.text)
	if replyText == "" {
		replyText = "See? That's where your money actually goes."
	}
	if st.SignupReason == "" {
		st.SignupReason = "spending picture"
	}
	if out.signupReason != "" {
		st.SignupReason = out.signupReason
	}
	// The picture is the reason to open the account. Do this even when the
	// model only wrote the aha and forgot the tool.
	return c.beginSignup(ctx, key, st, userText, replyText)
}

// guestLinkName/guestLinkEmail give Mono a display identity for the link
// session. No account exists yet, so these are best-effort from what the guest
// already shared, never required.
func guestLinkName(st *guestState) string {
	if strings.TrimSpace(st.FirstName) != "" {
		return strings.TrimSpace(st.FirstName)
	}
	return "Rail guest"
}

func guestLinkEmail(st *guestState) string {
	if strings.TrimSpace(st.Email) != "" {
		return strings.TrimSpace(st.Email)
	}
	// Mono requires a customer email; use an opaque placeholder when the guest
	// hasn't shared one. It carries no PII and is never used for delivery.
	return "guest+" + st.GuestToken + "@placeholder.invalid"
}

// summarizeGuestAnalysis turns the guest's spending picture into a compact,
// injectable state block: the top spending category, income stability, and any
// recurring subscriptions. This is the raw material for the conversational aha.
func summarizeGuestAnalysis(a *entities.MonoSpendingAnalysis) string {
	if a == nil || a.TransactionCount == 0 {
		return "no transactions in the last 30 days"
	}
	var top *entities.MonoCategoryBreakdown
	for i := range a.ByCategory {
		if top == nil || a.ByCategory[i].Amount > top.Amount {
			b := a.ByCategory[i]
			top = &b
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "last 30 days: %d transactions, total in %d, total out %d",
		a.TransactionCount, a.TotalCredits, a.TotalDebits)
	if top != nil {
		fmt.Fprintf(&b, "; biggest category: %s at %d across %d txns", top.Category, top.Amount, top.Count)
	}
	if a.IncomeStability > 0 {
		fmt.Fprintf(&b, "; income stability %.0f%% (%d source%s)", a.IncomeStability*100, a.IncomeSources, pluralS(a.IncomeSources))
	}
	if len(a.RecurringSubscriptions) > 0 {
		fmt.Fprintf(&b, "; recurring: ")
		for i, sub := range a.RecurringSubscriptions {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s at %d", sub.Merchant, sub.Amount)
		}
	}
	return b.String()
}

// statementAha runs one brain turn over the verified scan so the guest hears
// the aha (one spending category in plain words) before the signup ask.
// It returns ok=false when the model is unavailable — the caller then falls
// back to the deterministic beginSignup path. The aha text is capped so the
// appended link URLs never push the message past channel limits.
func (c *ChatOnboarder) statementAha(ctx context.Context, key string, st *guestState, userText string) (*PlatformReply, bool) {
	if c.brain == nil {
		return nil, false
	}
	out, err := c.brain.respond(ctx, st, "[a verified bank-statement scan is now available in the state block — react to it in one or two sentences (the single most interesting spending category), then call start_signup so the account and wallet can be set up. Do not paste the raw summary.]")
	if err != nil {
		c.logger.Warn("statement aha brain turn failed; falling back", zap.Error(err))
		return nil, false
	}
	for _, n := range out.notes {
		c.applyNote(st, n)
	}
	aha := truncate(strings.TrimSpace(out.text), 500)
	if aha == "" {
		return nil, false
	}
	if st.SignupReason == "" {
		st.SignupReason = "bank statement"
	}
	if out.signupReason != "" {
		st.SignupReason = out.signupReason
	}
	reply, err := c.beginSignup(ctx, key, st, userText, aha)
	if err != nil {
		return nil, false
	}
	return reply, true
}

// beginSignup transitions the conversation into identity verification. OTP and
// consent copy stay deterministic — compliance text is not generated.
func (c *ChatOnboarder) beginSignup(ctx context.Context, key string, st *guestState, userText, replyText string) (*PlatformReply, error) {
	if st.Phone != "" || (st.Email != "" && !st.EmailVerified) {
		c.recordTurn(st, userText, replyText)
		return c.startVerification(ctx, key, st)
	}

	st.Phase = phaseEmail
	ask := c.emailPrompt()
	if linked := c.accountLinkReply(ctx, key, st); linked != nil && strings.TrimSpace(linked.Text) != "" {
		ask = linked.Text
	}
	body := ask
	if spoken := strings.TrimSpace(replyText); spoken != "" && !strings.Contains(spoken, ask) {
		body = spoken + "\n\n" + ask
	}
	c.recordTurn(st, userText, body)
	st.LastReplyHash = hashReply(body)
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(body), nil
}

// SetChatAccountLinker offers Apple and Google as the way to pick an account.
func (c *ChatOnboarder) SetChatAccountLinker(linker *ChatAccountLinker) {
	c.accountLinks = linker
}

// accountLinkReply asks the person to link the account they already use.
// The email they pick there is the account. A different email creates a new one.
func (c *ChatOnboarder) accountLinkReply(ctx context.Context, key string, st *guestState) *PlatformReply {
	if c.accountLinks == nil || !c.accountLinks.Enabled() {
		return nil
	}
	offer, err := c.accountLinks.Offer(ctx, entities.Platform(st.Platform), st.SenderID)
	if err != nil || (offer.AppleURL == "" && offer.GoogleURL == "") {
		return nil
	}
	text := AccountLinkCopy(offer)
	st.LastReplyHash = hashReply(text)
	if err := c.save(ctx, key, *st); err != nil {
		c.logger.Warn("account link reply save failed", zap.Error(err))
	}
	return textReply(text)
}

// startVerification begins identity proof. Email is the anchor: the signup ask
// is the address, because users.email is the NOT NULL UNIQUE key while the phone
// is an optional profile attribute. The phone/SMS path stays for sessions that
// supplied a phone and no address.
func (c *ChatOnboarder) startVerification(ctx context.Context, key string, st *guestState) (*PlatformReply, error) {
	if st.Email != "" && !st.EmailVerified {
		existing, _ := c.users.GetByEmail(ctx, st.Email)
		if existing != nil && !existing.IsActive {
			c.clear(ctx, key)
			return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
		}
		// attach only when this session already created a row: then the verified
		// address is written onto it (or the chat is handed to its owner). With
		// no session row the verified address anchors the signup itself, and the
		// code path creates or claims the account.
		return c.sendEmailOTP(ctx, key, st, existing, c.canLinkAccount(st))
	}
	if st.Phone != "" && st.Email == "" {
		return c.sendPhoneOTP(ctx, key, st)
	}
	st.Phase = phaseEmail
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(c.emailPrompt()), nil
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
	case "money_dial":
		if d := truncate(strings.TrimSpace(n.value), 200); d != "" {
			st.MoneyDial = d
		}
	case "email":
		if st.Email == "" {
			st.Email = normalizeEmail(n.value)
		}
	}
}

// overDailyCap reports whether this sender has spent its rolling 24h model
// allowance. Read-only: see bumpDailyCap for the increment.
func (c *ChatOnboarder) overDailyCap(ctx context.Context, in OnboardInput) (bool, error) {
	key := dailyTurnsKey(in.Platform, in.SenderID)
	var n int
	if err := c.store.Get(ctx, key, &n); err != nil {
		return false, nil
	}
	return n >= maxGuestDailyTurns, nil
}

// bumpDailyCap charges one model turn against the sender's daily allowance.
// Called only after a turn produced a reply, so a failed completion and the
// redeliveries that follow it are free.
func (c *ChatOnboarder) bumpDailyCap(ctx context.Context, in OnboardInput) {
	key := dailyTurnsKey(in.Platform, in.SenderID)
	var n int
	if err := c.store.Get(ctx, key, &n); err != nil {
		n = 0
	}
	if err := c.store.Set(ctx, key, n+1, 24*time.Hour); err != nil {
		c.logger.Warn("guest daily cap increment failed", zap.Error(err))
	}
}

// transientApology is what the person hears when every redelivery of their
// message failed. It stays in Miriam's voice and, crucially, re-asks whatever
// she last asked so the thread survives the blip.
func transientApology(st *guestState) string {
	const lead = "My end lagged just then, not you."
	if st == nil {
		return lead + " Say that again?"
	}
	for i := len(st.Turns) - 1; i >= 0; i-- {
		if st.Turns[i].Role != "assistant" {
			continue
		}
		if q := lastQuestion(st.Turns[i].Content); q != "" {
			return lead + " " + q
		}
		break
	}
	return lead + " Say that again?"
}

// lastQuestion pulls the final question out of a reply so it can be re-asked
// verbatim. Returns "" when the reply didn't ask anything.
func lastQuestion(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if !strings.HasSuffix(trimmed, "?") {
		return ""
	}
	// Walk back to the start of the sentence the question mark closes.
	start := strings.LastIndexAny(trimmed[:len(trimmed)-1], ".!?\n")
	q := strings.TrimSpace(trimmed[start+1:])
	if q == "" || len([]rune(q)) < 4 {
		return ""
	}
	return q
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

	// A contact card answers the questions it safely can — a display name, and a
	// candidate address that still has to be proven. It never supplies identity,
	// so this starts the proof step rather than trusting the card.
	if in.Contact != nil {
		st.IntroSent = true
		if st.Email != "" || st.Phone != "" {
			return c.startVerification(ctx, key, st)
		}
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		if st.FirstName != "" {
			return textReply(fmt.Sprintf("Got it, %s. %s", st.FirstName, c.emailPrompt())), nil
		}
		return textReply("Whose card is that? Tell me your first name, and " + c.emailPrompt()), nil
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
				return textReply(fmt.Sprintf("Got it, %s. I'm still reconnecting. If you want to continue setup, %s", name, c.emailPrompt())), nil
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
	// Nothing deterministic left to say. A wired-but-failing model is a blip, so
	// apologise in Miriam's voice and keep her last question alive; no model at
	// all is a configuration state, so offer the path that still works.
	if c.brain != nil {
		return textReply(transientApology(st)), nil
	}
	return textReply("I can't chat properly right now, but I can still get you set up. " + c.emailPrompt()), nil
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

// handleEmail starts or continues the email step: the address is looked up, and
// either an existing account gets an email OTP to prove ownership, a free
// address is kept for creation/attachment, or a session-created placeholder
// account is pointed at the address that already owns an account.
func (c *ChatOnboarder) handleEmail(ctx context.Context, key string, st *guestState, in OnboardInput, email string) (*PlatformReply, error) {
	st.Email = email

	existing, err := c.users.GetByEmail(ctx, email)
	if err != nil || existing == nil {
		// No account under that address. Email is the anchor, so a verified free
		// address IS the signup: send the code and create the account when it
		// checks out. (When this session already owns a placeholder row, the
		// address is attached to that row instead.)
		return c.sendEmailOTP(ctx, key, st, nil, c.canLinkAccount(st))
	}
	if !existing.IsActive {
		c.clear(ctx, key)
		return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
	}
	attaching := c.canLinkAccount(st) && existing.ID.String() != st.UserID
	return c.sendEmailOTP(ctx, key, st, existing, attaching)
}

// canLinkAccount reports whether this session owns a freshly created account
// that may be linked to a verified email. A pre-existing account adopted by the
// chat (AccountCreated false) is never re-pointed: it may already hold money or
// history, and a silent handoff would strand it.
func (c *ChatOnboarder) canLinkAccount(st *guestState) bool {
	return st.UserID != "" && st.AccountCreated
}

// sendEmailOTP sends the email ownership code synchronously so a provider
// failure reaches the user instead of dying in a background worker after we
// already claimed success.
//
// attach=true means the code proves ownership of an address we intend to attach
// (or hand over the chat to), so a missing row is expected: a free address, or
// the session's own placeholder account.
func (c *ChatOnboarder) sendEmailOTP(ctx context.Context, key string, st *guestState, existing *entities.UserProfile, attach bool) (*PlatformReply, error) {
	code, simulated, err := c.verifier.GenerateAndSendCodeSync(ctx, "email", st.Email)
	if err != nil {
		c.logger.Warn("onboarding email OTP send failed", zap.Error(err))
		return textReply(otpSendErrorMessage(err)), nil
	}
	// Re-verify now that the OTP is in flight, so a deleted or deactivated
	// account can never receive a code that links a chat to a stale row. On the
	// existing-account path the row must still be live; on the attach path the
	// row may legitimately be absent, and only a *different* live account is
	// disqualifying (the address changed owner while we were checking).
	recheck, rerr := c.users.GetByEmail(ctx, st.Email)
	if rerr == nil && recheck != nil && !recheck.IsActive {
		c.clear(ctx, key)
		return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
	}
	if attach {
		if existing != nil && recheck != nil && recheck.ID != existing.ID {
			c.clear(ctx, key)
			return textReply("That email changed accounts while I was checking. Please link this chat from the RAIL app instead."), nil
		}
	} else if existing != nil && (rerr != nil || recheck == nil || !recheck.IsActive) {
		// An account was found a moment ago and is no longer resolvable: a genuine
		// race (deleted or deactivated mid-flight), not a free address.
		c.clear(ctx, key)
		return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
	}
	// Deliberately no "the account must already exist" guard here. Email is the
	// identity anchor, so a free address is a signup rather than an error: the
	// code proves ownership and handleEmailOTP then creates or claims the
	// account. The old unconditional guard refused every new signup with "That
	// account isn't active", which is what forced the phone step to come first.
	st.Phase = phaseEmailOTP
	st.EmailOTPAttempts = 0
	st.EmailAttach = attach
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	if simulated {
		return textReply(fmt.Sprintf("No email provider is configured here, so nothing was actually sent. Test code: %s.", code)), nil
	}
	if attach {
		return textReply(fmt.Sprintf("I just emailed a 6-digit code to %s. Reply with it here and I'll put it on your Rail account.", st.Email)), nil
	}
	// Wording is identical whether or not the address already owns an account:
	// otherwise this line tells an attacker which addresses are customers.
	return textReply(fmt.Sprintf("I just emailed a 6-digit code to %s. Reply with it here and I'll take it from there.", st.Email)), nil
}

func (c *ChatOnboarder) handleEmailOTP(ctx context.Context, key string, st *guestState, in OnboardInput, text string) (*PlatformReply, error) {
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

	// Attach path: the verified address belongs on an account that already
	// exists (the one this chat created, or the one the address already owns).
	// A backfill session is the same job for an already-linked account.
	if st.EmailAttach || st.EmailBackfill {
		return c.completeEmailAttach(ctx, key, st, in, existing, err)
	}

	if err == nil && existing != nil {
		if !existing.IsActive {
			c.clear(ctx, key)
			return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
		}
		// The verified address already owns an account, so it anchors this chat
		// onto that account. No phone is required: previously this branch set the
		// user id and then demanded a phone, so an existing customer could never
		// be let in on email alone. Consent follows, and linking/provisioning
		// happen there (handleConsent).
		st.UserID = existing.ID.String()
		st.AccountCreated = false
		c.markEmailVerified(ctx, existing.ID)
		st.Phase = phaseConsent
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return c.consentReply(), nil
	}

	// Unknown address: create the account now. The row exists and is provisioned
	// the moment identity is proven, which is the same failure-safety invariant
	// the phone path held — never a verified-but-unprovisioned account that every
	// read treats as missing.
	if err := c.createEmailFirstUser(ctx, st); err != nil {
		c.logger.Error("email-first account creation failed", zap.Error(err))
		c.clear(ctx, key)
		return textReply("Something went wrong on my end. Please try again in a moment."), nil
	}
	st.Phase = phaseConsent
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return c.consentReply(), nil
}

// completeEmailAttach finishes the email step for an account that already
// exists: it either hands the chat over to the account the verified email
// already owns, or writes the verified address onto the phone-first account this
// session created.
func (c *ChatOnboarder) completeEmailAttach(ctx context.Context, key string, st *guestState, in OnboardInput, existing *entities.UserProfile, lookupErr error) (*PlatformReply, error) {
	if lookupErr == nil && existing != nil && !existing.IsActive {
		c.clear(ctx, key)
		return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
	}
	// The verified address already owns a different account: hand the chat over
	// to it (attach the proven phone, move the messaging link) instead of
	// leaving the person with a second, empty account under the same email.
	if lookupErr == nil && existing != nil && existing.ID.String() != st.UserID {
		// A backfill session belongs to someone who is ALREADY in their own
		// account. Moving their chat to a different account because they typed an
		// address that happens to belong to one would hand their conversation to
		// a stranger's account, so refuse instead.
		if st.EmailBackfill {
			c.clear(ctx, key)
			return textReply(
				"That address is already on another RAIL account. Use a different one, " +
					"or change it from the RAIL app."), nil
		}
		if err := c.handoffToEmailOwner(ctx, st, in, existing); err != nil {
			c.logger.Error("email-owner handoff failed", zap.Error(err), zap.String("user_id", st.UserID))
			c.clear(ctx, key)
			return textReply("I found your existing account but couldn't move this chat onto it. Link from the RAIL app and I'll pick this back up there."), nil
		}
		c.clear(ctx, key)
		return c.completionReply(st), nil
	}

	uid, perr := uuid.Parse(st.UserID)
	if perr != nil {
		c.logger.Error("attach email with unparseable user id", zap.Error(perr), zap.String("user_id", st.UserID))
		c.clear(ctx, key)
		return textReply("Something went wrong on my end. Text me again and we'll finish this."), nil
	}
	if err := c.users.UpdateEmail(ctx, uid, st.Email); err != nil {
		c.logger.Error("failed to attach verified email", zap.Error(err), zap.String("user_id", st.UserID))
		c.clear(ctx, key)
		return textReply("I verified that email but couldn't save it to your account. Mind trying again in a moment?"), nil
	}
	// The address was just proven by OTP, so it is verified by definition.
	c.markEmailVerified(ctx, uid)
	c.clear(ctx, key)
	return c.completionReply(st), nil
}

// handoffToEmailOwner moves this chat from the placeholder account created for
// it onto the account the verified email already owns, and attaches the proven
// phone so the email account gains the number. Only a session-created account is
// ever handed off (see canLinkAccount), so no balance or history is stranded.
func (c *ChatOnboarder) handoffToEmailOwner(ctx context.Context, st *guestState, in OnboardInput, owner *entities.UserProfile) error {
	oldID, err := uuid.Parse(st.UserID)
	if err != nil {
		return fmt.Errorf("parse placeholder user id: %w", err)
	}
	// Attach the proven phone first: if this fails nothing has changed yet and
	// the placeholder account stays exactly as it was.
	if st.Phone != "" {
		if err := c.provisioner.ProvisionPhoneFirstUser(ctx, owner.ID, st.FirstName, st.Country, st.Phone); err != nil {
			return fmt.Errorf("attach phone to email account: %w", err)
		}
	}
	if c.linker == nil {
		st.UserID = owner.ID.String()
		st.AccountCreated = false
		return nil
	}
	unlinker, ok := c.linker.(PlatformUnlinker)
	if !ok {
		// The linker cannot release the placeholder link, so the sender id is
		// still bound to the empty account. Refuse rather than leave the person
		// half-moved between two accounts.
		return fmt.Errorf("linker does not support unlinking")
	}
	if err := unlinker.Unlink(ctx, oldID, in.Platform); err != nil {
		return fmt.Errorf("release placeholder link: %w", err)
	}
	linked, err := c.linker.LinkVerified(ctx, owner.ID, in.Platform, in.SenderID)
	if err != nil {
		// Roll the placeholder link back so the chat keeps working while the
		// person retries or links from the app.
		if _, rerr := c.linker.LinkVerified(ctx, oldID, in.Platform, in.SenderID); rerr != nil {
			c.logger.Error("failed to restore placeholder link after handoff failure",
				zap.Error(rerr), zap.String("user_id", st.UserID))
		}
		return fmt.Errorf("link email account: %w", err)
	}
	if linked == nil || linked.PlatformUserID != in.SenderID {
		// The owner account is already connected on this platform, so the link
		// could not be moved here. Put the placeholder link back rather than
		// leaving the chat unbound mid-handoff.
		if _, rerr := c.linker.LinkVerified(ctx, oldID, in.Platform, in.SenderID); rerr != nil {
			c.logger.Error("failed to restore placeholder link after refused handoff",
				zap.Error(rerr), zap.String("user_id", st.UserID))
		}
		return fmt.Errorf("owner account is already connected on this platform")
	}
	st.UserID = owner.ID.String()
	st.AccountCreated = false
	return nil
}

// PlatformUnlinker is the optional release half of the linking service. The
// onboarder only needs it for the email-owner handoff, so it is a type
// assertion rather than a required interface method.
type PlatformUnlinker interface {
	Unlink(ctx context.Context, userID uuid.UUID, platform entities.Platform) error
}

// completionReply is the single place the "you're in" reply is built, so a
// completion reached from any path looks the same.
func (c *ChatOnboarder) completionReply(st *guestState) *PlatformReply {
	return &PlatformReply{
		Text:   c.completionMessage(st),
		Effect: EffectCelebration,
	}
}

// emailAttachPrompt asks for the address to put on the account. It is asked once
// and is skippable, so nobody is trapped by not wanting to share an email.
func (c *ChatOnboarder) emailAttachPrompt() string {
	return "Last thing, what email should I put on your account? It's how receipts and anything needing a paper trail reach you. Say skip if you'd rather not."
}

// isSkipEmail reads a decline of the email step ("skip", "no", "later", ...).
func isSkipEmail(text string) bool {
	s := strings.ToLower(strings.TrimSpace(text))
	s = strings.Trim(s, ".!,")
	switch s {
	case "skip", "no", "nah", "nope", "later", "not now", "no thanks", "no thank you",
		"dont", "don't", "no email", "none", "without":
		return true
	}
	return false
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
	// The account exists the moment ownership is proven, and it is provisioned
	// right here rather than after the terms poll: a verified-but-unprovisioned
	// row has no KYC tier and no ledger/wallet accounts, so every later read or
	// tool call answered as if the account did not exist. Terms acceptance is
	// still recorded separately at the consent step, which re-runs this same
	// idempotent provision in case this best-effort attempt failed.
	if uid, perr := uuid.Parse(st.UserID); perr == nil {
		if perr := c.provisioner.ProvisionPhoneFirstUser(ctx, uid, st.FirstName, st.Country, st.Phone); perr != nil {
			c.logger.Warn("early phone-first provisioning failed; consent will retry",
				zap.Error(perr), zap.String("user_id", st.UserID))
		}
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
		// A conflict is definitive — the handle belongs to another account, or
		// this account is already connected — so "tap I agree to try again" would
		// be a lie that loops the person. This is also how a race between two
		// concurrent claims surfaces, thanks to the unique constraint.
		if errors.Is(err, entities.ErrIdentityAlreadyLinked) {
			if err := c.save(ctx, key, *st); err != nil {
				return nil, err
			}
			return textReply(
				"That account is already connected to a different chat. Open the RAIL app, " +
					"disconnect it there, and text me again — I'll pick this straight back up."), nil
		}
		c.logger.Error("phone-first auto-link failed", zap.Error(err), zap.String("user_id", st.UserID))
		return textReply("I couldn't finish linking this chat just now. Tap I agree to try again."), nil
	}
	// LinkVerified returns the account's EXISTING identity unchanged when that
	// account is already connected on this platform — it deliberately does not
	// move an established link to a new handle. Treating that as success would
	// tell the person they are in and then drop them back into onboarding on
	// their next message, so say what actually happened instead. This is
	// reachable whenever a claimed account was already linked (e.g. from the
	// app), not only in a race.
	if identity == nil || identity.PlatformUserID != in.SenderID {
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(
			"That account is already connected to a different chat. Open the RAIL app, " +
				"disconnect it there, and text me again — I'll pick this straight back up."), nil
	}
	// Fire the first-login goal seeder for the new user so the goal_progress
	// worker has a 7-step ladder to track on its next tick. Async + recover
	// so a failure here can't fail the onboarding completion.
	SeedBabyStepsOnLink(c.babySteps, uid, c.logger)
	c.fireGuestHandoff(uid, identity, in, st)
	c.attachGuestMono(ctx, uid, st)

	// Email comes last, after the conversation has already earned trust and the
	// account definitively exists. A phone-first row was created with an opaque
	// placeholder address, so this is the moment we can attach a real one, or,
	// if the address already owns an account, hand this chat over to it instead
	// of creating a second account for the same person.
	// Only a session-created account (an opaque placeholder email was used) is
	// asked for a real address. A pre-existing account adopted by this chat
	// already has its identity on file and is never re-prompted.
	if st.Email == "" && st.AccountCreated {
		st.Phase = phaseEmailAttach
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(c.emailAttachPrompt()), nil
	}

	c.clear(ctx, key)
	return c.completionReply(st), nil
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
	if c.moneyTypes != nil && st.MoneyDial != "" {
		dial := st.MoneyDial
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.moneyTypes.SetMoneyDials(ctx, uid, dial); err != nil {
				c.logger.Warn("money dials handoff failed", zap.Stringer("user_id", uid), zap.Error(err))
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

// attachGuestMono claims a pre-signup linked Mono account for the new user so
// the financial picture the guest already saw carries straight into the
// authenticated relationship. Best-effort: a failure here must not fail signup.
func (c *ChatOnboarder) attachGuestMono(ctx context.Context, uid uuid.UUID, st *guestState) {
	if c.monoLinker == nil || st.GuestToken == "" {
		return
	}
	if _, err := c.monoLinker.AttachGuestAccountToUser(ctx, st.GuestToken, uid); err != nil {
		c.logger.Warn("guest mono attach failed",
			zap.Stringer("user_id", uid), zap.String("guest_token", st.GuestToken), zap.Error(err))
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
	// Mark the row as created by this session. Only such a row may later be
	// pointed at an email that already owns an account (see handoffToEmailOwner)
	// or have an email attached to it, because a session-created row has no
	// history and no balance to strand.
	st.AccountCreated = true
	return nil
}

// createEmailFirstUser creates the account for an email-verified chat signup.
//
// Email is the identity anchor (users.email is NOT NULL UNIQUE), so no phone is
// required — the phone is an optional profile attribute collected later, if at
// all. Race handling mirrors ensureUser: a lost race adopts the winning row
// rather than failing the person.
func (c *ChatOnboarder) createEmailFirstUser(ctx context.Context, st *guestState) error {
	user, err := c.users.CreateUserWithHash(ctx, st.Email, nil, "")
	if err != nil {
		if existing, gerr := c.users.GetByEmail(ctx, st.Email); gerr == nil && existing != nil && existing.IsActive {
			st.UserID = existing.ID.String()
			st.AccountCreated = false
			c.markEmailVerified(ctx, existing.ID)
			return nil
		}
		return fmt.Errorf("create email-first user: %w", err)
	}
	st.UserID = user.ID.String()
	// Only a row this session created may later be re-pointed at an address that
	// already owns an account (see canLinkAccount/handoffToEmailOwner): it has no
	// history or balance to strand.
	st.AccountCreated = true
	c.markEmailVerified(ctx, user.ID)
	// Provision now, not at consent: the account must be usable the moment
	// identity is proven, so a mid-conversation failure can never leave a
	// verified-but-unprovisioned row that every read treats as missing. A
	// failure is non-fatal — handleConsent retries.
	if err := c.provisioner.ProvisionPhoneFirstUser(ctx, user.ID, st.FirstName, st.Country, ""); err != nil {
		c.logger.Warn("email-first provisioning failed; consent will retry",
			zap.Error(err), zap.String("user_id", user.ID.String()))
	}
	return nil
}

// markEmailVerified records that the address was proven by OTP. Without it the
// account would hold a proven address with email_verified = false, which every
// downstream reader — and the app — treats as unproven.
func (c *ChatOnboarder) markEmailVerified(ctx context.Context, id uuid.UUID) {
	marker, ok := c.users.(emailVerificationMarker)
	if !ok {
		// Loud rather than silent: if the store ever stops satisfying this, a
		// proven address would be left recorded as unproven with no signal.
		c.logger.Warn("user store cannot mark email verified; address stays unverified",
			zap.String("user_id", id.String()))
		return
	}
	if err := marker.MarkEmailVerified(ctx, id); err != nil {
		c.logger.Warn("failed to mark email verified",
			zap.Error(err), zap.String("user_id", id.String()))
	}
}

// emailVerificationMarker is the optional write-half of the user store. The
// onboarder only needs it to record a proven address, so it is a type assertion
// (like PlatformUnlinker) rather than a required interface method — that keeps
// existing fakes and tests compiling.
type emailVerificationMarker interface {
	MarkEmailVerified(ctx context.Context, userID uuid.UUID) error
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
	// A contact card's phone number is deliberately not taken. A card carries a
	// THIRD PARTY's data: forwarding a friend's card used to set st.Phone to the
	// friend's number, text the SMS OTP to the friend, and — on success — create
	// an account keyed on the friend's verified phone. A phone is only ever
	// accepted when the sender types it themselves.
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

// emailPrompt asks for the address that anchors the account. Email is the
// identity anchor, so this is the signup question — it must not presuppose an
// existing account.
func (c *ChatOnboarder) emailPrompt() string {
	return "What's the best email for you? I'll send a code to it to open your Rail account."
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

// IsPlaceholderEmail reports whether an address is the opaque placeholder a
// phone-first signup is created with. Such an account never collected a real
// address, so it can receive no receipts and cannot reset anything by email.
func IsPlaceholderEmail(email string) bool {
	e := strings.ToLower(strings.TrimSpace(email))
	return strings.HasPrefix(e, "phone+") && strings.HasSuffix(e, "@placeholder.invalid")
}

// emailBackfillKey marks that a linked account has already been asked for a real
// address, so the ask happens once rather than on every message. Losing this key
// only means asking again, which is benign — which is why it lives in Redis
// rather than needing a column.
func emailBackfillKey(platform entities.Platform, senderID string) string {
	return fmt.Sprintf("email_backfill_asked:%s:%s", platform, senderID)
}

// HasAskedEmailBackfill reports whether this sender has already been asked.
func (c *ChatOnboarder) HasAskedEmailBackfill(
	ctx context.Context, platform entities.Platform, senderID string,
) bool {
	var seen string
	if err := c.store.Get(ctx, emailBackfillKey(platform, senderID), &seen); err != nil {
		return false
	}
	return seen != ""
}

// MarkEmailBackfillAsked records the ask so it is not repeated.
func (c *ChatOnboarder) MarkEmailBackfillAsked(
	ctx context.Context, platform entities.Platform, senderID string,
) {
	if err := c.store.Set(ctx, emailBackfillKey(platform, senderID), "1", 90*24*time.Hour); err != nil {
		c.logger.Warn("failed to record email backfill ask",
			zap.Error(err), zap.String("sender", senderID))
	}
}

// IsEmailBackfillSession reports whether this sender is mid-way through the
// linked-account email backfill, so following turns (the address, then the code)
// go to the onboarder instead of the general agent. Deliberately narrow: a
// linked sender with some other leftover session must not be routed here.
func (c *ChatOnboarder) IsEmailBackfillSession(
	ctx context.Context, platform entities.Platform, senderID string,
) bool {
	var st guestState
	if err := c.store.Get(ctx, onboardingKey(platform, senderID), &st); err != nil {
		return false
	}
	return st.EmailBackfill && st.UserID != ""
}

// StartEmailBackfill seeds a session that collects a real address for an
// ALREADY-linked account whose row still holds the opaque placeholder.
//
// It reuses the normal attach machinery rather than a parallel flow: the session
// starts at phaseEmailAttach, so the next address the person sends is proven by
// email OTP and written onto their own account by completeEmailAttach. The
// EmailBackfill flag makes that completion refuse a handoff, because this person
// is already inside their account and must never be moved to a different one.
func (c *ChatOnboarder) StartEmailBackfill(
	ctx context.Context,
	platform entities.Platform,
	senderID string,
	userID uuid.UUID,
) (*PlatformReply, error) {
	key := onboardingKey(platform, senderID)
	st := guestState{
		Phase:         phaseEmailAttach,
		UserID:        userID.String(),
		EmailBackfill: true,
	}
	if err := c.save(ctx, key, st); err != nil {
		return nil, err
	}
	// Record the ask here rather than leaving it to the caller: this method is
	// what constitutes "we have asked", and a caller that forgot to mark it
	// would re-ask on every message.
	c.MarkEmailBackfillAsked(ctx, platform, senderID)
	return textReply(c.emailAttachPrompt()), nil
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
	// A permanent, recipient-level rejection means the code can never arrive at
	// that address (the provider suppressed it after a bounce or a complaint, or
	// refuses it outright). "Try again in a moment" would be a lie here — it
	// sends the person round a loop that cannot terminate. Say what is actually
	// wrong and give them the one move that works: another address.
	if entities.IsPermanentEmailDeliveryError(err) {
		return "Our email sender won't deliver to that address — it's been marked undeliverable, usually after a previous bounce. " +
			"Send me a different email address and I'll get your code out, or write to support@userail.money and we'll fix it."
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

// pluralS appends an "s" for counts > 1, used in generated copy.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
