package ai

import (
	"context"
	"strings"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// applyResponseGuard runs the deterministic pre-delivery guard on fully
// buffered content (mirrors core.Agent step 10b). Only active when the
// ai.response_guard flag is on; never blocks delivery — an empty guard result
// leaves the original reply untouched.
func (o *AgentAdapter) applyResponseGuard(ctx context.Context, userID uuid.UUID, content string, messages []infraai.Message) string {
	if !o.responseGuardOn || strings.TrimSpace(content) == "" {
		return content
	}
	corpus := groundingCorpusFromMessages(messages)
	anomalies := o.buildAnomalyContext(ctx, userID)
	guarded := GuardResponse(content, corpus, anomalies)
	if strings.TrimSpace(guarded) == "" {
		return content
	}
	return guarded
}

// logUngroundedAmounts is the observability-only counterpart used on content
// that was already streamed token-by-token — repair is impossible there, but
// violations are counted so drift shows up in logs and traces. Logs carry only
// redacted metadata (counts, sentence indexes, detected amounts) — never reply
// text, so financial sentences and personal context stay out of logs.
func (o *AgentAdapter) logUngroundedAmounts(userID uuid.UUID, content string, messages []infraai.Message, span interface {
	SetAttributes(...attribute.KeyValue)
}) {
	if !o.responseGuardOn || strings.TrimSpace(content) == "" {
		return
	}
	report := DetectUngroundedAmounts(content, groundingCorpusFromMessages(messages))
	if len(report.Indexes) == 0 {
		return
	}
	if span != nil {
		span.SetAttributes(attribute.Int("guard.ungrounded_sentences", len(report.Indexes)))
	}
	if o.logger != nil {
		amounts := report.Amounts
		if len(amounts) > 10 {
			amounts = amounts[:10]
		}
		o.logger.Warn("response guard: ungrounded amounts in streamed reply",
			zap.Int("count", len(report.Indexes)),
			zap.String("user_id", userID.String()),
			zap.Ints("sentence_indexes", report.Indexes),
			zap.Strings("detected_amounts", amounts),
		)
	}
}

// groundingCorpusFromMessages joins every non-assistant message content —
// injected context blocks, user text (prior turns included: user-stated figures
// are legitimate sources), and tool-result JSON — into the corpus the guard
// treats as ground truth. Assistant text is excluded so a hallucinated figure
// from a previous turn can't launder itself into the next reply.
func groundingCorpusFromMessages(messages []infraai.Message) string {
	var b strings.Builder
	for i := range messages {
		switch messages[i].Role {
		case "assistant":
			continue
		default:
			b.WriteString(messages[i].Content)
			b.WriteString("\n")
		}
	}
	out := b.String()
	const maxCorpus = 128 * 1024
	if len(out) > maxCorpus {
		out = out[:maxCorpus]
	}
	return out
}
