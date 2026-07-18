package ai

import (
	"context"
	"strings"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ─── Celebrate Tool ──────────────────────────────────────────────────────────

// ToolCelebrate lets Miriam trigger a real celebration moment in the app —
// confetti, haptics, a sound, and (for big wins) a shareable win card. She calls
// this on GENUINE wins so the moment feels earned: a savings milestone, a goal
// hit, a strong streak, first time over a round number. The frontend renders a
// "celebration" card that drives the confetti overlay (it is not shown as a chat
// bubble).
const ToolCelebrate = "celebrate"

func CelebrateTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolCelebrate,
		Description: `Trigger a celebration animation (confetti + sound + a shareable win card) for a GENUINE milestone — a savings goal hit, a new all-time-high stash, a strong streak, a first big deposit, debt cleared. Use it sparingly and only when the moment is truly earned; never for routine activity. Still say your reaction in text too — this is the visual on top.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"level": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"small", "big", "epic"},
					"description": "Intensity. 'small' = nice progress (confetti only). 'big' = a real milestone (confetti + shareable card). 'epic' = a huge, rare moment (max confetti + card).",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Short celebratory headline for the win card, e.g. 'First $1,000 stashed' or '30-day streak'. Keep under 6 words.",
				},
				"subtitle": map[string]interface{}{
					"type":        "string",
					"description": "Optional one-line context under the headline, e.g. 'Three months ago this was zero.'",
				},
			},
			"required":             []string{"level", "title"},
			"additionalProperties": false,
		},
	}
}

func (o *AgentAdapter) executeCelebrate(_ context.Context, _ uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	level := strings.ToLower(strings.TrimSpace(stringArg(args, "level")))
	switch level {
	case "small", "big", "epic":
	default:
		level = "big"
	}
	title := strings.TrimSpace(stringArg(args, "title"))
	if title == "" {
		title = "Big moment"
	}
	return map[string]interface{}{
		"level":    level,
		"title":    title,
		"subtitle": strings.TrimSpace(stringArg(args, "subtitle")),
	}, nil
}

// ─── Poll Tool ───────────────────────────────────────────────────────────────

// ToolSendPoll lets Miriam pose a playful "this-or-that" choice rendered as
// tappable buttons in the chat. Tapping an option sends it back as the user's
// reply, keeping the conversation interactive. Great for Gen-Z engagement.
const ToolSendPoll = "send_poll"

func SendPollTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolSendPoll,
		Description: `Pose a quick, playful "this or that" choice as tappable buttons (e.g. "stash it or spend it? 👀"). Use it to make a decision feel light and interactive, not as an interrogation. 2 to 4 short options. The user taps one and it becomes their reply.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"question": map[string]interface{}{
					"type":        "string",
					"description": "Short, casual question. A few words.",
				},
				"options": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "2 to 4 short, tappable options.",
				},
			},
			"required":             []string{"question", "options"},
			"additionalProperties": false,
		},
	}
}

func (o *AgentAdapter) executeSendPoll(_ context.Context, _ uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	question := strings.TrimSpace(stringArg(args, "question"))
	options := make([]string, 0, 4)
	if raw, ok := args["options"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					options = append(options, s)
				}
			}
			if len(options) >= 4 {
				break
			}
		}
	}
	return map[string]interface{}{
		"question": question,
		"options":  options,
	}, nil
}
