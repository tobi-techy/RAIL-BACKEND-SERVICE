package core

// Canonical tool-tier sets shared by the prompt builder and the enforcement
// paths, so the prompt can never drift from what the server actually stages.
//
// AutoExecuteTools are low-risk writes that never move money at call time —
// the model speaks as done right after calling them.
// StageConfirmTools mutate money or create lasting autonomous behavior — the
// chat loop stages them as PendingActions for explicit user confirmation.
var (
	AutoExecuteTools = map[string]bool{
		"set_budget":                 true,
		"set_savings_goal":           true,
		"create_obligation_reminder": true,
		"mark_obligation_paid":       true,
		"protect_subscription":       true,
	}

	StageConfirmTools = map[string]bool{
		"transfer_funds": true, "initiate_withdrawal": true,
		// Execution Engine (spec 5.2) — mutating tools, gated by Monitor mode.
		"setup_bill_autopay": true, "cancel_subscription": true,
		"execute_investment": true, "optimize_yield": true,
		"block_merchant": true, "unblock_merchant": true,
		"copy_trader": true, "pause_trade_copying": true,
		"resume_trade_copying": true, "stop_trade_copying": true,
		// Nigerian bill payments (Airbills).
		"pay_bill": true, "automate_bill": true, "save_bill_beneficiary": true,
		// P2P + receipt splits move real Spend balance (or reserve claim links).
		"send_money": true, "split_receipt": true,
		// Automations and mandate acceptance create lasting autonomous behavior.
		// (Mandates themselves are proposed system-side by the suggestion
		// engine; there is no Miriam-initiated create tool.)
		"create_automation":         true,
		"accept_mandate_suggestion": true,
	}
)
