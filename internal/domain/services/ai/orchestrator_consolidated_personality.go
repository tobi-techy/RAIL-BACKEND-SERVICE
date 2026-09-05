package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/shopspring/decimal"
)

// buildConsolidatedPersonalityContext merges voice phase, control level,
// personality mode, tone calibration, and per-request tone mode into a SINGLE
// system message. This eliminates conflicting personality instructions competing
// for the LLM's attention.
//
// Priority (highest wins on conflicts):
//  1. Voice phase (earned from data density + prediction accuracy)
//  2. Control level (user-set autonomy level — Full/Guided/Monitor)
//  3. Personality mode (user-chosen voice — Roast/Coach/Protector/Celebration/Quiet)
//  4. Tone calibration (learned from user's messaging style)
//  5. Per-request tone mode (gentle/hard)
func (o *AgentAdapter) buildConsolidatedPersonalityContext(ctx context.Context, userID uuid.UUID, toneMode string) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var parts []string

	// --- Voice Phase (highest priority — defines Miriam's confidence level) ---
	// For text chat only the phase rules/bluntness are relevant; the VOICE EXAMPLES
	// (voice-specific few-shot examples) are stripped to save tokens.
	if o.miriamIntelligence != nil {
		if state, err := o.miriamIntelligence.GetMoneyState(fetchCtx, userID); err == nil && state != nil {
			if phaseCtx := miriam.PhaseContext(state); phaseCtx != "" {
				if before, _, found := strings.Cut(phaseCtx, "VOICE EXAMPLES:"); found {
					phaseCtx = strings.TrimSpace(before)
				}
				parts = append(parts, phaseCtx)
			}
		}
	}

	// --- Control Level (user-set autonomy — Full/Guided/Monitor) ---
	if cl := o.buildControlLevelContext(fetchCtx, userID); cl != "" {
		parts = append(parts, cl)
	}

	// --- Personality Mode (user-chosen voice — Roast/Coach/Protector/Celebration/Quiet) ---
	if pm := o.buildPersonalityModeContext(fetchCtx, userID); pm != "" {
		parts = append(parts, pm)
	}

	// --- Money Type (Miriam's read on how they relate to money) ---
	// Set during the first conversations, so it deliberately bypasses the
	// SampleCount gate — it exists precisely when the EMA tone profile is thin.
	if o.memory != nil {
		if profile, err := o.memory.store.GetToneProfile(fetchCtx, userID); err == nil && profile != nil {
			if note := moneyTypeNote(profile.MoneyType); note != "" {
				parts = append(parts, note)
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

	// --- Memory Callbacks (conversation continuity — reference prior moments naturally) ---
	// Pull the most recent memorable moments stored from past exchanges and surface them
	// as callbacks so Miriam can open with "last time you said X" or "three weeks ago
	// you hit Y" without needing to call a tool. Cap at 2 so the prompt stays tight.
	if o.memory != nil {
		if callbacks, err := o.memory.GetRecentCallbacks(fetchCtx, userID, 2); err == nil && len(callbacks) > 0 {
			var cb strings.Builder
			cb.WriteString("[MEMORY CALLBACKS — weave these into your reply naturally where relevant, like a friend who was paying attention. Don't list them all; use one as a natural hook:]")
			for _, c := range callbacks {
				cb.WriteString("\n- ")
				cb.WriteString(c)
			}
			parts = append(parts, cb.String())
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// moneyTypeNote turns the stored money-type read into one tone instruction.
// The type is never named to the user — it's a lens, not a label.
func moneyTypeNote(moneyType string) string {
	switch moneyType {
	case entities.MoneyTypeAvoider:
		return "[MONEY READ — money talk makes them anxious. Keep it light and small. One gentle step at a time; never pile on numbers. Celebrate any time they look at their money at all.]"
	case entities.MoneyTypeOptimizer:
		return "[MONEY READ — they already track everything. Skip the basics, respect their spreadsheet brain, go straight to the sharper edge they haven't seen yet.]"
	case entities.MoneyTypeWorrier:
		return "[MONEY READ — they check constantly and still worry. Reassure with specifics, not vibes. Anchor every answer to one concrete number or fact.]"
	case entities.MoneyTypeDreamer:
		return "[MONEY READ — big vision, thin execution. Match the dream's energy, then shrink the next step until it's doable this week.]"
	default:
		return ""
	}
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
