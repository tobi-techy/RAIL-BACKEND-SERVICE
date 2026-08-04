package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	// workersAIAccountsBase is the Cloudflare Workers AI accounts base URL.
	// {account_id} is substituted at construction time.
	workersAIAccountsBase = "https://api.cloudflare.com/client/v4/accounts/%s"
	// workersAIRunSuffix appends /ai/run/{model}.
	workersAIRunSuffix = "/ai/run/"
)

// IntentCategory is the coarse tool-selection category an intent classifier
// returns. Values mirror core's ToolCategory strings so the agent can map them
// 1:1 without importing the domain package (which would create an import cycle).
type IntentCategory string

const (
	IntentOverview   IntentCategory = "Overview"
	IntentSpending   IntentCategory = "Spending"
	IntentAction     IntentCategory = "Action"
	IntentPlanning   IntentCategory = "Planning"
	IntentHistory    IntentCategory = "History"
	IntentAutomation IntentCategory = "Automation"
	IntentBudget     IntentCategory = "Budget"
	IntentMemory     IntentCategory = "Memory"
	IntentVoice      IntentCategory = "Voice"
	IntentInvestment IntentCategory = "Investment"
	IntentKnowledge  IntentCategory = "Knowledge"
	IntentFull       IntentCategory = "Full"
)

// IntentClassifier narrows the agent's tool set before the LLM call using a
// cheap/fast model (Cloudflare Workers AI). The agent falls back to keyword
// matching when the classifier is nil, errors, times out, or returns a
// low-confidence/unmappable category.
type IntentClassifier interface {
	// Classify returns an intent category and confidence. ok=false means "don't
	// trust this" — callers should fall back to their deterministic path.
	Classify(ctx context.Context, message string) (category IntentCategory, confidence float64, ok bool)
}

// WorkersAIConfig configures the Workers AI client.
type WorkersAIConfig struct {
	AccountID string
	APIToken  string
	Model     string // e.g. "@cf/meta/llama-3.1-8b-instruct"
	Timeout   time.Duration
	// BaseURL overrides the API base (default https://api.cloudflare.com/client/v4/accounts/{id}).
	// Used by tests; leave empty in production.
	BaseURL string
}

// WorkersAIClient is a thin REST client for Cloudflare Workers AI text
// generation models. It is intentionally narrow: the agent only uses it for
// cheap housekeeping steps (intent classification), never the main brain.
type WorkersAIClient struct {
	baseURL  string
	apiToken string
	model    string
	http     *http.Client
	logger   *zap.Logger
}

// NewWorkersAIClient creates a Workers AI client.
func NewWorkersAIClient(config *WorkersAIConfig, logger *zap.Logger) *WorkersAIClient {
	if config == nil {
		panic("WorkersAIConfig is required")
	}
	if config.AccountID == "" {
		panic("WorkersAIConfig.AccountID is required")
	}
	if config.APIToken == "" {
		panic("WorkersAIConfig.APIToken is required")
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	baseURL := strings.TrimSuffix(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf(workersAIAccountsBase, config.AccountID)
	}
	return &WorkersAIClient{
		baseURL:  baseURL,
		apiToken: config.APIToken,
		model:    config.Model,
		http:     &http.Client{Timeout: timeout},
		logger:   logger,
	}
}

// runURL returns the run endpoint for a model.
func (c *WorkersAIClient) runURL(model string) string {
	if model == "" {
		model = c.model
	}
	return c.baseURL + workersAIRunSuffix + model
}

// RunText sends a chat completion to the Workers AI model and returns the
// generated text. When forceJSON is true the request asks for a JSON object.
func (c *WorkersAIClient) RunText(ctx context.Context, model string, messages []WorkerAIMessage, maxTokens int, forceJSON bool) (string, error) {
	body := map[string]interface{}{
		"messages": messages,
	}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	if forceJSON {
		body["response_format"] = map[string]interface{}{"type": "json_object"}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("workers ai marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.runURL(model), bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("workers ai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("workers ai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("workers ai read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("workers ai status %d (response %d bytes)", resp.StatusCode, len(respBody))
	}

	var parsed struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("workers ai decode: %w", err)
	}

	// Text generation models return the output under result.response.
	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(parsed.Result, &result); err != nil {
		return "", fmt.Errorf("workers ai decode result: %w", err)
	}
	return result.Response, nil
}

// WorkerAIMessage is a single message in a Workers AI chat request.
type WorkerAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// IntentClassifierConfig configures WorkersAIIntentClassifier.
type IntentClassifierConfig struct {
	Client        *WorkersAIClient
	Model         string
	Timeout       time.Duration
	MinConfidence float64
	// MaxMessageRunes bounds the prompt to avoid excessive tokens on long messages.
	MaxMessageRunes int
}

// WorkersAIIntentClassifier implements IntentClassifier using a cheap Workers AI
// model. It is deliberately conservative: on any parse/network error, or when the
// model returns an unknown category or low confidence, it returns ok=false so the
// caller falls back to deterministic keyword routing.
type WorkersAIIntentClassifier struct {
	cfg    *IntentClassifierConfig
	logger *zap.Logger
}

// NewWorkersAIIntentClassifier creates an ML intent classifier backed by Workers AI.
func NewWorkersAIIntentClassifier(cfg *IntentClassifierConfig, logger *zap.Logger) *WorkersAIIntentClassifier {
	if cfg.Timeout == 0 {
		cfg.Timeout = 800 * time.Millisecond
	}
	if cfg.MinConfidence == 0 {
		cfg.MinConfidence = 0.5
	}
	if cfg.MaxMessageRunes == 0 {
		cfg.MaxMessageRunes = 400
	}
	return &WorkersAIIntentClassifier{cfg: cfg, logger: logger}
}

const intentSystemPrompt = `You classify a user's financial chat message into exactly one category.
Pick the single best category:
- Automation: recurring/scheduled moves, "every week", autopilot, thresholds
- Action: a one-off money move, transfer, save, budget change, bill pay, block/cancel
- Planning: advice, forecasts, audits, projections, health score, "what should I do"
- History: past transactions, deposits, withdrawals, receipts, income trend
- Spending: analysis of spending, categories, breakdowns, unusual charges
- Budget: setting or viewing a budget
- Overview: account balances or a general financial snapshot
- Memory: what the assistant should remember about the user
- Investment: portfolio, stocks, yield, investing
- Knowledge: general financial or product questions
- Full: anything ambiguous, mixed, or not clearly one of the above

Respond with ONLY JSON: {"category":"<one of the above>","confidence":<0.0 to 1.0>}.`

// Classify implements IntentClassifier.
func (c *WorkersAIIntentClassifier) Classify(ctx context.Context, message string) (IntentCategory, float64, bool) {
	if c.cfg == nil || c.cfg.Client == nil {
		return "", 0, false
	}
	msg := []rune(message)
	if len(msg) > c.cfg.MaxMessageRunes {
		msg = msg[:c.cfg.MaxMessageRunes]
	}

	classifyCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	text, err := c.cfg.Client.RunText(classifyCtx, c.cfg.Model, []WorkerAIMessage{
		{Role: "system", Content: intentSystemPrompt},
		{Role: "user", Content: string(msg)},
	}, 64, true)
	if err != nil {
		c.logger.Debug("workers ai intent classification failed, falling back to keywords", zap.Error(err))
		return "", 0, false
	}

	var parsed struct {
		Category   string  `json:"category"`
		Confidence float64 `json:"confidence"`
	}
	clean := strings.TrimSpace(text)
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		c.logger.Debug("workers ai classifier returned non-JSON, falling back to keywords", zap.Int("response_len", len(clean)))
		return "", 0, false
	}

	cat := IntentCategory(strings.TrimSpace(parsed.Category))
	if !validIntentCategory(cat) {
		return "", 0, false
	}
	if parsed.Confidence < c.cfg.MinConfidence {
		return "", 0, false
	}
	return cat, parsed.Confidence, true
}

// validIntentCategory reports whether the model returned one of the known
// categories. Unknown categories and "Full" are treated as untrustworthy so the
// caller falls back to its deterministic keyword router.
func validIntentCategory(cat IntentCategory) bool {
	switch cat {
	case IntentOverview, IntentSpending, IntentAction, IntentPlanning,
		IntentHistory, IntentAutomation, IntentBudget, IntentMemory,
		IntentVoice, IntentInvestment, IntentKnowledge:
		return true
	default:
		return false
	}
}
