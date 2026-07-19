package ai

import (
	"sort"
	"strings"
	"time"
)

// personalMemoryPrefix/Suffix wrap the recalled-memory system message. They are
// constants so the post-assembly deduper can locate and prune the payload against
// the structured memory slot.
const (
	personalMemoryPrefix = "[Recent & relevant to what they just said — weave in naturally, never say \"I recall\" or \"you mentioned\": "
	personalMemorySuffix = "]"
)

// timeframe describes a parsed time window from a user's message. sinceUnix is the
// inclusive lower bound (0 = no lower bound); untilUnix is the exclusive upper bound
// (0 = now/open). recencyBias is true when the user explicitly asked about a period,
// which sharpens recency weighting.
type timeframe struct {
	sinceUnix   int64
	untilUnix   int64
	recencyBias bool
}

// parseTimeframe extracts a coarse time window from a message using the supplied
// "now" reference (kept as a parameter to stay testable and deterministic).
func parseTimeframe(message string, now time.Time) timeframe {
	m := strings.ToLower(message)
	now = now.UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	switch {
	case strings.Contains(m, "today"):
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return timeframe{sinceUnix: start.Unix(), recencyBias: true}
	case strings.Contains(m, "yesterday"):
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
		return timeframe{sinceUnix: start.Unix(), untilUnix: start.AddDate(0, 0, 1).Unix(), recencyBias: true}
	case strings.Contains(m, "this week"):
		// Monday as the week start.
		offset := (int(now.Weekday()) + 6) % 7
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -offset)
		return timeframe{sinceUnix: start.Unix(), recencyBias: true}
	case strings.Contains(m, "last week"):
		offset := (int(now.Weekday()) + 6) % 7
		thisWeek := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -offset)
		lastWeek := thisWeek.AddDate(0, 0, -7)
		return timeframe{sinceUnix: lastWeek.Unix(), untilUnix: thisWeek.Unix(), recencyBias: true}
	case strings.Contains(m, "this month"):
		return timeframe{sinceUnix: monthStart.Unix(), recencyBias: true}
	case strings.Contains(m, "last month"):
		lastMonthStart := monthStart.AddDate(0, -1, 0)
		return timeframe{sinceUnix: lastMonthStart.Unix(), untilUnix: monthStart.Unix(), recencyBias: true}
	case strings.Contains(m, "this year"):
		yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		return timeframe{sinceUnix: yearStart.Unix(), recencyBias: true}
	case strings.Contains(m, "recently") || strings.Contains(m, "lately"):
		return timeframe{sinceUnix: now.AddDate(0, 0, -30).Unix(), recencyBias: true}
	}
	return timeframe{}
}

// scoredMemory pairs a memory string with its recency-adjusted rank score.
type scoredMemory struct {
	text  string
	score float64
}

// rankMemoriesByRecency combines semantic similarity with recency, using the parsed
// timeframe when present. Memories whose event date falls inside the requested window
// are boosted; those clearly outside it are demoted (but never dropped outright, so
// undated context is preserved). Results are returned high-to-low.
func rankMemoriesByRecency(results []SupermemoryResult, tf timeframe, now time.Time, minSimilarity float64) []string {
	nowUnix := now.UTC().Unix()
	scored := make([]scoredMemory, 0, len(results))
	for _, r := range results {
		text := strings.TrimSpace(r.Memory)
		if text == "" || r.Similarity < minSimilarity {
			continue
		}
		score := r.Similarity

		eventTs := r.EventUnix
		if eventTs == 0 {
			eventTs = r.UpdatedUnix
		}

		if tf.sinceUnix > 0 && eventTs > 0 {
			inWindow := eventTs >= tf.sinceUnix && (tf.untilUnix == 0 || eventTs < tf.untilUnix)
			if inWindow {
				score += 0.30 // strongly prefer memories inside the asked window
			} else {
				score -= 0.15 // demote out-of-window, but keep as fallback context
			}
		} else if eventTs > 0 {
			// No explicit window: gently favour fresher memories. Full boost within
			// ~30d, decaying to zero by ~1y.
			ageDays := float64(nowUnix-eventTs) / 86400.0
			if ageDays < 0 {
				ageDays = 0
			}
			switch {
			case ageDays <= 30:
				score += 0.10
			case ageDays <= 365:
				score += 0.10 * (1 - (ageDays-30)/335)
			}
		}

		scored = append(scored, scoredMemory{text: text, score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	out := make([]string, 0, len(scored))
	seen := make(map[string]struct{}, len(scored))
	for _, s := range scored {
		key := strings.ToLower(s.text)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s.text)
	}
	return out
}

// dedupePersonalMemory removes clauses from the recalled-memory system message
// (personalSlot) that are already conveyed by the structured memory slot
// (memorySlot), preventing the two memory injections from repeating each other.
// Returns the possibly-trimmed personal slot, or "" if nothing distinct remains.
func dedupePersonalMemory(memorySlot, personalSlot string) string {
	if personalSlot == "" {
		return ""
	}
	if !strings.HasPrefix(personalSlot, personalMemoryPrefix) {
		return personalSlot
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(personalSlot, personalMemoryPrefix), personalMemorySuffix)
	memLower := strings.ToLower(memorySlot)

	kept := make([]string, 0)
	for _, clause := range strings.Split(inner, " | ") {
		c := strings.TrimSpace(clause)
		if c == "" {
			continue
		}
		// Drop if the structured memory already states this fact.
		if memorySlot != "" && strings.Contains(memLower, strings.ToLower(c)) {
			continue
		}
		kept = append(kept, c)
	}
	if len(kept) == 0 {
		return ""
	}
	return personalMemoryPrefix + strings.Join(kept, " | ") + personalMemorySuffix
}
