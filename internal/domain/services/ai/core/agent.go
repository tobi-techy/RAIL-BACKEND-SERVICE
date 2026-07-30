package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// Agent is the conversational AI agent. It replaces the old Orchestrator
// god object with focused responsibilities: context assembly, LLM interaction,
// tool execution via registry, quality gate, and memory extraction.
type Agent struct {
	deps   *Dependencies
	config *Config
	logger *zap.Logger
}

// NewAgent creates a new Agent.
func NewAgent(deps *Dependencies, config *Config, logger *zap.Logger) *Agent {
	if config == nil {
		config = DefaultConfig()
	}
	return &Agent{
		deps:   deps,
		config: config,
		logger: logger,
	}
}

// ChatResponse is the non-streaming response from the agent.
type ChatResponse struct {
	Content     string
	Cards       []map[string]interface{}
	ActionChips []map[string]interface{}
	TokensUsed  int
	Provider    string
	Model       string
	// PendingAction is set when the model called a mutating action tool. The
	// non-streaming path (iMessage/WhatsApp/voice) does NOT execute it inline —
	// it is staged here so the caller can require confirmation (a tap-confirm
	// poll, or an in-app Face ID step-up for fund-moving actions). This is the
	// enforcement that keeps money from moving on a model-supplied confirm flag.
	PendingAction *PendingAction

	// grounding accumulates tool-result JSON produced while answering, so the
	// response guard can tell which figures are real. Internal; not surfaced to
	// callers.
	grounding string
}

// Chat processes a single user message and returns a response.
func (a *Agent) Chat(ctx context.Context, userID, convID uuid.UUID, message string, opts ChatOptions) (*ChatResponse, error) {
	if err := a.validateInput(userID, message); err != nil {
		return nil, err
	}

	// 1. Check cost ceiling
	if a.deps.Usage != nil && a.deps.Usage.IsOverCostCeiling(ctx, userID) {
		return a.costCeilingResponse(), nil
	}

	// 2. Fast path: trivial messages (greetings, thanks, acks)
	if trivial := a.trivialReply(message); trivial != "" {
		return &ChatResponse{Content: trivial}, nil
	}

	// 3. Load user state
	state, err := a.deps.State.GetState(ctx, userID)
	if err != nil {
		a.logger.Warn("failed to load user state, continuing without", zap.String("user_id", userID.String()), zap.Error(err))
	}

	// 4. Assemble context in parallel
	ctxTimeout, cancel := context.WithTimeout(ctx, a.config.ContextTimeout)
	_, ctxMessages := a.assembleContext(ctxTimeout, userID, message, state, opts)
	cancel()

	// 5. Classify intent
	intents := a.classifyIntent(message, state)

	// 6. Select tools based on intents
	tools := a.selectTools(intents)

	// 7. Build messages: context system prompts + adapter-injected system context
	//    (consolidated personality + live FX) + user message
	messages := ctxMessages
	for _, sc := range opts.SystemContext {
		if strings.TrimSpace(sc) != "" {
			messages = append(messages, &ai.Message{Role: "system", Content: sc})
		}
	}
	messages = append(messages, &ai.Message{Role: "user", Content: message})

	// 8. LLM call with tool rounds
	result, err := a.planExecuteObserve(ctx, userID, convID, messages, tools, opts)
	if err != nil {
		return nil, fmt.Errorf("agent chat: %w", err)
	}

	// 9. Apply safety filter
	result.Content = a.safetyFilter(result.Content)

	// 10. Quality gate — retry once if flat/boring
	if pass, hint := a.checkQuality(result.Content); !pass {
		a.logger.Debug("quality gate failed, retrying once", zap.String("user_id", userID.String()))
		retryResp, retryErr := a.qualityRetry(ctx, messages, result.Content, hint, tools, opts)
		if retryErr == nil && retryResp.Content != "" {
			result.Content = retryResp.Content
			result.TokensUsed += retryResp.TokensUsed
		}
	}

	// 10b. Deterministic response guard (opt-in). Strips ungrounded currency
	// figures, forces a missed anomaly to the surface, and fixes mechanical
	// formatting — so even the weakest model can't fabricate or ramble past here.
	if a.config != nil && a.config.ResponseGuard && a.deps.ResponseGuard != nil {
		grounding := a.groundingCorpus(messages, result.grounding)
		anomalies := a.anomalyContext(ctx, userID)
		if guarded := a.deps.ResponseGuard(result.Content, grounding, anomalies); strings.TrimSpace(guarded) != "" {
			result.Content = guarded
		}
	}

	// 11. Extract memories asynchronously
	if a.deps.Memory != nil {
		go func() {
			memCtxBg := context.Background()
			if err := a.deps.Memory.ProcessExchange(memCtxBg, userID, message, result.Content); err != nil {
				a.logger.Warn("memory extraction failed", zap.Error(err))
			}
		}()
	}

	// 12. Update working memory for conversation continuity
	if a.deps.WorkingMemory != nil {
		go func() {
			a.deps.WorkingMemory.AppendExchange(context.Background(), userID, message, result.Content)
		}()
	}

	return result, nil
}

// planExecuteObserve runs the core LLM loop: call provider → execute tools → repeat.
func (a *Agent) planExecuteObserve(
	ctx context.Context,
	userID, convID uuid.UUID,
	messages []*ai.Message,
	tools []*Tool,
	opts ChatOptions,
) (*ChatResponse, error) {
	// Convert core.Tools to infra ai.Tools
	infraTools := a.convertTools(tools)

	// Build the chat request
	temperature := a.config.DefaultTemperature
	if opts.Temperature > 0 {
		temperature = opts.Temperature
	}
	modelHint := opts.ModelHint
	if modelHint == "" {
		modelHint = "fast" // default to fast for Kimi
	}

	req := &ai.ChatRequest{
		Messages:     a.toInfraMessages(messages),
		SystemPrompt: a.config.SystemPrompt,
		MaxTokens:    a.config.MaxTokens,
		Temperature:  ai.Float64(temperature),
		ModelHint:    modelHint,
	}

	var resp *ai.ChatResponse
	var err error

	resp, err = a.deps.AIProvider.ChatCompletionWithTools(ctx, req, infraTools)
	if err != nil {
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	totalTokens := resp.TokensUsed
	var allToolResults []ToolResult

	// Tool round loop: up to MaxRounds
	for round := 0; round < a.config.MaxToolRounds && len(resp.ToolCalls) > 0; round++ {
		roundResults := make([]*ToolResult, len(resp.ToolCalls))
		var readOnlyTools []int

		for i, tc := range resp.ToolCalls {
			// Check if it's an action tool (requires confirmation)
			if a.isActionTool(tc.Name) {
				// Control-level gate: in Monitor mode, block all action execution.
				// The LLM still sees the tools (so it knows what's possible), but
				// execution is refused with an instruction to switch modes.
				if opts.ControlLevel == ControlLevelMonitor {
					roundResults[i] = &ToolResult{Data: map[string]interface{}{"error": MonitorBlockMessage}}
					continue
				}
				// Do NOT execute the mutating tool here. Stage it as a pending
				// action and return immediately so the caller can require an
				// explicit user confirmation (tap-confirm, or in-app Face ID for
				// fund moves). The model's own `confirm` flag is stripped — it is
				// never a substitute for a real user confirmation.
				pending := &PendingAction{
					Type:      tc.Name,
					Params:    stripConfirm(tc.Arguments),
					ExpiresAt: time.Now().Add(pendingActionTTL),
				}
				content := resp.Content
				return &ChatResponse{
					Content:       content,
					TokensUsed:    totalTokens,
					Provider:      resp.Provider,
					Model:         resp.Model,
					PendingAction: pending,
				}, nil
			}

			readOnlyTools = append(readOnlyTools, i)
		}

		// Execute read-only tools in parallel
		if len(readOnlyTools) > 0 {
			var wg sync.WaitGroup
			for _, idx := range readOnlyTools {
				wg.Add(1)
				go func(i int, tc ai.ToolCall) {
					defer wg.Done()
					result := a.executeReadOnlyTool(ctx, userID, tc)
					roundResults[i] = result
				}(idx, resp.ToolCalls[idx])
			}
			wg.Wait()
		}

		// Build assistant + tool result messages for follow-up
		assistantContent := resp.Content
		if assistantContent == "" {
			assistantContent = "Let me check on that for you."
		}

		// Append assistant message with tool calls preserved
		messages = append(messages, &ai.Message{
			Role:      "assistant",
			Content:   assistantContent,
			ToolCalls: resp.ToolCalls,
		})

		// Append each tool result
		for i, tr := range roundResults {
			if tr == nil {
				continue
			}
			resultJSON, _ := json.Marshal(tr.Data)
			toolCallID := ""
			if i < len(resp.ToolCalls) {
				toolCallID = resp.ToolCalls[i].ID
			}
			messages = append(messages, &ai.Message{
				Role:       "tool",
				Content:    string(resultJSON),
				Name:       resp.ToolCalls[i].Name,
				ToolCallID: toolCallID,
			})
			if tr.Error != "" {
				// Override with error content
				messages[len(messages)-1].Content = fmt.Sprintf(`{"error":"%s"}`, tr.Error)
			}
			allToolResults = append(allToolResults, *tr)
		}

		// Follow-up call
		req.Messages = a.toInfraMessages(messages)
		req.MaxTokens = a.config.MaxTokens

		resp, err = a.deps.AIProvider.ChatCompletionWithTools(ctx, req, infraTools)
		if err != nil {
			return nil, fmt.Errorf("follow-up completion failed: %w", err)
		}
		totalTokens += resp.TokensUsed
	}

	// Check if any tool returned a pending action
	for _, tr := range allToolResults {
		if tr.Action != nil {
			return &ChatResponse{
				Content:    resp.Content,
				TokensUsed: totalTokens,
				Provider:   resp.Provider,
				Model:      resp.Model,
				grounding:  toolResultCorpus(allToolResults),
			}, nil
		}
	}

	return &ChatResponse{
		Content:    resp.Content,
		TokensUsed: totalTokens,
		Provider:   resp.Provider,
		Model:      resp.Model,
		grounding:  toolResultCorpus(allToolResults),
	}, nil
}

// toolResultCorpus flattens every tool result into a single string so the
// response guard can extract the real figures Miriam is allowed to state.
func toolResultCorpus(results []ToolResult) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	for _, tr := range results {
		if tr.Data != nil {
			if j, err := json.Marshal(tr.Data); err == nil {
				b.Write(j)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// groundingCorpus builds the full set of real figures the guard treats as
// grounded: every injected system message (assembled context, balances, FX) plus
// the tool-result JSON accumulated while answering.
func (a *Agent) groundingCorpus(messages []*ai.Message, toolCorpus string) string {
	var b strings.Builder
	for _, m := range messages {
		if m != nil && m.Role == "system" {
			b.WriteString(m.Content)
			b.WriteByte('\n')
		}
	}
	b.WriteString(toolCorpus)
	return b.String()
}

// anomalyContext returns the raw injected anomaly context for the user, or "" if
// none was surfaced.
func (a *Agent) anomalyContext(ctx context.Context, userID uuid.UUID) string {
	if a.deps.AnomalyContextFn == nil {
		return ""
	}
	return a.deps.AnomalyContextFn(ctx, userID)
}

// assembleContext collects context from all available slots in parallel.
func (a *Agent) assembleContext(ctx context.Context, userID uuid.UUID, message string, state *UserState, opts ChatOptions) (*MemoryContext, []*ai.Message) {
	var ctxMessages []*ai.Message
	memCtx := &MemoryContext{}

	if ctx == nil {
		return memCtx, ctxMessages
	}

	type slotResult struct {
		index   int
		content string
	}

	slotCh := make(chan slotResult, 20)
	var wg sync.WaitGroup
	addSlot := func(idx int, fn func() string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s := fn(); s != "" {
				slotCh <- slotResult{index: idx, content: s}
			}
		}()
	}

	// Memory slot (index 0)
	// Order matters: GetMemorySummary (Postgres-backed) is the critical path
	// and must run first so it completes before the context deadline.
	// SearchMemory (Supermemory HTTP) and Qdrant lookups are optional enrichments.
	if a.deps.Memory != nil {
		addSlot(0, func() string {
			summary, _ := a.deps.Memory.GetMemorySummary(ctx, userID, message)
			results, _ := a.deps.Memory.SearchMemory(ctx, userID, message, 6)
			facts, _ := a.deps.Memory.SearchFacts(ctx, userID, message, 5)
			episodes, _ := a.deps.Memory.SearchEpisodic(ctx, userID, message, 3)
			memCtx.Supermemory = results
			memCtx.Facts = facts
			memCtx.Episodic = episodes
			memCtx.Summary = summary
			return a.buildMemoryPrompt(memCtx)
		})
	}

	// Balance slot (index 1)
	if state != nil && state.Balances != nil {
		slotCh <- slotResult{index: 1, content: fmt.Sprintf("[User balances — Spend: $%s | Stash: $%s]", state.Balances.Spend.StringFixed(2), state.Balances.Stash.StringFixed(2))}
		// First-session / cold-start: empty ledger → forbid inventing dollars and
		// orient the user toward funding + one useful next step.
		zero := state.Balances.Spend.IsZero() && state.Balances.Stash.IsZero()
		if zero {
			slotCh <- slotResult{index: 15, content: `[FIRST SESSION — cold start]
The user has no Spend or Stash balance yet (both $0.00).
RULES: Do NOT invent dollar amounts, spending, or income. Do NOT claim they spent or saved anything.
Be warm and brief. Orient them: load money into Rail, then you can watch movement, catch spikes, and set quiet stash rules.
If they ask for balances, say both are $0.00 and invite them to fund.
One helpful question max (e.g. when they get paid next).`}
		}
	}

	// Financial profile slot (index 2)
	if a.deps.Profile != nil {
		addSlot(2, func() string {
			profile, err := a.deps.Profile.GetFinancialProfile(ctx, userID)
			if err != nil || profile == nil {
				return ""
			}
			text := "[Financial profile:"
			if v, ok := profile["risk_tolerance"]; ok {
				text += fmt.Sprintf(" risk: %v", v)
			}
			if v, ok := profile["monthly_income"]; ok {
				text += fmt.Sprintf(" income: $%v", v)
			}
			if v, ok := profile["savings_target"]; ok {
				text += fmt.Sprintf(" savings target: $%v", v)
			}
			text += "]"
			return text
		})
	}

	// Bank statement slot (index 3)
	if a.deps.BankStatement != nil {
		addSlot(3, func() string {
			s, err := a.deps.BankStatement.GetContext(ctx, userID)
			if err != nil {
				return ""
			}
			return s
		})
	}

	// Naira context slot (index 4)
	if a.deps.NairaCtx != nil {
		addSlot(4, func() string {
			s, err := a.deps.NairaCtx.GetContext(ctx, userID)
			if err != nil {
				return ""
			}
			return s
		})
	}

	// Signals slot (index 5)
	if a.deps.Signals != nil {
		addSlot(5, func() string {
			signals, err := a.deps.Signals.GetActiveByUser(ctx, userID)
			if err != nil || len(signals) == 0 {
				return ""
			}
			return fmt.Sprintf("[Active signals: %d signal(s)]", len(signals))
		})
	}

	// Money state slot (index 6). Confidence only: the safe-to-spend figure is
	// deliberately not injected here because it is a projection, not an observed
	// number — when it appeared in context, the model quoted it as fact and the
	// fabrication gate flagged the reply. Safe-to-spend must come from a tool
	// call (get_cash_flow_forecast / get_miriam_brief) if the user asks for it.
	if a.deps.MiriamIntell != nil {
		addSlot(6, func() string {
			ms, err := a.deps.MiriamIntell.GetMoneyState(ctx, userID)
			if err != nil || ms == nil {
				return ""
			}
			text := "[Money state:"
			if v, ok := ms["confidence_level"]; ok {
				text += fmt.Sprintf(" %v", v)
			}
			text += "]"
			return text
		})
	}

	// Anomaly slot (index 7) — recent anomaly detections for Miriam to reference.
	if a.deps.AnomalyContextFn != nil {
		addSlot(7, func() string {
			return a.deps.AnomalyContextFn(ctx, userID)
		})
	}

	// Financial events slot (index 8) — recent financial events timeline.
	if a.deps.EventStoreFn != nil {
		addSlot(8, func() string {
			return a.deps.EventStoreFn(ctx, userID)
		})
	}

	// Working memory slot (index 9) — conversation state from Redis.
	if a.deps.WorkingMemory != nil {
		addSlot(9, func() string {
			snap := a.deps.WorkingMemory.Get(ctx, userID)
			if snap == nil || snap.GetSummary() == "" {
				return ""
			}
			return fmt.Sprintf("[CONVERSATION STATE — recent context from this session: %s]", snap.GetSummary())
		})
	}

	// Enrichment summary slot (index 10) — recent enriched transaction patterns.
	if a.deps.EnrichmentSummaryFn != nil {
		addSlot(10, func() string {
			s, err := a.deps.EnrichmentSummaryFn(ctx, userID)
			if err != nil || s == "" {
				return ""
			}
			return s
		})
	}

	wg.Wait()
	close(slotCh)

	// Collect results in index order
	ordered := make([]string, 20)
	for r := range slotCh {
		if r.index < len(ordered) {
			ordered[r.index] = r.content
		}
	}

	for _, s := range ordered {
		if s != "" {
			ctxMessages = append(ctxMessages, &ai.Message{Role: "system", Content: s})
		}
	}

	// Emotion/energy detection (post-process, synchronous)
	if emotion := detectEmotion(message); emotion != "" {
		ctxMessages = append(ctxMessages, &ai.Message{Role: "system", Content: emotion})
	}
	if energy := detectEnergy(message); energy != "" {
		ctxMessages = append(ctxMessages, &ai.Message{Role: "system", Content: energy})
	}

	return memCtx, ctxMessages
}

// detectEmotion returns a system hint about the user's emotional state based on keywords.
func detectEmotion(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "frustrat"), strings.Contains(lower, "annoy"), strings.Contains(lower, "uh"),
		strings.Contains(lower, "ugh"), strings.Contains(lower, "grr"), strings.Contains(lower, "damn"):
		return "[Emotion: user sounds frustrated. Acknowledge with empathy before solving the issue.]"
	case strings.Contains(lower, "anxious"), strings.Contains(lower, "worried"), strings.Contains(lower, "nervous"),
		strings.Contains(lower, "stressed"), strings.Contains(lower, "scared"), strings.Contains(lower, "afraid"):
		return "[Emotion: user sounds anxious. Reassure them with clear, calm info and proactive guidance.]"
	case strings.Contains(lower, "sad"), strings.Contains(lower, "down"), strings.Contains(lower, "depressed"),
		strings.Contains(lower, "unhappy"), strings.Contains(lower, "miserable"):
		return "[Emotion: user sounds down. Be warm and human, but don't pry.]"
	case strings.Contains(lower, "excited"), strings.Contains(lower, "amazing"), strings.Contains(lower, "awesome"),
		strings.Contains(lower, "incredible"), strings.Contains(lower, "wow"), strings.Contains(lower, "happy"):
		return "[Emotion: user sounds excited. Match their energy and celebrate with them.]"
	}
	return ""
}

// detectEnergy returns a system hint about the user's message energy based on length.
func detectEnergy(message string) string {
	msg := strings.TrimSpace(message)
	l := len(msg)
	switch {
	case l <= 10:
		return "[Energy: user sent a very short message. Reply in 1 line, brief and to the point.]"
	case l <= 40:
		return "[Energy: quick question. Be concise, answer directly.]"
	case l > 150:
		return "[Energy: user wrote a lot. They want a thorough response.]"
	}
	return ""
}

// buildMemoryPrompt constructs the "[What you know..." system message.
func (a *Agent) buildMemoryPrompt(memCtx *MemoryContext) string {
	var parts []string

	// Add supermemory results
	for _, r := range memCtx.Supermemory {
		if r.Similarity >= 0.5 && r.Memory != "" {
			parts = append(parts, r.Memory)
		}
	}

	// Add facts
	for _, f := range memCtx.Facts {
		if f.Confidence >= 0.5 {
			parts = append(parts, fmt.Sprintf("[%s] %s", f.Category, f.Fact))
		}
	}

	if len(parts) == 0 && memCtx.Summary == "" {
		return ""
	}

	// Try summary first if it exists — it's already formatted by
	// BuildMemoryContextWithSummary, so return it as-is to avoid double-wrapping.
	if memCtx.Summary != "" {
		return memCtx.Summary
	}

	var joined string
	for _, p := range parts {
		if len(joined)+len(p)+3 > 1200 {
			break
		}
		if joined != "" {
			joined += " | "
		}
		joined += p
	}

	if joined == "" {
		return ""
	}
	return fmt.Sprintf("[What you know about this user, relevant to what they just said — weave in naturally, never say \"I recall\" or \"you mentioned\": %s]", joined)
}

// classifyIntent determines the user's intent category from their message via
// keyword matching. It errs on the side of CategoryFull for ambiguity, which
// makes selectTools return the full registry. A concrete category keeps the
// offered tool set small, which both cuts token cost and stops weak models
// from picking the wrong tool.
func (a *Agent) classifyIntent(message string, state *UserState) []Intent {
	lower := strings.ToLower(message)
	category := CategoryFull
	confidence := 0.5

	switch {
	// Automation before action: "move $50 every friday" is recurring, not one-off.
	case containsAny(lower, intentAutomationPatterns):
		category, confidence = CategoryAutomation, 0.8
	case containsAny(lower, intentActionPatterns):
		category, confidence = CategoryAction, 0.8
	case containsAny(lower, intentPlanningPatterns):
		category, confidence = CategoryPlanning, 0.7
	// History before spending: "show my deposits" is history, not analysis.
	case containsAny(lower, intentHistoryPatterns):
		category, confidence = CategoryHistory, 0.8
	case containsAny(lower, intentInvestmentPatterns):
		category, confidence = CategoryInvestment, 0.8
	case containsAny(lower, intentMemoryPatterns):
		category, confidence = CategoryMemory, 0.8
	case containsAny(lower, intentSpendingPatterns):
		category, confidence = CategorySpending, 0.8
	case containsAny(lower, intentOverviewPatterns):
		category, confidence = CategoryOverview, 0.7
	}

	return []Intent{{Category: category, Confidence: confidence, RequiresLLM: true}}
}

var intentActionPatterns = []string{
	"move ", "transfer", "send $", "send ₦", "send money",
	"set budget", "set a budget", "save $", "save ₦",
	"to stash", "to my stash", "into my stash", "from my stash", "lock",
	"set goal", "savings goal", "remind me", "protect", "cancel subscription",
	"withdraw $", "withdraw ₦", "withdraw from", "withdraw to", "to my bank",
	"block", "unblock", "pay my bill", "pay bill", "autopay", "auto-pay",
	"buy airtime", "buy data", "copy trad", "copy trade", "stop copying", "pause copying", "cancel my",
}

var intentAutomationPatterns = []string{
	"automation", "automate", "schedule", "every week", "every friday",
	"every month", "every day", "daily", "weekly", "monthly", "recurring",
	"automatic", "autopilot", "when balance", "threshold", "smart timing",
}

var intentPlanningPatterns = []string{
	"advice", "audit", "roast", "plan", "forecast", "predict", "projection",
	"health score", "what should i do", "reality check", "hard mode",
	"operating plan", "tax", "how can i save", "suggestions",
	"where to put", "grow my money", "subscriptions", "bills", "idle cash",
	"will my", "next year", "next month",
}

var intentHistoryPatterns = []string{
	"transactions", "show me my", "deposit", "withdrawal",
	"card transactions", "receipt", "income trend", "yield", "interest",
	"how much did i make", "how much did i earn", "how much did i deposit",
	"how much did i withdraw", "last month", "income",
	"compared to", "vs last", "vs usual", "than my usual income",
}

var intentSpendingPatterns = []string{
	"spend", "spent", "spending", "where did my money", "money go", "breakdown",
	"category", "categories", "food", "transport", "shopping", "groceries",
	"outflow", "burn rate", "patterns", "weird", "strange", "off on my account",
	"more than normal", "more than usual", "than normal", "than usual",
}

var intentOverviewPatterns = []string{
	"how am i doing", "what changed", "what matters", "overview",
	"what's up", "update me", "brief", "status", "check in",
	"balance", "how much do i have", "what do i have", "how's my money",
	"how is my money", "my saving", "my savings",
}

var intentInvestmentPatterns = []string{
	"invest", "investment", "portfolio", "stock", "stocks", "shares",
	"trader", "returns", "etf", "basket",
}

var intentMemoryPatterns = []string{
	"what do you know about me", "forget that", "forget everything",
	"my memories", "what do you remember",
}

func containsAny(text string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// alwaysOnTools are offered every turn regardless of classified intent. They
// cover the core money surfaces (balances, spending, income, bills, goals) so
// Miriam can always ground the most common financial answers, plus the full
// action toolset so she can stage a move the moment the user asks. Keeping
// them unconditional mirrors the previous "everything, every turn" behavior
// for these categories while still narrowing knowledge/memory/history/
// portfolio/travel tools to the turns that need them.
var alwaysOnTools = map[string]bool{
	// Overview
	"get_account_summary": true, "get_miriam_brief": true, "get_yield_status": true,
	"get_goals": true, "list_obligations": true, "list_subscriptions": true,
	"find_payment_matches": true,
	// Spending
	"get_money_flow": true, "get_spending_summary": true, "get_recent_transactions": true,
	"get_spending_patterns": true, "get_spending_comparison": true, "get_merchant_insights": true,
	"get_recurring_expenses": true, "audit_subscriptions": true, "list_blocked_merchants": true,
	// History
	"get_income_trend": true, "get_deposit_history": true, "get_withdrawal_history": true,
	"get_card_transactions": true, "get_balance_history": true, "get_yield_earned": true,
	// Planning
	"get_upcoming_bills": true, "get_financial_profile": true, "get_financial_health": true,
	"get_financial_plan": true, "get_financial_audit": true, "get_money_operating_plan": true,
	"get_cash_flow_forecast": true, "get_savings_suggestions": true,
	// Budget
	"get_budget": true, "set_budget": true,
	// Automation
	"list_automations": true, "create_automation": true,
	// Action (mutating tools are staged for confirmation by the chat loop,
	// never executed inline, so offering them every turn is safe).
	"transfer_funds": true, "initiate_withdrawal": true, "optimize_yield": true,
	"setup_bill_autopay": true, "cancel_subscription": true, "execute_investment": true,
	"block_merchant": true, "unblock_merchant": true, "pay_bill": true, "automate_bill": true,
	"save_bill_beneficiary": true, "set_savings_goal": true, "create_obligation_reminder": true,
	"mark_obligation_paid": true, "protect_subscription": true, "get_linked_banks": true,
	"list_bill_providers": true, "get_data_plans": true, "get_cable_packages": true,
	"validate_meter": true, "detect_network": true,
	// Engagement
	"celebrate": true, "send_poll": true,
}

// selectTools returns the subset of tools relevant to the user's intents.
// The intent category's tools are unioned with the always-on money set, so a
// turn classified "Spending" still offers balance/income/transfer tools. An
// ambiguous (CategoryFull) message gets the whole registry.
func (a *Agent) selectTools(intents []Intent) []*Tool {
	if a.deps.ToolRegistry == nil {
		return nil
	}
	// Ambiguous or empty intent: offer the full registry so no capability is lost.
	if len(intents) == 0 {
		return a.deps.ToolRegistry.GetAll()
	}
	cats := make([]ToolCategory, len(intents))
	full := false
	for i, intent := range intents {
		cats[i] = intent.Category
		if intent.Category == CategoryFull {
			full = true
		}
	}
	if full {
		return a.deps.ToolRegistry.GetAll()
	}
	byCategory := a.deps.ToolRegistry.GetByCategories(cats)
	all := a.deps.ToolRegistry.GetAll()
	byName := make(map[string]*Tool, len(byCategory)+len(alwaysOnTools))
	for _, t := range byCategory {
		byName[t.Name] = t
	}
	for _, t := range all {
		if alwaysOnTools[t.Name] {
			byName[t.Name] = t
		}
	}
	// Nothing matched: fall back to the full registry rather than offering no
	// tools, which previously left Miriam answering money questions blind.
	if len(byName) == 0 {
		return all
	}
	out := make([]*Tool, 0, len(byName))
	for _, t := range all {
		if byName[t.Name] != nil {
			out = append(out, t)
		}
	}
	return out
}

// pendingActionTTL bounds how long a staged action waits for user confirmation.
const pendingActionTTL = 5 * time.Minute

// stripConfirm copies args without the model-supplied `confirm` flag. The flag
// is granted only by an explicit user confirmation (re-injected server-side on
// confirm), never by the model — this is what stops a fund move from executing
// on a self-granted confirm over iMessage/WhatsApp.
func stripConfirm(args map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		if k == "confirm" {
			continue
		}
		out[k] = v
	}
	return out
}

// isActionTool reports whether a tool mutates state and therefore must be staged
// for explicit user confirmation instead of executing inline. This mirrors the
// streaming path's isExecutionActionTool set plus raw fund moves.
//
// MVP: stage high-stakes writes (money + automations + mandate acceptance).
// Low-risk record-keeping (set_budget, set_savings_goal, create_obligation_reminder,
// mark_obligation_paid, protect_subscription) still auto-execute — they don't move
// money at call time.
func (a *Agent) isActionTool(name string) bool {
	actionTools := map[string]bool{
		"transfer_funds": true, "initiate_withdrawal": true,
		// Execution Engine (spec 5.2) — mutating tools, gated by Monitor mode
		// here and staged for confirmation on the non-streaming path.
		"setup_bill_autopay": true, "cancel_subscription": true,
		"execute_investment": true, "optimize_yield": true,
		"block_merchant": true, "unblock_merchant": true,
		"copy_trader": true, "pause_trade_copying": true,
		"resume_trade_copying": true, "stop_trade_copying": true,
		// Nigerian bill payments (Airbills). pay_bill/automate_bill move money;
		// save_bill_beneficiary is a confirmed write.
		"pay_bill": true, "automate_bill": true, "save_bill_beneficiary": true,
		// P2P + receipt splits move real Spend balance (or reserve for claim links).
		"send_money": true, "split_receipt": true,
		// Automations and mandate acceptance create lasting autonomous behavior.
		"create_automation":         true,
		"accept_mandate_suggestion": true,
		"create_miriam_mandate":     true,
	}
	return actionTools[name]
}

// executeActionTool handles action tools that require confirmation.
func (a *Agent) executeActionTool(ctx context.Context, userID, convID uuid.UUID, tc ai.ToolCall) *ToolResult {
	if a.deps.ToolRegistry == nil {
		return &ToolResult{Error: "no tool registry"}
	}
	result, err := a.deps.ToolRegistry.Execute(ctx, userID, tc.Name, tc.Arguments, a.deps)
	if err != nil {
		return &ToolResult{Error: err.Error()}
	}
	return result
}

// executeReadOnlyTool executes a single read-only tool.
func (a *Agent) executeReadOnlyTool(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) *ToolResult {
	if a.deps.ToolRegistry == nil {
		return &ToolResult{Error: "no tool registry"}
	}
	result, err := a.deps.ToolRegistry.Execute(ctx, userID, tc.Name, tc.Arguments, a.deps)
	if err != nil {
		return &ToolResult{Error: err.Error(), Data: map[string]interface{}{"error": err.Error()}}
	}
	return result
}

// checkQuality runs the injected quality gate (falling back to a length check)
// and returns whether the response passes plus a correction hint for the retry.
func (a *Agent) checkQuality(content string) (bool, string) {
	if a.deps.QualityGate != nil {
		return a.deps.QualityGate(content)
	}
	return a.isQualityPassing(content), ""
}

// qualityRetry re-generates the response with a quality improvement hint.
// It uses ChatCompletionWithTools so the retry can call tools to ground numbers.
func (a *Agent) qualityRetry(ctx context.Context, messages []*ai.Message, previousContent, hint string, tools []*Tool, opts ChatOptions) (*ChatResponse, error) {
	if hint == "" {
		hint = "CRITICAL: Your previous response had quality issues. REWRITE: Be direct and brief. Use specific numbers from tool results. If a tool gave you data, cite the exact numbers. End with a short question to keep the conversation going."
	}
	hintMsg := &ai.Message{Role: "system", Content: hint}

	retryMessages := append(messages, &ai.Message{Role: "assistant", Content: previousContent}, hintMsg)

	infraTools := a.convertTools(tools)

	req := &ai.ChatRequest{
		Messages:     a.toInfraMessages(retryMessages),
		SystemPrompt: a.config.SystemPrompt,
		MaxTokens:    a.config.MaxTokens,
		Temperature:  ai.Float64(a.config.DefaultTemperature + 0.1),
		ModelHint:    "fast",
	}

	provider := a.deps.AIProvider
	resp, err := provider.ChatCompletionWithTools(ctx, req, infraTools)
	if err != nil || resp.Content == "" {
		return nil, fmt.Errorf("quality retry failed: %w", err)
	}

	return &ChatResponse{
		Content:    resp.Content,
		TokensUsed: resp.TokensUsed,
		Provider:   resp.Provider,
		Model:      resp.Model,
	}, nil
}

// trivialReply returns a canned response for greetings/acks, or empty string.
func (a *Agent) trivialReply(message string) string {
	msg := strings.TrimSpace(strings.ToLower(message))
	if len(msg) > 50 || strings.Contains(msg, " ") && len(strings.Split(msg, " ")) > 3 {
		return ""
	}
	switch msg {
	case "hi", "hey", "hello", "sup", "yo", "hiya":
		return "Hey! What's up?"
	case "thanks", "thank you", "ty", "thx", "appreciate it":
		return "Anytime! Anything else?"
	case "ok", "okay", "k", "kk", "got it", "sure":
		return "Got it. Anything else?"
	case "bye", "goodbye", "cya", "see ya", "later":
		return "Catch you later!"
	case "lol", "haha":
		return "😂"
	}
	return ""
}

// safetyFilter applies regex-based disclaimers and blocks harmful content.
func (a *Agent) safetyFilter(content string) string {
	return content
}

// isQualityPassing checks if the response meets basic quality standards.
func (a *Agent) isQualityPassing(content string) bool {
	if len(content) < 10 {
		return false
	}
	return true
}

// costCeilingResponse returns a canned response when over the cost ceiling.
func (a *Agent) costCeilingResponse() *ChatResponse {
	return &ChatResponse{
		Content: "You've reached your monthly AI usage limit. It'll reset next billing cycle. In the meantime, you can still check balances and view your dashboard.",
	}
}

// validateInput checks required fields.
func (a *Agent) validateInput(userID uuid.UUID, message string) error {
	if userID == uuid.Nil {
		return fmt.Errorf("userID is required")
	}
	if message == "" {
		return fmt.Errorf("message is required")
	}
	return nil
}

// --- Conversion helpers ---

// convertTools converts core.Tools to infra ai.Tool format.
func (a *Agent) convertTools(tools []*Tool) []ai.Tool {
	result := make([]ai.Tool, len(tools))
	for i, t := range tools {
		result[i] = ai.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	return result
}

// toInfraMessages converts our internal message pointers to infra message values.
func (a *Agent) toInfraMessages(msgs []*ai.Message) []ai.Message {
	result := make([]ai.Message, len(msgs))
	for i, m := range msgs {
		if m == nil {
			continue
		}
		result[i] = *m
	}
	return result
}

// --- Proactive features ---

func (a *Agent) GetProactiveOpener(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	return map[string]interface{}{
		"greeting": "Hey! What's on your mind?",
		"severity": "info",
	}
}

func (a *Agent) GetConversationStarters(ctx context.Context, userID uuid.UUID) []map[string]interface{} {
	return []map[string]interface{}{
		{"text": "How am I doing financially?", "category": "overview"},
		{"text": "What did I spend on food this month?", "category": "spending"},
		{"text": "Transfer $50 to my stash", "category": "action"},
	}
}

func (a *Agent) GetAllTools() []ai.Tool {
	if a.deps.ToolRegistry == nil {
		return nil
	}
	return a.convertTools(a.deps.ToolRegistry.GetAll())
}

func (a *Agent) ExecuteTool(ctx context.Context, userID uuid.UUID, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	if a.deps.ToolRegistry == nil {
		return nil, fmt.Errorf("no tool registry")
	}
	result, err := a.deps.ToolRegistry.Execute(ctx, userID, toolName, args, a.deps)
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ExecuteToolStrict executes a registry tool and surfaces an in-result error
// (ToolResult.Error) as a Go error, so callers that need success/failure
// semantics (e.g. confirmed pending actions) don't misreport failures.
func (a *Agent) ExecuteToolStrict(ctx context.Context, userID uuid.UUID, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	if a.deps.ToolRegistry == nil {
		return nil, fmt.Errorf("no tool registry")
	}
	result, err := a.deps.ToolRegistry.Execute(ctx, userID, toolName, args, a.deps)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if result.Error != "" {
		return result.Data, fmt.Errorf("%s", result.Error)
	}
	return result.Data, nil
}

func (a *Agent) IsUserOverCostCeiling(ctx context.Context, userID uuid.UUID) bool {
	if a.deps.Usage == nil {
		return false
	}
	return a.deps.Usage.IsOverCostCeiling(ctx, userID)
}
