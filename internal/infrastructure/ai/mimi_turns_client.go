package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// This file is the Go side of the Python brain's turns contract, schema
// "turns.v1" (MIRIAM repo, docs/TURNS-CONTRACT.md and
// miriam_agent/turns/schemas.py, which are the source of truth).
//
// Go sends one inbound shop message to POST {PYTHON_AGENT_URL}/v1/turns. The
// brain answers with a reply, the typed intents it proposes, and drafts that
// need the owner. Every intent comes back with status "proposed": the brain
// never commits a record and never moves money. Go validates each proposal and
// decides; nothing in this file commits anything.

// TurnsSchemaVersion is the only schema this client speaks.
const TurnsSchemaVersion = "turns.v1"

// TurnsChannelWhatsApp is the only channel turns.v1 accepts.
const TurnsChannelWhatsApp = "whatsapp"

// Default timeout budgets for one /v1/turns call.
//
// A text turn is answered by the rules parser in milliseconds or, for a turn
// the rules call "other", by one model call. 25s covers one model call with
// headroom while staying well inside the bridge's inbound deadline budget.
//
// A turn with media runs the shop reader, which the contract bounds at
// SHOP_READ_WALL_CLOCK_S (140s). The media budget is set above that so Go does
// not give up on a read Python is still finishing. See the PR notes for why
// this is a longer synchronous budget and not an async acknowledge: the brain
// is idempotent on (tenant_id, channel, message_id), so a bridge retry while a
// photo is being read is serialised on the Python side and replays the stored
// answer rather than reading the photo twice.
const (
	DefaultTurnsTextTimeout  = 25 * time.Second
	DefaultTurnsMediaTimeout = 150 * time.Second
)

// Bounds from turns.v1 that Go checks before sending, so an obviously invalid
// request fails locally instead of costing a 422 round trip.
const (
	turnsMaxTextLen = 4000
	turnsMaxMedia   = 5
)

// TurnSender is who wrote the message, as Go resolved them. ID is Go's own id
// for the person and is what consent is checked against; Handle is audit only.
type TurnSender struct {
	ID          string `json:"id"`
	Handle      string `json:"handle,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	// Role is shop_owner, shop_staff or unknown. Informational in v1.
	Role string `json:"role,omitempty"`
}

// TurnMedia is one attachment. turns.v1 reads images and PDFs only from
// DataB64; URL is carried for audit and never fetched.
type TurnMedia struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // image, document, audio, video, other
	MIME       string `json:"mime,omitempty"`
	DataB64    string `json:"data_b64,omitempty"`
	URL        string `json:"url,omitempty"`
	Caption    string `json:"caption,omitempty"`
	DocHint    string `json:"doc_hint,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// TurnRequest is the body of POST /v1/turns. Python refuses unknown fields, so
// this struct carries exactly the contract's fields and nothing else.
type TurnRequest struct {
	SchemaVersion   string      `json:"schema_version"`
	TenantID        string      `json:"tenant_id"`
	ShopID          string      `json:"shop_id"`
	Sender          TurnSender  `json:"sender"`
	Channel         string      `json:"channel"`
	MessageID       string      `json:"message_id"`
	Text            string      `json:"text,omitempty"`
	Media           []TurnMedia `json:"media,omitempty"`
	SentAt          string      `json:"sent_at,omitempty"`
	LocaleHints     []string    `json:"locale_hints,omitempty"`
	DistributorName string      `json:"distributor_name,omitempty"`
}

// HasMedia reports whether the turn carries an attachment, which selects the
// longer timeout budget.
func (r *TurnRequest) HasMedia() bool { return len(r.Media) > 0 }

// TurnReply is what the brain wants sent back to the shop. Language is en or pcm.
type TurnReply struct {
	Text     string `json:"text"`
	Language string `json:"language"`
}

// TurnLine is one goods line in a delivery intent or an order draft. Quantity
// is a decimal string so half cartons stay exact; it may be null on delivery
// lines.
type TurnLine struct {
	Item     string  `json:"item"`
	Quantity *string `json:"quantity,omitempty"`
	Unit     string  `json:"unit,omitempty"`
}

// Intent kinds proposed by turns.v1.
const (
	TurnIntentRecordPaymentClaim = "record_payment_claim"
	TurnIntentRecordStockCount   = "record_stock_count"
	TurnIntentRecordDelivery     = "record_delivery"
)

// TurnIntent is one typed proposal. The kind-specific fields are flattened
// into one struct (only the fields of its own kind are set); Raw keeps the
// exact JSON the brain sent so a store can keep it verbatim for audit.
type TurnIntent struct {
	IntentID         string   `json:"intent_id"`
	Status           string   `json:"status"`
	Source           string   `json:"source"`
	Confidence       float64  `json:"confidence"`
	Evidence         string   `json:"evidence"`
	SourceReadID     *string  `json:"source_read_id"`
	EvidenceMediaIDs []string `json:"evidence_media_ids"`
	Kind             string   `json:"kind"`

	// record_payment_claim
	AmountKobo int64   `json:"amount_kobo,omitempty"`
	Currency   string  `json:"currency,omitempty"`
	Reference  *string `json:"reference,omitempty"`

	// record_stock_count
	Item     string `json:"item,omitempty"`
	Quantity string `json:"quantity,omitempty"`
	Unit     string `json:"unit,omitempty"`

	// record_delivery
	Claim       string     `json:"claim,omitempty"`
	Lines       []TurnLine `json:"lines,omitempty"`
	Issue       *string    `json:"issue,omitempty"`
	DeliveryRef *string    `json:"delivery_ref,omitempty"`

	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the intent and keeps the raw bytes alongside.
func (i *TurnIntent) UnmarshalJSON(data []byte) error {
	type plain TurnIntent
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*i = TurnIntent(p)
	i.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// TouchesMoney reports whether committing this intent would change what a shop
// is recorded as owing or having paid. Those are never committed automatically
// from a chat turn: a payment claim is at most "reported" until Go's own
// matcher ties it to a verified provider credit.
func (i *TurnIntent) TouchesMoney() bool {
	return i.Kind == TurnIntentRecordPaymentClaim || i.AmountKobo != 0
}

// TurnDraft is something that would commit the business (an order request).
// Always audience owner, always needs_approval.
type TurnDraft struct {
	DraftID       string     `json:"draft_id"`
	Kind          string     `json:"kind"`
	Audience      string     `json:"audience"`
	Text          string     `json:"text"`
	Lines         []TurnLine `json:"lines"`
	NeedsApproval bool       `json:"needs_approval"`

	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the draft and keeps the raw bytes alongside.
func (d *TurnDraft) UnmarshalJSON(data []byte) error {
	type plain TurnDraft
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*d = TurnDraft(p)
	d.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// TurnResponse is the 200 body of POST /v1/turns.
type TurnResponse struct {
	SchemaVersion      string       `json:"schema_version"`
	TurnID             string       `json:"turn_id"`
	TraceID            string       `json:"trace_id"`
	MessageID          string       `json:"message_id"`
	Label              string       `json:"label"`
	Confidence         float64      `json:"confidence"`
	NeedsClarification bool         `json:"needs_clarification"`
	Reply              *TurnReply   `json:"reply"`
	Intents            []TurnIntent `json:"intents"`
	Drafts             []TurnDraft  `json:"drafts"`
	Flags              []string     `json:"flags"`
	Replayed           bool         `json:"replayed"`
}

// ErrTurnConflict is a 409: the message id was already used with a different
// body for the same tenant and channel. The contract says log it and do not
// retry, because a retry with the same body would conflict again.
var ErrTurnConflict = errors.New("turns: message_id reused with a different body")

// ErrTurnRejected is any other 4xx (401 bad service key, 422 schema). Not
// retried: the same request will be refused the same way.
var ErrTurnRejected = errors.New("turns: request rejected")

// ErrTurnUnavailable is a 5xx, a timeout or a transport error after the
// retries ran out. The caller may retry later with the same body.
var ErrTurnUnavailable = errors.New("turns: brain unavailable")

// TurnHTTPError carries the status and the brain's error body for logs.
type TurnHTTPError struct {
	Status  int
	Detail  string
	TraceID string
	kind    error
}

func (e *TurnHTTPError) Error() string {
	return fmt.Sprintf("%v: status %d: %s (trace %s)", e.kind, e.Status, e.Detail, e.TraceID)
}

// Unwrap lets callers match ErrTurnConflict, ErrTurnRejected or ErrTurnUnavailable.
func (e *TurnHTTPError) Unwrap() error { return e.kind }

// MimiTurnsClientConfig holds the turns client's wiring.
type MimiTurnsClientConfig struct {
	// BaseURL is the Python brain, the same PYTHON_AGENT_URL the legacy client uses.
	BaseURL string
	// RailServiceKey is sent as X-Rail-Service-Key. Required: /v1/turns is
	// service only and answers 401 without it.
	RailServiceKey string
	// TextTimeout and MediaTimeout bound one attempt. Zero uses the defaults.
	TextTimeout  time.Duration
	MediaTimeout time.Duration
	// MaxAttempts bounds retries on timeout, transport errors and 5xx, always
	// with the same body (the contract's idempotency makes that safe). Zero
	// means 2 for text and 1 for media, so a photo is never read three times
	// back to back inside one inbound request.
	MaxAttempts int
	// HTTPClient is optional; tests inject one. Its own Timeout is not used,
	// the per-attempt context deadline is.
	HTTPClient *http.Client
}

// MimiTurnsClient calls POST /v1/turns.
type MimiTurnsClient struct {
	baseURL      string
	serviceKey   string
	textTimeout  time.Duration
	mediaTimeout time.Duration
	maxAttempts  int
	httpClient   *http.Client
	logger       *zap.Logger
	backoff      time.Duration
}

// NewMimiTurnsClient builds a turns client.
func NewMimiTurnsClient(cfg MimiTurnsClientConfig, logger *zap.Logger) *MimiTurnsClient {
	if logger == nil {
		logger = zap.NewNop()
	}
	text := cfg.TextTimeout
	if text <= 0 {
		text = DefaultTurnsTextTimeout
	}
	media := cfg.MediaTimeout
	if media <= 0 {
		media = DefaultTurnsMediaTimeout
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &MimiTurnsClient{
		baseURL:      strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		serviceKey:   strings.TrimSpace(cfg.RailServiceKey),
		textTimeout:  text,
		mediaTimeout: media,
		maxAttempts:  cfg.MaxAttempts,
		httpClient:   hc,
		logger:       logger,
		backoff:      300 * time.Millisecond,
	}
}

// Configured reports whether the client has what it needs to make a call.
func (c *MimiTurnsClient) Configured() bool {
	return c != nil && c.baseURL != "" && c.serviceKey != ""
}

// TimeoutFor returns the per-attempt budget for a request.
func (c *MimiTurnsClient) TimeoutFor(req *TurnRequest) time.Duration {
	if req != nil && req.HasMedia() {
		return c.mediaTimeout
	}
	return c.textTimeout
}

func (c *MimiTurnsClient) attemptsFor(req *TurnRequest) int {
	if c.maxAttempts > 0 {
		return c.maxAttempts
	}
	if req.HasMedia() {
		return 1
	}
	return 2
}

// ValidateTurnRequest checks the contract's required fields and bounds.
func ValidateTurnRequest(req *TurnRequest) error {
	if req == nil {
		return fmt.Errorf("turns: nil request")
	}
	if req.SchemaVersion != "" && req.SchemaVersion != TurnsSchemaVersion {
		return fmt.Errorf("turns: unsupported schema_version %q", req.SchemaVersion)
	}
	if req.Channel != "" && req.Channel != TurnsChannelWhatsApp {
		return fmt.Errorf("turns: unsupported channel %q", req.Channel)
	}
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.ShopID) == "" {
		return fmt.Errorf("turns: tenant_id and shop_id are required (never guessed)")
	}
	if strings.TrimSpace(req.Sender.ID) == "" {
		return fmt.Errorf("turns: sender.id is required")
	}
	if strings.TrimSpace(req.MessageID) == "" {
		return fmt.Errorf("turns: message_id is required (it is the idempotency key)")
	}
	if strings.TrimSpace(req.Text) == "" && len(req.Media) == 0 {
		return fmt.Errorf("turns: a turn needs text or at least one media item")
	}
	if len([]rune(req.Text)) > turnsMaxTextLen {
		return fmt.Errorf("turns: text longer than %d characters", turnsMaxTextLen)
	}
	if len(req.Media) > turnsMaxMedia {
		return fmt.Errorf("turns: at most %d media items", turnsMaxMedia)
	}
	for i, m := range req.Media {
		if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Kind) == "" {
			return fmt.Errorf("turns: media[%d] needs id and kind", i)
		}
	}
	return nil
}

// Turn sends one inbound message to the brain and returns its answer.
//
// The body is marshalled once and every retry sends the same bytes, which is
// what the contract's idempotency on (tenant_id, channel, message_id) needs: a
// retry of a turn that did run gets the stored answer with replayed=true and
// the same intent ids. traceID is optional and sent as X-Miriam-Trace-Id.
func (c *MimiTurnsClient) Turn(ctx context.Context, req *TurnRequest, traceID string) (*TurnResponse, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("turns: client not configured (need PYTHON_AGENT_URL and RAIL_SERVICE_KEY)")
	}
	if req == nil {
		return nil, fmt.Errorf("turns: nil request")
	}
	out := *req
	if out.SchemaVersion == "" {
		out.SchemaVersion = TurnsSchemaVersion
	}
	if out.Channel == "" {
		out.Channel = TurnsChannelWhatsApp
	}
	if err := ValidateTurnRequest(&out); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(&out)
	if err != nil {
		return nil, fmt.Errorf("turns: marshal request: %w", err)
	}

	attempts := c.attemptsFor(&out)
	timeout := c.TimeoutFor(&out)
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("%w: %v (last error: %v)", ErrTurnUnavailable, ctx.Err(), lastErr)
			case <-time.After(time.Duration(attempt) * c.backoff):
			}
		}
		resp, retry, err := c.post(ctx, payload, timeout, traceID)
		if err == nil {
			if resp.MessageID != "" && resp.MessageID != out.MessageID {
				return nil, fmt.Errorf("%w: response message_id %q does not match request %q",
					ErrTurnRejected, resp.MessageID, out.MessageID)
			}
			if resp.SchemaVersion != "" && resp.SchemaVersion != TurnsSchemaVersion {
				return nil, fmt.Errorf("%w: response schema_version %q", ErrTurnRejected, resp.SchemaVersion)
			}
			return resp, nil
		}
		lastErr = err
		if errors.Is(err, ErrTurnConflict) {
			c.logger.Warn("turns: message_id reused with a different body; not retrying",
				zap.String("message_id", out.MessageID),
				zap.String("tenant_id", out.TenantID),
				zap.Error(err))
			return nil, err
		}
		if !retry {
			return nil, err
		}
		if ctx.Err() != nil {
			break
		}
	}
	if errors.Is(lastErr, ErrTurnUnavailable) {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w: %v", ErrTurnUnavailable, lastErr)
}

// post sends one attempt. retry reports whether the failure is worth another
// attempt with the same body.
func (c *MimiTurnsClient) post(ctx context.Context, payload []byte, timeout time.Duration, traceID string) (*TurnResponse, bool, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, c.baseURL+"/v1/turns", bytes.NewReader(payload))
	if err != nil {
		return nil, false, fmt.Errorf("turns: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Rail-Service-Key", c.serviceKey)
	if t := strings.TrimSpace(traceID); t != "" {
		httpReq.Header.Set("X-Miriam-Trace-Id", t)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// A per-attempt timeout or a transport error is retryable with the same
		// body, unless the caller's own context is done.
		return nil, ctx.Err() == nil, fmt.Errorf("%w: %v", ErrTurnUnavailable, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, ctx.Err() == nil, fmt.Errorf("%w: read body: %v", ErrTurnUnavailable, err)
	}

	if resp.StatusCode == http.StatusOK {
		var out TurnResponse
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, false, fmt.Errorf("%w: decode response: %v", ErrTurnRejected, err)
		}
		return &out, false, nil
	}

	herr := &TurnHTTPError{Status: resp.StatusCode, TraceID: resp.Header.Get("X-Miriam-Trace-Id")}
	var eb struct {
		Detail  json.RawMessage `json:"detail"`
		TraceID string          `json:"trace_id"`
	}
	if json.Unmarshal(body, &eb) == nil {
		herr.Detail = strings.Trim(string(eb.Detail), `"`)
		if eb.TraceID != "" {
			herr.TraceID = eb.TraceID
		}
	} else {
		herr.Detail = truncateForLog(string(body), 300)
	}
	switch {
	case resp.StatusCode == http.StatusConflict:
		herr.kind = ErrTurnConflict
		return nil, false, herr
	case resp.StatusCode >= 500:
		herr.kind = ErrTurnUnavailable
		return nil, true, herr
	default:
		herr.kind = ErrTurnRejected
		return nil, false, herr
	}
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
