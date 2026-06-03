package statement

import (
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
		model:       "kimi-k2.6",
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
		model = "kimi-k2.6"
	}
	return &TransactionParser{apiKey: apiKey, baseURL: baseURL, model: model, logger: logger, minInterval: 3 * time.Second, cbThreshold: 5, cbCooldown: 2 * time.Minute}
}

const parserSystemPrompt = `You are a bank statement parser. Extract ALL transactions from the provided bank statement text.

You MUST respond with ONLY valid JSON (no markdown, no explanation) in this exact structure:
{
  "bank_name": "detected bank name",
  "currency": "NGN",
  "period_start": "2025-01-01",
  "period_end": "2025-06-30",
  "transactions": [
    {
      "date": "2025-01-15",
      "description": "POS Purchase at Shoprite",
      "amount": 15000.00,
      "type": "debit",
      "category": "groceries",
      "balance_after": 85000.00
    }
  ]
}

Rules:
- date: YYYY-MM-DD format
- amount: positive number (no currency symbols)
- type: "credit" for money in, "debit" for money out
- category: one of: food, groceries, transport, utilities, entertainment, shopping, health, education, rent, transfer_in, transfer_out, salary, airtime, betting, subscription, savings, loan, other
- balance_after: closing balance after transaction if shown, null otherwise
- Extract EVERY transaction visible in the text
- For Nigerian banks (Sterling, OPay, PalmPay, Kuda, GTBank, Access, UBA, Zenith, First Bank): amounts are in NGN unless stated otherwise
- Parse dates correctly even if format varies (DD/MM/YYYY, DD-Mon-YYYY, etc.)
- If you cannot determine period_start/period_end from the statement header, infer from first/last transaction dates
- IMPORTANT: Some transactions have multi-line descriptions (e.g. the narration continues on the next line). Merge these into a single description field. A new transaction always starts with a date — if a line has no date, it is a continuation of the previous transaction's description.
- Ignore header rows, footer rows, page numbers, and summary lines (e.g. "Total Debit", "Opening Balance", "Closing Balance")
- If a transaction shows both debit and credit columns, use whichever is non-zero`

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
	// Truncate to ~100k chars to stay within context window
	if len(userPrompt) > 100000 {
		userPrompt = userPrompt[:100000]
	}

	body := map[string]interface{}{
		"model":       p.model,
		"temperature": 1.0,
		"max_tokens":  32000,
		"messages": []map[string]interface{}{
			{"role": "system", "content": parserSystemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", strings.TrimRight(p.baseURL, "/")+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(req)
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
		resp, err = http.DefaultClient.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("api retry call: %w", err)
		}
		defer resp.Body.Close()
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	content := strings.TrimSpace(result.Choices[0].Message.Content)
	// Strip markdown fences
	if strings.HasPrefix(content, "```") {
		if idx := strings.Index(content, "\n"); idx != -1 {
			content = content[idx+1:]
		}
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	}

	var parsed ParseResult
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse LLM output: %w", err)
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
