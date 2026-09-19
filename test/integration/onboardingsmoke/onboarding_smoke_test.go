//go:build integration

package onboardingsmoke

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Chat-first onboarding smoke test.
//
// This is the test that answers "has a real account ever been created by texting
// the bot?". It drives the REAL inbound surface (signed bridge HMAC requests),
// reads the REAL Redis session state to decide each turn, and asserts on the
// resulting database rows — not on HTTP 200s. That matters: the whole point of
// onboarding is state change, and a 200 proves nothing about it.
//
// Required environment:
//
//	SMOKE_BACKEND_URL        e.g. http://localhost:8080 (a running RAIL backend)
//	SMOKE_BRIDGE_HMAC_SECRET must equal the backend's bridge_hmac_secret
//	SMOKE_REDIS_URL          the same Redis the backend uses
//
// Optional (enables the account-row assertions — strongly recommended):
//
//	SMOKE_DATABASE_URL       postgres DSN for the same database
//
// Run with:  go test -tags=integration ./test/integration/ -run OnboardingSmoke -v
//
// The conversation is driven by OBSERVING the session phase after each turn
// rather than by a fixed script, so it survives changes to the greeting or to
// how many turns the conversational brain takes to reach the email ask.

type smokeEnv struct {
	baseURL string
	secret  string
	db      *sql.DB // nil when SMOKE_DATABASE_URL is unset
	rdb     *redis.Client
}

func newSmokeEnv(t *testing.T) *smokeEnv {
	t.Helper()
	env := &smokeEnv{
		baseURL: strings.TrimRight(getEnvOrSkip(t, "SMOKE_BACKEND_URL"), "/"),
		secret:  getEnvOrSkip(t, "SMOKE_BRIDGE_HMAC_SECRET"),
	}

	redisURL := getEnvOrSkip(t, "SMOKE_REDIS_URL")
	opts, err := redis.ParseURL(redisURL)
	require.NoError(t, err, "parse SMOKE_REDIS_URL")
	env.rdb = redis.NewClient(opts)
	require.NoError(t, env.rdb.Ping(context.Background()).Err(), "reach Redis")

	if dsn := getEnvOrSkipOptional("SMOKE_DATABASE_URL"); dsn != "" {
		db, err := sql.Open("postgres", dsn)
		require.NoError(t, err, "open database")
		require.NoError(t, db.Ping(), "reach database")
		env.db = db
	} else {
		t.Log("SMOKE_DATABASE_URL is unset — skipping the account-row assertions")
	}
	t.Cleanup(func() {
		if env.db != nil {
			_ = env.db.Close()
		}
		_ = env.rdb.Close()
	})
	return env
}

// getEnvOrSkip fails the test with a clear reason when a required variable is
// unset. Local to this package on purpose: the sibling test/integration package
// does not currently compile, and this smoke test must stay runnable.
func getEnvOrSkip(t *testing.T, key string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Skipf("set %s to run this smoke test", key)
	}
	return value
}

// getEnvOrSkipOptional returns "" instead of skipping when a variable is unset.
func getEnvOrSkipOptional(key string) string { return strings.TrimSpace(os.Getenv(key)) }

// smokeInbound posts one signed inbound message, exactly as the bridge does:
// HMAC-SHA256 over "timestamp.nonce.body" with the shared secret.
func (e *smokeEnv) smokeInbound(t *testing.T, sender, text string) int {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"platform":     "imessage",
		"user_id":      sender,
		"thread_id":    sender,
		"space_id":     sender,
		"text":         text,
		"attempt":      1,
		"max_attempts": 1,
	})
	require.NoError(t, err)

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := uuid.NewString()
	mac := hmac.New(sha256.New, []byte(e.secret))
	mac.Write([]byte(timestamp + "." + nonce + "." + string(body)))
	signature := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(
		http.MethodPost, e.baseURL+"/api/v1/platform/inbound", bytes.NewReader(body),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HMAC-Timestamp", timestamp)
	req.Header.Set("X-HMAC-Nonce", nonce)
	req.Header.Set("X-HMAC-SHA256", signature)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// smokePhase reads the live session phase, which is what makes this test
// self-driving. Empty means no session exists (never started, or cleared on
// completion).
func (e *smokeEnv) smokePhase(t *testing.T, sender string) string {
	t.Helper()
	raw, err := e.rdb.Get(context.Background(), "onboarding:imessage:"+sender).Result()
	if err == redis.Nil {
		return ""
	}
	require.NoError(t, err, "read onboarding session")
	var state struct {
		Phase string `json:"phase"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &state), "decode session: %s", raw)
	return state.Phase
}

// smokeEmailCode reads the issued code straight out of Redis. The verification
// service stores it plaintext under "verification:email:<address>", which is how
// a test can complete the flow without an inbox.
func (e *smokeEnv) smokeEmailCode(t *testing.T, address string) string {
	t.Helper()
	raw, err := e.rdb.Get(context.Background(), "verification:email:"+address).Result()
	require.NoError(t, err, "expected an email verification code to have been issued")
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &payload), "decode code payload: %s", raw)
	for key, value := range payload {
		if strings.EqualFold(key, "code") {
			if code, ok := value.(string); ok && code != "" {
				return code
			}
		}
	}
	t.Fatalf("no code field in the verification payload: %s", raw)
	return ""
}

// drive runs the conversation until the session clears (onboarding completed),
// failing loudly if the flow ever asks for a phone before an address — that is
// precisely the regression the email anchor exists to remove.
func (e *smokeEnv) drive(t *testing.T, sender, address string) {
	t.Helper()
	scripted := []string{"hi", "Ada"}
	next := 0

	for turn := 0; turn < 12; turn++ {
		phase := e.smokePhase(t, sender)

		var reply string
		switch phase {
		case "":
			// No session yet (first turn) or already cleared (done).
			if turn == 0 {
				reply = scripted[next]
				next++
			} else {
				return // cleared => completed
			}
		case "awaiting_email":
			reply = address
		case "awaiting_email_otp":
			reply = e.smokeEmailCode(t, address)
		case "awaiting_consent":
			reply = "I agree"
		case "awaiting_phone", "awaiting_otp":
			t.Fatalf("email-first onboarding must not ask for a phone before an address "+
				"(session reached %q), so the anchor flip is not in effect", phase)
		default:
			// Conversational phase: advance the script, then offer the address.
			if next < len(scripted) {
				reply = scripted[next]
				next++
			} else {
				reply = address
			}
		}

		status := e.smokeInbound(t, sender, reply)
		require.Equal(t, http.StatusOK, status,
			"inbound %q (phase %q) should be accepted", reply, phase)

		if phase == "awaiting_consent" {
			// Consent was the last required step; the next loop sees a cleared session.
			continue
		}
	}
	t.Fatal("onboarding never completed within 12 turns")
}

// TestOnboardingSmoke_EmailFirstSignup is the headline run: a brand-new sender
// creates an account on email alone.
func TestOnboardingSmoke_EmailFirstSignup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	env := newSmokeEnv(t)
	ctx := context.Background()

	stamp := time.Now().UnixNano()
	sender := fmt.Sprintf("+1555%09d", stamp%1_000_000_000)
	address := fmt.Sprintf("smoke-%d@example.com", stamp)

	// Before: no session.
	require.Equal(t, "", env.smokePhase(t, sender))

	env.drive(t, sender, address)

	// Session is cleared only on a completed onboarding.
	require.Equal(t, "", env.smokePhase(t, sender),
		"a completed onboarding must clear its session")

	if env.db == nil {
		t.Log("PASS (HTTP+Redis only): the flow completed without asking for a phone")
		return
	}

	// The account exists, anchored on the address, with the address proven.
	var userID string
	var emailVerified, isActive bool
	err := env.db.QueryRowContext(ctx,
		`SELECT id, email_verified, is_active FROM users WHERE email = $1`, address,
	).Scan(&userID, &emailVerified, &isActive)
	require.NoError(t, err, "expected an account anchored on %s", address)
	require.True(t, emailVerified, "the proven address must be recorded as verified")
	require.True(t, isActive)

	// The chat handle is bound to that account — the thing that makes the next
	// message land in the same place.
	var boundUser string
	var linkedAt *time.Time
	err = env.db.QueryRowContext(ctx,
		`SELECT user_id, linked_at FROM platform_identities
		  WHERE platform = 'imessage' AND platform_user_id = $1`, sender,
	).Scan(&boundUser, &linkedAt)
	require.NoError(t, err, "expected the sender handle to be linked")
	require.Equal(t, userID, boundUser, "the handle must be bound to the new account")
	require.NotNil(t, linkedAt, "the link must be marked as completed")

	// No duplicate accounts were created for this person.
	var count int
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE email = $1`, address).Scan(&count))
	require.Equal(t, 1, count)

	// Preconditions for Miriam to act on this account: the Python RBAC role is
	// derived from email verification, so a false value here (or an inactive
	// account) would leave the person unable to move money even though
	// onboarding "completed".
	var kycTier int
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT coalesce(kyc_tier, 0) FROM users WHERE id = $1`, userID).Scan(&kycTier))
	require.GreaterOrEqual(t, kycTier, 1,
		"a chat-onboarded account must reach at least tier 1 to be usable")

	t.Logf("PASS: account %s created by chat, address verified, handle linked, tier %d",
		userID, kycTier)
	t.Log("NOTE: this proves the account exists and is capable. It does NOT move money — " +
		"that is the separate manual step in the test's doc comment.")
}

// TestOnboardingSmoke_ExistingAccountClaimedOnEmailAlone covers the original
// ask: someone who already has a RAIL account texts the bot, and is let into
// that account on email alone — no phone, no second account.
func TestOnboardingSmoke_ExistingAccountClaimedOnEmailAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	env := newSmokeEnv(t)
	require.NotNil(t, env.db, "this test needs SMOKE_DATABASE_URL to seed the existing account")
	ctx := context.Background()

	stamp := time.Now().UnixNano()
	sender := fmt.Sprintf("+1555%09d", (stamp/7)%1_000_000_000)
	address := fmt.Sprintf("existing-%d@example.com", stamp)

	// Seed an account that has never been linked to a chat.
	var ownerID string
	require.NoError(t, env.db.QueryRowContext(ctx,
		`INSERT INTO users (email, email_verified, is_active, onboarding_status, kyc_status)
		 VALUES ($1, TRUE, TRUE, 'completed', 'approved') RETURNING id`, address,
	).Scan(&ownerID))
	t.Cleanup(func() {
		_, _ = env.db.ExecContext(ctx, `DELETE FROM platform_identities WHERE user_id = $1`, ownerID)
		_, _ = env.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, ownerID)
	})

	env.drive(t, sender, address)

	// The chat is bound to the EXISTING account, and no duplicate was created.
	var boundUser string
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT user_id FROM platform_identities
		  WHERE platform = 'imessage' AND platform_user_id = $1`, sender,
	).Scan(&boundUser))
	require.Equal(t, ownerID, boundUser, "the handle must bind to the existing account")

	var count int
	require.NoError(t, env.db.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE email = $1`, address).Scan(&count))
	require.Equal(t, 1, count, "claiming must not create a second account")

	t.Logf("PASS: existing account %s claimed by chat on email alone", ownerID)
}
