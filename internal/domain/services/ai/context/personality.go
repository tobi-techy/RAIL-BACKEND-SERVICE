package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/shopspring/decimal"
)

// buildConsolidatedPersonalityContext merges voice phase, control level,
// personality mode, tone calibration, and per-request tone mode into a SINGLE
// system message.
func (b *Builder) buildConsolidatedPersonalityContext(ctx context.Context, userID uuid.UUID, toneMode string) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var parts []string

	if b.deps.GetMoneyStateFn != nil {
		if state, err := b.deps.GetMoneyStateFn(fetchCtx, userID); err == nil && state != nil {
			if phaseCtx := miriam.PhaseContext(state); phaseCtx != "" {
				if before, _, found := strings.Cut(phaseCtx, "VOICE EXAMPLES:"); found {
					phaseCtx = strings.TrimSpace(before)
				}
				parts = append(parts, phaseCtx)
			}
		}
	}

	parts = append(parts, `[VOICE ANCHORS — Miriam's voice for this turn:
- Be specific, not generic.
- React first, then the number, then what it means.
- Never open with filler.
- Use concrete comparisons instead of abstract claims.
- Match energy: lowercase is okay, slang is okay only if they lead it.
- Keep replies plain-text and conversational; no markdown fluff.
- No named personalities; keep the voice in the writing, not in references.]`)

	if b.deps.ControlLevelFn != nil {
		if cl := b.deps.ControlLevelFn(fetchCtx, userID); cl != "" {
			parts = append(parts, cl)
		}
	}

	if b.deps.ToneProfileFn != nil {
		if profile := b.deps.ToneProfileFn(fetchCtx, userID); profile != nil && profile.SampleCount >= 3 {
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

	switch normalizePersonalityToneMode(toneMode) {
	case "gentle":
		parts = append(parts, "[TONE THIS RESPONSE: gentler than usual. Keep the personality but soften the edge.]")
	case "hard":
		parts = append(parts, "[TONE THIS RESPONSE: extra blunt. User wants accountability. Roast patterns, not the person. Tie every hard line to exact numbers.]")
	}

	if b.deps.MemoryCallbacksFn != nil {
		if callbacks, err := b.deps.MemoryCallbacksFn(fetchCtx, userID, 2); err == nil && len(callbacks) > 0 {
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
