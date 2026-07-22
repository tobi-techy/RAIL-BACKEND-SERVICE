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
	EventDepositReceived         = "deposit_received"
	EventWithdrawalInitiated     = "withdrawal_initiated"
	EventWithdrawalCompleted     = "withdrawal_completed"
	EventCardCreated             = "card_created"
	EventCardTransaction         = "card_transaction" // approved authorization
	EventCardTransactionDeclined = "card_transaction_declined"
	EventP2PTransfer             = "p2p_transfer"
	EventStashTransfer           = "stash_transfer"
	EventRoundUpTriggered        = "round_up_triggered"
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
	EventMiriamEvaluationRun    = "miriam_evaluation_run"    // intelligence pass completed
	EventMiriamActionExecuted   = "miriam_action_executed"   // autonomous money move
	EventMiriamNudgeSent        = "miriam_nudge_sent"        // proactive alert delivered
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

// ── Card Lifecycle Metrics ──────────────────────────────

const (
	EventCardFrozen    = "card_frozen"
	EventCardUnfrozen  = "card_unfrozen"
	EventCardCancelled = "card_cancelled"
)

// ── Subscription Lifecycle Metrics ──────────────────────

const (
	EventSubscriptionRenewed        = "subscription_renewed"
	EventSubscriptionCancelled      = "subscription_cancelled"
	EventSubscriptionExpired        = "subscription_expired"
	EventSubscriptionPaymentFailed  = "subscription_payment_failed"
	EventSubscriptionBillingCycle   = "subscription_billing_cycle"
	EventSubscriptionTransferRetry  = "subscription_transfer_retry"
)

// ── Recovery & Resilience Metrics ───────────────────────

const (
	EventWithdrawalRecovered = "withdrawal_recovered"
)

// ══════════════════════════════════════════════════════════
// ── Churn & Retention Deep Signals ──────────────────────
// ══════════════════════════════════════════════════════════

const (
	// Churn risk: declining engagement patterns
	EventChurnRiskDetected     = "churn_risk_detected"      // backend heuristic flagged user
	EventInactivityWarning     = "inactivity_warning"        // user hasn't opened app in N days
	EventFeatureAbandoned      = "feature_abandoned"         // used feature 3+ times then stopped
	EventAutoInvestDisabled    = "auto_invest_disabled"      // turned off auto-invest (reversal)
	EventAccountDeactivation   = "account_deactivation_requested"
	EventNegativeBalance       = "negative_balance"          // balance went below zero
	EventDepositStreakBroken   = "deposit_streak_broken"     // missed expected deposit cadence

	// Retention: positive habit signals
	EventReturnVisit           = "return_visit"              // came back after 7+ day gap
	EventWeeklyActive          = "weekly_active"             // 3rd+ session in a rolling week
	EventMonthlyActive         = "monthly_active"            // 5th+ session in a rolling month
	EventStreakMaintained       = "streak_maintained"         // extended a savings/streak
	EventFeatureRepeatUse      = "feature_repeat_use"        // 3rd+ use of same feature in a week
	EventGoalMilestoneReached  = "goal_milestone_reached"    // hit 25/50/75/100% of a goal
	EventSavingsStreakExtended = "savings_streak_extended"   // added another month to streak
	EventPaycheckAutoRouted    = "paycheck_auto_routed"      // paycheck automatically split
	EventRecurringDepositSet   = "recurring_deposit_created" // scheduled recurring deposit
)

// ══════════════════════════════════════════════════════════
// ── Agent Interaction Quality ───────────────────────────
// ══════════════════════════════════════════════════════════

const (
	// Conversation quality
	EventAISessionDuration     = "ai_session_duration"      // how long the conversation lasted
	EventAIToolCallSuccess     = "ai_tool_call_success"     // tool executed successfully
	EventAIToolCallFailure     = "ai_tool_call_failure"     // tool execution failed
	EventAIResponseUseful      = "ai_response_useful"       // user followed through on rec
	EventAIResponseIgnored     = "ai_response_ignored"      // user dismissed/ignored recommendation
	EventAIFallbackTriggered   = "ai_fallback_triggered"    // fell back to generic response
	EventAIClarificationNeeded = "ai_clarification_needed"  // Miriam had to ask follow-up
	EventAIContextUsed         = "ai_context_used"          // personal memory/budget context injected
	EventAIVoiceMessage        = "ai_voice_message"         // user sent voice input
	EventAIVoiceResponse       = "ai_voice_response"        // Miriam responded with voice

	// Action quality
	EventAIActionCompleted     = "ai_action_completed"      // action executed without errors
	EventAIActionFailed        = "ai_action_failed"         // action attempted but failed
	EventAIActionCancelled     = "ai_action_cancelled"      // user cancelled proposed action
	EventAIActionStepUp        = "ai_action_step_up_required" // needed Face ID / passcode
	EventAIProactiveNudgeHit   = "ai_proactive_nudge_hit"  // nudge led to user action within 24h
	EventAIProactiveNudgeMiss  = "ai_proactive_nudge_miss"  // nudge ignored for 24h+

	// Platform channel tracking
	EventPlatformChannel       = "platform_channel"         // imessage | whatsapp | telegram | app
	EventPlatformLinked        = "platform_account_linked"  // messaging account linked to rail
	EventPlatformUnlinked      = "platform_account_unlinked"
)

// ══════════════════════════════════════════════════════════
// ── Onboarding Funnel (Step-by-Step) ────────────────────
// ══════════════════════════════════════════════════════════

const (
	EventOnboardStepStarted    = "onboard_step_started"     // property: step_name
	EventOnboardStepCompleted  = "onboard_step_completed"   // property: step_name
	EventOnboardStepDropped    = "onboard_step_dropped"     // property: step_name, reason
	EventOnboardCompleted      = "onboard_completed"        // full funnel done
	EventOnboardTimeToActivate = "onboard_time_to_activate" // seconds from signup to first deposit
)

// ══════════════════════════════════════════════════════════
// ── Feature Adoption & Engagement Depth ─────────────────
// ══════════════════════════════════════════════════════════

const (
	EventFeatureFirstUse       = "feature_first_use"        // property: feature_name
	EventFeatureUsed           = "feature_used"             // property: feature_name, count
	EventFeatureDisabled       = "feature_disabled"         // property: feature_name
	EventScreenViewed          = "screen_viewed"            // property: screen_name
	EventDeepLinkOpened        = "deep_link_opened"         // property: path
	EventNotificationTapped    = "notification_tapped"      // property: notification_type
	EventNotificationDismissed = "notification_dismissed"   // property: notification_type
	EventShareFlowStarted      = "share_flow_started"       // user tried to share
	EventShareFlowCompleted    = "share_flow_completed"     // share succeeded
)

// ══════════════════════════════════════════════════════════
// ── Session & Engagement Quality ────────────────────────
// ══════════════════════════════════════════════════════════

const (
	EventSessionEnd            = "session_end"              // with duration, screens_viewed
	EventSessionDepth          = "session_depth"            // number of distinct features used
	EventTimeToFirstAction     = "time_to_first_action"     // seconds from open to first meaningful action
	EventBackgroundEntry       = "background_entered"       // app went to background
	EventAppColdStart          = "app_cold_start"           // fresh launch vs resume
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
	PropHasCard            = "has_card"
	PropMiriamActionsTotal = "miriam_actions_total"
	PropWithdrawalCount    = "withdrawal_count"

	// ── Churn & retention profile props ────────────────────
	PropDaysSinceLastSession    = "days_since_last_session"
	PropTotalSessions           = "total_sessions"
	PropLastSessionAt           = "last_session_at"
	PropAccountAgeDays          = "account_age_days"
	PropChurnRiskScore          = "churn_risk_score"        // 0-100
	PropFeaturesUsedCount       = "features_used_count"
	PropMostUsedFeature         = "most_used_feature"
	PropDepositFrequencyDays    = "deposit_frequency_days"  // avg days between deposits
	PropRetentionCohort         = "retention_cohort"        // week/month of signup
	PropLifecycleStage          = "lifecycle_stage"         // new | activated | engaged | at_risk | dormant | churned
	PropL7DActivityCount        = "l7d_activity_count"      // events in last 7 days
	PropL30DActivityCount       = "l30d_activity_count"     // events in last 30 days

	// ── Agent quality profile props ────────────────────────
	PropMiriamConversationsTotal = "miriam_conversations_total"
	PropMiriamToolsUsedTotal     = "miriam_tools_used_total"
	PropMiriamRecommendationRate = "miriam_recommendation_acceptance_rate"
	PropMiriamLastInteractionAt  = "miriam_last_interaction_at"
	PropMiriamPreferredChannel   = "miriam_preferred_channel" // imessage | whatsapp | app

	// ── Financial behavior profile props ───────────────────
	PropAvgMonthlySpend     = "avg_monthly_spend"
	PropSavingsRate         = "savings_rate"               // percent of income saved
	PropCardSpendLast30D    = "card_spend_last_30d"
	PropYieldEarnedTotal    = "yield_earned_total"
	PropGoalCompletionRate  = "goal_completion_rate"       // percent of goals completed
	PropBudgetAdherenceRate = "budget_adherence_rate"      // percent of months under budget
)
