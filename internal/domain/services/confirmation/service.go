package confirmation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// ErrConfirmationNotFound is returned by Store.Load for unknown ids. Stores
// must return it (not a generic error) so callers can distinguish "no card"
// from "the database is down" — the former is a 404, the latter fails closed.
var ErrConfirmationNotFound = errors.New("confirmation not found")

// ErrTokenConsumed is returned by Store.Claim when the single-use token is
// already spent. It is not a failure: the caller reloads and either returns
// the terminal record or resumes the pipeline.
var ErrTokenConsumed = errors.New("confirmation token already consumed")

// Store persists confirmations. Memory is the default (tests, single-process
// dev); production wires the Postgres repository so the single-use token and
// idempotency hold across restarts and replicas. Every error fails closed:
// callers abort rather than guess.
type Store interface {
	Save(ctx context.Context, c *entities.Confirmation) error
	Load(ctx context.Context, id uuid.UUID) (*entities.Confirmation, error)
	// Claim atomically flips token_used (and records assurance) for exactly
	// one caller across replicas and crash-retries. Second callers get
	// ErrTokenConsumed and must reload: terminal cards return as-is,
	// consumed-but-live cards resume.
	Claim(ctx context.Context, id uuid.UUID, assurance string) (*entities.Confirmation, error)
}

// CardEditor mutates the live transcript card in place (Spectrum edit()).
// It must never send a new bubble. Returning an error marks the record
// CardEditFailed so a retry can run without duplicating the card.
type CardEditor func(ctx context.Context, c *entities.Confirmation) error

// AuditSink receives audit entries; nil sinks drop.
type AuditSink func(e entities.ConfirmationAuditEntry)

// Executor runs the money movement for one action. Keyed by actionId for
// idempotency: the service guarantees it runs at most once per confirmation.
type Executor func(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (resultSummary string, err error)

// Renderer builds human copy for an action. Renderers are data (copy + amount
// fields), not new infrastructure — one ConfirmationCard for every action.
type Renderer func(action entities.ConfirmationAction, payload map[string]any) (title, subtitle, amount, asset, destination, fee, riskLine string)

// Config for the service.
type Config struct {
	TokenSecret string
	ConfirmBase string // e.g. https://api.userail.money/confirm
	TTL         time.Duration
}

type memoryStore struct {
	mu sync.RWMutex
	m  map[uuid.UUID]*entities.Confirmation
}

func newMemoryStore() *memoryStore { return &memoryStore{m: map[uuid.UUID]*entities.Confirmation{}} }

func (s *memoryStore) Save(_ context.Context, c *entities.Confirmation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.m[c.ID] = &cp
	return nil
}

func (s *memoryStore) Load(_ context.Context, id uuid.UUID) (*entities.Confirmation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.m[id]
	if !ok {
		return nil, ErrConfirmationNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *memoryStore) Claim(_ context.Context, id uuid.UUID, assurance string) (*entities.Confirmation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[id]
	if !ok {
		return nil, ErrConfirmationNotFound
	}
	if c.TokenUsed {
		return nil, ErrTokenConsumed
	}
	cp := *c
	cp.TokenUsed = true
	if assurance != "" {
		cp.Assurance = assurance
	}
	cp.UpdatedAt = time.Now().UTC()
	s.m[id] = &cp
	out := cp
	return &out, nil
}

// Service is the server-side source of truth for confirmation cards.
type Service struct {
	cfg       Config
	store     Store
	log       *logger.Logger
	mu        sync.Mutex
	executors map[entities.ConfirmationAction]Executor
	renderers map[entities.ConfirmationAction]Renderer
	executed  map[string]string // execute key -> result summary (idempotent replay)
	cardEdit  CardEditor
	audit     AuditSink
	// txAssert verifies passkey assertions. Nil means passkeys are
	// unavailable: assertion paths fail closed; legacy token-only approves
	// are governed by requirePasskey.
	txAssert TxAssertionService
	// userEmail resolves the WebAuthn user name for passkey ceremonies.
	userEmail UserEmailLookup
	// requirePasskey rejects token-only approves outright. Default false:
	// users without a passkey fall back to token-only (audited) until they
	// enroll one in the app.
	requirePasskey bool
	// txSessions holds one WebAuthn ceremony per card, pruned on use.
	txMu       sync.Mutex
	txSessions map[uuid.UUID]txSession
	// terminalNotify tells Miriam when a Miriam-originated card goes terminal
	// on the Go side first (rejected/expired). Fire-and-forget; nil drops.
	terminalNotify func(ctx context.Context, miriamConfirmID, state string)
	now            func() time.Time
}

// NewService builds the primitive. A nil store uses memory; empty secret
// fails closed at token time (never issue cards without a signing secret).
func NewService(cfg Config, store Store, log *logger.Logger) *Service {
	if store == nil {
		store = newMemoryStore()
	}
	if cfg.TTL <= 0 {
		cfg.TTL = entities.DefaultConfirmationTTL
	}
	s := &Service{
		cfg:       cfg,
		store:     store,
		log:       log,
		executors: map[entities.ConfirmationAction]Executor{},
		renderers: map[entities.ConfirmationAction]Renderer{},
		executed:  map[string]string{},
		now:       time.Now,
	}
	s.renderers[entities.ConfirmationActionInvestBuy] = investBuyRenderer
	s.renderers[entities.ConfirmationActionTransferSend] = transferSendRenderer
	s.renderers[entities.ConfirmationActionLimitChange] = limitChangeRenderer
	s.renderers[entities.ConfirmationActionSaveSweep] = saveSweepRenderer
	return s
}

// SetCardEditor wires the Spectrum edit() notifier. Edits run after state is
// persisted; edit failure never rolls back state.
func (s *Service) SetCardEditor(fn CardEditor) { s.cardEdit = fn }

// SetAuditSink wires audit persistence.
func (s *Service) SetAuditSink(fn AuditSink) {
	s.audit = fn
}

// SetTerminalNotifier wires the Go->Miriam terminal callback for
// Miriam-originated cards. It fires (fire-and-forget) when such a card
// reaches rejected/expired on the Go side first, so Miriam can decline/expire
// the joined challenge. Approve/completed never notify: that path returns
// through the settle call itself.
func (s *Service) SetTerminalNotifier(fn func(ctx context.Context, miriamConfirmID, state string)) {
	s.terminalNotify = fn
}

// RegisterExecutor wires the backend handler for one action type.
func (s *Service) RegisterExecutor(a entities.ConfirmationAction, fn Executor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executors[a] = fn
}

// RegisterRenderer overrides/adds copy for an action type.
func (s *Service) RegisterRenderer(a entities.ConfirmationAction, fn Renderer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renderers[a] = fn
}

// CreateInput stages one confirmation. Creating it never moves money.
type CreateInput struct {
	UserID      uuid.UUID
	Action      entities.ConfirmationAction
	Payload     map[string]any
	Title       string // optional override; renderers fill when empty
	Subtitle    string
	Amount      string
	Asset       string
	Destination string
	Fee         string
	RiskLine    string
	TTL         time.Duration
}

// Create stages a confirmation and returns it with its signed URL.
func (s *Service) Create(ctx context.Context, in CreateInput) (*entities.Confirmation, string, error) {
	if !entities.ValidConfirmationAction(in.Action) {
		return nil, "", fmt.Errorf("unknown confirmation action %q", in.Action)
	}
	if in.UserID == uuid.Nil {
		return nil, "", fmt.Errorf("user id required")
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = s.cfg.TTL
	}
	now := s.now().UTC()
	title, subtitle, amount, asset, dest, fee, risk := s.renderCopy(in)
	if title == "" {
		return nil, "", fmt.Errorf("confirmation title required")
	}
	c := &entities.Confirmation{
		ID:          uuid.New(),
		UserID:      in.UserID,
		Action:      in.Action,
		State:       entities.ConfirmationPending,
		Title:       title,
		Subtitle:    subtitle,
		Amount:      amount,
		Asset:       asset,
		Destination: dest,
		Fee:         fee,
		RiskLine:    risk,
		Payload:     in.Payload,
		ExpiresAt:   now.Add(ttl),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	c.ExecuteKey = c.ID.String()
	if err := s.store.Save(ctx, c); err != nil {
		return nil, "", err
	}
	s.auditOf(c, "", entities.ConfirmationPending, "", "created", nil)
	url, err := s.ConfirmURL(c)
	if err != nil {
		return nil, "", err
	}
	return c, url, nil
}

func (s *Service) renderer(a entities.ConfirmationAction) Renderer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renderers[a]
}

// renderCopy fills empty copy fields from the action renderer. Renderers are
// data (copy + amount fields), never new infrastructure.
func (s *Service) renderCopy(in CreateInput) (title, subtitle, amount, asset, dest, fee, risk string) {
	title, subtitle, amount, asset, dest, fee, risk =
		in.Title, in.Subtitle, in.Amount, in.Asset, in.Destination, in.Fee, in.RiskLine
	r := s.renderer(in.Action)
	if r == nil || (title != "" && subtitle != "") {
		return title, subtitle, amount, asset, dest, fee, risk
	}
	rt, rs, ra, rat, rd, rf, rr := r(in.Action, in.Payload)
	return firstStr(title, rt), firstStr(subtitle, rs), firstStr(amount, ra),
		firstStr(asset, rat), firstStr(dest, rd), firstStr(fee, rf), firstStr(risk, rr)
}

func firstStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ConfirmURL mints the signed, short-lived URL the extension opens:
// {base}/{actionId}?t={expiryUnix}.{hexHMAC}.
func (s *Service) ConfirmURL(c *entities.Confirmation) (string, error) {
	if s.cfg.TokenSecret == "" {
		return "", fmt.Errorf("confirmation token secret not configured (fail-closed)")
	}
	exp := c.ExpiresAt.Unix()
	sig := s.sign(c.ID.String(), exp)
	base := strings.TrimRight(s.cfg.ConfirmBase, "/")
	if base == "" {
		base = "/confirm"
	}
	return fmt.Sprintf("%s/%s?t=%d.%s", base, c.ID.String(), exp, sig), nil
}

// VerifyToken checks signature + expiry timestamp. It does not consume the
// token: fetch may render terminal states, only approve/reject consume.
func (s *Service) VerifyToken(id uuid.UUID, token string) error {
	exp, sig, err := splitToken(token)
	if err != nil {
		return err
	}
	if s.now().Unix() > exp {
		return fmt.Errorf("confirmation token expired")
	}
	want := s.sign(id.String(), exp)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return fmt.Errorf("invalid confirmation token")
	}
	return nil
}

func splitToken(t string) (int64, string, error) {
	parts := strings.SplitN(t, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, "", fmt.Errorf("malformed confirmation token")
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("malformed confirmation token")
	}
	return exp, parts[1], nil
}

func (s *Service) sign(actionID string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.TokenSecret))
	mac.Write([]byte(actionID + "." + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Fetch returns the card payload for the extension. Expired or consumed cards
// return the terminal record (dead state) — never a live approve button.
func (s *Service) Fetch(ctx context.Context, id uuid.UUID, token string) (*entities.Confirmation, error) {
	if err := s.VerifyToken(id, token); err != nil {
		// Signature/expiry failure: still try to surface the terminal record
		// when the id is known so the extension renders dead, not broken.
		// A store outage here fails closed (no record to show).
		if c, lerr := s.store.Load(ctx, id); lerr == nil {
			cp := *c
			return &cp, nil
		}
		return nil, err
	}
	c, err := s.store.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	if !c.IsTerminal() && c.IsExpired(s.now()) {
		if terr := s.transition(ctx, c, entities.ConfirmationExpired, "", "ttl elapsed on fetch"); terr != nil && s.log != nil {
			s.log.Warn("confirmation lazy-expiry failed", "action_id", id.String(), "error", terr.Error())
		}
	}
	return s.reload(ctx, id)
}

// loadOwned loads a card and enforces ownership. Store errors fail closed;
// unknown ids surface ErrConfirmationNotFound.
func (s *Service) loadOwned(ctx context.Context, userID, id uuid.UUID) (*entities.Confirmation, error) {
	c, err := s.store.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.UserID != userID {
		return nil, fmt.Errorf("confirmation does not belong to user")
	}
	return c, nil
}

// reload returns the fresh record or the store error.
func (s *Service) reload(ctx context.Context, id uuid.UUID) (*entities.Confirmation, error) {
	return s.store.Load(ctx, id)
}

// claimAndExecute consumes the token atomically (exactly one winner across
// replicas and crash-retries), records assurance, walks
// authenticating->approved, and executes. A lost claim race reloads:
// terminal cards return as-is; consumed-but-live cards resume the pipeline
// (verification already ran fresh in the calling request).
func (s *Service) claimAndExecute(ctx context.Context, userID, id uuid.UUID, assurance, authNote string) (*entities.Confirmation, error) {
	c, err := s.store.Claim(ctx, id, assurance)
	if err != nil {
		if errors.Is(err, ErrTokenConsumed) {
			return s.resume(ctx, userID, id)
		}
		return nil, err
	}
	if c.IsTerminal() {
		// Lost a race with MarkExternal after claiming: the token burn is
		// harmless (replays return terminal) and nobody executes twice.
		return c, nil
	}
	if err := s.transition(ctx, c, entities.ConfirmationAuthenticating, "pass", authNote); err != nil {
		return nil, err
	}
	if err := s.transition(ctx, c, entities.ConfirmationApproved, "pass", "server accepted"); err != nil {
		return nil, err
	}
	return s.executeAndComplete(ctx, userID, c)
}

// resume continues a consumed-but-live card: crash recovery or a lost claim
// race. Transitions are no-ops when already in-state; execution is idempotent
// by execute key (UNIQUE in Postgres, idempotency keys downstream).
func (s *Service) resume(ctx context.Context, userID, id uuid.UUID) (*entities.Confirmation, error) {
	c, err := s.reload(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.IsTerminal() {
		return c, nil
	}
	if c.State == entities.ConfirmationPending {
		if err := s.transition(ctx, c, entities.ConfirmationAuthenticating, "pass", "resumed"); err != nil {
			return nil, err
		}
	}
	if c.State == entities.ConfirmationAuthenticating {
		if err := s.transition(ctx, c, entities.ConfirmationApproved, "pass", "server accepted"); err != nil {
			return nil, err
		}
	}
	return s.executeAndComplete(ctx, userID, c)
}

// executeAndComplete runs the registered executor and lands the card
// completed/failed. Safe to call on resume: the in-memory result cache plus
// downstream idempotency keys make a second call replay, not re-execute.
func (s *Service) executeAndComplete(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (*entities.Confirmation, error) {
	s.mu.Lock()
	ex, registered := s.executors[c.Action]
	prev, seen := s.executed[c.ExecuteKey]
	s.mu.Unlock()
	if seen { // executor raced or retried: replay stored result
		if err := s.transition(ctx, c, entities.ConfirmationCompleted, "pass", "idempotent replay"); err != nil {
			return nil, err
		}
		_ = prev
		return s.reload(ctx, c.ID)
	}
	if !registered {
		if err := s.transition(ctx, c, entities.ConfirmationFailed, "pass", "no executor registered (fail-closed)"); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("no executor for action %q", c.Action)
	}
	summary, err := ex(ctx, userID, c)
	if err != nil {
		if terr := s.transition(ctx, c, entities.ConfirmationFailed, "pass", err.Error()); terr != nil {
			return nil, terr
		}
		return nil, err
	}
	s.mu.Lock()
	s.executed[c.ExecuteKey] = summary
	s.mu.Unlock()
	if err := s.transition(ctx, c, entities.ConfirmationCompleted, "pass", summary); err != nil {
		return nil, err
	}
	return s.reload(ctx, c.ID)
}

// Approve runs token-only success + server accept: the pre-enrollment
// stopgap (and the web fallback, where the browser can also drive a passkey
// ceremony through the options endpoint). Once the user has any passkey
// enrolled — or when requirePasskey is on — token-only is refused and the
// passkey path (AssertionOptions + ApproveWithAssertion) is the only way
// through. Idempotent by actionId: replays after a terminal state return the
// terminal record as a no-op.
func (s *Service) Approve(ctx context.Context, userID, id uuid.UUID, token, biometric string) (*entities.Confirmation, error) {
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
		if err := s.transition(ctx, c, entities.ConfirmationExpired, biometric, "ttl elapsed on approve"); err != nil {
			return nil, err
		}
		return s.reload(ctx, id)
	}
	if biometric != "" && biometric != "pass" {
		return nil, fmt.Errorf("biometric not passed (got %q)", biometric)
	}
	if s.passkeyRequiredFor(ctx, userID) {
		return nil, fmt.Errorf("passkey required (credential enrolled)")
	}
	return s.claimAndExecute(ctx, userID, id, AssuranceTokenOnly, "approved without passkey (assurance=token_only)")
}

// Reject records a Face ID cancel. Single-use: later replays are no-ops.
func (s *Service) Reject(ctx context.Context, userID, id uuid.UUID, token, biometric string) (*entities.Confirmation, error) {
	if err := s.VerifyToken(id, token); err != nil {
		return nil, err
	}
	c, err := s.loadOwned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if c.IsTerminal() {
		return c, nil
	}
	if biometric == "" {
		biometric = "cancel"
	}
	// Claim first so a concurrent approve wins the race deterministically:
	// a lost claim means biometric authorization already happened, and a
	// cancel must not override it. Approved cards reject the edge below.
	if _, err := s.store.Claim(ctx, id, c.Assurance); err != nil {
		if errors.Is(err, ErrTokenConsumed) {
			return s.reload(ctx, id)
		}
		return nil, err
	}
	if err := s.transition(ctx, c, entities.ConfirmationRejected, biometric, "user cancelled"); err != nil {
		return nil, err
	}
	return s.reload(ctx, id)
}

// ExpireSweep marks a known card expired (TTL worker / lazy expiry path).
func (s *Service) ExpireSweep(ctx context.Context, id uuid.UUID) (*entities.Confirmation, bool) {
	c, err := s.store.Load(ctx, id)
	if err != nil || c.IsTerminal() {
		return c, false
	}
	if !c.IsExpired(s.now()) {
		return c, false
	}
	if err := s.transition(ctx, c, entities.ConfirmationExpired, "", "ttl sweep"); err != nil {
		if s.log != nil {
			s.log.Warn("confirmation sweep transition failed", "action_id", id.String(), "error", err.Error())
		}
		return c, false
	}
	out, err := s.reload(ctx, id)
	if err != nil {
		return c, true
	}
	return out, true
}

// MarkExternal applies a terminal state reported by Miriam (chat settled
// first): it validates every edge via ValidConfirmationTransition, persists,
// and triggers the card edit. Already-terminal cards return their current
// state as a no-op, so replays and races converge instead of erroring.
func (s *Service) MarkExternal(ctx context.Context, id uuid.UUID, to entities.ConfirmationState, result string) (*entities.Confirmation, error) {
	c, err := s.store.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.IsTerminal() || c.State == to {
		return c, nil
	}
	// The chat side won: burn the card token so no biometric approve can
	// start afterwards. A concurrent in-flight approve holds the claim and
	// finishes; both sides converge on terminal.
	c.TokenUsed = true
	if err := s.store.Save(ctx, c); err != nil {
		return nil, err
	}
	// Walk the legal path step by step: a pending card marked completed moves
	// through authenticating/approved (each step persisted + edited) rather
	// than jumping states or sticking forever.
	for i := 0; i < 4; i++ {
		cur, err := s.store.Load(ctx, id)
		if err != nil {
			return nil, err
		}
		if cur.IsTerminal() || cur.State == to {
			break
		}
		next := stepToward(cur.State, to)
		if next == "" {
			break // no legal edge onward: leave the card where it stands
		}
		if err := s.transition(ctx, cur, next, "", result); err != nil {
			return nil, err
		}
	}
	return s.reload(ctx, id)
}

// stepToward returns the next legal state from cur toward a terminal target,
// or "" when no legal edge leads onward.
func stepToward(cur, to entities.ConfirmationState) entities.ConfirmationState {
	switch to {
	case entities.ConfirmationCompleted, entities.ConfirmationFailed:
		switch cur {
		case entities.ConfirmationPending:
			return entities.ConfirmationAuthenticating
		case entities.ConfirmationAuthenticating:
			return entities.ConfirmationApproved
		case entities.ConfirmationApproved:
			return to
		}
	case entities.ConfirmationRejected, entities.ConfirmationExpired:
		switch cur {
		case entities.ConfirmationPending, entities.ConfirmationAuthenticating:
			return to
		case entities.ConfirmationApproved:
			// Approved already passed biometrics; only execution outcomes
			// (or expiry) may follow, never a user cancel.
			if to == entities.ConfirmationExpired {
				return to
			}
		}
	}
	return ""
}

// transition validates the edge, persists, audits, then asks the card editor
// to mutate the transcript card. Edit failure sets CardEditFailed and is
// returned for retry — state is already persisted, and no duplicate bubble is
// ever sent (the editor contract only allows edit()).
func (s *Service) transition(ctx context.Context, c *entities.Confirmation, to entities.ConfirmationState, biometric, note string) error {
	from := c.State
	if from == to {
		return nil
	}
	if !entities.ValidConfirmationTransition(from, to) {
		// Fail closed: illegal edges never persist. Terminal replay paths
		// call with equal states or legal edges only.
		if s.log != nil {
			s.log.Warn("illegal confirmation transition",
				"action_id", c.ID.String(), "from", string(from), "to", string(to))
		}
		return fmt.Errorf("illegal confirmation transition %s -> %s", from, to)
	}
	now := s.now().UTC()
	c.State = to
	c.UpdatedAt = now
	if entities.TerminalConfirmationState(to) {
		c.CompletedAt = &now
		if to == entities.ConfirmationCompleted && note != "" {
			c.ResultSummary = note
		} else if to == entities.ConfirmationFailed && note != "" {
			c.ResultSummary = note
		}
	}
	c.CardEditFailed = false
	if err := s.store.Save(ctx, c); err != nil {
		return err
	}
	var ok *bool
	if s.cardEdit != nil {
		err := s.cardEdit(ctx, c)
		b := err == nil
		ok = &b
		if err != nil {
			c.CardEditFailed = true
			if serr := s.store.Save(ctx, c); serr != nil && s.log != nil {
				s.log.Warn("confirmation edit-retry flag lost (state itself is durable)",
					"action_id", c.ID.String(), "error", serr.Error())
			}
			if s.log != nil {
				s.log.Warn("confirmation card edit failed (state persisted, retry edit)",
					"action_id", c.ID.String(), "state", string(to), "error", err.Error())
			}
		}
	}
	s.auditOf(c, from, to, biometric, note, ok)
	s.maybeNotifyTerminal(ctx, c, to)
	return nil
}

// maybeNotifyTerminal tells Miriam when a Miriam-originated card goes
// rejected/expired on the Go side first (Face ID cancel, TTL hit), so the
// joined challenge is declined/expired there too. Approve/completed never
// notify — that path returns through the settle call. State is already
// persisted; notification is fire-and-forget.
func (s *Service) maybeNotifyTerminal(ctx context.Context, c *entities.Confirmation, to entities.ConfirmationState) {
	if to != entities.ConfirmationRejected && to != entities.ConfirmationExpired {
		return
	}
	if s.terminalNotify == nil {
		return
	}
	mid := MiriamConfirmID(c.Payload)
	if mid == "" {
		return
	}
	go s.terminalNotify(context.WithoutCancel(ctx), mid, string(to))
}

func (s *Service) auditOf(c *entities.Confirmation, from, to entities.ConfirmationState, biometric, note string, editOK *bool) {
	if s.audit == nil {
		return
	}
	if from == "" {
		from = entities.ConfirmationPending
	}
	s.audit(entities.ConfirmationAuditEntry{
		ID:             uuid.New(),
		ConfirmationID: c.ID,
		UserID:         c.UserID,
		Action:         c.Action,
		FromState:      from,
		ToState:        to,
		Amount:         c.Amount,
		Biometric:      biometric,
		Note:           note,
		CardEditOK:     editOK,
		CreatedAt:      s.now().UTC(),
	})
}
