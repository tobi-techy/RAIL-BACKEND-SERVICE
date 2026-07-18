package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

const enrichedColumns = `id, transaction_id, user_id, raw_description, amount, currency,
       transaction_date, direction, counterparty, category_l1, category_l2,
       is_essential, is_recurring, plain_description, merchant_context,
       bank, tx_type,
       behavior_tags, facts, embedding,
       classification_layer, confidence, created_at`

// EnrichmentRepository persists enriched transaction data.
type EnrichmentRepository struct {
	db     *sqlx.DB
	logger *zap.Logger
}

func NewEnrichmentRepository(db *sqlx.DB) *EnrichmentRepository {
	return &EnrichmentRepository{db: db, logger: zap.NewNop()}
}

func NewEnrichmentRepositoryWithLogger(db *sqlx.DB, logger *zap.Logger) *EnrichmentRepository {
	return &EnrichmentRepository{db: db, logger: logger}
}

func (r *EnrichmentRepository) UpsertEnrichedTransaction(ctx context.Context, et *entities.EnrichedTransaction) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_enriched_transactions
			(id, transaction_id, user_id, raw_description, amount, currency,
			 transaction_date, direction, counterparty, category_l1, category_l2,
			 is_essential, is_recurring, plain_description, merchant_context,
			 bank, tx_type,
			 behavior_tags, facts, embedding,
			 classification_layer, confidence, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (transaction_id) DO UPDATE SET
			counterparty = EXCLUDED.counterparty,
			category_l1 = EXCLUDED.category_l1,
			category_l2 = EXCLUDED.category_l2,
			is_essential = EXCLUDED.is_essential,
			is_recurring = EXCLUDED.is_recurring,
			plain_description = EXCLUDED.plain_description,
			merchant_context = EXCLUDED.merchant_context,
			bank = EXCLUDED.bank,
			tx_type = EXCLUDED.tx_type,
			behavior_tags = EXCLUDED.behavior_tags,
			facts = EXCLUDED.facts,
			embedding = EXCLUDED.embedding,
			classification_layer = EXCLUDED.classification_layer,
			confidence = EXCLUDED.confidence`,
		et.ID, et.TransactionID, et.UserID, et.RawDescription, et.Amount, et.Currency,
		et.TransactionDate, et.Direction, et.Counterparty, et.CategoryL1, et.CategoryL2,
		et.IsEssential, et.IsRecurring, et.PlainDescription, et.MerchantContext,
		et.Bank, et.TxType,
		et.BehaviorTags, et.Facts, pq.Array(et.Embedding),
		et.ClassificationLayer, et.Confidence, et.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert enriched transaction: %w", err)
	}
	return nil
}

func (r *EnrichmentRepository) GetEnrichedByTransactionID(ctx context.Context, txnID uuid.UUID) (*entities.EnrichedTransaction, error) {
	var et entities.EnrichedTransaction
	err := r.db.GetContext(ctx, &et, `
		SELECT `+enrichedColumns+`
		FROM miriam_enriched_transactions
		WHERE transaction_id = $1`, txnID)
	if err != nil {
		return nil, fmt.Errorf("get enriched by transaction id: %w", err)
	}
	return &et, nil
}

func (r *EnrichmentRepository) GetEnrichedByTransactionIDs(ctx context.Context, txnIDs []uuid.UUID) (map[uuid.UUID]*entities.EnrichedTransaction, error) {
	if len(txnIDs) == 0 {
		return map[uuid.UUID]*entities.EnrichedTransaction{}, nil
	}

	query := `SELECT ` + enrichedColumns + `
		FROM miriam_enriched_transactions
		WHERE transaction_id = ANY($1)`

	var rows []entities.EnrichedTransaction
	if err := r.db.SelectContext(ctx, &rows, query, pq.Array(txnIDs)); err != nil {
		return nil, fmt.Errorf("get enriched by transaction ids: %w", err)
	}

	result := make(map[uuid.UUID]*entities.EnrichedTransaction, len(rows))
	for i := range rows {
		result[rows[i].TransactionID] = &rows[i]
	}
	return result, nil
}

func (r *EnrichmentRepository) GetEnrichedByUser(ctx context.Context, userID uuid.UUID, limit int) ([]entities.EnrichedTransaction, error) {
	if limit <= 0 {
		limit = 50
	}
	var results []entities.EnrichedTransaction
	err := r.db.SelectContext(ctx, &results, `
		SELECT `+enrichedColumns+`
		FROM miriam_enriched_transactions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get enriched by user: %w", err)
	}
	return results, nil
}

// GetEnrichedByUserAndTransactionIDs returns enriched data for specific transactions of a user.
func (r *EnrichmentRepository) GetEnrichedByUserAndTransactionIDs(ctx context.Context, userID uuid.UUID, txnIDs []uuid.UUID) (map[uuid.UUID]*entities.EnrichedTransaction, error) {
	if len(txnIDs) == 0 {
		return map[uuid.UUID]*entities.EnrichedTransaction{}, nil
	}

	query := `SELECT ` + enrichedColumns + `
		FROM miriam_enriched_transactions
		WHERE user_id = $1 AND transaction_id = ANY($2)`

	var rows []entities.EnrichedTransaction
	if err := r.db.SelectContext(ctx, &rows, query, userID, pq.Array(txnIDs)); err != nil {
		return nil, fmt.Errorf("get enriched by user and transaction ids: %w", err)
	}

	result := make(map[uuid.UUID]*entities.EnrichedTransaction, len(rows))
	for i := range rows {
		result[rows[i].TransactionID] = &rows[i]
	}
	return result, nil
}

// BulkUpsertEnrichedTransactions upserts multiple enriched transactions in one query.
func (r *EnrichmentRepository) BulkUpsertEnrichedTransactions(ctx context.Context, enrichments []*entities.EnrichedTransaction) error {
	if len(enrichments) == 0 {
		return nil
	}

	query := `
		INSERT INTO miriam_enriched_transactions
			(id, transaction_id, user_id, raw_description, amount, currency,
			 transaction_date, direction, counterparty, category_l1, category_l2,
			 is_essential, is_recurring, plain_description, merchant_context,
			 bank, tx_type,
			 behavior_tags, facts, embedding,
			 classification_layer, confidence, created_at)
		VALUES `
	const colsPerRow = 23
	valueStrings := make([]string, 0, len(enrichments))
	valueArgs := make([]interface{}, 0, len(enrichments)*colsPerRow)
	for i, et := range enrichments {
		offset := i * colsPerRow
		placeholders := make([]string, colsPerRow)
		for j := 0; j < colsPerRow; j++ {
			placeholders[j] = fmt.Sprintf("$%d", offset+j+1)
		}
		valueStrings = append(valueStrings, "("+strings.Join(placeholders, ",")+")")
		valueArgs = append(valueArgs,
			et.ID, et.TransactionID, et.UserID, et.RawDescription, et.Amount, et.Currency,
			et.TransactionDate, et.Direction, et.Counterparty, et.CategoryL1, et.CategoryL2,
			et.IsEssential, et.IsRecurring, et.PlainDescription, et.MerchantContext,
			et.Bank, et.TxType,
			et.BehaviorTags, et.Facts, pq.Array(et.Embedding),
			et.ClassificationLayer, et.Confidence, et.CreatedAt)
	}

	query += strings.Join(valueStrings, ",") + `
		ON CONFLICT (transaction_id) DO UPDATE SET
			counterparty = EXCLUDED.counterparty,
			category_l1 = EXCLUDED.category_l1,
			category_l2 = EXCLUDED.category_l2,
			is_essential = EXCLUDED.is_essential,
			is_recurring = EXCLUDED.is_recurring,
			plain_description = EXCLUDED.plain_description,
			merchant_context = EXCLUDED.merchant_context,
			bank = EXCLUDED.bank,
			tx_type = EXCLUDED.tx_type,
			behavior_tags = EXCLUDED.behavior_tags,
			facts = EXCLUDED.facts,
			embedding = EXCLUDED.embedding,
			classification_layer = EXCLUDED.classification_layer,
			confidence = EXCLUDED.confidence`

	_, err := r.db.ExecContext(ctx, query, valueArgs...)
	if err != nil {
		return fmt.Errorf("bulk upsert enriched transactions: %w", err)
	}
	return nil
}

// GetUserEnrichmentSummary returns a compact summary of recent enrichment for context assembly.
func (r *EnrichmentRepository) GetUserEnrichmentSummary(ctx context.Context, userID uuid.UUID) (string, error) {
	var summary struct {
		TotalEnriched  int     `db:"total_enriched"`
		TopMerchants   string  `db:"top_merchants"`
		EssentialRatio float64 `db:"essential_ratio"`
		RecentTags     string  `db:"recent_tags"`
		CategoryBreak  string  `db:"category_break"`
		BankInfo       string  `db:"bank_info"`
	}

	err := r.db.GetContext(ctx, &summary, `
		SELECT
			COUNT(*) as total_enriched,
			COALESCE(
				(SELECT string_agg(sub.counterparty, ', ' ORDER BY sub.counterparty)
				 FROM (SELECT DISTINCT counterparty FROM miriam_enriched_transactions WHERE user_id = $1 LIMIT 10) sub),
				''
			) as top_merchants,
			CASE WHEN COUNT(*) > 0 THEN
				ROUND(COUNT(*) FILTER (WHERE is_essential)::numeric / COUNT(*)::numeric, 2)
			ELSE 0 END as essential_ratio,
			COALESCE(
				(SELECT string_agg(DISTINCT elem->>'tag', ', ')
				 FROM miriam_enriched_transactions,
				 jsonb_array_elements(behavior_tags) elem
				 WHERE user_id = $1 AND behavior_tags != '[]'::jsonb
				 LIMIT 5),
				''
			) as recent_tags,
			COALESCE(
				(SELECT string_agg(sub.cat, ', ' ORDER BY sub.cat)
				 FROM (SELECT category_l1 || ': ' || COUNT(*)::text as cat
				       FROM miriam_enriched_transactions
				       WHERE user_id = $1 AND created_at > NOW() - INTERVAL '30 days'
				       GROUP BY category_l1 ORDER BY COUNT(*) DESC LIMIT 5) sub),
				''
			) as category_break,
			COALESCE(
				(SELECT string_agg(bank, ', ') FROM (
					SELECT DISTINCT bank
					FROM miriam_enriched_transactions
					WHERE user_id = $1 AND bank != '' AND created_at > NOW() - INTERVAL '30 days'
					LIMIT 5
				) sub),
				''
			) as bank_info
		FROM miriam_enriched_transactions
		WHERE user_id = $1
		  AND created_at > NOW() - INTERVAL '30 days'`, userID)
	if err != nil {
		return "", fmt.Errorf("get enrichment summary: %w", err)
	}

	if summary.TotalEnriched == 0 {
		return "", nil
	}

	text := fmt.Sprintf("[ENRICHMENT SUMMARY — %d transactions enriched in last 30 days. Essential spending: %.0f%%",
		summary.TotalEnriched, summary.EssentialRatio*100)
	if summary.CategoryBreak != "" {
		text += ". Categories: " + summary.CategoryBreak
	}
	if summary.TopMerchants != "" {
		text += ". Top merchants: " + summary.TopMerchants
	}
	if summary.BankInfo != "" {
		text += ". Banks: " + summary.BankInfo
	}
	if summary.RecentTags != "" {
		text += ". Detected patterns: " + summary.RecentTags
	}
	text += "]"
	return text, nil
}

// GetRecentFactsByUser returns recent enrichment facts for bridging to miriam_user_facts.
func (r *EnrichmentRepository) GetRecentFactsByUser(ctx context.Context, userID uuid.UUID, limit int) ([]entities.TransactionFact, error) {
	if limit <= 0 {
		limit = 50
	}

	var rawFacts []json.RawMessage
	err := r.db.SelectContext(ctx, &rawFacts, `
		SELECT DISTINCT elem
		FROM miriam_enriched_transactions,
		     jsonb_array_elements(facts) elem
		WHERE user_id = $1
		  AND facts != '[]'::jsonb
		  AND created_at > NOW() - INTERVAL '7 days'
		ORDER BY (elem->>'confidence')::numeric DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent facts by user: %w", err)
	}

	facts := make([]entities.TransactionFact, 0, len(rawFacts))
	for _, raw := range rawFacts {
		var f entities.TransactionFact
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// BridgeFactsToMemory upserts enrichment-extracted facts into miriam_user_facts
// so they're available to Miriam's memory ranker without tool calls.
// source is always 'transaction_pattern'. Returns the number of new facts inserted.
func (r *EnrichmentRepository) BridgeFactsToMemory(ctx context.Context, userID uuid.UUID, facts []entities.TransactionFact) (int, error) {
	if len(facts) == 0 {
		return 0, nil
	}

	inserted := 0
	for _, f := range facts {
		if f.Value == "" {
			continue
		}

		category := mapFactCategory(f.Type, f.Category)

		// Skip if an active fact with the same text already exists for this user
		var exists bool
		err := r.db.GetContext(ctx, &exists, `
			SELECT EXISTS(
				SELECT 1 FROM miriam_user_facts
				WHERE user_id = $1 AND fact = $2 AND superseded_by IS NULL
			)`, userID, f.Value)
		if err != nil {
			r.logger.Warn("fact existence check failed", zap.String("fact", f.Value), zap.Error(err))
			continue
		}
		if exists {
			continue
		}

		confidence := f.Confidence
		if confidence > 1.0 {
			confidence = 1.0
		}
		importance := int16(5)
		if confidence > 0.8 {
			importance = 7
		} else if confidence < 0.3 {
			importance = 3
		}

		_, err = r.db.ExecContext(ctx, `
			INSERT INTO miriam_user_facts
				(user_id, category, fact, source, confidence, importance)
			VALUES ($1, $2, $3, 'transaction_pattern', $4, $5)`,
			userID, category, f.Value, confidence, importance)
		if err != nil {
			r.logger.Warn("fact insert failed", zap.String("fact", f.Value), zap.Error(err))
			continue
		}
		inserted++
	}
	return inserted, nil
}

// mapFactCategory maps enrichment fact types to miriam_user_facts category values.
func mapFactCategory(factType, factCategory string) string {
	// Direct mapping from enrichment category if it matches an allowed value
	switch factCategory {
	case "income_pattern", "deposit_cadence", "salary_day", "freelance_pattern",
		"family_support", "currency_context", "risk_preference", "stash_behavior",
		"financial_behavior", "habit":
		return factCategory
	}

	// Map by fact type
	switch factType {
	case "recurring_expense", "subscription", "recurring_income":
		return "financial_behavior"
	case "spending_pattern", "merchant_pattern":
		return "habit"
	case "income_source", "salary":
		return "income_pattern"
	case "transfer_pattern", "family_support":
		return "family_support"
	case "risk_indicator":
		return "risk_preference"
	default:
		return "financial_behavior"
	}
}
