package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// RegisterSavingsGoalsV2Tools wires the new multi-goal toolset backed by the
// Postgres user_goals table. The legacy set_savings_goal / get_savings_goals
// tools are preserved for backward compatibility but the v2 tools are the
// preferred entrypoint — they expose list semantics and Baby-Step awareness.
func RegisterSavingsGoalsV2Tools(r *Registry) {
	r.Register(NewTool(
		"create_user_goal",
		"Create a free-standing savings goal. Use when the user wants to save toward something specific (a laptop, an emergency fund, a trip). The goal is tagged with a Baby Step when applicable so the goal_progress worker can fire milestone notifications. Multi-goal: each call creates one goal; the user may have many at once.",
		SimpleArgs(map[string]map[string]interface{}{
			"name":     StringParam("Human-readable name for the goal (e.g. 'New laptop')."),
			"target":   NumberParam("Target amount in USD (or user's local currency if currency is provided)."),
			"currency": StringParam("Optional currency code; defaults to USD."),
			"deadline": StringParam("Optional ISO 8601 deadline for the goal."),
			"baby_step": {
				"type":        "integer",
				"description": "Optional Baby Step tag (1-7). 1=starter emergency, 2=debt snowball, 3=full emergency, 4=retirement, 5=college, 6=mortgage, 7=wealth.",
				"minimum":     1, "maximum": 7,
			},
			"category": EnumParam("Optional category tag for analytics.", []string{
				"starter_emergency", "debt_payoff", "full_emergency",
				"retirement", "college", "mortgage", "wealth", "freeform",
			}),
		}, []string{"name", "target"}),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.UserGoals == nil {
				return &core.ToolResult{Error: "user goal store unavailable"}, nil
			}
			name := strings.TrimSpace(GetArgString(args, "name"))
			if name == "" {
				return &core.ToolResult{Error: "name is required"}, nil
			}
			target := GetArgFloat(args, "target")
			if target <= 0 {
				return &core.ToolResult{Error: "target must be positive"}, nil
			}
			in := core.CreateUserGoalInput{
				Name:           name,
				TargetAmount:   strconv.FormatFloat(target, 'f', 2, 64),
				TargetCurrency: GetArgString(args, "currency"),
				Category:       GetArgString(args, "category"),
			}
			if d := GetArgString(args, "deadline"); d != "" {
				in.Deadline = &d
			}
			if bsStr := GetArgString(args, "baby_step"); bsStr != "" {
				bs, err := strconv.Atoi(bsStr)
				if err == nil {
					in.BabyStep = &bs
				}
			}
			goal, err := deps.UserGoals.Create(ctx, userID, in)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{
				"goal":  goal,
				"created": true,
			}}, nil
		},
	))

	r.Register(NewTool(
		"list_user_goals",
		"List the user's free-standing savings goals. Always-on. Returns active goals by default; pass include_archived=true to also see completed/archived ones. Use to ground any conversation about progress toward a goal.",
		SimpleArgs(map[string]map[string]interface{}{
			"include_archived": BoolParam("Optional; defaults to false (active goals only)."),
		}, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.UserGoals == nil {
				return &core.ToolResult{Data: map[string]interface{}{"goals": []interface{}{}}}, nil
			}
			list, err := deps.UserGoals.List(ctx, userID, GetArgFloat(args, "include_archived") == 1)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"goals": list, "count": len(list)}}, nil
		},
	))

	r.Register(NewTool(
		"archive_user_goal",
		"Archive (soft-delete) a savings goal. Use when the user no longer wants to track a goal. Idempotent.",
		SimpleArgs(map[string]map[string]interface{}{
			"goal_id": StringParam("UUID of the goal to archive."),
		}, []string{"goal_id"}),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.UserGoals == nil {
				return &core.ToolResult{Error: "user goal store unavailable"}, nil
			}
			id := strings.TrimSpace(GetArgString(args, "goal_id"))
			if _, err := uuid.Parse(id); err != nil {
				return &core.ToolResult{Error: "goal_id must be a valid UUID"}, nil
			}
			if err := deps.UserGoals.Archive(ctx, userID, id); err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"archived": true, "goal_id": id}}, nil
		},
	))

	r.Register(NewTool(
		"update_user_goal_progress",
		"Update the saved-so-far amount on a goal. Use when the user manually moves money toward a goal (e.g., 'I just put another $50 toward my laptop'). The goal_progress worker reads this on its next tick and may fire a milestone notification if a 25/50/75/100% threshold was crossed.",
		SimpleArgs(map[string]map[string]interface{}{
			"goal_id":     StringParam("UUID of the goal."),
			"new_amount":  NumberParam("New saved-so-far amount in the goal's currency."),
		}, []string{"goal_id", "new_amount"}),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.UserGoals == nil {
				return &core.ToolResult{Error: "user goal store unavailable"}, nil
			}
			id := strings.TrimSpace(GetArgString(args, "goal_id"))
			amount := GetArgFloat(args, "new_amount")
			if amount < 0 {
				return &core.ToolResult{Error: "new_amount must be non-negative"}, nil
			}
			updated, err := deps.UserGoals.UpdateProgress(ctx, userID, id, strconv.FormatFloat(amount, 'f', 2, 64))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{
				"goal":     updated,
				"updated":  true,
			}}, nil
		},
	))
}

// ensure time package is referenced even if the file is trimmed down in the
// future (compile-time check that imports stay live).
var _ = time.Now
var _ = fmt.Sprintf
