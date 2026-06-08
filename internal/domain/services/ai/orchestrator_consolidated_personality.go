package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/shopspring/decimal"
)

// buildConsolidatedPersonalityContext merges voice phase, tone calibration,
// and per-request tone mode into a SINGLE system message.
// This eliminates conflicting personality instructions competing for the LLM's attention.
//
// Priority (highest wins on conflicts):
//  1. Voice phase (earned from data density + prediction accuracy)
//  2. Tone calibration (learned from user's messaging style)
//  3. Per-request tone mode (gentle/direct/hard)
func (o *Orchestrator) buildConsolidatedPersonalityContext(ctx context.Context, userID uuid.UUID, toneMode string) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var parts []string

	// --- Voice Phase (highest priority — defines Miriam's confidence level) ---
	if o.miriamIntelligence != nil {
		if state, err := o.miriamIntelligence.GetMoneyState(fetchCtx, userID); err == nil && state != nil {
			if phaseCtx := miriam.PhaseContext(state); phaseCtx != "" {
				parts = append(parts, phaseCtx)
			}
		}
	}

	// --- Tone Calibration (style preferences — language, brevity, locale) ---
	// Only include NON-CONFLICTING aspects: language style, locale, preferred name, brevity.
	// Exclude formality/directness/warmth since the voice phase + base personality already set those.
	if o.memory != nil {
		if profile, err := o.memory.store.GetToneProfile(fetchCtx, userID); err == nil && profile != nil && profile.SampleCount >= 3 {
			var toneNotes []string
			if profile.PreferredName != "" {
				toneNotes = append(toneNotes, fmt.Sprintf("Call this user %q", profile.PreferredName))
			}
			if profile.Brevity.GreaterThan(decimal.NewFromFloat(0.7)) {
				toneNotes = append(toneNotes, "This user prefers very short, punchy responses")
			} else if profile.Brevity.LessThan(decimal.NewFromFloat(0.3)) {
				toneNotes = append(toneNotes, "This user likes detailed explanations — give depth")
			}
			if profile.LanguageStyle == "pidgin_mix" {
				toneNotes = append(toneNotes, "Mix in Nigerian Pidgin naturally when it fits")
			}
			switch profile.LocaleStyle {
			case "nigeria":
				toneNotes = append(toneNotes, "Use Naira references and Nigerian financial context")
			case "diaspora_uk":
				toneNotes = append(toneNotes, "Reference GBP and UK financial context")
			case "diaspora_us":
				toneNotes = append(toneNotes, "Reference USD and US financial context")
			}
			if len(toneNotes) > 0 {
				parts = append(parts, "[STYLE NOTES — "+strings.Join(toneNotes, ". ")+"]")
			}
		}
	}

	// --- Per-request tone mode (lowest priority — only adds intensity, never overrides personality) ---
	switch normalizePersonalityToneMode(toneMode) {
	case "gentle":
		parts = append(parts, "[TONE THIS RESPONSE: gentler than usual. Keep the personality but soften the edge.]")
	case "hard":
		parts = append(parts, "[TONE THIS RESPONSE: extra blunt. User wants accountability. Roast patterns, not the person. Tie every hard line to exact numbers.]")
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func normalizePersonalityToneMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "gentle", "soft", "kind":
		return "gentle"
	case "hard", "blunt", "savage", "roast":
		return "hard"
	default:
		return ""
	}
}
