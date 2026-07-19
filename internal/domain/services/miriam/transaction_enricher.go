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
	"golang.org/x/sync/errgroup"
)

const (
	llmFallbackThreshold    = 0.5
	enrichBatchConcurrency  = 10
	defaultCurrency         = "USD"
)

// EnrichmentStore persists enriched transactions.
type EnrichmentStore interface {
	UpsertEnrichedTransaction(ctx context.Context, et *entities.EnrichedTransaction) error
	GetEnrichedByTransactionID(ctx context.Context, txnID uuid.UUID) (*entities.EnrichedTransaction, error)
	GetEnrichedByTransactionIDs(ctx context.Context, txnIDs []uuid.UUID) (map[uuid.UUID]*entities.EnrichedTransaction, error)
	GetEnrichedByUser(ctx context.Context, userID uuid.UUID, limit int) ([]entities.EnrichedTransaction, error)
	GetUserEnrichmentSummary(ctx context.Context, userID uuid.UUID) (string, error)
	GetRecentFactsByUser(ctx context.Context, userID uuid.UUID, limit int) ([]entities.TransactionFact, error)
	BridgeFactsToMemory(ctx context.Context, userID uuid.UUID, facts []entities.TransactionFact) (int, error)
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
func (e *TransactionEnricher) Enrich(ctx context.Context, txnID, userID uuid.UUID, rawDesc string, amount decimal.Decimal, txnDate time.Time, currency string) (*entities.EnrichedTransaction, error) {
	if currency == "" {
		currency = defaultCurrency
	}

	// Try ML sidecar first
	mlResult, err := e.client.Enrich(ctx, rawDesc, currency)
	if err != nil {
		e.logger.Warn("enrichment sidecar failed, using LLM fallback", zap.Error(err))
		return e.llmEnrich(ctx, txnID, userID, rawDesc, amount, txnDate, currency)
	}

	// If confidence is too low, escalate to LLM
	if mlResult.Confidence < llmFallbackThreshold {
		return e.llmEnrich(ctx, txnID, userID, rawDesc, amount, txnDate, currency)
	}

	// Marshal pipeline output fields
	behaviorTagsJSON, err := json.Marshal(mlResult.BehaviorTags)
	if err != nil {
		e.logger.Warn("failed to marshal behavior tags", zap.Error(err))
		behaviorTagsJSON = []byte("[]")
	}
	factsJSON, err := json.Marshal(mlResult.Facts)
	if err != nil {
		e.logger.Warn("failed to marshal facts", zap.Error(err))
		factsJSON = []byte("[]")
	}
	embedding := make([]float32, len(mlResult.Embedding))
	for i, v := range mlResult.Embedding {
		embedding[i] = float32(v)
	}

	et := &entities.EnrichedTransaction{
		ID:                  uuid.New(),
		TransactionID:       txnID,
		UserID:              userID,
		RawDescription:      rawDesc,
		Amount:              amount,
		Currency:            currency,
		TransactionDate:     txnDate,
		Direction:           direction(amount),
		Counterparty:        mlResult.Counterparty,
		CategoryL1:          deref(mlResult.CategoryL1, "Uncategorized"),
		CategoryL2:          deref(mlResult.CategoryL2, "Other"),
		IsEssential:         mlResult.IsEssential,
		PlainDescription:    mlResult.PlainDescription,
		MerchantContext:     mlResult.MerchantContext,
		Bank:                deref(mlResult.Bank, ""),
		TxType:              deref(mlResult.TxType, ""),
		BehaviorTags:        behaviorTagsJSON,
		Facts:               factsJSON,
		Embedding:           embedding,
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

// EnrichBatch processes multiple transactions in parallel with dedup.
// It skips transactions that already have enrichment in the store.
func (e *TransactionEnricher) EnrichBatch(ctx context.Context, txns []entities.Transaction) ([]entities.EnrichedTransaction, error) {
	if len(txns) == 0 {
		return nil, nil
	}

	// Dedup: check which transactions are already enriched
	unenriched := txns
	if e.store != nil {
		ids := make([]uuid.UUID, len(txns))
		for i, tx := range txns {
			ids[i] = tx.ID
		}
		existing, err := e.store.GetEnrichedByTransactionIDs(ctx, ids)
		if err != nil {
			e.logger.Warn("dedup check failed, enriching all", zap.Error(err))
		} else if len(existing) > 0 {
			filtered := make([]entities.Transaction, 0, len(txns)-len(existing))
			for _, tx := range txns {
				if _, ok := existing[tx.ID]; !ok {
					filtered = append(filtered, tx)
				}
			}
			unenriched = filtered
		}
	}

	if len(unenriched) == 0 {
		// All transactions already enriched — return them so callers get results
		if e.store != nil {
			ids := make([]uuid.UUID, len(txns))
			for i, tx := range txns {
				ids[i] = tx.ID
			}
			existing, err := e.store.GetEnrichedByTransactionIDs(ctx, ids)
			if err != nil {
				return nil, nil
			}
			enriched := make([]entities.EnrichedTransaction, 0, len(existing))
			for _, et := range existing {
				enriched = append(enriched, *et)
			}
			return enriched, nil
		}
		return nil, nil
	}

	// Parallel enrichment with worker pool
	results := make([]entities.EnrichedTransaction, len(unenriched))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(enrichBatchConcurrency)

	for i, tx := range unenriched {
		i, tx := i, tx
		g.Go(func() error {
			txnDate := tx.CreatedAt
			if tx.ConfirmedAt != nil {
				txnDate = *tx.ConfirmedAt
			}
			et, err := e.Enrich(gctx, tx.ID, tx.UserID, tx.Description, tx.Amount, txnDate, tx.Currency)
			if err != nil {
				e.logger.Warn("batch enrich failed for txn",
					zap.String("txn_id", tx.ID.String()),
					zap.Error(err))
				return nil // don't abort the batch on single-txn failure
			}
			results[i] = *et
			return nil
		})
	}

	_ = g.Wait() // errors are already logged per-txn

	// Filter out zero-value entries (failed enrichments)
	enriched := make([]entities.EnrichedTransaction, 0, len(unenriched))
	for _, r := range results {
		if r.ID != uuid.Nil {
			enriched = append(enriched, r)
		}
	}
	return enriched, nil
}

func (e *TransactionEnricher) llmEnrich(ctx context.Context, txnID, userID uuid.UUID, rawDesc string, amount decimal.Decimal, txnDate time.Time, currency string) (*entities.EnrichedTransaction, error) {
	if currency == "" {
		currency = defaultCurrency
	}

	if e.llm == nil {
		return &entities.EnrichedTransaction{
			ID: uuid.New(), TransactionID: txnID, UserID: userID,
			RawDescription: rawDesc, Amount: amount, Currency: currency,
			TransactionDate: txnDate, Direction: direction(amount),
			Counterparty: rawDesc, CategoryL1: "Uncategorized", CategoryL2: "Other",
			ClassificationLayer: entities.ClassificationLayerRule,
			Confidence: decimal.NewFromFloat(0.1), CreatedAt: time.Now().UTC(),
		}, nil
	}

	prompt := fmt.Sprintf(`Classify this bank transaction and describe it in plain English. Return JSON only.
Transaction: "%s", Amount: %s %s, Date: %s

Return: {"counterparty":"<resolved name>","category_l1":"<top category>","category_l2":"<sub category>","is_essential":<bool>,"plain_description":"<1 sentence: what this transaction was, in plain English>","merchant_context":"<short note: what this merchant/service is, e.g. ride-hailing, supermarket, streaming service>"}

Categories L1: Housing, Food & Drink, Transport, Shopping, Entertainment, Health, Financial, Income, Insurance, Education, Travel, Gifts, Debt Payments, Uncategorized
is_essential = true for necessities (housing, groceries, medical, utilities, insurance).
plain_description should read naturally like a sentence: "Uber ride to Victoria Island", "Netflix monthly subscription", "Groceries at Shoprite".`,
		rawDesc, amount.StringFixed(2), currency, txnDate.Format("2006-01-02"))

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
			RawDescription: rawDesc, Amount: amount, Currency: currency,
			TransactionDate: txnDate, Direction: direction(amount),
			Counterparty: rawDesc, CategoryL1: "Uncategorized", CategoryL2: "Other",
			ClassificationLayer: entities.ClassificationLayerRule,
			Confidence: decimal.NewFromFloat(0.1), CreatedAt: time.Now().UTC(),
		}, nil
	}

	var parsed struct {
		Counterparty     string `json:"counterparty"`
		CategoryL1       string `json:"category_l1"`
		CategoryL2       string `json:"category_l2"`
		IsEssential      bool   `json:"is_essential"`
		PlainDescription string `json:"plain_description"`
		MerchantContext  string `json:"merchant_context"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		e.logger.Warn("LLM response parse failed", zap.String("content", resp.Content), zap.Error(err))
		parsed.Counterparty = rawDesc
		parsed.CategoryL1 = "Uncategorized"
		parsed.CategoryL2 = "Other"
	}

	plainDesc := parsed.PlainDescription
	if plainDesc == "" {
		plainDesc = parsed.Counterparty
	}

	et := &entities.EnrichedTransaction{
		ID:                  uuid.New(),
		TransactionID:       txnID,
		UserID:              userID,
		RawDescription:      rawDesc,
		Amount:              amount,
		Currency:            currency,
		TransactionDate:     txnDate,
		Direction:           direction(amount),
		Counterparty:        parsed.Counterparty,
		CategoryL1:          parsed.CategoryL1,
		CategoryL2:          parsed.CategoryL2,
		IsEssential:         parsed.IsEssential,
		PlainDescription:    plainDesc,
		MerchantContext:     parsed.MerchantContext,
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
