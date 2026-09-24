package chainrails

import (
	"context"
	"strings"

	"go.uber.org/zap"
)

// IntentTriggerer is the manual-processing half of the ChainRails client.
// Adapter interfaces embed it so funding sites can kick testnet intents
// without depending on the concrete *Client.
type IntentTriggerer interface {
	TriggerIntentProcessing(ctx context.Context, intentAddress string) error
}

// MaybeTriggerTestnetProcessing fires trigger-processing best-effort after an
// intent is funded. Testnets have no indexer support, so without this call a
// funded testnet intent sits until expiry. No-op on mainnet chains, empty
// addresses, or nil triggerers; failures only log (the intent may still be
// picked up, and callers must not fail a funded transfer over this).
func MaybeTriggerTestnetProcessing(ctx context.Context, t IntentTriggerer, sourceChain, intentAddress string, logger *zap.Logger) {
	// Nil-interface check is not enough: a typed-nil *Client wrapped in the
	// interface is non-nil here but panics on use. Guard via a nil-receiver
	// check on the concrete type before calling through.
	if t == nil || !IsTestnetChain(sourceChain) || strings.TrimSpace(intentAddress) == "" {
		return
	}
	if c, ok := t.(*Client); ok && (c == nil || c.httpClient == nil) {
		if logger != nil {
			logger.Warn("chainrails testnet trigger skipped: nil client (fail-open, intent may stall until expiry)")
		}
		return
	}
	if err := t.TriggerIntentProcessing(ctx, intentAddress); err != nil {
		if logger == nil {
			return
		}
		logger.Warn("chainrails testnet trigger-processing failed (intent may stall until expiry)",
			zap.String("source_chain", sourceChain),
			zap.String("intent_address", intentAddress),
			zap.Error(err))
		return
	}
	if logger == nil {
		return
	}
	logger.Info("chainrails testnet intent processing triggered",
		zap.String("source_chain", sourceChain),
		zap.String("intent_address", intentAddress))
}

// Canonical intent-status classification.
//
// The documented intent_status enum is PENDING, FUNDED, INITIATED, COMPLETED,
// EXPIRED (plus PROCESSING/FAILED in the create schema), but provider
// responses and webhooks have carried adjacent spellings, so every polling
// site historically grew its own match arms — and the five sites disagreed.
// These helpers are the union of all previously-handled spellings, so
// adopting them changes no behavior; they exist so every caller classifies
// identically from here on. Statuses outside these sets are non-terminal
// (keep polling / leave in-progress).
func normalizeIntentStatus(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// IsTerminalSuccess reports whether an intent status means funds settled.
func IsTerminalSuccess(status string) bool {
	switch normalizeIntentStatus(status) {
	case "COMPLETED", "COMPLETE", "SUCCESS", "SUCCEEDED", "SETTLED", "FULFILLED":
		return true
	}
	return false
}

// IsTerminalFailure reports whether an intent status means funds will never
// settle (failed, refunded, expired, or cancelled in any spelling).
func IsTerminalFailure(status string) bool {
	switch normalizeIntentStatus(status) {
	case "FAILED", "FAILURE", "REFUNDED", "EXPIRED", "CANCELLED", "CANCELED", "REJECTED":
		return true
	}
	return false
}

// IsTerminal reports whether an intent status is final in either direction.
func IsTerminal(status string) bool {
	return IsTerminalSuccess(status) || IsTerminalFailure(status)
}
