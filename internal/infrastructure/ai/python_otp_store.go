package ai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// otpKeyPrefix scopes Redis keys for platform-OTP-confirmed Python actions.
const otpKeyPrefix = "platform_otp:"

// pendingPythonAction is the confirmation record staged when the Python agent
// asks for a mutation confirmation. The six-digit code is stored as a SHA-256
// hash; only the action + attempt counter live in plaintext.
type pendingPythonAction struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
	Summary   string                 `json:"summary"`
	CodeHash  string                 `json:"code_hash"`
	Attempts  int                    `json:"attempts"`
	ExpiresAt time.Time              `json:"expires_at"`
}

// OtpStore stages and verifies email-OTP confirmations for mutations proposed
// by the Python agent. On verification it returns the approved action ready to
// be replayed to Python. Fail-closed: any Redis error returns an error.
type OtpStore struct {
	redis       cache.RedisClient
	otpTTL      time.Duration
	maxAttempts int
	logger      *zap.Logger
}

// NewOtpStore builds a Redis-backed OTP store.
func NewOtpStore(redis cache.RedisClient, otpTTL time.Duration, maxAttempts int, logger *zap.Logger) *OtpStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	if otpTTL <= 0 {
		otpTTL = 10 * time.Minute
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return &OtpStore{redis: redis, otpTTL: otpTTL, maxAttempts: maxAttempts, logger: logger}
}

func otpKey(convID uuid.UUID) string {
	return otpKeyPrefix + convID.String()
}

// generateCode returns a cryptographically-random six-digit code.
func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate random code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func constantTimeEquals(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Create stages a new OTP confirmation for a Python-proposed mutation and
// returns the plaintext six-digit code to email to the user. If a stale entry
// exists it is replaced.
func (s *OtpStore) Create(ctx context.Context, convID uuid.UUID, tool string, arguments map[string]interface{}, summary string) (string, error) {
	code, err := generateCode()
	if err != nil {
		return "", err
	}
	entry := pendingPythonAction{
		Tool:      tool,
		Arguments: arguments,
		Summary:   summary,
		CodeHash:  hashCode(code),
		Attempts:  0,
		ExpiresAt: time.Now().Add(s.otpTTL),
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("marshal otp entry: %w", err)
	}
	if err := s.redis.Set(ctx, otpKey(convID), string(raw), s.otpTTL); err != nil {
		return "", fmt.Errorf("store otp entry: %w", err)
	}
	return code, nil
}

// Verify checks a submitted code against the staged entry. It returns the
// approved action when the code matches before expiry, nil/error otherwise.
// Wrong-guess attempts are tracked and the entry is removed once the attempt
// budget is exhausted or the TTL expires.
func (s *OtpStore) Verify(ctx context.Context, convID uuid.UUID, code string) (*PythonApprovedAction, error) {
	entry, err := s.load(ctx, convID)
	if err != nil || entry == nil {
		// No staged confirmation (miss, expired TTL, or lost entry): this must
		// never look like a silent success — the caller replies asking the user
		// to re-request the action.
		return nil, fmt.Errorf("invalid_confirmation: No confirmation is pending here. Ask me again and I'll set it up fresh.")
	}
	if time.Now().After(entry.ExpiresAt) {
		s.Delete(ctx, convID)
		return nil, fmt.Errorf("invalid_confirmation: That code has expired. Ask me again and I'll send a fresh one.")
	}
	if entry.Attempts >= s.maxAttempts {
		s.Delete(ctx, convID)
		return nil, fmt.Errorf("invalid_confirmation: Too many wrong attempts. Ask me again and I'll send a fresh code.")
	}
	if !constantTimeEquals(entry.CodeHash, hashCode(code)) {
		entry.Attempts++
		raw, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			s.Delete(ctx, convID)
			return nil, fmt.Errorf("invalid_confirmation: That code didn't match. Ask me again and I'll send a fresh one.")
		}
		_ = s.redis.Set(ctx, otpKey(convID), string(raw), s.otpTTL) // best-effort attempt bump
		return nil, fmt.Errorf("invalid_confirmation: That code didn't match. Reply with the code I emailed you.")
	}

	// Success — consume the entry before returning so it can't be replayed.
	s.Delete(ctx, convID)
	return &PythonApprovedAction{
		Tool:      entry.Tool,
		Arguments: entry.Arguments,
	}, nil
}

// Peek returns whether a confirmation is currently staged for the conversation.
// Used to interpret bare YES/NO replies and stray poll votes.
func (s *OtpStore) Peek(ctx context.Context, convID uuid.UUID) (*PythonApprovedAction, bool) {
	entry, err := s.load(ctx, convID)
	if err != nil || entry == nil {
		return nil, false
	}
	return &PythonApprovedAction{
		Tool:      entry.Tool,
		Arguments: entry.Arguments,
	}, true
}

// DryPeek is Peek without the return value — just presence.
func (s *OtpStore) DryPeek(ctx context.Context, convID uuid.UUID) bool {
	_, ok := s.Peek(ctx, convID)
	return ok
}

// Delete removes a staged confirmation (after success, cancel, or expiry).
func (s *OtpStore) Delete(ctx context.Context, convID uuid.UUID) {
	_ = s.redis.Del(ctx, otpKey(convID))
}

func (s *OtpStore) load(ctx context.Context, convID uuid.UUID) (*pendingPythonAction, error) {
	var raw string
	if err := s.redis.Get(ctx, otpKey(convID), &raw); err != nil {
		s.logger.Debug("otp lookup miss",
			zap.String("conversation", convID.String()),
			zap.Error(err))
		return nil, nil // miss is not an error; caller treats as "no pending"
	}
	if raw == "" {
		return nil, nil
	}
	var entry pendingPythonAction
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		s.logger.Warn("malformed otp entry",
			zap.String("conversation", convID.String()),
			zap.Error(err))
		return nil, nil
	}
	return &entry, nil
}