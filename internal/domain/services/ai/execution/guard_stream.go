package execution

import (
	"context"
	"strings"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// ApplyResponseGuard runs the deterministic pre-delivery guard on fully
// buffered content. Only active when the flag is on; never blocks delivery.
func ApplyResponseGuard(responseGuardOn bool, buildAnomalyContext func(ctx context.Context, userID uuid.UUID) string, logger *zap.Logger, ctx context.Context, userID uuid.UUID, content string, messages []infraai.Message) string {
	if !responseGuardOn || strings.TrimSpace(content) == "" {
		return content
	}
	corpus := groundingCorpusFromMessages(messages)
	anomalies := buildAnomalyContext(ctx, userID)
	guarded := GuardResponse(content, corpus, anomalies)
	if strings.TrimSpace(guarded) == "" {
		return content
	}
	return guarded
}

// LogUngroundedAmounts is the observability-only counterpart used on content
// that was already streamed token-by-token.
func LogUngroundedAmounts(responseGuardOn bool, logger *zap.Logger, userID uuid.UUID, content string, messages []infraai.Message, span interface {
	SetAttributes(...attribute.KeyValue)
}) {
	if !responseGuardOn || strings.TrimSpace(content) == "" {
		return
	}
	report := DetectUngroundedAmounts(content, groundingCorpusFromMessages(messages))
	if len(report.Indexes) == 0 {
		return
	}
	if span != nil {
		span.SetAttributes(attribute.Int("guard.ungrounded_sentences", len(report.Indexes)))
	}
	if logger != nil {
		amounts := report.Amounts
		if len(amounts) > 10 {
			amounts = amounts[:10]
		}
		logger.Warn("response guard: ungrounded amounts in streamed reply",
			zap.Int("count", len(report.Indexes)),
			zap.String("user_id", userID.String()),
			zap.Ints("sentence_indexes", report.Indexes),
			zap.Strings("detected_amounts", amounts),
		)
	}
}

// groundingCorpusFromMessages joins every non-assistant message content into
// the corpus the guard treats as ground truth.
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
