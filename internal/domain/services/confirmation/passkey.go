package confirmation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	webauthnSvc "github.com/rail-service/rail_service/internal/domain/services/webauthn"
)

// Assurance levels for an approval. The server never sees biometrics —
// Face ID is local-only by Apple design. What the server CAN know is whether
// the approve call carries a WebAuthn assertion from an enrolled passkey with
// user verification: phishing-resistant, iCloud-synced, and verified with the
// standard library instead of a custom protocol.
const (
	AssuranceTokenOnly = "token_only" // signed URL possession only (pre-enrollment stopgap)
	AssurancePasskey   = "passkey"    // WebAuthn assertion, userVerification=required
)

// ErrNoPasskey tells the extension to render the "set up a passkey in the
// app" state instead of the approve button. The login passkey (same RP ID)
// is the approval passkey — there is no separate enrollment.
var ErrNoPasskey = errors.New("no passkey enrolled")

// ErrCardTerminal tells the extension the card already ended: fetch the
// record and render the dead state instead of retrying.
var ErrCardTerminal = errors.New("card already terminal")

// TxAssertionService is the slice of the WebAuthn service the confirmation
// primitive needs. The domain implementation lives in
// internal/domain/services/webauthn; tests fake this interface.
type TxAssertionService interface {
	BeginTransactionAssertion(ctx context.Context, userID uuid.UUID, email string) (*protocol.CredentialAssertion, *webauthn.SessionData, error)
	FinishTransactionAssertion(ctx context.Context, userID uuid.UUID, email string, session *webauthn.SessionData, response *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error)
	HasCredentials(ctx context.Context, userID uuid.UUID) (bool, error)
}

// UserEmailLookup resolves the WebAuthn user name for an ID. Wiring provides
// it from the user repo; the primitive never imports repositories.
type UserEmailLookup func(ctx context.Context, userID uuid.UUID) (string, error)

// txSession holds one ceremony, bound to its card. Sessions are single-card:
// the challenge is random per ceremony and verification requires the same
// session the options came from.
type txSession struct {
	session webauthn.SessionData
	expires time.Time
}

// SetTxAssertion wires the WebAuthn transaction assertion backend. Nil (or
// unset) means passkeys are unavailable: assertion paths fail closed and
// only the token-only legacy path remains, governed by requirePasskey.
func (s *Service) SetTxAssertion(tx TxAssertionService) { s.txAssert = tx }

// SetUserEmailLookup wires WebAuthn user-name resolution.
func (s *Service) SetUserEmailLookup(fn UserEmailLookup) { s.userEmail = fn }

// SetRequirePasskey rejects token-only approves outright. Default false:
// users without a passkey fall back to token-only (audited) until they
// enroll one in the app. Prefer flipping this on once passkey adoption
// covers the fleet.
func (s *Service) SetRequirePasskey(require bool) { s.requirePasskey = require }

func (s *Service) txEmail(ctx context.Context, userID uuid.UUID) (string, error) {
	if s.userEmail == nil {
		return "", fmt.Errorf("user email lookup not wired (fail-closed)")
	}
	email, err := s.userEmail(ctx, userID)
	if err != nil {
		return "", err
	}
	if email == "" {
		return "", fmt.Errorf("no email for user (fail-closed)")
	}
	return email, nil
}

func (s *Service) pruneTxSessions(now time.Time) {
	s.txMu.Lock()
	defer s.txMu.Unlock()
	for id, sess := range s.txSessions {
		if !now.Before(sess.expires) {
			delete(s.txSessions, id)
		}
	}
}

// AssertionOptions mints a WebAuthn assertion ceremony for one card. It
// verifies token + ownership + liveness but never consumes the token: the
// extension may fetch options, background the sheet, and come back. Expiry
// still kills the card (transition to expired), and terminal cards get
// ErrCardTerminal so the extension renders dead instead of a live prompt.
func (s *Service) AssertionOptions(ctx context.Context, userID, id uuid.UUID, token string) (*protocol.CredentialAssertion, error) {
	if err := s.VerifyToken(id, token); err != nil {
		return nil, err
	}
	c, err := s.loadOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if c.TokenUsed || c.IsTerminal() {
		return nil, ErrCardTerminal
	}
	if c.IsExpired(s.now()) {
		if terr := s.transition(ctx, c, entities.ConfirmationExpired, "", "ttl elapsed on options fetch"); terr != nil && s.log != nil {
			s.log.Warn("confirmation lazy-expiry failed", "action_id", id.String(), "error", terr.Error())
		}
		return nil, ErrCardTerminal
	}
	if s.txAssert == nil {
		return nil, fmt.Errorf("passkey unavailable (fail-closed)")
	}
	email, err := s.txEmail(ctx, userID)
	if err != nil {
		return nil, err
	}
	options, session, err := s.txAssert.BeginTransactionAssertion(ctx, userID, email)
	if err != nil {
		if errors.Is(err, webauthnSvc.ErrNoCredentials) || err.Error() == "no passkey enrolled" {
			return nil, ErrNoPasskey
		}
		return nil, err
	}
	expiry := c.ExpiresAt
	if max := s.now().Add(5 * time.Minute); expiry.After(max) {
		expiry = max
	}
	s.txMu.Lock()
	if s.txSessions == nil {
		s.txSessions = map[uuid.UUID]txSession{}
	}
	s.txSessions[id] = txSession{session: *session, expires: expiry}
	s.txMu.Unlock()
	// Cross-replica fallback: persist the ceremony in the card row (shared
	// Postgres) so an approve landing on another replica can still verify.
	// Best-effort: in-memory is the fast path, DB is the fallback.
	s.persistTxSession(ctx, id, session, expiry)
	s.pruneTxSessions(s.now())
	return options, nil
}

// persistTxSession stores the ceremony in the confirmation payload so any
// replica can verify the approve. Reserved keys (_tx_*) are never rendered.
func (s *Service) persistTxSession(ctx context.Context, id uuid.UUID, session *webauthn.SessionData, expiry time.Time) {
	c, err := s.store.Load(ctx, id)
	if err != nil {
		return
	}
	raw, err := json.Marshal(session)
	if err != nil {
		return
	}
	if c.Payload == nil {
		c.Payload = map[string]any{}
	}
	c.Payload["_tx_session"] = string(raw)
	c.Payload["_tx_session_exp"] = expiry.UTC().Format(time.RFC3339)
	_ = s.store.Save(ctx, c)
}

// loadTxSession returns the ceremony from memory, falling back to the
// DB-persisted copy for cross-replica approves.
func (s *Service) loadTxSession(ctx context.Context, id uuid.UUID) (webauthn.SessionData, bool) {
	s.txMu.Lock()
	sess, found := s.txSessions[id]
	s.txMu.Unlock()
	if found {
		if s.now().Before(sess.expires) {
			return sess.session, true
		}
		s.txMu.Lock()
		delete(s.txSessions, id)
		s.txMu.Unlock()
		return webauthn.SessionData{}, false
	}
	c, err := s.store.Load(ctx, id)
	if err != nil || c.Payload == nil {
		return webauthn.SessionData{}, false
	}
	raw, _ := c.Payload["_tx_session"].(string)
	expStr, _ := c.Payload["_tx_session_exp"].(string)
	if raw == "" || expStr == "" {
		return webauthn.SessionData{}, false
	}
	exp, err := time.Parse(time.RFC3339, expStr)
	if err != nil || !s.now().Before(exp) {
		return webauthn.SessionData{}, false
	}
	var out webauthn.SessionData
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return webauthn.SessionData{}, false
	}
	return out, true
}

// clearTxSession removes the ceremony from both memory and the persisted copy.
func (s *Service) clearTxSession(ctx context.Context, id uuid.UUID) {
	s.txMu.Lock()
	delete(s.txSessions, id)
	s.txMu.Unlock()
	if c, err := s.store.Load(ctx, id); err == nil && c.Payload != nil {
		if _, ok := c.Payload["_tx_session"]; ok {
			delete(c.Payload, "_tx_session")
			delete(c.Payload, "_tx_session_exp")
			_ = s.store.Save(ctx, c)
		}
	}
}

// ApproveWithAssertion verifies a passkey assertion and, on success, runs
// Face ID success + server accept. A failed verification never burns the
// single-use token: the extension fetches fresh options and the user retries.
// Sessions are single-card and short-lived; a missing session means the
// ceremony expired, not the card — refetch, don't re-mint.
func (s *Service) ApproveWithAssertion(ctx context.Context, userID, id uuid.UUID, token string, assertion []byte) (*entities.Confirmation, error) {
	if err := s.VerifyToken(id, token); err != nil {
		return nil, err
	}
	c, err := s.loadOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if c.IsTerminal() {
		return c, nil // replay: no-op, terminal state stands
	}
	if c.IsExpired(s.now()) {
		if err := s.transition(ctx, c, entities.ConfirmationExpired, "", "ttl elapsed on approve"); err != nil {
			return nil, err
		}
		return s.reload(ctx, id)
	}
	if s.txAssert == nil {
		return nil, fmt.Errorf("passkey unavailable (fail-closed)")
	}
	sess, found := s.loadTxSession(ctx, id)
	if !found {
		return nil, fmt.Errorf("assertion ceremony expired — fetch fresh options")
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(assertion))
	if err != nil {
		return nil, fmt.Errorf("malformed assertion: %w", err)
	}
	email, err := s.txEmail(ctx, userID)
	if err != nil {
		return nil, err
	}
	if _, err := s.txAssert.FinishTransactionAssertion(ctx, userID, email, &sess, parsed); err != nil {
		return nil, fmt.Errorf("passkey verification failed: %w", err)
	}
	s.clearTxSession(ctx, id)
	return s.claimAndExecute(ctx, userID, id, AssurancePasskey, "passkey verified (assurance=passkey)")
}

// PasskeyRegistered reports whether the user can approve with a passkey.
// The handler exposes it on fetch so the extension chooses between the
// passkey sheet and the setup state without a wasted ceremony.
func (s *Service) PasskeyRegistered(ctx context.Context, userID uuid.UUID) bool {
	if s.txAssert == nil {
		return false
	}
	ok, err := s.txAssert.HasCredentials(ctx, userID)
	return err == nil && ok
}

// passkeyRequiredFor reports whether a token-only approve must be refused:
// strict mode always refuses; otherwise any enrolled passkey refuses (the
// user has the stronger factor, so the weaker one no longer suffices). A
// lookup failure fails closed.
func (s *Service) passkeyRequiredFor(ctx context.Context, userID uuid.UUID) bool {
	if s.requirePasskey {
		return true
	}
	if s.txAssert == nil {
		return false
	}
	ok, err := s.txAssert.HasCredentials(ctx, userID)
	if err != nil {
		return true
	}
	return ok
}
