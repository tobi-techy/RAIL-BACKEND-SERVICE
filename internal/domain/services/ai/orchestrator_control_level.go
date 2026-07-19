package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolSetControlLevel = "set_control_level"

// ControlLevelTool returns the tool definition for setting Miriam's autonomy level.
func ControlLevelTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolSetControlLevel,
		Description: "Control how autonomous Miriam is. Levels: 'full' (Full Autopilot — Miriam acts on pre-approved actions, asks on new ones. Review weekly summary.), 'guided' (Guided — Miriam suggests actions, waits for approval before doing anything. Approve or deny each suggestion.), 'monitor' (Manual — Miriam only alerts and advises. User executes all actions themselves.). Use when user says 'take full control', 'ask me first', 'hands off', 'I want to approve everything', 'autopilot mode', 'guided mode', 'manual mode', 'be more autonomous', or 'let me control things'.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"level": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"full", "guided", "monitor"},
					"description": "The control level: full (act autonomously), guided (suggest + confirm), monitor (only advise).",
				},
			},
			"required":             []string{"level"},
			"additionalProperties": false,
		},
	}
}

// executeSetControlLevel updates the user's control level preference.
func (o *AgentAdapter) executeSetControlLevel(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	level, _ := args["level"].(string)
	switch level {
	case entities.ControlLevelFull, entities.ControlLevelGuided, entities.ControlLevelMonitor:
		// valid
	default:
		return map[string]interface{}{"error": fmt.Sprintf("invalid level %q — use full, guided, or monitor", level)}, nil
	}

	if o.memory == nil {
		return map[string]interface{}{"error": "control level unavailable"}, nil
	}

	if err := o.memory.SetControlLevel(ctx, userID, level); err != nil {
		return nil, err
	}

	confirmations := map[string]string{
		entities.ControlLevelFull:    "Full autopilot activated. I'll handle your pre-approved actions and ask on new ones. You review the weekly summary.",
		entities.ControlLevelGuided:  "Guided mode. I'll suggest actions and wait for your go-ahead before doing anything.",
		entities.ControlLevelMonitor: "Monitor mode. I'll watch and advise only — no actions without you executing them yourself.",
	}

	return map[string]interface{}{
		"success":      true,
		"level":        level,
		"confirmation": confirmations[level],
	}, nil
}

// buildControlLevelContext returns the user's control level as a system prompt injection.
func (o *AgentAdapter) buildControlLevelContext(ctx context.Context, userID uuid.UUID) string {
	if o.memory == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	level, err := o.memory.GetControlLevel(fetchCtx, userID)
	if err != nil || level == "" {
		return ""
	}

	switch level {
	case entities.ControlLevelFull:
		return "[CONTROL LEVEL: Full Autopilot — The user trusts you to act. Create actions when needed and execute them. Only ask for confirmation on actions above $500 or actions you've never done before. The user reviews their weekly summary.]"
	case entities.ControlLevelGuided:
		return "[CONTROL LEVEL: Guided — The user wants to approve things before they happen. You can suggest actions and create them for review, but the user MUST confirm before execution. Always present options clearly and wait for approval.]"
	case entities.ControlLevelMonitor:
		return "[CONTROL LEVEL: Monitor — The user handles everything themselves. You CANNOT create any actions, transfers, savings goals, automations, obligations, budgets, or subscriptions. You can only observe, alert, advise, and explain. If the user asks you to do something, explain what it would do and tell them it requires switching to Guided or Full Autopilot mode via set_control_level.]"
	default:
		return ""
	}
}
