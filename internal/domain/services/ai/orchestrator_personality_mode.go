package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolSetPersonalityMode = "set_personality_mode"

// PersonalityModeTool returns the tool definition for setting Miriam's personality mode.
func PersonalityModeTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolSetPersonalityMode,
		Description: "Change how Miriam talks to the user. Modes: 'default' (direct, sharp, slightly witty), 'roast' (brutally honest, funny, calls out bad habits), 'coach' (encouraging, strategic, accountability-focused), 'protector' (urgent, clear, action-oriented when financial threats detected), 'celebration' (excited, proud, amplifies wins), 'quiet' (silent, invisible — minimal talk, just action). Use when user says things like 'roast me', 'be more savage', 'coach me', 'protect me', 'celebrate with me', 'be quiet', 'switch to protector mode', or 'go back to normal'.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"mode": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"default", "roast", "coach", "protector", "celebration", "quiet"},
					"description": "The personality mode to switch to.",
				},
			},
			"required":             []string{"mode"},
			"additionalProperties": false,
		},
	}
}

// executeSetPersonalityMode updates the user's personality mode preference.
func (o *AgentAdapter) executeSetPersonalityMode(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	mode, _ := args["mode"].(string)
	switch mode {
	case entities.PersonalityModeDefault, entities.PersonalityModeRoast,
		entities.PersonalityModeCoach, entities.PersonalityModeProtector,
		entities.PersonalityModeCelebration, entities.PersonalityModeQuiet:
		// valid
	default:
		return map[string]interface{}{"error": fmt.Sprintf("invalid mode %q — use default, roast, coach, protector, celebration, or quiet", mode)}, nil
	}

	if o.memory == nil {
		return map[string]interface{}{"error": "personality mode unavailable"}, nil
	}

	if err := o.memory.SetPersonalityMode(ctx, userID, mode); err != nil {
		return nil, err
	}

	confirmations := map[string]string{
		entities.PersonalityModeDefault:     "Switched to default mode. Direct, sharp, no fluff.",
		entities.PersonalityModeRoast:       "Roast mode ON. Don't say I didn't warn you.",
		entities.PersonalityModeCoach:       "Coach mode. Let's build something.",
		entities.PersonalityModeProtector:   "Protector mode active. I've got your back.",
		entities.PersonalityModeCelebration: "Celelebration mode. Let's gooo! 🎉",
		entities.PersonalityModeQuiet:       "Quiet mode. I'll handle things, you won't hear from me unless it matters.",
	}

	return map[string]interface{}{
		"success":      true,
		"mode":         mode,
		"confirmation": confirmations[mode],
	}, nil
}
