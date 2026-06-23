package analytics

// ── Growth Metrics ──────────────────────────────────────

const (
	EventSignupStarted     = "signup_started"
	EventSignupCompleted   = "signup_completed"
	EventKYCStarted        = "kyc_started"
	EventKYCCompleted      = "kyc_completed"
	EventKYCFailed         = "kyc_failed"
	EventFirstDeposit      = "first_deposit"
	EventReferralSent      = "referral_sent"
	EventReferralConverted = "referral_converted"
	EventWaitlistJoined    = "waitlist_joined"
	EventWaitlistConverted = "waitlist_converted"
)

// ── Activation Metrics ──────────────────────────────────

const (
	EventFundingSourceConnected = "funding_source_connected"
	EventDepositCompleted       = "deposit_completed"
	EventAllocationExecuted     = "allocation_executed"
	EventFirstAllocation        = "first_allocation"
	EventAutoInvestEnabled      = "auto_invest_enabled"  // user toggles the feature on
	EventAutoInvestTriggered    = "autoinvest_triggered" // auto-invest actually fires
	EventFirstInvestment        = "first_investment"
	EventInvestmentOrderPlaced  = "investment_order_placed"
)

// ── Retention Metrics ───────────────────────────────────

const (
	EventAppOpened       = "app_opened"
	EventSessionStarted  = "session_started"
	EventDepositRepeated = "deposit_repeated"
	EventPaycheckDeposit = "paycheck_deposit"
)

// ── Money Movement Metrics ──────────────────────────────

const (
	EventDepositReceived     = "deposit_received"
	EventWithdrawalInitiated = "withdrawal_initiated"
	EventWithdrawalCompleted = "withdrawal_completed"
	EventCardCreated         = "card_created"
	EventCardTransaction     = "card_transaction" // approved authorization
	EventCardTransactionDeclined = "card_transaction_declined"
	EventP2PTransfer         = "p2p_transfer"
	EventStashTransfer       = "stash_transfer"
	EventRoundUpTriggered    = "round_up_triggered"
)

// ── Financial Behavior Metrics ──────────────────────────

const (
	EventSavingsStreakUpdated = "savings_streak_updated"
	EventGoalCreated         = "goal_created"
	EventGoalCompleted       = "goal_completed"
	EventOverspendingAlert   = "overspending_alert"
	EventImpulseWithdrawal   = "impulse_withdrawal"
	EventBudgetSet           = "budget_set"
	EventBudgetExceeded      = "budget_exceeded"
	EventYieldDistributed    = "yield_distributed"
	EventAUMUpdated          = "aum_updated"
	EventNetInflowRecorded   = "net_inflow_recorded"
	EventPaycheckDetected    = "paycheck_detected"
	EventFinancialHealthUpdated = "financial_health_updated"
)

// ── AI (Miriam) Metrics ─────────────────────────────────

const (
	EventAIConversationStarted    = "ai_conversation_started"
	EventAIQuestionAsked          = "ai_question_asked"
	EventAIConversationCompleted  = "ai_conversation_completed"
	EventAIToolUsed               = "ai_tool_used"
	EventAIRecommendationGiven    = "ai_recommendation_given"
	EventAIRecommendationAccepted = "ai_recommendation_accepted"
	EventAIActionTriggered        = "ai_action_triggered"
	EventAISummaryViewed          = "ai_summary_viewed"
)

// ── Miriam Autonomous Intelligence Metrics ──────────────

const (
	EventMiriamEvaluationRun   = "miriam_evaluation_run"   // intelligence pass completed
	EventMiriamActionExecuted  = "miriam_action_executed"  // autonomous money move
	EventMiriamNudgeSent       = "miriam_nudge_sent"       // proactive alert delivered
	EventMiriamMandateSuggested = "miriam_mandate_suggested" // new automation suggested
)

// ── Revenue Metrics ─────────────────────────────────────

const (
	EventRevenueEarned       = "revenue_earned"
	EventPremiumConverted    = "premium_converted"
	EventCardInterchange     = "card_interchange_earned"
	EventWithdrawalFeeEarned = "withdrawal_fee_earned"
)

// ── Trust & Risk Metrics ────────────────────────────────

const (
	EventDepositFailed       = "deposit_failed"
	EventWithdrawalFailed    = "withdrawal_failed"
	EventTransactionFailed   = "transaction_failed"
	EventFraudDetected       = "fraud_detected"
	EventSupportTicketOpened = "support_ticket_opened"
	EventChargeback          = "chargeback"
)

// ── User Profile Properties ─────────────────────────────

const (
	PropFirstDepositAt    = "first_deposit_at"
	PropTotalDeposits     = "total_deposits"
	PropTotalWithdrawals  = "total_withdrawals"
	PropNetInflow         = "net_inflow"
	PropAUM               = "aum"
	PropAutoInvestEnabled = "auto_invest_enabled"
	PropKYCStatus         = "kyc_status"
	PropAccountCurrency   = "account_currency"
	PropCountry           = "country"
	PropSignupSource      = "signup_source"
	PropDepositCount      = "deposit_count"
	PropLastDepositAt     = "last_deposit_at"
	PropConsecutiveMonths = "consecutive_deposit_months"
	PropPlan              = "plan"

	// Financial health (populated by Miriam on each evaluation pass)
	PropFinancialHealthScore = "financial_health_score"
	PropLiquidityRunwayDays  = "liquidity_runway_days"
	PropIncomeCadence        = "income_cadence"
	PropAvgMonthlyIncome     = "avg_monthly_income"
	PropConfidenceLevel      = "confidence_level"

	// Product engagement
	PropHasCard             = "has_card"
	PropMiriamActionsTotal  = "miriam_actions_total"
	PropWithdrawalCount     = "withdrawal_count"
)
