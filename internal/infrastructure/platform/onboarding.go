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
	stepName           onboardingStep = "awaiting_name"
	stepConfirmContact onboardingStep = "awaiting_contact_confirm"
	stepCountry        onboardingStep = "awaiting_country"
	stepEmail          onboardingStep = "awaiting_email"
	stepEmailOTP       onboardingStep = "awaiting_email_otp"
	stepPhone          onboardingStep = "awaiting_phone"
	stepOTP            onboardingStep = "awaiting_otp"
	stepConsent        onboardingStep = "awaiting_consent"
)

// onboardingState is the per-sender progress persisted in Redis.
type onboardingState struct {
	Step             onboardingStep `json:"step"`
	FirstName        string         `json:"first_name,omitempty"`
	Country          string         `json:"country,omitempty"`
	CountryAttempts  int            `json:"country_attempts,omitempty"`
	Email            string         `json:"email,omitempty"`
	PendingEmail     string         `json:"pending_email,omitempty"`
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
	Contact  *SharedContact
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
	babySteps   BabyStepsSeeder
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

// Handle advances the onboarding conversation by one step and returns the reply
// to send back. A nil reply means nothing should be sent.
func (c *ChatOnboarder) Handle(ctx context.Context, in OnboardInput) (*PlatformReply, error) {
	key := onboardingKey(in.Platform, in.SenderID)
	text := strings.TrimSpace(in.Text)
	contact := in.Contact

	var st onboardingState
	if err := c.store.Get(ctx, key, &st); err != nil || st.Step == "" {
		st = onboardingState{Step: stepName}
		if contact != nil {
			c.mergeContact(&st, contact)
			if reply, err := c.afterContact(ctx, key, &st); err != nil {
				return nil, err
			} else if reply != nil {
				return reply, nil
			}
		}
		if err := c.save(ctx, key, st); err != nil {
			return nil, err
		}
		return textReply(c.introMessage(in.Platform)), nil
	}

	if contact != nil {
		c.mergeContact(&st, contact)
		if reply, err := c.afterContact(ctx, key, &st); err != nil {
			return nil, err
		} else if reply != nil {
			return reply, nil
		}
	}

	switch st.Step {
	case stepName:
		return c.handleName(ctx, key, &st, text)
	case stepConfirmContact:
		return c.handleConfirmContact(ctx, key, &st, text)
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
		c.clear(ctx, key)
		return textReply(c.introMessage(in.Platform)), nil
	}
}

func (c *ChatOnboarder) mergeContact(st *onboardingState, contact *SharedContact) {
	if st == nil || contact == nil {
		return
	}
	if st.FirstName == "" {
		if n := contact.FirstNameResolved(); n != "" {
			st.FirstName = n
		}
	}
	if st.Email == "" && st.PendingEmail == "" {
		st.PendingEmail = contact.PrimaryEmail()
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

// afterContact jumps the state machine when a shared card filled enough fields.
// Returns a non-nil reply when the card advanced the conversation on its own.
func (c *ChatOnboarder) afterContact(ctx context.Context, key string, st *onboardingState) (*PlatformReply, error) {
	if st.FirstName == "" {
		return nil, nil
	}

	if st.Phone != "" && st.FirstName != "" && st.Step != stepOTP && st.Step != stepConsent && st.Step != stepEmailOTP {
		st.Step = stepConfirmContact
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(c.contactConfirmMessage(st.FirstName, st.Phone)), nil
	}

	if st.Country == "" {
		st.Step = stepCountry
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(c.countryPrompt(st.FirstName)), nil
	}

	if st.Phone == "" {
		st.Step = stepPhone
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(c.phonePrompt()), nil
	}

	return nil, nil
}

func (c *ChatOnboarder) handleConfirmContact(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	if isContactReject(text) {
		st.Phone = ""
		st.Email = ""
		st.PendingEmail = ""
		st.Step = stepPhone
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply("Got it. Whose number should I text the code to? Include the country code, like +2348012345678."), nil
	}
	if text != "" && !isAffirmative(text) && !looksLikeOTP(text) {
		// They typed something else — treat as a correction of the name.
		if name := parseFirstName(text); name != "" && !strings.Contains(strings.ToLower(text), "yes") {
			// If it's clearly a short name correction, keep the phone and re-confirm.
			if len(strings.Fields(text)) == 1 {
				st.FirstName = name
				if err := c.save(ctx, key, *st); err != nil {
					return nil, err
				}
				return textReply(c.contactConfirmMessage(st.FirstName, st.Phone)), nil
			}
		}
		return textReply(c.contactConfirmMessage(st.FirstName, st.Phone)), nil
	}
	if looksLikeOTP(text) {
		return c.handleOTP(ctx, key, st, text)
	}
	if st.PendingEmail != "" {
		st.Email = st.PendingEmail
		st.PendingEmail = ""
	}
	if st.Email != "" {
		if existing, err := c.users.GetByEmail(ctx, st.Email); err == nil && existing != nil {
			return c.handleEmail(ctx, key, st, st.Email)
		}
	}
	return c.sendPhoneOTP(ctx, key, st)
}

func looksLikeOTP(text string) bool {
	return len(digitsOnly(text)) == 6
}

func (c *ChatOnboarder) handleName(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	name := parseFirstName(text)
	if name == "" {
		return textReply("What should I call you? First name is plenty."), nil
	}
	st.FirstName = name
	if st.Country == "" {
		st.Step = stepCountry
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(c.countryPrompt(name)), nil
	}
	return c.advancePastCountry(ctx, key, st)
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
			return textReply("Where's home? Nigeria, Ghana, the US? Name or code is fine."), nil
		}
	}
	st.Country = country
	return c.advancePastCountry(ctx, key, st)
}

func (c *ChatOnboarder) advancePastCountry(ctx context.Context, key string, st *onboardingState) (*PlatformReply, error) {
	// Contact already gave us an email — run the existing-account check.
	if st.Email != "" {
		return c.handleEmail(ctx, key, st, st.Email)
	}
	// Keep the optional email beat for typing-path users so we can link an
	// existing account. Contact-share users without an email skip it.
	if st.Phone != "" {
		st.Step = stepConfirmContact
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(c.contactConfirmMessage(st.FirstName, st.Phone)), nil
	}
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
		return textReply(c.phonePrompt()), nil
	}
	if email == "" {
		return textReply(c.emailPrompt()), nil
	}
	st.Email = email

	if existing, err := c.users.GetByEmail(ctx, email); err == nil && existing != nil {
		if !existing.IsActive {
			c.clear(ctx, key)
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
		recheck, rerr := c.users.GetByEmail(ctx, email)
		if rerr != nil || recheck == nil {
			c.clear(ctx, key)
			return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
		}
		if !recheck.IsActive {
			c.clear(ctx, key)
			return textReply("That account isn't active. Please reach out to support@userail.money for help."), nil
		}
		st.Step = stepEmailOTP
		st.EmailOTPAttempts = 0
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(fmt.Sprintf("I found your RAIL account. I just emailed a 6-digit code to %s. Reply with it here to confirm it's you.", email)), nil
	}

	if st.Phone != "" {
		st.Step = stepConfirmContact
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(c.contactConfirmMessage(st.FirstName, st.Phone)), nil
	}
	st.Step = stepPhone
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(c.phonePrompt()), nil
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
		st.Step = stepConfirmContact
		if err := c.save(ctx, key, *st); err != nil {
			return nil, err
		}
		return textReply(c.contactConfirmMessage(st.FirstName, st.Phone)), nil
	}
	st.Step = stepPhone
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(c.phonePrompt()), nil
}

func (c *ChatOnboarder) handlePhone(ctx context.Context, key string, st *onboardingState, text string) (*PlatformReply, error) {
	phone, ok := normalizePhone(text, st.Country)
	if !ok {
		return textReply("That number doesn't look right. Send it with the country code, like +2348012345678."), nil
	}
	st.Phone = phone
	return c.sendPhoneOTP(ctx, key, st)
}

func (c *ChatOnboarder) sendPhoneOTP(ctx context.Context, key string, st *onboardingState) (*PlatformReply, error) {
	if _, err := c.verifier.GenerateAndSendCode(ctx, "phone", st.Phone); err != nil {
		c.logger.Warn("onboarding OTP send failed", zap.Error(err))
		return textReply(otpSendErrorMessage(err)), nil
	}
	st.Step = stepOTP
	st.OTPAttempts = 0
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return textReply(fmt.Sprintf("Just texted a code to %s. Drop it here.", maskPhone(st.Phone))), nil
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
	st.Step = stepConsent
	if err := c.save(ctx, key, *st); err != nil {
		return nil, err
	}
	return c.consentReply(), nil
}

func (c *ChatOnboarder) handleConsent(ctx context.Context, key string, st *onboardingState, in OnboardInput, text string) (*PlatformReply, error) {
	if !isAffirmative(text) {
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
	if _, err := c.linker.LinkVerified(ctx, uid, in.Platform, in.SenderID); err != nil {
		c.logger.Error("phone-first auto-link failed", zap.Error(err), zap.String("user_id", st.UserID))
		return textReply("I couldn't finish linking this chat just now. Tap I agree to try again."), nil
	}
	// Fire the first-login goal seeder for the new user so the goal_progress
	// worker has a 7-step ladder to track on its next tick. Async + recover
	// so a failure here can't fail the onboarding completion.
	SeedBabyStepsOnLink(c.babySteps, uid, c.logger)
	c.clear(ctx, key)
	return &PlatformReply{
		Text:   c.completionMessage(st.FirstName, st.Country),
		Effect: EffectCelebration,
	}, nil
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
	return "Got an email I can use? Same one as a RAIL account if you already have one. Or say skip."
}

func (c *ChatOnboarder) phonePrompt() string {
	return "What's a number I can text a code to? Include the country code, like +2348012345678."
}

func (c *ChatOnboarder) countryPrompt(name string) string {
	if strings.TrimSpace(name) != "" {
		return fmt.Sprintf("Nice to meet you, %s. Where's home? Nigeria, Ghana, the US, somewhere else?", name)
	}
	return "Where's home? Nigeria, Ghana, the US, somewhere else?"
}

func (c *ChatOnboarder) contactConfirmMessage(name, phone string) string {
	who := strings.TrimSpace(name)
	if who == "" {
		who = "Okay"
	}
	return fmt.Sprintf("%s. Nice. I'll text a code to %s to make sure it's you. Then we're in. Sound right?", who, maskPhone(phone))
}

func (c *ChatOnboarder) introMessage(plat entities.Platform) string {
	if plat == entities.PlatformIMessage {
		return "Hey! I'm Miriam. I help people actually keep money, not just stare at it.\n\nEasiest way to start: share your contact. Tap the + button, then Share Contact. Or just tell me your first name."
	}
	return "Hey! I'm Miriam. I help people actually keep money, not just stare at it.\n\nWhat should I call you?"
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

func (c *ChatOnboarder) completionMessage(name, country string) string {
	who := "You're in"
	if strings.TrimSpace(name) != "" {
		who = fmt.Sprintf("You're in, %s", name)
	}

	countryLine := "Wallet's spinning up."
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "NG":
		countryLine = "Wallet's spinning up. I'll keep it in stable dollars until you need naira."
	case "GH":
		countryLine = "Wallet's spinning up. I'll keep it in stable dollars until you need cedis."
	case "KE":
		countryLine = "Wallet's spinning up. I'll keep it in stable dollars until you need shillings."
	}

	app := ""
	if c.appURL != "" {
		app = " " + c.appURL
	}
	return fmt.Sprintf("%s. %s%s\n\nWhat's money actually for, for you, right now? A trip, breathing room, something you want, anything.", who, countryLine, app)
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
