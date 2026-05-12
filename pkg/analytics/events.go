package analytics

// ── Growth Metrics ──────────────────────────────────────

const (
	EventSignupStarted       = "signup_started"
	EventSignupCompleted     = "signup_completed"
	EventKYCStarted          = "kyc_started"
	EventKYCCompleted        = "kyc_completed"
	EventKYCFailed           = "kyc_failed"
	EventFirstDeposit        = "first_deposit"
	EventReferralSent        = "referral_sent"
	EventReferralConverted   = "referral_converted"
	EventWaitlistJoined      = "waitlist_joined"
	EventWaitlistConverted   = "waitlist_converted"
)

// ── Activation Metrics ──────────────────────────────────

const (
	EventFundingSourceConnected = "funding_source_connected"
	EventDepositCompleted       = "deposit_completed"
	EventAllocationExecuted     = "allocation_executed"
	EventFirstAllocation        = "first_allocation"
	EventAutoInvestEnabled      = "auto_invest_enabled"
	EventFirstInvestment        = "first_investment"
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
	EventDepositReceived    = "deposit_received"
	EventWithdrawalInitiated = "withdrawal_initiated"
	EventWithdrawalCompleted = "withdrawal_completed"
	EventCardTransaction     = "card_transaction"
	EventP2PTransfer         = "p2p_transfer"
	EventStashTransfer       = "stash_transfer"
	EventRoundUpTriggered    = "round_up_triggered"
)

// ── Financial Behavior Metrics ──────────────────────────

const (
	EventSavingsStreakUpdated   = "savings_streak_updated"
	EventGoalCreated            = "goal_created"
	EventGoalCompleted          = "goal_completed"
	EventOverspendingAlert      = "overspending_alert"
	EventImpulseWithdrawal      = "impulse_withdrawal"
	EventBudgetSet              = "budget_set"
	EventBudgetExceeded         = "budget_exceeded"
	EventYieldDistributed       = "yield_distributed"
	EventAUMUpdated             = "aum_updated"
	EventNetInflowRecorded      = "net_inflow_recorded"
	EventPaycheckDetected       = "paycheck_detected"
)

// ── AI Agent Metrics ────────────────────────────────────

const (
	EventAIConversationStarted = "ai_conversation_started"
	EventAIQuestionAsked       = "ai_question_asked"
	EventAIRecommendationGiven = "ai_recommendation_given"
	EventAIRecommendationAccepted = "ai_recommendation_accepted"
	EventAIActionTriggered     = "ai_action_triggered"
	EventAISummaryViewed       = "ai_summary_viewed"
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
	PropFirstDepositAt     = "first_deposit_at"
	PropTotalDeposits      = "total_deposits"
	PropTotalWithdrawals   = "total_withdrawals"
	PropNetInflow          = "net_inflow"
	PropAUM                = "aum"
	PropAutoInvestEnabled  = "auto_invest_enabled"
	PropKYCStatus          = "kyc_status"
	PropAccountCurrency    = "account_currency"
	PropCountry            = "country"
	PropSignupSource       = "signup_source"
	PropDepositCount       = "deposit_count"
	PropLastDepositAt      = "last_deposit_at"
	PropConsecutiveMonths  = "consecutive_deposit_months"
	PropPlan               = "plan"
)
