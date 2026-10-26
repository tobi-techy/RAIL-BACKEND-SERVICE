package platform

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/document"
)

// fakeShopReader stands in for the Python reader: it records the request and
// answers with a canned result, an error, or a panic.
type fakeShopReader struct {
	mu      sync.Mutex
	calls   []ShopReadRequest
	result  *ShopReadResult
	err     error
	panicOn bool
}

func (f *fakeShopReader) ReadShopDocument(_ context.Context, req ShopReadRequest) (*ShopReadResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	if f.panicOn {
		panic("reader exploded")
	}
	return f.result, f.err
}

func (f *fakeShopReader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeShopReader) last() ShopReadRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ShopReadRequest{}
	}
	return f.calls[len(f.calls)-1]
}

// fakeShopVision reports the page's kind without OCR.
type fakeShopVision struct {
	kind    document.DocType
	summary string
	err     error
}

func (f *fakeShopVision) Available() bool { return true }

func (f *fakeShopVision) ReadImage(context.Context, []byte, string) (*AttachmentRead, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &AttachmentRead{Kind: f.kind, Summary: f.summary}, nil
}

// sentRecorder records outbound messages from both the request path and the
// read's goroutine, so a test can wait for the asynchronous answer.
type sentRecorder struct {
	mu   sync.Mutex
	msgs []*OutboundMessage
}

func (r *sentRecorder) send(_ context.Context, m *OutboundMessage) error {
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
	return nil
}

func (r *sentRecorder) all() []*OutboundMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*OutboundMessage(nil), r.msgs...)
}

// waitFor waits until at least n messages have been sent.
func (r *sentRecorder) waitFor(t *testing.T, n int) []*OutboundMessage {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.all(); len(got) >= n {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	got := r.all()
	t.Fatalf("timed out waiting for %d messages, got %d: %v", n, len(got), texts(got))
	return nil
}

func texts(msgs []*OutboundMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Text)
	}
	return out
}

func shopTestProcessor(repo *fakeRepo, orch Orchestrator) (*Processor, *sentRecorder) {
	rec := &sentRecorder{}
	p := NewProcessor(NewUserResolver(repo), orch, NewResponseBuilder(), NewLinkingService(repo, 900), nil, rec.send)
	return p, rec
}

// shopImageMessage is a photo from a linked trader, with a caption.
func shopImageMessage(msgID, caption string) InboundMessage {
	return InboundMessage{
		Platform:  entities.PlatformIMessage,
		UserID:    "+15551234",
		ThreadID:  "thread-1",
		MsgID:     msgID,
		IsImage:   true,
		ImageB64:  base64.StdEncoding.EncodeToString([]byte("pretend-jpeg-page")),
		ImageMime: "image/jpeg",
		Text:      caption,
	}
}

func shopDocumentMessage(msgID, caption string, pdf []byte) InboundMessage {
	return InboundMessage{
		Platform:     entities.PlatformIMessage,
		UserID:       "+15551234",
		ThreadID:     "thread-1",
		MsgID:        msgID,
		IsDocument:   true,
		DocumentB64:  base64.StdEncoding.EncodeToString(pdf),
		DocumentMime: "application/pdf",
		DocumentName: "invoice.pdf",
		Text:         caption,
	}
}

func mustRaw(t *testing.T, msg InboundMessage) []byte {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal inbound: %v", err)
	}
	return raw
}

// shopTestPDF builds a one-page PDF whose text layer carries content, optionally
// Flate-encoded — the difference between a PDF that travelled as a file and a
// scan of one.
// shopTestTextPDF writes a PDF whose text layer a reader can actually pull
// words out of: "(line) Tj" operators, one per line. shopTestPDF above writes a
// bare content stream, which is exactly the unclassifiable file the statement
// path gets from a scanner.
func shopTestTextPDF(lines ...string) []byte {
	body := "BT /F1 12 Tf 40 700 Td 14 TL\n"
	for _, l := range lines {
		body += "(" + l + ") Tj T*\n"
	}
	body += "ET"
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	fmt.Fprintf(&out, "4 0 obj\n<< /Length %d >>\nstream\n", len(body))
	out.WriteString(body)
	out.WriteString("\nendstream\nendobj\ntrailer\n<< >>\n%%EOF\n")
	return out.Bytes()
}

func shopTestPDF(content string, compress bool) []byte {
	stream := []byte("BT /F1 12 Tf 50 700 Td " + content + " ET")
	filter := ""
	if compress {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		// A zlib writer over a bytes.Buffer cannot fail; the helper has no
		// testing.T to report to.
		_, _ = zw.Write(stream) //nolint:errcheck
		_ = zw.Close()          //nolint:errcheck
		stream = buf.Bytes()
		filter = " /Filter /FlateDecode"
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")
	fmt.Fprintf(&out, "4 0 obj\n<< /Length %d%s >>\nstream\n", len(stream), filter)
	out.Write(stream)
	out.WriteString("\nendstream\nendobj\ntrailer\n<< >>\n%%EOF\n")
	return out.Bytes()
}

func TestShopRead_StockBookPhotoIsReadVerbatim(t *testing.T) {
	repo := newFakeRepo()
	identity := linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	p.SetReceiptVision(&fakeShopVision{kind: document.DocStockBook, summary: "a handwritten shop record"})
	reader := &fakeShopReader{result: &ShopReadResult{
		ReplyText:  "Mama Tobi: 7 rows read, N286,300 on hand. One Dangote row looks cancelled.",
		DocID:      "doc-1",
		DocType:    "stock_book",
		Confidence: 0.91,
		ModelPath:  []string{"read_big"},
	}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	msg := shopImageMessage("msg-1", "here is today's stock book")
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	got := rec.waitFor(t, 2)
	if got[0].Text != shopHoldingText {
		t.Fatalf("first bubble = %q, want the holding line", got[0].Text)
	}
	if got[1].Text != reader.result.ReplyText {
		t.Fatalf("answer = %q, want the reader's own words", got[1].Text)
	}
	if got[1].ThreadID != "thread-1" {
		t.Fatalf("answer went to thread %q, want the thread it came from", got[1].ThreadID)
	}
	// The figures are the reader's: the guest brain must not be asked at all.
	if orch.lastMessage != "" {
		t.Fatalf("orchestrator was handed %q; a shop read must not go through the model", orch.lastMessage)
	}

	if reader.count() != 1 {
		t.Fatalf("reader calls = %d, want 1", reader.count())
	}
	req := reader.last()
	if req.UserID != identity.UserID {
		t.Fatalf("read ran for user %s, want the linked owner %s", req.UserID, identity.UserID)
	}
	if req.DocHint != "stock_book" {
		t.Fatalf("doc hint = %q, want stock_book", req.DocHint)
	}
	if req.Caption != "here is today's stock book" {
		t.Fatalf("caption = %q, want the owner's own words", req.Caption)
	}
	if req.FileB64 != msg.ImageB64 {
		t.Fatal("the reader must get the photo exactly as it arrived")
	}
	if req.MessageID != "msg-1" {
		t.Fatalf("message id = %q, want msg-1", req.MessageID)
	}
}

func TestShopRead_ReceiptPhotoStaysOnTheReceiptPath(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	p.SetReceiptVision(&fakeShopVision{kind: document.DocReceipt, summary: "Got it, Shoprite. Total comes to 12.00."})
	reader := &fakeShopReader{result: &ShopReadResult{ReplyText: "should never be sent"}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	if err := p.Process(context.Background(), mustRaw(t, shopImageMessage("msg-2", ""))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if reader.count() != 0 {
		t.Fatal("a receipt photo must not be read as a shop document")
	}
	if orch.lastMessage != "Got it, Shoprite. Total comes to 12.00." {
		t.Fatalf("orchestrator saw %q, want the receipt summary", orch.lastMessage)
	}
	if len(rec.all()) == 0 {
		t.Fatal("expected the receipt reply to be sent")
	}
}

func TestShopRead_RedeliveryIsReadOnce(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	p.SetReceiptVision(&fakeShopVision{kind: document.DocStockBook, summary: "a handwritten shop record"})
	reader := &fakeShopReader{result: &ShopReadResult{ReplyText: "7 rows read.", DocID: "doc-1", DocType: "stock_book"}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	raw := mustRaw(t, shopImageMessage("msg-3", ""))
	if err := p.Process(context.Background(), raw); err != nil {
		t.Fatalf("Process: %v", err)
	}
	rec.waitFor(t, 2)
	// The bridge redelivers the same message (same msg_id) — the owner must not
	// be charged for, or messaged with, a second read.
	if err := p.Process(context.Background(), raw); err != nil {
		t.Fatalf("redelivery Process: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if reader.count() != 1 {
		t.Fatalf("reader calls = %d, want exactly 1 for a redelivered message", reader.count())
	}
	if got := len(rec.all()); got != 2 {
		t.Fatalf("sent %d messages, want the holding bubble and one answer: %v", got, texts(rec.all()))
	}
}

func TestShopRead_FailureFailsClosedWithNoFigures(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	p.SetReceiptVision(&fakeShopVision{kind: document.DocStockBook, summary: "a handwritten shop record"})
	p.SetShopReader(&fakeShopReader{err: errors.New("connection refused")})
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	if err := p.Process(context.Background(), mustRaw(t, shopImageMessage("msg-4", ""))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := rec.waitFor(t, 2)
	if got[0].Text != shopHoldingText {
		t.Fatalf("first bubble = %q", got[0].Text)
	}
	if got[1].Text != shopUnreadableText {
		t.Fatalf("answer = %q, want the fail-closed retake line", got[1].Text)
	}
}

func TestShopRead_PanicStillAnswersTheOwner(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	p.SetReceiptVision(&fakeShopVision{kind: document.DocDeliveryNote, summary: "a delivery note"})
	p.SetShopReader(&fakeShopReader{panicOn: true})
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	if err := p.Process(context.Background(), mustRaw(t, shopImageMessage("msg-5", ""))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := rec.waitFor(t, 2)
	if got[1].Text != shopUnreadableText {
		t.Fatalf("answer = %q, want the fail-closed retake line after a panic", got[1].Text)
	}
}

func TestShopRead_DeclinedPageKeepsTheReadersRetake(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	p.SetReceiptVision(&fakeShopVision{kind: document.DocStockBook, summary: "a handwritten shop record"})
	p.SetShopReader(&fakeShopReader{result: &ShopReadResult{
		ReplyText: "I can't read the left-hand column clearly. Send it again flat?",
		DocType:   "other",
		Flags:     []string{"failed"},
	}})
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	if err := p.Process(context.Background(), mustRaw(t, shopImageMessage("msg-6", ""))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := rec.waitFor(t, 2)
	if got[1].Text != "I can't read the left-hand column clearly. Send it again flat?" {
		t.Fatalf("answer = %q, want the reader's own retake line", got[1].Text)
	}
}

func TestShopRead_OversizedPageIsRefusedBeforeTheRead(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	reader := &fakeShopReader{result: &ShopReadResult{ReplyText: "nope"}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	resolved := &ResolvedUser{UserID: uuid.New(), Identity: &entities.PlatformIdentity{ID: uuid.New()}}
	page := &shopPage{
		Kind:    ShopKindStockBook,
		FileB64: strings.Repeat("A", maxShopReadBase64Bytes+1),
		MIME:    "image/jpeg",
	}
	if err := p.handleShopDocument(context.Background(), shopImageMessage("msg-7", ""), resolved, page); err != nil {
		t.Fatalf("handleShopDocument: %v", err)
	}
	got := rec.waitFor(t, 1)
	if got[0].Text != shopTooLargeText {
		t.Fatalf("answer = %q, want the too-large refusal", got[0].Text)
	}
	if reader.count() != 0 {
		t.Fatal("an oversized page must not cost a read")
	}
}

func TestShopRead_UnlinkedSenderIsNotRead(t *testing.T) {
	repo := newFakeRepo() // nobody linked
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	p.SetReceiptVision(&fakeShopVision{kind: document.DocStockBook, summary: "a handwritten shop record"})
	reader := &fakeShopReader{result: &ShopReadResult{ReplyText: "nope"}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	if err := p.Process(context.Background(), mustRaw(t, shopImageMessage("msg-8", ""))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if reader.count() != 0 {
		t.Fatal("an unlinked sender has no account to store a document against")
	}
	got := rec.waitFor(t, 1)
	if !strings.Contains(got[0].Text, "link") {
		t.Fatalf("answer = %q, want the link-your-account reply", got[0].Text)
	}
}

func TestShopRead_PdfInvoiceGoesToTheReader(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	pdf := shopTestPDF(
		"(WIGMORE NIGERIA LTD) Tj (Invoice Number WGT-2610-0417) Tj (Bill To Mama Tobi Stores) Tj (Due Date 17/10/2026) Tj",
		true,
	)
	reader := &fakeShopReader{result: &ShopReadResult{
		ReplyText: "Wigmore: 6 cartons, total N180,000, N150,000 paid, N30,000 left.",
		DocID:     "doc-2",
		DocType:   "invoice",
	}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	msg := shopDocumentMessage("msg-9", "the distributor's invoice", pdf)
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := rec.waitFor(t, 2)
	if got[1].Text != reader.result.ReplyText {
		t.Fatalf("answer = %q, want the reader's read", got[1].Text)
	}
	if reader.count() != 1 {
		t.Fatalf("reader calls = %d, want 1", reader.count())
	}
	req := reader.last()
	// Go's text layer is a first cut, not the verdict: the reader sees the page
	// and classifies it itself.
	if req.DocHint != string(ShopKindUnknownImage) {
		t.Fatalf("doc hint = %q, want %q", req.DocHint, ShopKindUnknownImage)
	}
	if req.MIME != "application/pdf" {
		t.Fatalf("mime = %q, want application/pdf", req.MIME)
	}
	if req.Caption != "the distributor's invoice" {
		t.Fatalf("caption = %q", req.Caption)
	}
	if req.FileB64 != msg.DocumentB64 {
		t.Fatal("the reader must get the PDF exactly as it arrived")
	}
	if orch.lastMessage != "" {
		t.Fatalf("orchestrator was handed %q; a PDF invoice must not go through the model", orch.lastMessage)
	}
}

// A PDF whose text layer says nothing — a scan — is offered to the reader
// first, because such a file may be a distributor invoice rather than a
// statement. It stays on the statement path when the reader cannot deliver it,
// which is what this file got before the reader could look at it.
func TestShopRead_ScannedStatementStaysOnTheStatementPath(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	reader := &fakeShopReader{result: &ShopReadResult{DocType: "bank_statement"}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())
	handler := &fakeStatementHandler{}
	p.SetStatementAttachmentHandler(handler)

	// A scan: no text layer at all, so Go has no opinion.
	scan := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\ntrailer\n<< >>\n%%EOF\n")
	if err := p.Process(context.Background(), mustRaw(t, shopDocumentMessage("msg-10", "", scan))); err != nil {
		t.Fatalf("Process: %v", err)
	}
	rec.waitFor(t, 2)
	if reader.count() != 1 {
		t.Fatalf("reader calls = %d, want the reader to get a look", reader.count())
	}
	if got := reader.last().DocHint; got != string(ShopKindUnknownImage) {
		t.Fatalf("doc hint = %q, want %q", got, ShopKindUnknownImage)
	}
	// The reader claimed nothing from it, so the statement pipeline keeps the
	// file, byte for byte, exactly as it did before the reader existed.
	if handler.linkedCalls != 1 {
		t.Fatalf("statement enqueue calls = %d, want 1", handler.linkedCalls)
	}
	if !bytes.Equal(handler.lastBytes, scan) {
		t.Fatal("the statement path must keep the file's own bytes")
	}
}

func TestMemoryMessageClaimStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMessageClaimStore()

	first, err := store.SetNX(ctx, "k", 1, time.Minute)
	if err != nil || !first {
		t.Fatalf("first claim = (%v, %v), want (true, nil)", first, err)
	}
	second, err := store.SetNX(ctx, "k", 1, time.Minute)
	if err != nil {
		t.Fatalf("second claim errored: %v", err)
	}
	if second {
		t.Fatal("the same key must not be claimable twice")
	}
	if _, err := store.SetNX(ctx, "  ", 1, time.Minute); err == nil {
		t.Fatal("an empty key must be refused")
	}

	// An expired claim is available again, and the entry is dropped with it.
	if _, err := store.SetNX(ctx, "short", 1, 5*time.Millisecond); err != nil {
		t.Fatalf("claim: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	again, err := store.SetNX(ctx, "short", 1, time.Minute)
	if err != nil || !again {
		t.Fatalf("expired claim = (%v, %v), want (true, nil)", again, err)
	}
}

func TestShopKindHint(t *testing.T) {
	cases := map[string]string{
		"stock_book":     "stock_book",
		"invoice":        "invoice",
		"delivery_note":  "delivery_note",
		"receipt":        "",
		"unknown":        "",
		"":               "",
		"bank statement": "",
	}
	for in, want := range cases {
		if got := shopKindHint(in); got != want {
			t.Fatalf("shopKindHint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShopClaimKeyPrefersTheMessageID(t *testing.T) {
	page := &shopPage{FileB64: "abc"}
	withID := shopClaimKey(InboundMessage{Platform: entities.PlatformIMessage, MsgID: "m-1"}, page)
	if !strings.Contains(withID, "m-1") {
		t.Fatalf("claim key %q must carry the bridge message id", withID)
	}
	// No message id (older bridge): the page's own bytes still identify it, so
	// the same photo is not read twice.
	withoutID := shopClaimKey(InboundMessage{Platform: entities.PlatformIMessage}, page)
	samePage := shopClaimKey(InboundMessage{Platform: entities.PlatformIMessage}, &shopPage{FileB64: "abc"})
	otherPage := shopClaimKey(InboundMessage{Platform: entities.PlatformIMessage}, &shopPage{FileB64: "xyz"})
	if withoutID != samePage {
		t.Fatal("the same page bytes must claim the same key")
	}
	if withoutID == otherPage {
		t.Fatal("different page bytes must claim different keys")
	}
}

// An unknown image: nobody in Go could say what the page is, so the reader is
// asked to decide. Before this, a handwritten book whose text no OCR pass could
// read was answered as if Miriam could not look at photos at all.
func TestShopRead_APhotoNobodyCouldClassifyGoesToTheReaderAsUnknown(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	// No vision pass at all: the state a machine without OCR is in.
	reader := &fakeShopReader{result: &ShopReadResult{
		ReplyText:  "Mama Tobi, 10/10: 7 items, gap N31,900.",
		DocID:      "doc-9",
		DocType:    "stock_book",
		Confidence: 0.9,
	}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	msg := shopImageMessage("msg-unknown", "my book")
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	// The read runs off the request, so wait for its answer before asking what
	// the reader was told.
	msgs := rec.waitFor(t, 2)
	if got := msgs[0].Text; got != shopHoldingText {
		t.Fatalf("first message = %q, want the holding bubble", got)
	}
	if got := msgs[1].Text; !strings.Contains(got, "gap N31,900") {
		t.Fatalf("reader reply = %q, want the reader's own text", got)
	}
	if reader.count() != 1 {
		t.Fatalf("reader calls = %d, want 1: the photo must reach the reader", reader.count())
	}
	if got := reader.last().DocHint; got != string(ShopKindUnknownImage) {
		t.Fatalf("doc hint = %q, want %q", got, ShopKindUnknownImage)
	}
	if got := reader.last().Caption; got != "my book" {
		t.Fatalf("caption = %q, want the owner's words", got)
	}
}

// The reader looked and said the page is not one of its kinds. The photo then
// gets exactly the answer it got before the reader could look at it, never a
// read-failure line about a read that did not happen.
func TestShopRead_ADeclinedUnknownImageKeepsThePhotosOwnAnswer(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	reader := &fakeShopReader{result: &ShopReadResult{
		ReplyText: "",
		DocType:   "receipt",
		Flags:     []string{"not a shop document (receipt)"},
	}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	msg := shopImageMessage("msg-declined", "")
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	msgs := rec.waitFor(t, 2)
	if got := msgs[1].Text; got != "I can't look at photos just yet, but tell me the merchant and total and I'll log or split it for you." {
		t.Fatalf("declined photo answered %q, want the photo's own pre-reader answer", got)
	}
}

// A read that failed is not a decline: the owner is told the read failed, and
// never handed the receipt answer for a page the reader was willing to read.
func TestShopRead_AFailedReadNeverUsesThePhotoFallback(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	reader := &fakeShopReader{result: &ShopReadResult{Flags: []string{"failed"}}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	msg := shopImageMessage("msg-failed", "")
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	msgs := rec.waitFor(t, 2)
	if got := msgs[1].Text; got != shopUnreadableText {
		t.Fatalf("failed read answered %q, want the retake line", got)
	}
}

// A photo the vision pass did read keeps the path it always had: Go's own
// summary, the orchestrator, no reader and no second spend on the same image.
func TestShopRead_APhotoTheVisionPassReadKeepsItsOwnPath(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	p.SetReceiptVision(&fakeShopVision{
		kind:    document.DocReceipt,
		summary: "Coffee shop receipt, N2,300 on 10/10/2026",
	})
	reader := &fakeShopReader{result: &ShopReadResult{ReplyText: "should never be asked"}}
	p.SetShopReader(reader)

	msg := shopImageMessage("msg-receipt", "")
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if reader.count() != 0 {
		t.Fatalf("reader calls = %d, want 0 for a receipt Go read itself", reader.count())
	}
	if orch.lastMessage == "" || !strings.Contains(orch.lastMessage, "Coffee shop receipt") {
		t.Fatalf("orchestrator saw %q, want the vision summary", orch.lastMessage)
	}
	msgs := rec.waitFor(t, 1)
	if got := msgs[0].Text; got == shopHoldingText {
		t.Fatalf("a receipt must not get a shop holding bubble")
	}
}

// A PDF whose text layer says nothing used to fall straight to the statement
// path, which answered "I can't scan statements from chat just yet" for a
// distributor invoice the reader could read. A real Wigmore PDF off the owner's
// phone did exactly that. The reader gets the first look now.
func TestShopRead_APDFGoCannotClassifyGoesToTheReader(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	reader := &fakeShopReader{result: &ShopReadResult{
		ReplyText:  "Invoice from WIGMORE TRADING LTD, 5 lines, balance N30,000 due 17/10/2026.",
		DocID:      "doc-pdf",
		DocType:    "invoice",
		Confidence: 1,
	}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	// A PDF with no text layer at all: nothing for Go to classify from.
	msg := shopDocumentMessage("msg-pdf", "", shopTestPDF("", false))
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	msgs := rec.waitFor(t, 2)
	if got := msgs[0].Text; got != shopHoldingText {
		t.Fatalf("first message = %q, want the holding bubble", got)
	}
	if got := msgs[1].Text; !strings.Contains(got, "balance N30,000") {
		t.Fatalf("reply = %q, want the reader's own text", got)
	}
	if reader.count() != 1 {
		t.Fatalf("reader calls = %d, want 1", reader.count())
	}
	if got := reader.last().DocHint; got != string(ShopKindUnknownImage) {
		t.Fatalf("doc hint = %q, want %q", got, ShopKindUnknownImage)
	}
	if got := reader.last().MIME; got != "application/pdf" {
		t.Fatalf("mime = %q, want the PDF as it arrived", got)
	}
}

// And when the reader looks and owns none of it, the owner hears the statement
// path's own answer, byte for byte: an unreadable PDF is not worse off than it
// was before the reader existed.
func TestShopRead_ADeclinedUnreadablePDFFallsBackToTheStatementPath(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	orch := &fakeOrchestrator{}
	p, rec := shopTestProcessor(repo, orch)

	reader := &fakeShopReader{result: &ShopReadResult{
		ReplyText: "",
		DocType:   "bank_statement",
		Flags:     []string{"not a shop document (bank_statement)"},
	}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())

	msg := shopDocumentMessage("msg-pdf-declined", "", shopTestPDF("", false))
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	msgs := rec.waitFor(t, 2)
	want := "I can't scan statements from chat just yet. Please upload it in the RAIL app."
	if got := msgs[1].Text; got != want {
		t.Fatalf("declined PDF answered %q, want the statement path's own line", got)
	}
}

// A readable statement keeps the statement path it always had: the new route is
// only for files whose text layer says nothing.
func TestShopRead_AReadableStatementPDFKeepsTheStatementPath(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	reader := &fakeShopReader{result: &ShopReadResult{ReplyText: "should never be asked"}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())
	handler := &fakeStatementHandler{}
	p.SetStatementAttachmentHandler(handler)

	msg := shopDocumentMessage("msg-statement", "", shopTestTextPDF(
		"Account Number 0123456",
		"Statement Period 01/09/2026 - 30/09/2026",
		"Opening Balance",
		"Value Date",
		"Closing Balance",
	))
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}

	rec.waitFor(t, 1)
	if reader.count() != 0 {
		t.Fatalf("reader calls = %d, want 0 for a readable statement", reader.count())
	}
	if handler.linkedCalls != 1 {
		t.Fatalf("statement enqueue calls = %d, want 1", handler.linkedCalls)
	}
}

// A reader that errors on a PDF, rather than declining it, must not cost the
// owner the statement path either.
func TestShopRead_AFailedPDFReadFallsBackToTheStatementPath(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})
	reader := &fakeShopReader{err: errors.New("reader is down")}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())
	handler := &fakeStatementHandler{}
	p.SetStatementAttachmentHandler(handler)

	msg := shopDocumentMessage("msg-pdf-failed", "", shopTestPDF("", false))
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	rec.waitFor(t, 2)
	if handler.linkedCalls != 1 {
		t.Fatalf("statement enqueue calls = %d, want the statement path to still get it", handler.linkedCalls)
	}
}

// The Wigmore invoice, in its own words. A keyword count calls it a delivery
// note ("goods received", "received by") or a receipt ("subtotal", "total",
// "qty", "thank you", "tel"), and either verdict would read an invoice with the
// wrong rules. The reader sees the page, so the reader decides; the statement
// path, which owns bank statements, must not see this file at all.
func TestShopRead_AnInvoicePDFWithDeliveryNoteBoilerplateIsNotMisrouted(t *testing.T) {
	repo := newFakeRepo()
	linkedIdentity(repo, "+15551234")
	p, rec := shopTestProcessor(repo, &fakeOrchestrator{})

	pdf := shopTestTextPDF(
		"WIGMORE TRADING LTD",
		"FMCG Distribution | 14 Ogunlana Drive, Surulere, Lagos",
		"0803 412 7790 RC 1184522 | sales@wigmoretrading.ng",
		"INVOICE",
		"Invoice No: WGT/INV/2610-0417",
		"Delivery Note: DN-2610-0388",
		"BILL TO",
		"Mama Tobi Stores",
		"Item Qty Unit Price (N) Amount (N)",
		"1 Indomie Instant Noodles 70g (ctn of 40) 10 ctn 9,200 92,000",
		"Subtotal 182,600",
		"Trade discount -2,600",
		"TOTAL 180,000",
		"Balance due 17/10/2026 30,000",
		"Payment terms 7 days credit.",
		"Goods received in good condition. Report shortages within 24 hours of delivery.",
		"Received by (customer) For Wigmore Trading Ltd",
		"Thank you for your business.",
	)
	reader := &fakeShopReader{result: &ShopReadResult{
		ReplyText:  "Wigmore Trading Ltd: 5 lines, N180,000 due, N30,000 left.",
		DocID:      "doc-fixture",
		DocType:    "invoice",
		Confidence: 1,
	}}
	p.SetShopReader(reader)
	p.SetShopClaimStore(NewMemoryMessageClaimStore())
	handler := &fakeStatementHandler{}
	p.SetStatementAttachmentHandler(handler)

	msg := shopDocumentMessage("msg-invoice-pdf", "", pdf)
	if err := p.Process(context.Background(), mustRaw(t, msg)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	rec.waitFor(t, 2)
	if got := reader.count(); got != 1 {
		t.Fatalf("reader calls = %d, want the reader to see this invoice", got)
	}
	if got := reader.last().DocHint; got != string(ShopKindUnknownImage) {
		t.Fatalf("doc hint = %q, want %q so the reader classifies the page", got, ShopKindUnknownImage)
	}
	if handler.linkedCalls != 0 {
		t.Fatal("an invoice must never reach the statement path")
	}
}
