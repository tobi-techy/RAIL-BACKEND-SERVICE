package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyMessage(t *testing.T) {
	tests := []struct {
		msg      string
		expected ToolCategory
	}{
		// Overview
		{"how am I doing?", CategoryOverview},
		{"what changed this week?", CategoryOverview},
		{"what's my balance", CategoryOverview},

		// Spending
		{"how much did I spend this month", CategorySpending},
		{"where did my money go", CategorySpending},
		{"show me spending breakdown", CategorySpending},

		// Action
		{"move $50 to stash", CategoryAction},
		{"transfer 30 to savings", CategoryAction},
		{"set budget to 500", CategoryAction},
		{"withdraw $100", CategoryAction},

		// Planning
		{"audit me", CategoryPlanning},
		{"give me financial advice", CategoryPlanning},
		{"what should I do this month", CategoryPlanning},
		{"roast my finances", CategoryPlanning},

		// History
		{"show me my transactions", CategoryHistory},
		{"how much did I deposit", CategoryHistory},
		{"withdrawal history this month", CategoryHistory},

		// Automation
		{"set up automation to save every week", CategoryAutomation},
		{"what automations do I have", CategoryAutomation},
		{"move money every friday", CategoryAutomation},

		// Full (ambiguous)
		{"hey", CategoryFull},
		{"yo miriam", CategoryFull},
		{"thanks", CategoryFull},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got := classifyMessage(tt.msg)
			assert.Equal(t, tt.expected, got, "message: %q", tt.msg)
		})
	}
}

func TestRouteToolsReturnsSubset(t *testing.T) {
	o := &Orchestrator{}
	allTools := o.GetTools()

	// Overview should return fewer tools than the full set
	overviewTools := o.RouteTools("how am I doing?")
	assert.Less(t, len(overviewTools), len(allTools), "routed tools should be fewer than all tools")
	assert.Greater(t, len(overviewTools), 0, "should have at least some tools")

	// Ambiguous messages should return full set
	fullTools := o.RouteTools("hey")
	assert.Equal(t, len(allTools), len(fullTools))
}

func TestRouteToolsAlwaysIncludesAccountSummary(t *testing.T) {
	o := &Orchestrator{}

	categories := []string{
		"how am I doing?",         // overview
		"how much did I spend",    // spending
		"move money to stash",     // action
		"audit me",                // planning
		"what automations exist",  // automation
	}

	for _, msg := range categories {
		tools := o.RouteTools(msg)
		found := false
		for _, tool := range tools {
			if tool.Name == ToolGetAccountSummary {
				found = true
				break
			}
		}
		assert.True(t, found, "category for %q should include get_account_summary", msg)
	}
}
