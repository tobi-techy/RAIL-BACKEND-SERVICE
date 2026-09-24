package confirmation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

const (
	emailOTPLength     = 6
	emailOTPTTL        = 10 * time.Minute
	emailOTPCooldown   = 30 * time.Second
	emailOTPMaxAttempt = 5
)

// EmailSender delivers confirmation OTP codes. Production wires the shared
// Unosend/Resend EmailService.SendVerificationEmail path.
type EmailSender interface {
	SendVerificationEmail(ctx context.Context, email, code string) error
}

// UserEmailLookup resolves the account email that receives the OTP.
type UserEmailLookup interface {
	EmailForUser(ctx context.Context, userID uuid.UUID) (string, error)
}

// OTPStore persists hashed confirmation OTPs. Memory is the default (tests);
// production should back this with Redis so replicas share codes.
type OTPStore interface {
	Put(ctx context.Context, confirmationID uuid.UUID, hash string, ttl time.Duration) error
	Get(ctx context.Context, confirmationID uuid.UUID) (hash string, ok bool, err error)
	Delete(ctx context.Context, confirmationID uuid.UUID) error
}

type memoryOTPStore struct {
	mu   sync.Mutex
	data map[uuid.UUID]memoryOTPEntry
}

type memoryOTPEntry struct {
	hash      string
	expiresAt time.Time
}

func newMemoryOTPStore() *memoryOTPStore {
	return &memoryOTPStore{data: map[uuid.UUID]memoryOTPEntry{}}
}

func (s *memoryOTPStore) Put(_ context.Context, confirmationID uuid.UUID, hash string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[confirmationID] = memoryOTPEntry{hash: hash, expiresAt: time.Now().UTC().Add(ttl)}
	return nil
}

func (s *memoryOTPStore) Get(_ context.Context, confirmationID uuid.UUID) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[confirmationID]
	if !ok || !time.Now().UTC().Before(e.expiresAt) {
		delete(s.data, confirmationID)
		return "", false, nil
	}
	return e.hash, true, nil
}

func (s *memoryOTPStore) Delete(_ context.Context, confirmationID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, confirmationID)
	return nil
}

// RedisOTPStore backs confirmation OTPs with the shared Redis client.
type RedisOTPStore struct {
	Client interface {
		Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
		Get(ctx context.Context, key string, dest interface{}) error
		Del(ctx context.Context, key string) error
	}
}

func otpRedisKey(id uuid.UUID) string { return "confirmation_otp:" + id.String() }

func (s *RedisOTPStore) Put(ctx context.Context, confirmationID uuid.UUID, hash string, ttl time.Duration) error {
	if s == nil || s.Client == nil {
		return fmt.Errorf("confirmation OTP redis store not configured")
	}
	return s.Client.Set(ctx, otpRedisKey(confirmationID), hash, ttl)
}

func (s *RedisOTPStore) Get(ctx context.Context, confirmationID uuid.UUID) (string, bool, error) {
	if s == nil || s.Client == nil {
		return "", false, fmt.Errorf("confirmation OTP redis store not configured")
	}
	var hash string
	if err := s.Client.Get(ctx, otpRedisKey(confirmationID), &hash); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "nil") || strings.Contains(msg, "not found") {
			return "", false, nil
		}
		return "", false, err
	}
	if hash == "" {
		return "", false, nil
	}
	return hash, true, nil
}

func (s *RedisOTPStore) Delete(ctx context.Context, confirmationID uuid.UUID) error {
	if s == nil || s.Client == nil {
		return nil
	}
	return s.Client.Del(ctx, otpRedisKey(confirmationID))
}

// SetDemoEmailOTP enables the hackathon email-OTP approve path and makes the
// Face ID / token-only Approve endpoints fail closed. Default off so production
// Face ID behavior is unchanged.
func (s *Service) SetDemoEmailOTP(enabled bool) { s.demoEmailOTP = enabled }

// DemoEmailOTP reports whether the hackathon email-OTP path is active.
func (s *Service) DemoEmailOTP() bool { return s.demoEmailOTP }

// SetEmailOTPDeps wires email delivery + user email lookup + OTP persistence.
// A nil store keeps/creates the in-memory store (suitable for unit tests).
func (s *Service) SetEmailOTPDeps(mailer EmailSender, users UserEmailLookup, store OTPStore) {
	s.emailSender = mailer
	s.userEmails = users
	if store != nil {
		s.otpStore = store
	} else if s.otpStore == nil {
		s.otpStore = newMemoryOTPStore()
	}
}

// SendEmailOTPResult is returned after a successful send. The code is never included.
type SendEmailOTPResult struct {
	MaskedEmail string        `json:"masked_email"`
	ExpiresIn   time.Duration `json:"expires_in"`
	Cooldown    time.Duration `json:"cooldown"`
}

// SendEmailOTP mails a 6-digit code for a pending confirmation. Fail-closed:
// requires CONFIRMATION_DEMO_EMAIL_OTP, a valid card token, a resolvable user
// email, and a configured mailer. Does not move money.
func (s *Service) SendEmailOTP(ctx context.Context, id uuid.UUID, token string) (*SendEmailOTPResult, error) {
	if !s.demoEmailOTP {
		return nil, fmt.Errorf("email OTP confirmation disabled (set CONFIRMATION_DEMO_EMAIL_OTP=true)")
	}
	if s.emailSender == nil || s.userEmails == nil {
		return nil, fmt.Errorf("email OTP confirmation misconfigured (mailer/user lookup missing)")
	}
	if s.otpStore == nil {
		s.otpStore = newMemoryOTPStore()
	}
	if err := s.VerifyToken(id, token); err != nil {
		return nil, err
	}
	c, ok := s.store.Load(id)
	if !ok {
		return nil, fmt.Errorf("confirmation not found")
	}
	if c.TokenUsed || c.IsTerminal() {
		return nil, fmt.Errorf("confirmation is no longer pending")
	}
	if c.IsExpired(s.now()) {
		s.transition(ctx, c, entities.ConfirmationExpired, "", "ttl elapsed on otp send")
		return nil, fmt.Errorf("confirmation expired")
	}

	s.mu.Lock()
	if last, ok := s.otpSentAt[id]; ok && s.now().Sub(last) < emailOTPCooldown {
		remaining := emailOTPCooldown - s.now().Sub(last)
		s.mu.Unlock()
		return nil, fmt.Errorf("OTP cooldown active; retry in %s", remaining.Round(time.Second))
	}
	s.mu.Unlock()

	email, err := s.userEmails.EmailForUser(ctx, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("resolve user email: %w", err)
	}
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("user has no email on file for OTP delivery")
	}

	code, err := generateDigits(emailOTPLength)
	if err != nil {
		return nil, fmt.Errorf("generate OTP: %w", err)
	}
	hash := hashOTP(id, code)
	if err := s.otpStore.Put(ctx, id, hash, emailOTPTTL); err != nil {
		return nil, fmt.Errorf("store OTP: %w", err)
	}

	if err := s.emailSender.SendVerificationEmail(ctx, email, code); err != nil {
		_ = s.otpStore.Delete(ctx, id)
		return nil, fmt.Errorf("send OTP email: %w", err)
	}

	s.mu.Lock()
	s.otpSentAt[id] = s.now()
	s.otpAttempts[id] = 0
	s.mu.Unlock()

	s.auditOf(c, c.State, c.State, "", "email_otp_sent:"+maskEmail(email), nil)

	return &SendEmailOTPResult{
		MaskedEmail: maskEmail(email),
		ExpiresIn:   emailOTPTTL,
		Cooldown:    emailOTPCooldown,
	}, nil
}

// ApproveWithEmailOTP verifies the emailed code then runs the same settle path
// as Face ID approve, recording assurance=email_otp. Fail-closed when the demo
// flag is off, the code is wrong/expired, or attempts are exhausted.
func (s *Service) ApproveWithEmailOTP(ctx context.Context, userID, id uuid.UUID, token, code string) (*entities.Confirmation, error) {
	if !s.demoEmailOTP {
		return nil, fmt.Errorf("email OTP confirmation disabled (set CONFIRMATION_DEMO_EMAIL_OTP=true)")
	}
	if s.otpStore == nil {
		s.otpStore = newMemoryOTPStore()
	}
	code = strings.TrimSpace(code)
	if len(code) != emailOTPLength {
		return nil, fmt.Errorf("invalid OTP code")
	}
	if err := s.VerifyToken(id, token); err != nil {
		return nil, err
	}
	c, ok := s.store.Load(id)
	if !ok {
		return nil, fmt.Errorf("confirmation not found")
	}
	if c.UserID != userID {
		return nil, fmt.Errorf("confirmation does not belong to user")
	}
	if c.TokenUsed || c.IsTerminal() {
		out, _ := s.store.Load(id)
		return out, nil
	}
	if c.IsExpired(s.now()) {
		s.transition(ctx, c, entities.ConfirmationExpired, "", "ttl elapsed on otp approve")
		out, _ := s.store.Load(id)
		return out, nil
	}

	s.mu.Lock()
	attempts := s.otpAttempts[id]
	if attempts >= emailOTPMaxAttempt {
		s.mu.Unlock()
		return nil, fmt.Errorf("too many invalid OTP attempts; request a new code")
	}
	s.mu.Unlock()

	storedHash, ok, err := s.otpStore.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("OTP lookup failed (fail-closed): %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("OTP expired or not sent; request a new code")
	}
	want := hashOTP(id, code)
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(want)) != 1 {
		s.mu.Lock()
		s.otpAttempts[id]++
		n := s.otpAttempts[id]
		s.mu.Unlock()
		if n >= emailOTPMaxAttempt {
			_ = s.otpStore.Delete(ctx, id)
			return nil, fmt.Errorf("too many invalid OTP attempts; request a new code")
		}
		return nil, fmt.Errorf("invalid OTP code")
	}
	// Consume OTP before settling so replays cannot reuse the code even if the
	// card token consume races.
	_ = s.otpStore.Delete(ctx, id)
	s.mu.Lock()
	delete(s.otpAttempts, id)
	delete(s.otpSentAt, id)
	s.mu.Unlock()

	return s.completeApproval(ctx, userID, id, c, AssuranceEmailOTP, "", "email_otp verified")
}

// completeApproval is the shared post-proof settle path used by Face ID and
// email OTP: reserve execute key, consume token, transition, run executor.
func (s *Service) completeApproval(ctx context.Context, userID, id uuid.UUID, c *entities.Confirmation, assurance, enrolledKeyID, note string) (*entities.Confirmation, error) {
	s.mu.Lock()
	ex, registered := s.executors[c.Action]
	s.mu.Unlock()
	if !registered {
		return nil, fmt.Errorf("no executor for action %q (fail-closed)", c.Action)
	}
	s.mu.Lock()
	fresh, ok := s.store.Load(id)
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("confirmation not found")
	}
	if fresh.TokenUsed || fresh.IsTerminal() {
		s.mu.Unlock()
		out, _ := s.store.Load(id)
		return out, nil
	}
	if _, inFlight := s.executed[c.ExecuteKey]; inFlight {
		s.mu.Unlock()
		return nil, fmt.Errorf("approval already in progress (retry for terminal state)")
	}
	s.executed[c.ExecuteKey] = executeInFlight
	c.Assurance = assurance
	c.EnrolledKeyID = enrolledKeyID
	c.TokenUsed = true
	s.store.Save(c)
	s.mu.Unlock()
	s.transition(ctx, c, entities.ConfirmationAuthenticating, "pass", note+" (assurance="+assurance+")")
	s.transition(ctx, c, entities.ConfirmationApproved, "pass", "server accepted")

	summary, err := ex(ctx, userID, c)
	if err != nil {
		s.mu.Lock()
		delete(s.executed, c.ExecuteKey)
		s.mu.Unlock()
		s.transition(ctx, c, entities.ConfirmationFailed, "pass", err.Error())
		return nil, err
	}
	s.mu.Lock()
	s.executed[c.ExecuteKey] = summary
	s.mu.Unlock()
	s.transition(ctx, c, entities.ConfirmationCompleted, "pass", summary)
	out, _ := s.store.Load(id)
	return out, nil
}

func generateDigits(n int) (string, error) {
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		b.WriteByte(byte('0' + v.Int64()))
	}
	return b.String(), nil
}

func hashOTP(confirmationID uuid.UUID, code string) string {
	sum := sha256.Sum256([]byte(confirmationID.String() + ":" + code))
	return hex.EncodeToString(sum[:])
}

func maskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	local, domain := parts[0], parts[1]
	switch {
	case len(local) <= 1:
		local = "*"
	case len(local) == 2:
		local = string(local[0]) + "*"
	default:
		local = string(local[0]) + strings.Repeat("*", len(local)-2) + string(local[len(local)-1])
	}
	return local + "@" + domain
}
