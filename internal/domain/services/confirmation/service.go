package confirmation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Store persists confirmations. The default is in-memory; production should
// back this with Postgres (one row per card) so replays stay single-use
// across restarts.
type Store interface {
	Save(c *entities.Confirmation)
	Load(id uuid.UUID) (*entities.Confirmation, bool)
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

func (s *memoryStore) Save(c *entities.Confirmation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.m[c.ID] = &cp
}

func (s *memoryStore) Load(id uuid.UUID) (*entities.Confirmation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.m[id]
	if !ok {
		return nil, false
	}
	cp := *c
	return &cp, true
}

// executeInFlight reserves an ExecuteKey while the money movement runs so a
// concurrent approve on the same card fails closed instead of double-spending.
const executeInFlight = "__inflight__"

// Service is the server-side source of truth for confirmation cards.
type Service struct {
	cfg       Config
	store     Store
	devices   DeviceStore
	log       *logger.Logger
	mu        sync.Mutex
	executors map[entities.ConfirmationAction]Executor
	renderers map[entities.ConfirmationAction]Renderer
	executed  map[string]string // execute key -> result summary (idempotent replay)
	cardEdit  CardEditor
	audit     AuditSink
	// strictDeviceSig rejects token-only approves outright. Default false:
	// unknown devices fall back to token-only (audited) until they enroll.
	strictDeviceSig bool
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
		devices:   newMemoryDeviceStore(),
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

// SetDeviceStore replaces the default in-memory approval-key store
// (e.g. with the confirmation_device_keys table).
func (s *Service) SetDeviceStore(d DeviceStore) {
	if d != nil {
		s.devices = d
	}
}

// SetStrictDeviceSignature rejects token-only approves outright. Leave off
// until the fleet is enrolled — strict mode with no enrolled keys rejects
// everything, including first-use enrollment.
func (s *Service) SetStrictDeviceSignature(strict bool) { s.strictDeviceSig = strict }

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

// maxCreateTTL caps caller-requested card lifetimes so a compromised JWT
// cannot mint year-long money-moving links (default is 5m).
const maxCreateTTL = 15 * time.Minute

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
	if ttl > maxCreateTTL {
		ttl = maxCreateTTL
	}
	// NOTE: the miriam_confirm_id payload key (Miriam-originated challenge
	// binding) is stripped at the app HTTP boundary (Handler.Create), not
	// here: service-level Create is also how Miriam-originated cards are
	// staged in tests and server-side flows, and MarkExternal/settle read
	// the binding from the payload.
	// NOTE: Create stays permissive on actions without a direct executor:
	// Miriam-originated cards settle through MarkExternal/chat-first flows
	// that never touch Approve. Approve itself fails closed BEFORE consuming
	// the single-use token when no executor is registered (see below), so a
	// token is never burned into terminal failed.
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
	s.store.Save(c)
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
// On signature/expiry failure only terminal records are surfaced (so the
// extension renders dead, not broken); pending cards require a valid token
// so a bare action_id cannot leak amount/destination.
func (s *Service) Fetch(ctx context.Context, id uuid.UUID, token string) (*entities.Confirmation, error) {
	if err := s.VerifyToken(id, token); err != nil {
		if c, ok := s.store.Load(id); ok && c.IsTerminal() {
			cp := *c
			return &cp, nil
		}
		return nil, err
	}
	c, ok := s.store.Load(id)
	if !ok {
		return nil, fmt.Errorf("confirmation not found")
	}
	if !c.IsTerminal() && c.IsExpired(s.now()) {
		s.transition(ctx, c, entities.ConfirmationExpired, "", "ttl elapsed on fetch")
	}
	out, _ := s.store.Load(id)
	return out, nil
}

// DeviceApproval carries the extension's biometric-bound proof. KeyID +
// Signature come from the enrolled Secure Enclave key; EnrollKey is a base64
// SPKI sent once for trust-on-first-use enrollment. All empty = legacy
// token-only approve (audited, rejected under strict mode).
type DeviceApproval struct {
	KeyID     string
	Signature string
	EnrollKey string
}

// Approve runs Face ID success + server accept. Idempotent by actionId:
// replays after a terminal state return the terminal record as a no-op.
func (s *Service) Approve(ctx context.Context, userID, id uuid.UUID, token, biometric string) (*entities.Confirmation, error) {
	return s.ApproveWithDevice(ctx, userID, id, token, biometric, DeviceApproval{})
}

// ApproveWithDevice is Approve plus Secure Enclave verification. The device
// check runs after token + ownership + expiry checks and before the token is
// consumed, so a failed signature never burns the single-use token.
//
// Concurrency: the TokenUsed consume + executed-map reservation hold s.mu,
// so N concurrent approves on one card execute the money movement exactly
// once. The second arrival sees the in-flight reservation and fails closed
// (retry → terminal replay), never a second execution.
func (s *Service) ApproveWithDevice(ctx context.Context, userID, id uuid.UUID, token, biometric string, dev DeviceApproval) (*entities.Confirmation, error) {
	if err := s.VerifyToken(id, token); err != nil {
		return nil, err
	}
	exp, _, err := splitToken(token)
	if err != nil {
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
		out, _ := s.store.Load(id) // replay: no-op, terminal state stands
		return out, nil
	}
	if c.IsExpired(s.now()) {
		s.transition(ctx, c, entities.ConfirmationExpired, biometric, "ttl elapsed on approve")
		out, _ := s.store.Load(id)
		return out, nil
	}
	if biometric != "" && biometric != "pass" {
		return nil, fmt.Errorf("biometric not passed (got %q)", biometric)
	}
	// Client-asserted biometrics alone never move money when device material
	// is present: an empty biometric with a key/enrollment attached is a
	// protocol violation, not a legacy approve. Fully-empty (legacy) is still
	// allowed for unenrolled users until strict mode flips (audited).
	if biometric == "" && (dev.KeyID != "" || dev.Signature != "" || dev.EnrollKey != "") {
		return nil, fmt.Errorf("biometric required with device approval (fail-closed)")
	}
	assurance, enrolledKeyID, err := s.checkDevice(userID, id, exp, biometric, dev)
	if err != nil {
		return nil, err
	}
	// Fail closed BEFORE consuming the single-use token: an executor-less
	// action (renderer exists, no backend handler) must never burn a token
	// into terminal failed — the card stays live and retryable.
	s.mu.Lock()
	ex, registered := s.executors[c.Action]
	s.mu.Unlock()
	if !registered {
		return nil, fmt.Errorf("no executor for action %q (fail-closed)", c.Action)
	}
	s.mu.Lock()
	// Re-check under lock: a concurrent approve may have consumed the token
	// between our first load and now.
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
	// Reserve before running the executor so a concurrent approve cannot
	// start a second execution while we hold no lock during ex(). ex was
	// resolved in the pre-consume check above (executors are wired once at
	// startup, never unregistered at runtime).
	s.executed[c.ExecuteKey] = executeInFlight
	c.Assurance = assurance
	c.EnrolledKeyID = enrolledKeyID
	c.TokenUsed = true
	s.store.Save(c)
	s.mu.Unlock()
	s.transition(ctx, c, entities.ConfirmationAuthenticating, "pass", "biometrics passed (assurance="+assurance+")")
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

// checkDevice enforces biometric-bound approval. Enrolled users must present
// a valid Enclave signature (fail closed); unenrolled users enroll once via
// trust-on-first-use (short-lived token + client-asserted biometric, audited)
// or fall back to token-only unless strict mode rejects it.
func (s *Service) checkDevice(userID, id uuid.UUID, exp int64, biometric string, dev DeviceApproval) (assurance, enrolledKeyID string, err error) {
	store := s.devices
	if store == nil {
		store = newMemoryDeviceStore()
	}
	keys, err := store.Keys(userID)
	if err != nil {
		return "", "", fmt.Errorf("device lookup failed (fail-closed): %w", err)
	}
	if len(keys) > 0 {
		return s.verifyEnrolled(store, id, exp, biometric, dev, keys)
	}
	return s.enrollOrLegacy(store, userID, biometric, dev)
}

// verifyEnrolled requires a valid Enclave signature once keys exist. Every
// failure rejects without consuming the single-use token, so the real device
// can retry.
func (s *Service) verifyEnrolled(store DeviceStore, id uuid.UUID, exp int64, biometric string, dev DeviceApproval, keys []DeviceKey) (string, string, error) {
	if dev.KeyID == "" || dev.Signature == "" {
		return "", "", fmt.Errorf("device signature required (approval key enrolled)")
	}
	if biometric != "" && biometric != "pass" {
		return "", "", fmt.Errorf("biometric not passed (got %q)", biometric)
	}
	keyID, err := uuid.Parse(dev.KeyID)
	if err != nil {
		return "", "", fmt.Errorf("unknown device key")
	}
	var enrolled *DeviceKey
	for i := range keys {
		if keys[i].ID == keyID {
			enrolled = &keys[i]
			break
		}
	}
	if enrolled == nil {
		return "", "", fmt.Errorf("unknown device key")
	}
	sig, err := base64.StdEncoding.DecodeString(dev.Signature)
	if err != nil {
		return "", "", fmt.Errorf("device signature is not base64")
	}
	if err := VerifyDeviceSignature(enrolled.SPKI, SignedMessage(id.String(), exp), sig); err != nil {
		return "", "", err
	}
	store.Touch(enrolled.ID)
	return AssuranceSecureEnclave, "", nil
}

// enrollOrLegacy handles first contact: trust-on-first-use enrollment when the
// extension offers a key (requires explicit biometric "pass" — an empty
// biometric with an enroll key is rejected as a protocol violation), or
// token-only fallback otherwise (rejected in strict mode).
func (s *Service) enrollOrLegacy(store DeviceStore, userID uuid.UUID, biometric string, dev DeviceApproval) (string, string, error) {
	if dev.EnrollKey != "" {
		if s.strictDeviceSig {
			return "", "", fmt.Errorf("device enrollment required out-of-band (strict mode)")
		}
		if biometric != "pass" {
			return "", "", fmt.Errorf("biometric required for device enrollment (fail-closed)")
		}
		spki, err := ParseDevicePublicKey(dev.EnrollKey)
		if err != nil {
			return "", "", err
		}
		enrolled, err := store.Enroll(userID, spki)
		if err != nil {
			return "", "", fmt.Errorf("device enrollment failed: %w", err)
		}
		return AssuranceEnrolled, enrolled.ID.String(), nil
	}
	if s.strictDeviceSig {
		return "", "", fmt.Errorf("device enrollment required (strict mode)")
	}
	return AssuranceTokenOnly, "", nil
}

// Reject records a Face ID cancel. Single-use: later replays are no-ops.
func (s *Service) Reject(ctx context.Context, userID, id uuid.UUID, token, biometric string) (*entities.Confirmation, error) {
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
	// Atomic consume shared with ApproveWithDevice: a concurrent approve
	// cannot slip a money movement past a simultaneous cancel.
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
	c.TokenUsed = true
	s.store.Save(c)
	s.mu.Unlock()
	if biometric == "" {
		biometric = "cancel"
	}
	s.transition(ctx, c, entities.ConfirmationRejected, biometric, "user cancelled")
	out, _ := s.store.Load(id)
	return out, nil
}

// ExpireSweep marks a known card expired (TTL worker / lazy expiry path).
func (s *Service) ExpireSweep(ctx context.Context, id uuid.UUID) (*entities.Confirmation, bool) {
	c, ok := s.store.Load(id)
	if !ok || c.IsTerminal() {
		return c, false
	}
	if !c.IsExpired(s.now()) {
		return c, false
	}
	s.transition(ctx, c, entities.ConfirmationExpired, "", "ttl sweep")
	out, _ := s.store.Load(id)
	return out, true
}

// MarkExternal applies a terminal state reported by Miriam (chat settled
// first): it validates every edge via ValidConfirmationTransition, persists,
// and triggers the card edit. Already-terminal cards return their current
// state as a no-op, so replays and races converge instead of erroring.
func (s *Service) MarkExternal(ctx context.Context, id uuid.UUID, to entities.ConfirmationState, result string) (*entities.Confirmation, error) {
	c, ok := s.store.Load(id)
	if !ok {
		return nil, fmt.Errorf("confirmation not found")
	}
	if c.IsTerminal() || c.State == to {
		out, _ := s.store.Load(id)
		return out, nil
	}
	// Walk the legal path step by step: a pending card marked completed moves
	// through authenticating/approved (each step persisted + edited) rather
	// than jumping states or sticking forever.
	for i := 0; i < 4; i++ {
		cur, ok := s.store.Load(id)
		if !ok || cur.IsTerminal() || cur.State == to {
			break
		}
		next := stepToward(cur.State, to)
		if next == "" {
			break // no legal edge onward: leave the card where it stands
		}
		s.transition(ctx, cur, next, "", result)
	}
	out, _ := s.store.Load(id)
	return out, nil
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
func (s *Service) transition(ctx context.Context, c *entities.Confirmation, to entities.ConfirmationState, biometric, note string) {
	from := c.State
	if from == to {
		return
	}
	if !entities.ValidConfirmationTransition(from, to) {
		// Fail closed: illegal edges never persist. Terminal replay paths
		// call with equal states or legal edges only.
		if s.log != nil {
			s.log.Warn("illegal confirmation transition",
				"action_id", c.ID.String(), "from", string(from), "to", string(to))
		}
		return
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
	s.store.Save(c)
	var ok *bool
	if s.cardEdit != nil {
		err := s.cardEdit(ctx, c)
		b := err == nil
		ok = &b
		if err != nil {
			c.CardEditFailed = true
			s.store.Save(c)
			if s.log != nil {
				s.log.Warn("confirmation card edit failed (state persisted, retry edit)",
					"action_id", c.ID.String(), "state", string(to), "error", err.Error())
			}
		}
	}
	s.auditOf(c, from, to, biometric, note, ok)
	s.maybeNotifyTerminal(ctx, c, to)
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
