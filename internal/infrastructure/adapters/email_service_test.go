package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// These tests pin the two contracts the suppressed-recipient incident broke:
// a permanent, recipient-level rejection is classified as permanent (so the
// person is asked for another address instead of being told to retry), and it
// falls through once to the configured fallback provider, because a provider's
// suppression list is per provider — a hard bounce on one says nothing about
// another.
//
// The other half of the contract is what must NOT happen: a transient failure
// never falls through, because the provider may already have accepted the
// message and a second copy from a different provider is worse than an honest
// error.

const (
	testFromEmail  = "noreply@userail.money"
	testUnosendKey = "un_test_primary_key"
	testResendKey  = "re_test_fallback_key"
)

// suppressedBody is Unosend's real rejection body for a suppressed recipient,
// copied from production (Sep 26): HTTP 400, no machine-readable code.
const suppressedBody = `{"error":{"code":400,"message":"recipient blocked@example.com is suppressed"},"message":"recipient blocked@example.com is suppressed","success":false}`

type recordedRequest struct {
	path string
	auth string
	body map[string]any
}

// emailAPIStub stands in for a provider's /emails endpoint and records what it
// was sent. Guarded by a mutex: the handler runs on the server's goroutine while
// the test reads the recording, and -race is enabled in `make test`.
type emailAPIStub struct {
	mu       sync.Mutex
	requests []recordedRequest
	status   int
	response string
}

func newEmailAPIStub(t *testing.T, status int, response string) *emailAPIStub {
	t.Helper()
	return &emailAPIStub{status: status, response: response}
}

func (s *emailAPIStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			// A malformed body is itself a finding, so record the request anyway
			// and let the assertion on the recorded body fail loudly.
			payload = map[string]any{"decode_error": err.Error()}
		}

		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{
			path: r.URL.Path,
			auth: r.Header.Get("Authorization"),
			body: payload,
		})
		s.mu.Unlock()

		if s.status >= 400 {
			w.WriteHeader(s.status)
		}
		if s.response != "" {
			if _, err := w.Write([]byte(s.response)); err != nil {
				return
			}
		}
	}
}

func (s *emailAPIStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return srv
}

func (s *emailAPIStub) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

func (s *emailAPIStub) hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// newTestEmailService builds a service and points it at the stub servers so no
// test can reach a real provider.
func newTestEmailService(t *testing.T, unosend, resend *httptest.Server, cfg EmailServiceConfig) *EmailService {
	t.Helper()
	if cfg.Provider == "" {
		cfg.Provider = emailProviderUnosend
	}
	if cfg.FromEmail == "" {
		cfg.FromEmail = testFromEmail
	}

	svc, err := NewEmailService(zap.NewNop(), cfg)
	if err != nil {
		t.Fatalf("NewEmailService: %v", err)
	}
	if unosend != nil {
		svc.unosendBaseURL = unosend.URL
	}
	if resend != nil {
		svc.resendBaseURL = resend.URL
	}
	return svc
}

func TestEmailService_PermanentRejectionFallsBackToSecondProvider(t *testing.T) {
	unosend := newEmailAPIStub(t, http.StatusBadRequest, suppressedBody)
	resend := newEmailAPIStub(t, http.StatusOK, `{"id":"msg_1"}`)

	svc := newTestEmailService(t, unosend.server(t), resend.server(t), EmailServiceConfig{
		Provider:         emailProviderUnosend,
		APIKey:           testUnosendKey,
		FallbackProvider: emailProviderResend,
		FallbackAPIKey:   testResendKey,
	})

	if err := svc.SendVerificationEmail(context.Background(), "blocked@example.com", "123456"); err != nil {
		t.Fatalf("the fallback provider should have delivered the code, got: %v", err)
	}

	if unosend.hits() != 1 {
		t.Fatalf("expected exactly one attempt against the primary provider, got %d", unosend.hits())
	}
	// Resend has a /emails endpoint too: /emails/batch is the growth path.
	if got := resend.hits(); got != 1 {
		t.Fatalf("expected exactly one attempt against the fallback provider, got %d", got)
	}

	// The fallback must authenticate as itself. Reusing the primary's key was a
	// real bug when both keys were present in the environment.
	if got := resend.recorded()[0].auth; got != "Bearer "+testResendKey {
		t.Fatalf("fallback provider used the wrong credentials: %q", got)
	}
	if got := resend.recorded()[0].body["to"]; got == nil {
		t.Fatalf("fallback request carried no recipient: %+v", resend.recorded()[0].body)
	}
}

func TestEmailService_PermanentRejectionWithoutFallbackIsTypedPermanent(t *testing.T) {
	unosend := newEmailAPIStub(t, http.StatusBadRequest, suppressedBody)
	svc := newTestEmailService(t, unosend.server(t), nil, EmailServiceConfig{
		Provider: emailProviderUnosend,
		APIKey:   testUnosendKey,
	})

	err := svc.SendVerificationEmail(context.Background(), "blocked@example.com", "123456")
	if err == nil {
		t.Fatal("a suppressed recipient must surface as an error, not as success")
	}
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("a nonexistent address must be a permanent error, got: %v", err)
	}
	if errors.Is(err, entities.ErrEmailDeliveryTransient) {
		t.Fatal("a permanent rejection must not also read as transient")
	}

	var deliveryErr *entities.EmailDeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("expected a typed delivery error, got %T", err)
	}
	if deliveryErr.Reason != entities.EmailReasonRecipientSuppressed {
		t.Fatalf("expected reason %q, got %q", entities.EmailReasonRecipientSuppressed, deliveryErr.Reason)
	}
	if deliveryErr.Provider != emailProviderUnosend || deliveryErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected provider/status: %+v", deliveryErr)
	}
}

func TestEmailService_BothProvidersRejectingPermanentlyKeepsPrimaryReason(t *testing.T) {
	unosend := newEmailAPIStub(t, http.StatusBadRequest, suppressedBody)
	resend := newEmailAPIStub(t, http.StatusUnprocessableEntity, `{"message":"invalid recipient"}`)

	svc := newTestEmailService(t, unosend.server(t), resend.server(t), EmailServiceConfig{
		Provider:         emailProviderUnosend,
		APIKey:           testUnosendKey,
		FallbackProvider: emailProviderResend,
		FallbackAPIKey:   testResendKey,
	})

	err := svc.SendVerificationEmail(context.Background(), "blocked@example.com", "123456")
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("expected a permanent error, got: %v", err)
	}
	if unosend.hits() != 1 || resend.hits() != 1 {
		t.Fatalf("expected one attempt per provider, got primary=%d fallback=%d", unosend.hits(), resend.hits())
	}

	// The primary's reason is the one the person is told about; the fallback's
	// failure is noise for the user, so it must not replace it.
	var deliveryErr *entities.EmailDeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("expected a typed delivery error, got %T", err)
	}
	if deliveryErr.Provider != emailProviderUnosend {
		t.Fatalf("expected the primary provider's error to be reported, got %q", deliveryErr.Provider)
	}
}

func TestEmailService_TransientFailureNeverFallsThrough(t *testing.T) {
	unosend := newEmailAPIStub(t, http.StatusInternalServerError, `{"message":"internal server error"}`)
	resend := newEmailAPIStub(t, http.StatusOK, `{"id":"msg_1"}`)

	svc := newTestEmailService(t, unosend.server(t), resend.server(t), EmailServiceConfig{
		Provider:         emailProviderUnosend,
		APIKey:           testUnosendKey,
		FallbackProvider: emailProviderResend,
		FallbackAPIKey:   testResendKey,
	})

	err := svc.SendVerificationEmail(context.Background(), "person@example.com", "123456")
	if err == nil {
		t.Fatal("expected the transient failure to surface")
	}
	if entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("a 500 is not a permanent rejection: %v", err)
	}
	if resend.hits() != 0 {
		t.Fatalf("a transient failure may already have been delivered, so it must not be retried elsewhere (fallback hits=%d)", resend.hits())
	}
}

func TestEmailService_AuthFailureNeverFallsThrough(t *testing.T) {
	unosend := newEmailAPIStub(t, http.StatusUnauthorized, `{"error":{"message":"invalid api key"},"success":false}`)
	resend := newEmailAPIStub(t, http.StatusOK, `{"id":"msg_1"}`)

	svc := newTestEmailService(t, unosend.server(t), resend.server(t), EmailServiceConfig{
		Provider:         emailProviderUnosend,
		APIKey:           testUnosendKey,
		FallbackProvider: emailProviderResend,
		FallbackAPIKey:   testResendKey,
	})

	err := svc.SendVerificationEmail(context.Background(), "person@example.com", "123456")
	if entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("our own bad credentials say nothing about the recipient: %v", err)
	}
	if resend.hits() != 0 {
		t.Fatal("a 401 is a credential problem, not a recipient rejection — no fallback send")
	}
}

func TestEmailService_SuppressionTextOnA500IsNotPermanent(t *testing.T) {
	// The phrase alone must never be enough: only a 400/422 is recipient-level.
	unosend := newEmailAPIStub(t, http.StatusInternalServerError, `{"message":"recipient is suppressed"}`)
	resend := newEmailAPIStub(t, http.StatusOK, `{"id":"msg_1"}`)

	svc := newTestEmailService(t, unosend.server(t), resend.server(t), EmailServiceConfig{
		Provider:         emailProviderUnosend,
		APIKey:           testUnosendKey,
		FallbackProvider: emailProviderResend,
		FallbackAPIKey:   testResendKey,
	})

	err := svc.SendVerificationEmail(context.Background(), "person@example.com", "123456")
	if entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("a 500 must stay transient regardless of its wording: %v", err)
	}
	if resend.hits() != 0 {
		t.Fatal("a transient failure must not fall through")
	}
}

func TestEmailService_FallbackFailureDoesNotHideThePrimaryRejection(t *testing.T) {
	// The realistic production shape: the fallback provider is configured but its
	// key is stale, so it answers 401. The person must still be told their address
	// is the problem — reporting the 401 would put them back on "try again".
	unosend := newEmailAPIStub(t, http.StatusBadRequest, suppressedBody)
	resend := newEmailAPIStub(t, http.StatusUnauthorized, `{"message":"invalid api key"}`)

	svc := newTestEmailService(t, unosend.server(t), resend.server(t), EmailServiceConfig{
		Provider:         emailProviderUnosend,
		APIKey:           testUnosendKey,
		FallbackProvider: emailProviderResend,
		FallbackAPIKey:   testResendKey,
	})

	err := svc.SendVerificationEmail(context.Background(), "blocked@example.com", "123456")
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("the primary rejection must survive a fallback failure, got: %v", err)
	}
	var deliveryErr *entities.EmailDeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("expected a typed delivery error, got %T", err)
	}
	if deliveryErr.Provider != emailProviderUnosend || deliveryErr.Reason != entities.EmailReasonRecipientSuppressed {
		t.Fatalf("expected the primary provider's rejection, got %+v", deliveryErr)
	}
	if resend.hits() != 1 {
		t.Fatalf("expected the fallback to be attempted once, got %d", resend.hits())
	}
}

func TestEmailService_InvalidRecipientIsPermanent(t *testing.T) {
	unosend := newEmailAPIStub(t, http.StatusUnprocessableEntity, `{"message":"invalid to address"}`)
	svc := newTestEmailService(t, unosend.server(t), nil, EmailServiceConfig{
		Provider: emailProviderUnosend,
		APIKey:   testUnosendKey,
	})

	err := svc.SendVerificationEmail(context.Background(), "not-an-address", "123456")
	if !entities.IsPermanentEmailDeliveryError(err) {
		t.Fatalf("expected a permanent error, got: %v", err)
	}
	var deliveryErr *entities.EmailDeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("expected a typed delivery error, got %T", err)
	}
	if deliveryErr.Reason != entities.EmailReasonRecipientInvalid {
		t.Fatalf("expected reason %q, got %q", entities.EmailReasonRecipientInvalid, deliveryErr.Reason)
	}
}

func TestEmailService_LogProviderStillSendsNothing(t *testing.T) {
	svc := newTestEmailService(t, nil, nil, EmailServiceConfig{Provider: emailProviderLog})
	if err := svc.SendVerificationEmail(context.Background(), "dev@example.com", "123456"); err != nil {
		t.Fatalf("the dev sink must not fail: %v", err)
	}
}

func TestEmailService_BatchUsesPrimaryProviderCredentials(t *testing.T) {
	unosend := newEmailAPIStub(t, http.StatusOK, `{"success":true}`)
	resend := newEmailAPIStub(t, http.StatusOK, `{"id":"msg_1"}`)

	svc := newTestEmailService(t, unosend.server(t), resend.server(t), EmailServiceConfig{
		Provider:         emailProviderUnosend,
		APIKey:           testUnosendKey,
		FallbackProvider: emailProviderResend,
		FallbackAPIKey:   testResendKey,
	})

	err := svc.SendBatchEmails(context.Background(), []BatchEmail{{
		From:    testFromEmail,
		To:      []string{"person@example.com"},
		Subject: "hello",
		HTML:    "<p>hi</p>",
	}})
	if err != nil {
		t.Fatalf("SendBatchEmails: %v", err)
	}
	if unosend.hits() != 1 {
		t.Fatalf("expected the batch on the primary provider, primary hits=%d", unosend.hits())
	}
	if resend.hits() != 0 {
		t.Fatal("batches must not be routed to the fallback provider")
	}
	if got := unosend.recorded()[0].auth; got != "Bearer "+testUnosendKey {
		t.Fatalf("batch used the wrong credentials: %q", got)
	}
}

func TestNewEmailService_RejectsUnusableConfiguration(t *testing.T) {
	tests := []struct {
		name string
		cfg  EmailServiceConfig
	}{
		{
			name: "unknown provider is a boot error, not a silent unosend default",
			cfg:  EmailServiceConfig{Provider: "sendgrid", APIKey: "x", FromEmail: testFromEmail},
		},
		{
			name: "missing provider",
			cfg:  EmailServiceConfig{FromEmail: testFromEmail},
		},
		{
			name: "log sink in production",
			cfg:  EmailServiceConfig{Provider: emailProviderLog, Environment: "production"},
		},
		{
			name: "log sink as a fallback would fake success",
			cfg: EmailServiceConfig{
				Provider: emailProviderUnosend, APIKey: testUnosendKey, FromEmail: testFromEmail,
				FallbackProvider: emailProviderLog,
			},
		},
		{
			name: "primary api key missing",
			cfg:  EmailServiceConfig{Provider: emailProviderUnosend, FromEmail: testFromEmail},
		},
		{
			name: "from address missing",
			cfg:  EmailServiceConfig{Provider: emailProviderUnosend, APIKey: testUnosendKey},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewEmailService(zap.NewNop(), tc.cfg); err == nil {
				t.Fatal("expected NewEmailService to reject this configuration")
			}
		})
	}
}

func TestNewEmailService_DropsUnusableFallbackInsteadOfFailingBoot(t *testing.T) {
	tests := []struct {
		name string
		cfg  EmailServiceConfig
	}{
		{
			name: "fallback api key missing",
			cfg: EmailServiceConfig{
				Provider: emailProviderUnosend, APIKey: testUnosendKey, FromEmail: testFromEmail,
				FallbackProvider: emailProviderResend,
			},
		},
		{
			name: "fallback is the primary provider",
			cfg: EmailServiceConfig{
				Provider: emailProviderUnosend, APIKey: testUnosendKey, FromEmail: testFromEmail,
				FallbackProvider: emailProviderUnosend, FallbackAPIKey: testUnosendKey,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := NewEmailService(zap.NewNop(), tc.cfg)
			if err != nil {
				t.Fatalf("an unusable fallback must not break boot: %v", err)
			}
			if targets := svc.providerTargets(); len(targets) != 1 {
				t.Fatalf("expected only the primary provider to remain, got %d targets", len(targets))
			}
		})
	}
}
