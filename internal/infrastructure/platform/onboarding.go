package platform

import (
	"context"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// onboardingSessionTTL bounds how long a half-finished chat onboarding lives in
// Redis before the user has to start over.
const onboardingSessionTTL = 30 * time.Minute

// maxOnboardingOTPAttempts caps wrong SMS OTP entries before the session is reset.
const maxOnboardingOTPAttempts = 5

// maxEmailOTPAttempts caps wrong email OTP entries before the session is reset.
const maxEmailOTPAttempts = 5

// OnboardingStateStore is the subset of the Redis client the onboarder needs.
type OnboardingStateStore interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string, dest interface{}) error
	Del(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// OnboardingOTPVerifier sends and checks SMS one-time codes. Satisfied by the
// domain VerificationService.
type OnboardingOTPVerifier interface {
	GenerateAndSendCode(ctx context.Context, identifierType, identifier string) (string, error)
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

type onboardingStep string

const (
	stepName     onboardingStep = "awaiting_name"
	stepCountry  onboardingStep = "awaiting_country"
	stepEmail    onboardingStep = "awaiting_email"
	stepEmailOTP onboardingStep = "awaiting_email_otp"
	stepPhone    onboardingStep = "awaiting_phone"
	stepOTP      onboardingStep = "awaiting_otp"
	stepConsent  onboardingStep = "awaiting_consent"
)

// onboardingState is the per-sender progress persisted in Redis.
type onboardingState struct {
	Step             onboardingStep `json:"step"`
	FirstName        string         `json:"first_name,omitempty"`
	Country          string         `json:"country,omitempty"`
	CountryAttempts  int            `json:"country_attempts,omitempty"`
	Email            string         `json:"email,omitempty"`
	EmailOTPAttempts int            `json:"email_otp_attempts,omitempty"`
	EmailVerified    bool           `json:"email_verified,omitempty"`
	Phone            string         `json:"phone,omitempty"`
	OTPAttempts      int            `json:"otp_attempts,omitempty"`
	UserID           string         `json:"user_id,omitempty"`
}

// OnboardInput is a normalized inbound message from an unlinked sender.
type OnboardInput struct {
	Platform entities.Platform
	SenderID string
	ThreadID string
	Text     string
}

// ChatOnboarder walks an unknown messaging sender through account creation:
// name → country → phone → SMS OTP → consent → Tier 1 user + wallet + auto-link.
type ChatOnboarder struct {
	store       OnboardingStateStore
	verifier    OnboardingOTPVerifier
	users       OnboardingUserStore
	provisioner OnboardingProvisioner
	linker      OnboardingLinker
	appURL      string
	logger      *zap.Logger
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

func onboardingKey(platform entities.Platform, senderID string) string {
	return fmt.Sprintf("onboarding:%s:%s", platform, senderID)
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

// Handle advances the onboarding conversation by one step and returns the reply
// to send back. A nil reply means nothing should be sent.
func (c *ChatOnboarder) Handle(ctx context.Context, in OnboardInput) (*PlatformReply, error) {
	key := onboardingKey(in.Platform, in.SenderID)
	text := strings.TrimSpace(in.Text)

	var st onboardingState
	if err := c.store.Get(ctx, key, &st); err != nil || st.Step == "" {
		// New sender — greet, share the app link, and ask for their name.
		st = onboardingState{Step: stepName}
		if err := c.save(ctx, key, st); err != nil {
			return nil, err
		}
		return textReply(c.introMessage()), nil
	}

	switch st.Step {
	case stepName:
		return c.handleName(ctx, key, &st, text)
	case stepCountry:
		return c.handleCountry(ctx, key, &st, text)
	case stepEmail:
		return c.handleEmail(ctx, key, &st, text)
	case stepEmailOTP:
		return c.handleEmailOTP(ctx, key, &st, text)
	case stepPhone:
		return c.handlePhone(ctx, key, &st, text)
	case stepOTP:
		return c.handleOTP(ctx, key, &st, text)
	case stepConsent:
		return c.handleConsent(ctx, key, &st, in, text)
	default:
		// Unknown state — reset and greet fresh.
		_ = c.store.Del(ctx, key)
		return textReply(c.introMessage()), nil
	}
}

func (c *ChatOnboarder) handleName(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	name := parseFirstName(text)
	if name == "" {
		return textReply("What should I call you? Just your first name is fine."), nil
	}
	st.FirstName = name
	st.Step = stepCountry
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(fmt.Sprintf("Nice to meet you, %s. Which country are you in? You can say the name (like Nigeria) or its code (like NG).", name)), nil
}

func (c *ChatOnboarder) handleCountry(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	country := normalizeCountry(text)
	if country == "" {
		if st.CountryAttempts >= 1 && strings.TrimSpace(text) != "" {
			// Accept a best-effort value rather than looping forever.
			country = strings.ToUpper(strings.TrimSpace(text))
		} else {
			st.CountryAttempts++
			if err := c.save(ctx, key, *st); err != nil {
				return nil, err
			}
			return textReply("Which country are you in? You can say the name (like Nigeria) or its code (like NG)."), nil
		}
	}
	st.Country = country
	st.Step = stepEmail
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(c.emailPrompt()), nil
}

func (c *ChatOnboarder) handleEmail(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	email := normalizeEmail(text)
	if email == "" && strings.ToLower(strings.TrimSpace(text)) == "skip" {
		st.Email = ""
		st.Step = stepPhone
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply("Got it. What's your phone number? Include your country code, like +2348012345678 — I'll text you a code to confirm it."), nil
	}
	if email == "" {
		return textReply(c.emailPrompt()), nil
	}
	st.Email = email

	if existing, err := c.users.GetByEmail(ctx, email); err == nil && existing != nil {
		if !existing.IsActive {
			_ = c.store.Del(ctx, key)
			return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
		}
		// Found an existing account. Prove ownership by sending an OTP to the
		// registered email before we link this chat to it.
		if _, err := c.verifier.GenerateAndSendCode(ctx, "email", email); err != nil {
			c.logger.Warn("onboarding email OTP send failed", zap.Error(err))
			return textReply(otpSendErrorMessage(err)), nil
		}
		// Re-verify the account still exists and is active now that the OTP is
		// in flight, so a deleted or deactivated account can never receive a
		// code that links a chat to a stale row.
		if recheck, err := c.users.GetByEmail(ctx, email); err != nil || recheck == nil || !recheck.IsActive {
			_ = c.store.Del(ctx, key)
			return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
		}
		st.Step = stepEmailOTP
		st.EmailOTPAttempts = 0
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(fmt.Sprintf("I found your RAIL account. I just emailed a 6-digit code to %s. Reply with it here to confirm it's you.", email)), nil
	}

	st.Step = stepPhone
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply("Got it. What's your phone number? Include your country code, like +2348012345678 — I'll text you a code to confirm it."), nil
}

func (c *ChatOnboarder) handleEmailOTP(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	code := digitsOnly(text)
	if len(code) != 6 {
		return textReply("That doesn't look like the 6-digit code. Reply with the code I emailed you."), nil
	}
	ok, err := c.verifier.VerifyCode(ctx, "email", st.Email, code)
	if err != nil || !ok {
		st.EmailOTPAttempts++
		if st.EmailOTPAttempts >= maxEmailOTPAttempts {
			_ = c.store.Del(ctx, key)
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
		c.logger.Error("verified email account disappeared during onboarding", zap.String("email", st.Email), zap.Error(err))
		_ = c.store.Del(ctx, key)
		return textReply("Something went wrong on my end. Please try again in a moment."), nil
	}
	if !existing.IsActive {
		_ = c.store.Del(ctx, key)
		return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
	}
	st.UserID = existing.ID.String()
	st.Step = stepPhone
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply("Verified. What's your phone number? Include your country code, like +2348012345678 — I'll text a code to confirm it."), nil
}

func (c *ChatOnboarder) handlePhone(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	phone, ok := normalizePhone(text, st.Country)
	if !ok {
		return textReply("Hmm, that number doesn't look right. Send it with your country code, like +2348012345678."), nil
	}
	if _, err := c.verifier.GenerateAndSendCode(ctx, "phone", phone); err != nil {
		c.logger.Warn("onboarding OTP send failed", zap.Error(err))
		return textReply(otpSendErrorMessage(err)), nil
	}
	st.Phone = phone
	st.Step = stepOTP
	st.OTPAttempts = 0
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(fmt.Sprintf("I just texted a 6-digit code to %s. Reply with it here to confirm.", phone)), nil
}

func (c *ChatOnboarder) handleOTP(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	code := digitsOnly(text)
	if len(code) != 6 {
		return textReply("That doesn't look like the 6-digit code. Reply with the code I texted you."), nil
	}
	ok, err := c.verifier.VerifyCode(ctx, "phone", st.Phone, code)
	if err != nil || !ok {
		st.OTPAttempts++
		if st.OTPAttempts >= maxOnboardingOTPAttempts {
			_ = c.store.Del(ctx, key)
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
			_ = c.store.Del(ctx, key)
			return textReply("That phone number belongs to an inactive account. Please reach out to support@userail.money for help."), nil
		}
		return textReply("Something went wrong setting up your account. Mind trying that code again in a moment?"), nil
	}
	st.Step = stepConsent
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(c.consentMessage()), nil
}

func (c *ChatOnboarder) handleConsent(ctx context.Context, key string, st *onboardingState, in OnboardInput, text string) (*PlatformReply, error) {
	if !isAffirmative(text) {
		return textReply("No rush — reply YES to agree to RAIL's Terms of Service and Privacy Policy, and I'll finish setting up your account."), nil
	}
	uid, err := uuid.Parse(st.UserID)
	if err != nil {
		c.logger.Error("onboarding consent with unparseable user id", zap.String("user_id", st.UserID), zap.Error(err))
		return textReply("Something went wrong on my end. Text me again and we'll pick this back up."), nil
	}
	if err := c.provisioner.ProvisionPhoneFirstUser(ctx, uid, st.FirstName, st.Country, st.Phone); err != nil {
		c.logger.Error("phone-first provisioning failed", zap.Error(err), zap.String("user_id", st.UserID))
		return textReply("I couldn't finish setting up your account just now. Reply YES to try again."), nil
	}
	if _, err := c.linker.LinkVerified(ctx, uid, in.Platform, in.SenderID); err != nil {
		c.logger.Error("phone-first auto-link failed", zap.Error(err), zap.String("user_id", st.UserID))
		return textReply("I couldn't finish linking this chat to your account just now. Reply YES to try again."), nil
	}
	_ = c.store.Del(ctx, key)
	return textReply(c.completionMessage(st.FirstName, st.Country)), nil
}

// ensureUser finds an existing user by the verified email (already proven) or
// verified phone, or creates a new passwordless one. Idempotent across retries
// via the stored user id.
func (c *ChatOnboarder) ensureUser(ctx context.Context, st *onboardingState) error {
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

func (c *ChatOnboarder) save(ctx context.Context, key string, st onboardingState) error {
	if err := c.store.Set(ctx, key, st, onboardingSessionTTL); err != nil {
		c.logger.Warn("onboarding state save failed", zap.Error(err))
		return Retryable(fmt.Errorf("save onboarding state: %w", err))
	}
	return nil
}

func (c *ChatOnboarder) appLink() string {
	if c.appURL == "" {
		return "the RAIL app"
	}
	return c.appURL
}

func (c *ChatOnboarder) emailPrompt() string {
	return "What's your email address? If you already have a RAIL account, use the same email — I'll check and link this chat. Reply 'skip' if you'd rather not say."
}

func (c *ChatOnboarder) introMessage() string {
	return fmt.Sprintf("Hey! I'm Miriam, your money assistant at RAIL. I can set you up with an account right here in a couple of messages.\n\nFirst, grab the RAIL app: %s. That's where you'll approve any money moves with Face ID, so it's worth downloading now.\n\nTo get started, what's your first name?", c.appLink())
}

func (c *ChatOnboarder) consentMessage() string {
	return "Almost done. Do you agree to RAIL's Terms of Service and Privacy Policy? Reply YES to continue."
}

func (c *ChatOnboarder) completionMessage(name, country string) string {
	greeting := "You're all set"
	if strings.TrimSpace(name) != "" {
		greeting = fmt.Sprintf("You're all set, %s", name)
	}

	countryLine := "Your money will be kept safe in stable dollars, ready to spend or invest whenever you are."
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "NG":
		countryLine = "I'll keep your money safe in stable dollars and convert to naira the moment you need it."
	case "GH":
		countryLine = "I'll keep your money safe in stable dollars and convert to cedis the moment you need it."
	case "KE":
		countryLine = "I'll keep your money safe in stable dollars and convert to shillings the moment you need it."
	}

	return fmt.Sprintf("%s! Your RAIL account is ready and your wallet is being created now.\n\n%s\n\nOpen the app to approve money moves, add money, and unlock USD accounts, cards and investing: %s\n\nOne last thing — what are you saving for? Text me a goal (like an emergency fund or a new phone) and I'll set up a plan and track it for you. Ask me anything any time.", greeting, countryLine, c.appLink())
}

func textReply(s string) *PlatformReply {
	return &PlatformReply{Text: s}
}

var (
	nonDigits     = regexp.MustCompile(`\D`)
	e164Pattern   = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
	nameCleanRe   = regexp.MustCompile(`[^\p{L}'-]`)
	lettersOnlyRe = regexp.MustCompile(`[^\p{L}]`)
)

func normalizeEmail(input string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	s = strings.TrimPrefix(s, "mailto:")
	if s == "" || s == "skip" {
		return ""
	}
	if _, err := mail.ParseAddress(s); err != nil {
		return ""
	}
	return s
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
	case "yes", "y", "yeah", "yep", "yup", "sure", "ok", "okay", "agree", "i agree", "yes i agree", "confirm", "accept", "i accept":
		return true
	}
	return strings.HasPrefix(s, "yes")
}

func otpSendErrorMessage(err error) string {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "too many") {
		return "You've asked for a few codes already — give it a minute, then text me to try again."
	}
	return "I couldn't send the code just now. Mind trying again in a moment?"
}
