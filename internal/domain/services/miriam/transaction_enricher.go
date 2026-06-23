package miriam

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/enrichment"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const llmFallbackThreshold = 0.5

// EnrichmentStore persists enriched transactions.
type EnrichmentStore interface {
	UpsertEnrichedTransaction(ctx context.Context, et *entities.EnrichedTransaction) error
	GetEnrichedByTransactionID(ctx context.Context, txnID uuid.UUID) (*entities.EnrichedTransaction, error)
	GetEnrichedByUser(ctx context.Context, userID uuid.UUID, limit int) ([]entities.EnrichedTransaction, error)
}

// TransactionEnricher orchestrates ML sidecar + LLM fallback.
type TransactionEnricher struct {
	client       *enrichment.Client
	llm          ai.AIProvider
	store        EnrichmentStore
	transactions TransactionProvider
	logger       *zap.Logger
}

// NewTransactionEnricher creates the enricher.
func NewTransactionEnricher(client *enrichment.Client, llm ai.AIProvider, store EnrichmentStore, transactions TransactionProvider, logger *zap.Logger) *TransactionEnricher {
	return &TransactionEnricher{client: client, llm: llm, store: store, transactions: transactions, logger: logger}
}

// Enrich processes a single transaction through ML sidecar, falling back to LLM
// when confidence is below threshold. Persists the result.
func (e *TransactionEnricher) Enrich(ctx context.Context, txnID, userID uuid.UUID, rawDesc string, amount decimal.Decimal, txnDate time.Time) (*entities.EnrichedTransaction, error) {
	// Try ML sidecar first
	mlResult, err := e.client.Enrich(ctx, rawDesc)
	if err != nil {
		e.logger.Warn("enrichment sidecar failed, using LLM fallback", zap.Error(err))
		return e.llmEnrich(ctx, txnID, userID, rawDesc, amount, txnDate)
	}

	// If confidence is too low, escalate to LLM
	if mlResult.Confidence < llmFallbackThreshold {
		return e.llmEnrich(ctx, txnID, userID, rawDesc, amount, txnDate)
	}

	et := &entities.EnrichedTransaction{
		ID:                  uuid.New(),
		TransactionID:       txnID,
		UserID:              userID,
		RawDescription:      rawDesc,
		Amount:              amount,
		Currency:            "USD",
		TransactionDate:     txnDate,
		Direction:           direction(amount),
		Counterparty:        mlResult.Counterparty,
		CategoryL1:          deref(mlResult.CategoryL1, "Uncategorized"),
		CategoryL2:          deref(mlResult.CategoryL2, "Other"),
		IsEssential:         mlResult.IsEssential,
		ClassificationLayer: entities.ClassificationLayerML,
		Confidence:          decimal.NewFromFloat(mlResult.Confidence),
		CreatedAt:           time.Now().UTC(),
	}

	if e.store != nil {
		if err := e.store.UpsertEnrichedTransaction(ctx, et); err != nil {
			e.logger.Warn("failed to persist enriched transaction", zap.Error(err))
		}
	}
	return et, nil
}

// EnrichBatch processes multiple transactions.
func (e *TransactionEnricher) EnrichBatch(ctx context.Context, txns []entities.Transaction) ([]entities.EnrichedTransaction, error) {
	results := make([]entities.EnrichedTransaction, 0, len(txns))
	for _, tx := range txns {
		et, err := e.Enrich(ctx, tx.ID, tx.UserID, tx.Description, tx.Amount, tx.CreatedAt)
		if err != nil {
			e.logger.Warn("batch enrich failed for txn", zap.String("txn_id", tx.ID.String()), zap.Error(err))
			continue
		}
		results = append(results, *et)
	}
	return results, nil
}

func (e *TransactionEnricher) llmEnrich(ctx context.Context, txnID, userID uuid.UUID, rawDesc string, amount decimal.Decimal, txnDate time.Time) (*entities.EnrichedTransaction, error) {
	if e.llm == nil {
		// No LLM available — return low-confidence result
		return &entities.EnrichedTransaction{
			ID: uuid.New(), TransactionID: txnID, UserID: userID,
			RawDescription: rawDesc, Amount: amount, Currency: "USD",
			TransactionDate: txnDate, Direction: direction(amount),
			Counterparty: rawDesc, CategoryL1: "Uncategorized", CategoryL2: "Other",
			ClassificationLayer: entities.ClassificationLayerRule,
			Confidence: decimal.NewFromFloat(0.1), CreatedAt: time.Now().UTC(),
		}, nil
	}

	prompt := fmt.Sprintf(`Classify this bank transaction. Return JSON only.
Transaction: "%s", Amount: %s, Date: %s

Return: {"counterparty":"<resolved name>","category_l1":"<top category>","category_l2":"<sub category>","is_essential":<bool>}

Categories L1: Housing, Food & Drink, Transport, Shopping, Entertainment, Health, Financial, Income, Insurance, Education, Travel, Gifts, Debt Payments, Uncategorized
is_essential = true for necessities (housing, groceries, medical, utilities, insurance).`,
		rawDesc, amount.StringFixed(2), txnDate.Format("2006-01-02"))

	temp := 0.1
	resp, err := e.llm.ChatCompletion(ctx, &ai.ChatRequest{
		Messages:    []ai.Message{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   200,
		ModelHint:   "fast",
	})
	if err != nil {
		e.logger.Warn("LLM enrichment failed", zap.Error(err))
		return &entities.EnrichedTransaction{
			ID: uuid.New(), TransactionID: txnID, UserID: userID,
			RawDescription: rawDesc, Amount: amount, Currency: "USD",
			TransactionDate: txnDate, Direction: direction(amount),
			Counterparty: rawDesc, CategoryL1: "Uncategorized", CategoryL2: "Other",
			ClassificationLayer: entities.ClassificationLayerRule,
			Confidence: decimal.NewFromFloat(0.1), CreatedAt: time.Now().UTC(),
		}, nil
	}

	var parsed struct {
		Counterparty string `json:"counterparty"`
		CategoryL1   string `json:"category_l1"`
		CategoryL2   string `json:"category_l2"`
		IsEssential  bool   `json:"is_essential"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		e.logger.Warn("LLM response parse failed", zap.String("content", resp.Content), zap.Error(err))
		parsed.Counterparty = rawDesc
		parsed.CategoryL1 = "Uncategorized"
		parsed.CategoryL2 = "Other"
	}

	et := &entities.EnrichedTransaction{
		ID:                  uuid.New(),
		TransactionID:       txnID,
		UserID:              userID,
		RawDescription:      rawDesc,
		Amount:              amount,
		Currency:            "USD",
		TransactionDate:     txnDate,
		Direction:           direction(amount),
		Counterparty:        parsed.Counterparty,
		CategoryL1:          parsed.CategoryL1,
		CategoryL2:          parsed.CategoryL2,
		IsEssential:         parsed.IsEssential,
		ClassificationLayer: entities.ClassificationLayerLLM,
		Confidence:          decimal.NewFromFloat(0.75),
		CreatedAt:           time.Now().UTC(),
	}

	if e.store != nil {
		if err := e.store.UpsertEnrichedTransaction(ctx, et); err != nil {
			e.logger.Warn("failed to persist LLM-enriched transaction", zap.Error(err))
		}
	}
	return et, nil
}

func direction(amount decimal.Decimal) string {
	if amount.IsNegative() {
		return "outflow"
	}
	return "inflow"
}

func deref(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}
