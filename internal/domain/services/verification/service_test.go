package verification

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
)

// These tests cover the failure that produced "miriam couldn't send an otp":
// the provider permanently refused the recipient (suppressed after a bounce),
// the send failed, and the attempt had already been charged against the OTP
// budget — so retrying was both pointless (it can never deliver) and costly
// (it burns 3/minute, 8/hour, 15/day and ends in a 24-hour lockout for someone
// whose only mistake was an address the provider refuses).
//
// The contract: a permanent, recipient-level failure gives the budget back; a
// transient failure keeps it charged, because a retry can genuinely succeed.

type stubEmailSender struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *stubEmailSender) SendVerificationEmail(_ context.Context, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *stubEmailSender) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func permanentDeliveryErr(recipient string) error {
	return fmt.Errorf("failed to send verification code: %w", &entities.EmailDeliveryError{
		Provider:   "unosend",
		Recipient:  recipient,
		StatusCode: 400,
		Reason:     entities.EmailReasonRecipientSuppressed,
		Permanent:  true,
	})
}

func newTestRedis(t *testing.T) (cache.RedisClient, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	port, err := strconv.Atoi(mr.Port())
	if err != nil {
		t.Fatalf("parse miniredis port %q: %v", mr.Port(), err)
	}
	client, err := cache.NewRedisClient(&config.RedisConfig{Host: mr.Host(), Port: port}, zap.NewNop())
	if err != nil {
		t.Fatalf("build redis client: %v", err)
	}
	t.Cleanup(func() {
		if cerr := client.Close(); cerr != nil {
			t.Logf("close redis client: %v", cerr)
		}
	})
	return client, mr
}

func newTestVerificationService(t *testing.T, client cache.RedisClient, sender *stubEmailSender) VerificationService {
	t.Helper()
	// "production" so the dev-mode fallbacks (which swallow send errors and
	// return a locally generated code) stay out of the picture.
	return NewVerificationService(client, sender, nil, zap.NewNop(), &config.Config{Environment: "production"})
}

func budgetKeys(identifier string) otpAttemptKeys {
	return newOTPAttemptKeys("email", identifier)
}

// assertBudgetReleased fails if any counter or the cooldown survived.
func assertBudgetReleased(t *testing.T, ctx context.Context, client cache.RedisClient, identifier string) {
	t.Helper()
	keys := budgetKeys(identifier)
	for name, key := range map[string]string{
		"minute":   keys.minute,
		"hourly":   keys.hourly,
		"daily":    keys.daily,
		"cooldown": keys.cooldown,
	} {
		exists, err := client.Exists(ctx, key)
		if err != nil {
			t.Fatalf("exists %s: %v", name, err)
		}
		if exists {
			t.Errorf("%s key %q still holds a charge after a permanent delivery failure", name, key)
		}
	}
}

func TestGenerateAndSendCodeSync_PermanentRejectionRefundsBudgetAndMarksTheAddress(t *testing.T) {
	ctx := context.Background()
	client, mr := newTestRedis(t)
	address := "blocked@example.com"
	sender := &stubEmailSender{err: permanentDeliveryErr(address)}
	svc := newTestVerificationService(t, client, sender)

	_, _, err := svc.GenerateAndSendCodeSync(ctx, "email", address)
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("expected the permanent rejection to reach the caller, got: %v", err)
	}
	if sender.callCount() != 1 {
		t.Fatalf("expected one send attempt, got %d", sender.callCount())
	}
	assertBudgetReleased(t, ctx, client, address)

	// The marker is the record of "every provider refused this address". It must
	// survive the Redis JSON round-trip, because the next attempt may land on a
	// different replica.
	raw, err := mr.Get(otpUndeliverableKey("email", address))
	if err != nil {
		t.Fatalf("expected an undeliverable marker: %v", err)
	}
	for _, want := range []string{"provider", "reason", entities.EmailReasonRecipientSuppressed} {
		if !strings.Contains(raw, want) {
			t.Errorf("marker payload %q is missing %q", raw, want)
		}
	}

	// A marked address is refused up front: no second provider call, and nothing
	// charged against a budget that can never buy delivery.
	_, _, err = svc.GenerateAndSendCodeSync(ctx, "email", address)
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("a marked address must still report a permanent failure, got: %v", err)
	}
	if sender.callCount() != 1 {
		t.Fatalf("a marked address must not be sent to again, sender calls=%d", sender.callCount())
	}
	assertBudgetReleased(t, ctx, client, address)

	// The marker is a hint, not a sentence: once it expires, the provider is
	// tried again, so clearing the suppression upstream restores delivery without
	// a deploy or a manual cache flush.
	mr.FastForward(otpUndeliverableTTL + time.Minute)
	_, _, err = svc.GenerateAndSendCodeSync(ctx, "email", address)
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("expected the provider to be tried and to fail again, got: %v", err)
	}
	if sender.callCount() != 2 {
		t.Fatalf("an expired marker must allow another attempt, sender calls=%d", sender.callCount())
	}
}

func TestGenerateAndSendCode_AsyncPathReportsABlockedRecipientInsteadOfQueueing(t *testing.T) {
	ctx := context.Background()
	client, _ := newTestRedis(t)
	address := "blocked@example.com"
	sender := &stubEmailSender{err: permanentDeliveryErr(address)}
	svc := newTestVerificationService(t, client, sender)

	// First failure marks the address (this is the send that discovers it).
	_, _, err := svc.GenerateAndSendCodeSync(ctx, "email", address)
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("expected the marking send to fail permanently, got: %v", err)
	}

	// The app-facing path is the one that used to lie: it charges the budget,
	// enqueues, and answers "queued" while the worker finds out the code can never
	// be delivered. It must now answer with the real reason.
	_, err = svc.GenerateAndSendCode(ctx, "email", address)
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("the async path must report the blocked recipient, got: %v", err)
	}
	if sender.callCount() != 1 {
		t.Fatalf("a marked address must not be enqueued, sender calls=%d", sender.callCount())
	}
	assertBudgetReleased(t, ctx, client, address)
}

func TestBlockedRecipientError_FailsOpenWhenRedisIsUnavailable(t *testing.T) {
	ctx := context.Background()
	client, mr := newTestRedis(t)

	svc := &verificationService{
		redisClient: client,
		logger:      zap.NewNop(),
		config:      &config.Config{Environment: "production"},
	}

	svc.markUndeliverable(ctx, "email", "blocked@example.com", permanentDeliveryErr("blocked@example.com"))
	mr.Close()

	// A marker we cannot read must never stop a code that might otherwise arrive:
	// the check is an optimisation on top of the provider's own answer.
	if err := svc.blockedRecipientError(ctx, "email", "blocked@example.com"); err != nil {
		t.Fatalf("a Redis failure must fail open, got: %v", err)
	}
}

func TestGenerateAndSendCodeSync_TransientFailureKeepsTheAttemptCharged(t *testing.T) {
	ctx := context.Background()
	client, _ := newTestRedis(t)
	address := "person@example.com"
	sender := &stubEmailSender{err: errors.New("resend: status 500")}
	svc := newTestVerificationService(t, client, sender)

	_, _, err := svc.GenerateAndSendCodeSync(ctx, "email", address)
	if err == nil {
		t.Fatal("expected the transient failure to surface")
	}
	if entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("a 500 says nothing about the address: %v", err)
	}

	// Counters and cooldown must survive: an infrastructure outage is exactly
	// when the limiter has to hold, or a retry storm follows.
	keys := budgetKeys(address)
	for name, key := range map[string]string{"minute": keys.minute, "hourly": keys.hourly, "daily": keys.daily, "cooldown": keys.cooldown} {
		exists, err := client.Exists(ctx, key)
		if err != nil {
			t.Fatalf("exists %s: %v", name, err)
		}
		if !exists {
			t.Errorf("%s key %q was dropped after a transient failure", name, key)
		}
	}

	_, _, err = svc.GenerateAndSendCodeSync(ctx, "email", address)
	if err == nil || !strings.Contains(err.Error(), "wait") {
		t.Fatalf("expected the cooldown to refuse the immediate retry, got: %v", err)
	}
	if sender.callCount() != 1 {
		t.Fatalf("a rate-limited attempt must not reach the provider, sender calls=%d", sender.callCount())
	}
}

func TestRefundSendAttempt_IsIdempotentAndLeavesNoNegativeResidue(t *testing.T) {
	ctx := context.Background()
	client, _ := newTestRedis(t)
	address := "blocked@example.com"

	svc := &verificationService{
		redisClient: client,
		logger:      zap.NewNop(),
		config:      &config.Config{Environment: "production"},
	}

	// Charge the budget through the real limiter, so the keys under test are the
	// production ones and not a test-only guess.
	if err := svc.checkSendRateLimits(ctx, "email", address); err != nil {
		t.Fatalf("charge budget: %v", err)
	}

	svc.refundSendAttempt(ctx, "email", address)
	assertBudgetReleased(t, ctx, client, address)

	// Refunding twice must not leave a key standing at -1, which the limiter
	// would then never trip again.
	svc.refundSendAttempt(ctx, "email", address)
	assertBudgetReleased(t, ctx, client, address)

	// A refund on a clean identifier is a no-op, not a negative counter.
	svc.refundSendAttempt(ctx, "email", "untouched@example.com")
	assertBudgetReleased(t, ctx, client, "untouched@example.com")
}

func TestSendWorker_PermanentRejectionRefundsAndStopsRetrying(t *testing.T) {
	ctx := context.Background()
	client, _ := newTestRedis(t)
	address := "blocked@example.com"
	sender := &stubEmailSender{err: permanentDeliveryErr(address)}

	svc := &verificationService{
		redisClient: client,
		emailSender: sender,
		logger:      zap.NewNop(),
		config:      &config.Config{Environment: "production"},
		sendQueue:   make(chan sendRequest, sendQueueSize),
	}
	go svc.sendWorker(0)

	// The async path charges the budget before enqueuing, so the worker is the
	// only place that can give it back.
	if err := svc.checkSendRateLimits(ctx, "email", address); err != nil {
		t.Fatalf("charge budget: %v", err)
	}
	svc.sendQueue <- sendRequest{identifierType: "email", identifier: address, code: "123456"}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if !budgetCharged(t, ctx, client, address) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker never refunded the attempt budget after a permanent rejection")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The worker retries transient failures; a permanent one must be attempted
	// exactly once, or every refusal also damages our sending reputation.
	if got := sender.callCount(); got != 1 {
		t.Fatalf("expected exactly one provider attempt for a permanent rejection, got %d", got)
	}
}

func budgetCharged(t *testing.T, ctx context.Context, client cache.RedisClient, identifier string) bool {
	t.Helper()
	keys := budgetKeys(identifier)
	exists, err := client.Exists(ctx, keys.cooldown)
	if err != nil {
		t.Fatalf("exists cooldown: %v", err)
	}
	return exists
}
