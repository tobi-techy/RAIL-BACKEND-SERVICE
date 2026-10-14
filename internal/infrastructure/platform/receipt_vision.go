package platform

import (
	"context"
	"fmt"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/services/document"
)

// maxReceiptImageBytes bounds a receipt photo pulled into memory for OCR.
// Phone photos are typically 2-8MB; anything larger is almost certainly not a
// receipt and would only slow the OCR sidecar down.
const maxReceiptImageBytes = 15 * 1024 * 1024

// AttachmentRead is what the vision pipeline saw in one attached photo: the
// page's kind, plus either the receipt summary or — for a page the shop reader
// owns — a plain description of what the photo is.
type AttachmentRead struct {
	// Kind is the classified page type.
	Kind document.DocType
	// Summary is what to tell the user. For a shop document it is a description
	// rather than a read: the figures come from the reader, never from here.
	Summary string
}

// ReceiptVision extracts a quick, human-readable summary from a receipt photo
// so Miriam can react to an image moments after it's texted to her, and reports
// what the page actually is so the processor can route a shop document to the
// reader. One OCR pass answers both. It is the synchronous "fast path" — full
// document persistence/enrichment stays on the async document pipeline.
type ReceiptVision interface {
	Available() bool
	ReadImage(ctx context.Context, image []byte, mimeType string) (*AttachmentRead, error)
}

// documentReceiptVision adapts the shared document pipeline (OCR -> classify ->
// extract) to ReceiptVision. Only receipts get a rich summary; other document
// types get a gentle nudge toward the app's statement upload.
type documentReceiptVision struct {
	pipeline *document.Pipeline
}

// NewDocumentReceiptVision builds a ReceiptVision on top of the document
// pipeline. Returns nil when pipeline is nil so the processor degrades to its
// "I can't look at images yet" copy.
func NewDocumentReceiptVision(pipeline *document.Pipeline) ReceiptVision {
	if pipeline == nil {
		return nil
	}
	return &documentReceiptVision{pipeline: pipeline}
}

func (v *documentReceiptVision) Available() bool { return v != nil && v.pipeline != nil }

func (v *documentReceiptVision) ReadImage(ctx context.Context, image []byte, mimeType string) (*AttachmentRead, error) {
	if len(image) == 0 {
		return nil, fmt.Errorf("empty image")
	}
	if len(image) > maxReceiptImageBytes {
		return nil, fmt.Errorf("image too large (%d bytes)", len(image))
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	res, err := v.pipeline.Process(ctx, image, mimeType)
	if err != nil {
		return nil, fmt.Errorf("process receipt: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("no result")
	}

	read := &AttachmentRead{Kind: res.Type}
	switch res.Type {
	case document.DocReceipt, document.DocInvoice:
		read.Summary = formatReceiptSummary(res)
	case document.DocBankStatement:
		read.Summary = "That looks like a bank statement. For a full breakdown, upload it in the app (Documents) and I'll categorize every transaction."
	case document.DocStockBook:
		// Handed to the shop reader once the sender is known to be linked. The
		// description stands in as the turn's text until then, so an unlinked
		// sender is onboarded rather than left with an empty message.
		read.Summary = "a handwritten shop record — a stock book with what came in, what sold and what's left"
	case document.DocDeliveryNote:
		read.Summary = "a distributor's delivery note, signed off when the goods arrived"
	default:
		read.Summary = "I couldn't make out a receipt in that photo. Try a clearer, top-down shot with the totals visible."
	}
	return read, nil
}

// formatReceiptSummary renders extracted receipt fields the way Miriam talks:
// merchant, date, biggest items, and the total. Missing fields are skipped so
// the summary never fabricates a figure OCR didn't see.
func formatReceiptSummary(res *document.Result) string {
	var b strings.Builder
	f := res.Fields

	b.WriteString("Got it")
	if f != nil && f.Merchant != "" {
		fmt.Fprintf(&b, ", %s", f.Merchant)
	}
	b.WriteString(".")

	if f != nil && f.Date != nil {
		fmt.Fprintf(&b, " Dated %s.", f.Date.Format("Jan 2, 2006"))
	}

	if f != nil && len(f.Items) > 0 {
		limit := len(f.Items)
		if limit > 5 {
			limit = 5
		}
		names := make([]string, 0, limit)
		for i := 0; i < limit; i++ {
			if name := strings.TrimSpace(f.Items[i].Name); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, " I see %d items, including %s.", len(f.Items), strings.Join(names, ", "))
		}
	}

	if f != nil && f.Total != nil {
		currency := f.Currency
		fmt.Fprintf(&b, " Total comes to %s%s.", currency, f.Total.StringFixed(2))
	}

	b.WriteString(" Want me to log it, or split it with someone? (e.g. \"split with @jane\")")
	return b.String()
}
