package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// The shop read is the one place Miriam answers with figures instead of prose.
// A trader texts a photo of their own stock book, or a distributor's invoice /
// delivery note, and the Python reader returns the reply text verbatim: it has
// already checked every number against the page with plain code, so Go must not
// paraphrase it, model it, or add a figure of its own.
//
// Go's job here is only the routing and the honesty around it: recognise the
// page, put a holding bubble in the thread immediately, run the read off the
// request (it takes tens of seconds), and send back either the reader's own
// words or a plain "I couldn't read that" — never a partial read.
const (
	// shopReadTimeout bounds one read end to end. The reader's own wall clock
	// ceiling is 140s; this sits above it so a slow read fails here rather than
	// half-answering there.
	shopReadTimeout = 150 * time.Second
	// shopReplySendTimeout bounds the delivery of a finished read. It runs on
	// its own context because the read's context has usually expired by then.
	shopReplySendTimeout = 20 * time.Second
	// shopClaimTTL is how long one inbound message id stays claimed. The bridge
	// redelivers within minutes, so a claim that outlives the longest possible
	// read (and its retry) is enough to stop a double read of the same photo.
	shopClaimTTL = 15 * time.Minute
	// maxShopReadBase64Bytes bounds what we hand to the reader. Base64 expands
	// by ~4/3, so this is the wire size of a 15MB photo — the same ceiling the
	// receipt path uses, well above a phone photo of a page.
	maxShopReadBase64Bytes = (15 << 20) * 4 / 3
	// maxShopCaptionRunes bounds the caption the reader sees. It is context for
	// the reader, never the source of a figure.
	maxShopCaptionRunes = 400
)

// Shop read copy. Fixed strings on purpose: everything the owner reads about
// money comes from the reader, and this is only the frame around it.
const (
	// shopHoldingText is sent the moment we accept the page, so the owner knows
	// Miriam is looking at it rather than stuck or ignoring them.
	shopHoldingText = "Let me read this one properly — give me a moment, I'll come back with the figures."
	// shopUnreadableText is the fail-closed answer: no numbers, nothing saved.
	shopUnreadableText = "I couldn't read that one well enough to trust the numbers, so I haven't saved anything. " +
		"Send it again flat and well lit — or as a PDF if you have one — and I'll take another look."
	// shopTooLargeText refuses a page before spending a read on it.
	shopTooLargeText = "That one is too big for me to read. Send it again, or crop it to the page itself."
)

// ShopReadKind is the reader's own vocabulary for the page. The values are the
// wire contract with the reader's classifier and its schemas.
type ShopReadKind string

const (
	ShopKindStockBook    ShopReadKind = "stock_book"
	ShopKindInvoice      ShopReadKind = "invoice"
	ShopKindDeliveryNote ShopReadKind = "delivery_note"
)

// ShopReadRequest is one page handed to the reader on a linked owner's behalf.
type ShopReadRequest struct {
	// UserID is the rail account that owns the page — the reader stores the
	// document against it.
	UserID uuid.UUID
	// Platform and ThreadID are for the reader's logs only; the reply goes back
	// through the processor, never straight to the bridge.
	Platform entities.Platform
	ThreadID string
	// MessageID makes the read idempotent on the reader's side: the same bridge
	// message read twice is one document.
	MessageID string
	// Caption is what the owner typed with the page.
	Caption string
	// DocHint is Go's classification, which the reader uses to break a tie in
	// its own routing. Empty when Go has no opinion.
	DocHint string
	// FileB64 is the attachment exactly as it arrived — the reader owns
	// preprocessing (EXIF, HEIC, downscale, PDF rasterization).
	FileB64 string
	MIME    string
}

// ShopReadResult is the reader's answer. ReplyText is sent to the owner
// verbatim.
type ShopReadResult struct {
	ReplyText  string
	DocID      string
	DocType    string
	Flags      []string
	Confidence float64
	ModelPath  []string
}

// Failed reports whether the reader gave up. It answers with the retake
// message and no stored document rather than a partial read, so the processor
// must not treat its text as a saved result.
func (r *ShopReadResult) Failed() bool {
	if r == nil {
		return true
	}
	for _, f := range r.Flags {
		if f == "failed" {
			return true
		}
	}
	return false
}

// ShopReader reads a shop document through the Python reader, which owns
// vision, model routing and the arithmetic checks. Implemented in DI, where the
// owner's email and role are known for the per-user token.
type ShopReader interface {
	ReadShopDocument(ctx context.Context, req ShopReadRequest) (*ShopReadResult, error)
}

// MessageClaimStore claims an inbound message id exactly once. Satisfied by the
// same Redis client the onboarding turn lock uses; the in-memory implementation
// below is for tests and single-instance runs.
type MessageClaimStore interface {
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error)
}

// NewMemoryMessageClaimStore returns a process-local claim store. It stops a
// redelivery inside one backend process; with more than one replica a bridge
// redelivery that lands elsewhere could read the same page twice. Wire Redis in
// production — it is already required for the platform messaging path.
func NewMemoryMessageClaimStore() *MemoryMessageClaimStore {
	return &MemoryMessageClaimStore{seen: map[string]time.Time{}}
}

// MemoryMessageClaimStore is a mutex-guarded expiring set of claim keys.
type MemoryMessageClaimStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// SetNX claims key unless it is still held. Expired entries are dropped on the
// way past, so the map cannot grow without bound.
func (s *MemoryMessageClaimStore) SetNX(_ context.Context, key string, _ interface{}, expiration time.Duration) (bool, error) {
	if strings.TrimSpace(key) == "" {
		return false, fmt.Errorf("claim store: empty key")
	}
	if expiration <= 0 {
		expiration = shopClaimTTL
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if until, ok := s.seen[key]; ok {
		if until.After(now) {
			return false, nil
		}
		delete(s.seen, key)
	}
	s.seen[key] = now.Add(expiration)
	return true, nil
}

// shopPage is an attachment Go has decided belongs to the reader, held between
// the point it was recognised and the point the sender's account is known.
type shopPage struct {
	Kind    ShopReadKind
	FileB64 string
	MIME    string
	// Caption is what the owner typed with the page, kept from before the
	// vision pass overwrites the turn's text with its own description.
	Caption string
}

// shopKindHint validates a classification as a hint for the reader. Anything
// that is not one of the reader's own kinds is sent as no hint at all, so a
// wrong guess in Go cannot steer the read.
func shopKindHint(kind string) string {
	switch ShopReadKind(strings.TrimSpace(kind)) {
	case ShopKindStockBook, ShopKindInvoice, ShopKindDeliveryNote:
		return strings.TrimSpace(kind)
	default:
		return ""
	}
}

// SetShopReader enables the shop read path. Nil-safe: with no reader, a stock
// book photo keeps its previous behaviour instead of failing loudly.
func (p *Processor) SetShopReader(r ShopReader) {
	p.shop = r
}

// SetShopReadTimeout overrides the read deadline. Zero keeps the default.
func (p *Processor) SetShopReadTimeout(d time.Duration) {
	p.shopReadTimeout = d
}

// SetShopClaimStore installs the claim store that makes one inbound message
// read once, however many times the bridge delivers it.
func (p *Processor) SetShopClaimStore(s MessageClaimStore) {
	p.shopClaims = s
}

func (p *Processor) shopEnabled() bool {
	return p.shop != nil
}

func (p *Processor) shopReadDeadline() time.Duration {
	if p.shopReadTimeout > 0 {
		return p.shopReadTimeout
	}
	return shopReadTimeout
}

// handleShopDocument accepts a recognised shop page: it claims the message,
// answers with a holding bubble, and starts the read off-request so the bridge
// sees a fast, committed response.
func (p *Processor) handleShopDocument(ctx context.Context, msg InboundMessage, resolved *ResolvedUser, page *shopPage) error {
	if page == nil || strings.TrimSpace(page.FileB64) == "" {
		return p.sendErrorMessage(ctx, msg, shopUnreadableText)
	}
	if len(page.FileB64) > maxShopReadBase64Bytes {
		return p.sendErrorMessage(ctx, msg, shopTooLargeText)
	}
	if resolved == nil || resolved.UserID == uuid.Nil {
		// Never read a document without an owner: it would be stored against
		// nobody, and nobody could be told the result.
		return p.sendErrorMessage(ctx, msg, shopUnreadableText)
	}
	if !p.claimShopMessage(ctx, msg, page) {
		return nil
	}

	// Holding bubble first: the read can take a minute and a half, and a silent
	// thread is indistinguishable from a broken one.
	p.noticeSender(ctx, msg, shopHoldingText)

	p.readShopDocumentAsync(msg, resolved.UserID, page)
	return nil
}

// claimShopMessage claims this inbound message so a bridge redelivery cannot
// read (and pay for) the same page twice. A claim-store failure fails closed:
// an unclaimed redelivery is a double read, so the owner is asked to send it
// again rather than being read twice.
func (p *Processor) claimShopMessage(ctx context.Context, msg InboundMessage, page *shopPage) bool {
	if p.shopClaims == nil {
		return true
	}
	claimed, err := p.shopClaims.SetNX(ctx, shopClaimKey(msg, page), 1, shopClaimTTL)
	if err != nil {
		p.logger.Warn("shop read claim failed; refusing the read rather than risking a double charge",
			zap.Error(err),
			zap.String("platform", string(msg.Platform)),
			zap.String("user_id", msg.UserID),
		)
		_ = p.sendErrorMessage(ctx, msg, shopUnreadableText)
		return false
	}
	if !claimed {
		p.logger.Info("shop read already claimed for this message; ignoring redelivery",
			zap.String("platform", string(msg.Platform)),
			zap.String("msg_id", msg.MsgID),
			zap.String("thread_id", msg.ThreadID),
		)
		return false
	}
	return true
}

// shopClaimKey identifies the claimed read. The bridge message id is the real
// key; when it is missing (older bridges), the page's own bytes serve so the
// same photo is still read once.
func shopClaimKey(msg InboundMessage, page *shopPage) string {
	id := strings.TrimSpace(msg.MsgID)
	if id == "" {
		sum := sha256.Sum256([]byte(page.FileB64))
		id = "sha:" + hex.EncodeToString(sum[:16])
	}
	return fmt.Sprintf("platform:shopread:%s:%s", msg.Platform, id)
}

// readShopDocumentAsync runs the read in the background. It deliberately does
// not inherit the inbound request's context: the bridge's POST is already
// answered, and that context dies the moment the handler returns.
func (p *Processor) readShopDocumentAsync(msg InboundMessage, userID uuid.UUID, page *shopPage) {
	caption := page.Caption
	if runes := []rune(caption); len(runes) > maxShopCaptionRunes {
		caption = string(runes[:maxShopCaptionRunes])
	}
	req := ShopReadRequest{
		UserID:    userID,
		Platform:  msg.Platform,
		ThreadID:  msg.ThreadID,
		MessageID: msg.MsgID,
		Caption:   caption,
		DocHint:   shopKindHint(string(page.Kind)),
		FileB64:   page.FileB64,
		MIME:      page.MIME,
	}
	deadline := p.shopReadDeadline()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()

		// A panic in the client must not take the process down: the owner gets
		// the retake line and can send the page again.
		defer func() {
			if r := recover(); r != nil {
				p.logger.Error("shop read panicked",
					zap.String("platform", string(msg.Platform)),
					zap.String("user_id", msg.UserID),
					zap.String("msg_id", msg.MsgID),
					zap.Any("panic", r),
				)
				p.deliverShopReply(msg, shopUnreadableText)
			}
		}()

		result, err := p.shop.ReadShopDocument(ctx, req)
		if err != nil {
			// Timeouts, provider failures and transport errors all land here.
			// Nothing was saved and nothing is claimed to the owner.
			p.logger.Warn("shop read failed",
				zap.Error(err),
				zap.String("platform", string(msg.Platform)),
				zap.String("user_id", msg.UserID),
				zap.String("msg_id", msg.MsgID),
				zap.String("kind", string(page.Kind)),
			)
			p.deliverShopReply(msg, shopUnreadableText)
			return
		}

		text := strings.TrimSpace(result.ReplyText)
		if result.Failed() {
			p.logger.Info("shop read declined the page",
				zap.String("user_id", msg.UserID),
				zap.String("msg_id", msg.MsgID),
				zap.Float64("confidence", result.Confidence),
			)
			if text == "" {
				text = shopUnreadableText
			}
			p.deliverShopReply(msg, text)
			return
		}
		if text == "" {
			p.deliverShopReply(msg, shopUnreadableText)
			return
		}

		p.logger.Info("shop read delivered",
			zap.String("user_id", msg.UserID),
			zap.String("msg_id", msg.MsgID),
			zap.String("doc_type", result.DocType),
			zap.String("doc_id", result.DocID),
			zap.Float64("confidence", result.Confidence),
			zap.Any("model_path", result.ModelPath),
		)
		p.deliverShopReply(msg, text)
	}()
}

// deliverShopReply sends the finished answer back into the thread it came from.
// It builds its own short-lived context: the read's context has usually expired
// by the time we deliver, and the answer must still land.
func (p *Processor) deliverShopReply(msg InboundMessage, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shopReplySendTimeout)
	defer cancel()
	if err := p.sendToSender(ctx, msg, text); err != nil {
		p.logger.Error("shop read reply failed to send",
			zap.Error(err),
			zap.String("platform", string(msg.Platform)),
			zap.String("user_id", msg.UserID),
			zap.String("thread_id", msg.ThreadID),
		)
	}
}
