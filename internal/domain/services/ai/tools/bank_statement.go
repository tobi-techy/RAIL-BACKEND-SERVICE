package tools

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// RegisterBankStatementTools registers the bank statement analysis tool.
func RegisterBankStatementTools(r *Registry) {
	r.Register(NewTool(
		"get_bank_statement_analysis",
		`Get a detailed analysis of the user's uploaded bank statements: spending by category with percentages, total income vs total expenses, savings rate, top recurring payments, and a personalized growth plan mapped to their Baby Step. Use this after the user uploads a bank statement and asks "what does it say?", "analyze my spending", "how am I doing", or when they want a spending breakdown from their external bank data. If no statements have been uploaded, tell them to upload one in the app.`,
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"months": map[string]interface{}{
					"type":        "integer",
					"description": "Number of months to analyze. Defaults to 6. Max 12.",
				},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.BankStatementAnalysis == nil {
				return &core.ToolResult{Data: map[string]interface{}{
					"has_data":   false,
					"error":      "Bank statement analysis is not available right now.",
					"suggestion": "You can upload your bank statement in the RAIL app and I'll break down your spending, find your savings rate, and build you a growth plan.",
				}}, nil
			}

			months := 6
			if m, ok := args["months"]; ok {
				if mi, ok := m.(float64); ok && mi > 0 && mi <= 12 {
					months = int(mi)
				}
			}

			data, err := deps.BankStatementAnalysis.GetAnalysis(ctx, userID, months)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: data}, nil
		},
	))

	r.Register(NewTool(
		"connect_bank",
		`Start a bank-connect session (Mono) and return a tappable URL so the user can share their real spending. Call this when they agree to let you look at their bank, or when seeing the picture would make the next step obvious. Never tell them to hunt for Add Bank in the app — this tool sends the link.`,
		map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"required":             []string{},
			"additionalProperties": false,
		},
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps == nil || deps.BankLinker == nil {
				return &core.ToolResult{Data: map[string]interface{}{
					"available":  false,
					"suggestion": "Bank linking isn't available right now. They can still tell you about their spending.",
				}}, nil
			}
			name, email := bankLinkIdentity(ctx, userID, deps)
			url, err := deps.BankLinker.InitiateLinking(ctx, userID, name, email, "rail://bank-linked")
			if err != nil {
				return &core.ToolResult{Data: map[string]interface{}{
					"available":  false,
					"error":      err.Error(),
					"suggestion": "I couldn't start the bank link just now. We can do this later, or they can tell me how they spend.",
				}}, nil
			}
			if strings.TrimSpace(url) == "" {
				return &core.ToolResult{Data: map[string]interface{}{
					"available":  false,
					"suggestion": "Bank linking isn't available in their country yet.",
				}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{
				"available": true,
				"provider":  "mono",
				"url":       url,
				"title":     "Connect your bank",
			}}, nil
		},
	))
}

func bankLinkIdentity(ctx context.Context, userID uuid.UUID, deps *core.Dependencies) (name, email string) {
	name, email = "Rail user", ""
	if deps.FullUserProfile != nil {
		if p, err := deps.FullUserProfile.GetProfile(ctx, userID); err == nil && p != nil {
			if p.FirstName != nil && strings.TrimSpace(*p.FirstName) != "" {
				name = strings.TrimSpace(*p.FirstName)
			}
			if strings.TrimSpace(p.Email) != "" && !strings.Contains(p.Email, "@placeholder.invalid") {
				email = p.Email
			}
		}
	}
	if email == "" && deps.UserProfile != nil {
		if e, err := deps.UserProfile.GetEmail(ctx, userID); err == nil {
			email = e
		}
	}
	if email == "" {
		email = "hello+" + userID.String() + "@userail.money"
	}
	return name, email
}
