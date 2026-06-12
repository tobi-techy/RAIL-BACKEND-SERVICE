package statement

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// streamingHTTPClient is configured for long-running SSE connections (no idle timeout killing streams).
var streamingHTTPClient = &http.Client{
	Transport: &http.Transport{
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
	},
	// No Timeout set — controlled by context.WithTimeout per request
}

// TransactionParser uses an LLM to extract structured transactions from bank statement text.
type TransactionParser struct {
	apiKey      string
	baseURL     string
	model       string
	logger      *zap.Logger
	lastCall    time.Time
	minInterval time.Duration
	// Circuit breaker: if consecutiveFailures >= threshold, reject calls for cooldown period
	consecutiveFailures int
	lastFailure         time.Time
	cbThreshold         int
	cbCooldown          time.Duration

	mu sync.Mutex
}

func NewTransactionParser(apiKey string, logger *zap.Logger) *TransactionParser {
	return &TransactionParser{
		apiKey:      apiKey,
		baseURL:     "https://api.moonshot.ai/v1",
		model:       "moonshot-v1-128k",
		logger:      logger,
		minInterval: 3 * time.Second,
		cbThreshold: 5,
		cbCooldown:  2 * time.Minute,
	}
}

// NewTransactionParserWithConfig creates a parser with explicit base URL and model.
func NewTransactionParserWithConfig(apiKey, baseURL, model string, logger *zap.Logger) *TransactionParser {
	if baseURL == "" {
		baseURL = "https://api.moonshot.ai/v1"
	}
	if model == "" {
		model = "moonshot-v1-128k"
	}
	return &TransactionParser{apiKey: apiKey, baseURL: baseURL, model: model, logger: logger, minInterval: 3 * time.Second, cbThreshold: 5, cbCooldown: 2 * time.Minute}
}

const parserSystemPrompt = `Bank statement parser. Extract ALL transactions. Respond with ONLY compact valid JSON, no markdown.
Format: {"bank_name":"X","currency":"NGN","period_start":"2025-01-01","period_end":"2025-06-30","transactions":[{"date":"2025-01-15","description":"POS Shoprite","amount":15000,"type":"debit","category":"groceries"}]}
Rules:
- date: YYYY-MM-DD. amount: positive number. type: credit|debit
- category: food|groceries|transport|utilities|entertainment|shopping|health|education|rent|transfer_in|transfer_out|salary|airtime|betting|subscription|savings|loan|other
- Extract EVERY transaction. Skip headers/footers/summaries.
- Multi-line descriptions: merge (new txn starts with date)
- Output compact JSON: no whitespace, no balance_after field
- Nigerian banks: NGN unless stated otherwise`

// ParseResult holds the LLM's structured output.
type ParseResult struct {
	BankName     string        `json:"bank_name"`
	Currency     string        `json:"currency"`
	PeriodStart  string        `json:"period_start"`
	PeriodEnd    string        `json:"period_end"`
	Transactions []ParsedTxn   `json:"transactions"`
}

type ParsedTxn struct {
	Date         string   `json:"date"`
	Description  string   `json:"description"`
	Amount       float64  `json:"amount"`
	Type         string   `json:"type"`
	Category     string   `json:"category"`
	BalanceAfter *float64 `json:"balance_after"`
}

// detectBank examines the first ~2 pages of text for bank identifiers.
func detectBank(text string) string {
	sample := text
	if len(sample) > 4000 {
		sample = sample[:4000]
	}
	upper := strings.ToUpper(sample)

	checks := []struct {
		bank     string
		keywords []string
	}{
		{"moniepoint", []string{"MONIEPOINT", "TEAMAPT", "MONIEPOINT.COM"}},
		{"opay", []string{"OPAY", "OPERA", "OPAY.COM"}},
		{"sterling", []string{"STERLING BANK", "STERLING", "ONEBANK.STERLING"}},
		{"kuda", []string{"KUDA", "KUDA.COM", "KUDA MICROFINANCE"}},
		{"gtbank", []string{"GUARANTY TRUST", "GTBANK", "GTBANK.COM", "GTB"}},
		{"access", []string{"ACCESS BANK", "DIAMOND BANK", "ACCESS.BANK"}},
		{"nectarfi", []string{"NECTARFI", "NECTAR FI", "NECTAR FINANCE", "NECTARFI.COM"}},
	}

	for _, c := range checks {
		for _, kw := range c.keywords {
			if strings.Contains(upper, kw) {
				return c.bank
			}
		}
	}
	return "unknown"
}

// bankParsingHints returns bank-specific format context for the LLM prompt.
func bankParsingHints(bank string) string {
	switch bank {
	case "sterling":
		return "Sterling Bank statements show Date | Description | Debit | Credit | Balance in columns. Narration field contains transfer details. Watch for 'SMS Alert Charge' and 'VAT' entries."
	case "opay":
		return "OPay statements show transactions as Date | Details | Amount | Balance. Credits show as positive, debits as negative or in a separate column. Look for 'Cashback' entries."
	case "kuda":
		return "Kuda statements list Date | Narration | Debit | Credit | Balance. Transfers show recipient name in narration."
	case "gtbank":
		return "GTBank statements show Post Date | Value Date | Description | Debit | Credit | Balance. Look for 'NIP/' prefix for transfers."
	case "access":
		return "Access Bank statements show Trans Date | Value Date | Reference | Debits | Credits | Balance | Remarks. The Remarks field contains transfer details."
	case "moniepoint":
		return "Moniepoint statements show Date | Description | Amount | Balance. Transfers include recipient name and bank in description."
	case "nectarfi":
		return "NectarFi statements show Date | Description | Debit | Credit | Balance. Digital finance platform; transactions may include wallet top-ups, transfers, and bill payments."
	default:
		return ""
	}
}

// Parse sends extracted text to the LLM and returns structured transactions.
func (p *TransactionParser) Parse(ctx context.Context, text string, bankHint string) (*ParseResult, error) {
	p.mu.Lock()

	// Circuit breaker: if too many consecutive failures, reject immediately
	if p.consecutiveFailures >= p.cbThreshold {
		if time.Since(p.lastFailure) < p.cbCooldown {
			p.mu.Unlock()
			return nil, fmt.Errorf("circuit breaker open: Kimi API has failed %d times consecutively, cooling down", p.consecutiveFailures)
		}
		// Cooldown expired, reset and try again
		p.consecutiveFailures = 0
		p.logger.Info("circuit breaker reset, retrying Kimi")
	}

	// Calculate required sleep before next call while holding the lock.
	// This prevents multiple goroutines from bypassing the rate limit.
	var sleepDuration time.Duration
	if !p.lastCall.IsZero() {
		elapsed := time.Since(p.lastCall)
		if elapsed < p.minInterval {
			sleepDuration = p.minInterval - elapsed
		}
	}
	p.lastCall = time.Now()

	// Release lock during sleep to avoid blocking other operations,
	// then re-acquire for the actual API call to enforce single-flight.
	if sleepDuration > 0 {
		p.mu.Unlock()
		select {
		case <-time.After(sleepDuration):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		p.mu.Lock()
	}

	// Keep the lock held during callKimi so only one API call happens at a time,
	// enforcing the rate limit. The lock is released after updating state below.
	result, err := p.callKimi(ctx, text, bankHint)

	if err != nil {
		p.consecutiveFailures++
		p.lastFailure = time.Now()
		p.mu.Unlock()
		return nil, err
	}
	p.consecutiveFailures = 0
	p.mu.Unlock()
	return result, nil
}

func (p *TransactionParser) callKimi(ctx context.Context, text string, bankHint string) (*ParseResult, error) {
	userPrompt := text
	if bankHint != "" {
		userPrompt = fmt.Sprintf("Bank: %s\n\n%s", bankHint, text)
	}
	// Kimi handles 256K tokens — 30K chars is well within limits
	if len(userPrompt) > 30000 {
		userPrompt = userPrompt[:30000]
	}

	// Build system prompt with bank-specific hints
	sysPrompt := parserSystemPrompt
	if hints := bankParsingHints(bankHint); hints != "" {
		sysPrompt = hints + "\n\n" + sysPrompt
	}

	body := map[string]interface{}{
		"model":       p.model,
		"temperature": 0.1,
		"max_tokens":  16000,
		"stream":      true,
		"messages": []map[string]interface{}{
			{"role": "system", "content": sysPrompt},
			{"role": "user", "content": userPrompt},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", strings.TrimRight(p.baseURL, "/")+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := streamingHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	// Retry once on rate limit
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		p.logger.Warn("Kimi rate limited, retrying after 10s")
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		req2, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.baseURL, "/")+"/chat/completions", bytes.NewReader(jsonBody))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Authorization", "Bearer "+p.apiKey)
		resp, err = streamingHTTPClient.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("api retry call: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, string(errBody))
	}

	// Read SSE stream — collect delta content tokens
	var contentBuilder strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) == nil && len(chunk.Choices) > 0 {
			contentBuilder.WriteString(chunk.Choices[0].Delta.Content)
		}
	}

	content := strings.TrimSpace(contentBuilder.String())
	// Strip markdown fences
	if strings.HasPrefix(content, "```") {
		if idx := strings.Index(content, "\n"); idx != -1 {
			content = content[idx+1:]
		}
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	}

	if len(content) == 0 {
		return nil, fmt.Errorf("empty response from model %s", p.model)
	}

	p.logger.Info("LLM parse response", zap.String("model", p.model), zap.Int("response_length", len(content)), zap.Int("transactions_preview", min(len(content), 200)))

	var parsed ParseResult
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// Log first 500 chars for debugging
		preview := content
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return nil, fmt.Errorf("parse LLM output (%s): %w — preview: %s", p.model, err, preview)
	}

	if len(parsed.Transactions) == 0 {
		preview := content
		if len(preview) > 500 {
			preview = preview[:500]
		}
		p.logger.Warn("model returned 0 transactions", zap.String("model", p.model), zap.String("response_preview", preview))
	}

	return &parsed, nil
}

// ToEntities converts parsed transactions to domain entities.
func (p *TransactionParser) ToEntities(parsed *ParseResult, uploadID, userID uuid.UUID) ([]*entities.BankStatementTransaction, *time.Time, *time.Time) {
	var txns []*entities.BankStatementTransaction
	var periodStart, periodEnd *time.Time

	if t, err := time.Parse("2006-01-02", parsed.PeriodStart); err == nil {
		periodStart = &t
	}
	if t, err := time.Parse("2006-01-02", parsed.PeriodEnd); err == nil {
		periodEnd = &t
	}

	currency := parsed.Currency
	if currency == "" {
		currency = "NGN"
	}

	for _, tx := range parsed.Transactions {
		txDate, err := time.Parse("2006-01-02", tx.Date)
		if err != nil {
			continue
		}
		amount := decimal.NewFromFloat(tx.Amount)
		if amount.IsZero() || amount.IsNegative() {
			continue
		}
		// Sanity check: skip transactions over 1 billion (likely LLM hallucination)
		if amount.GreaterThan(decimal.NewFromInt(1_000_000_000)) {
			continue
		}

		txnType := tx.Type
		if txnType != entities.StatementTxnTypeCredit && txnType != entities.StatementTxnTypeDebit {
			txnType = entities.StatementTxnTypeDebit
		}

		category := tx.Category
		if category == "" {
			category = "other"
		}

		txn := &entities.BankStatementTransaction{
			UploadID:        uploadID,
			UserID:          userID,
			TransactionDate: txDate,
			Description:     tx.Description,
			Amount:          amount,
			Currency:        currency,
			Type:            txnType,
			Category:        category,
		}
		if tx.BalanceAfter != nil {
			bal := decimal.NewFromFloat(*tx.BalanceAfter)
			txn.BalanceAfter = &bal
		}

		txns = append(txns, txn)
	}

	return txns, periodStart, periodEnd
}
