package memory

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Metrics provides memory health observability for a single user or globally.
type Metrics struct {
	db *sqlx.DB
}

// NewMetrics creates a new memory metrics module.
func NewMetrics(db *sqlx.DB) *Metrics {
	return &Metrics{db: db}
}

// UserMemoryHealth is the health snapshot for a single user's memory.
type UserMemoryHealth struct {
	UserID         uuid.UUID `json:"user_id" db:"user_id"`
	TotalFacts     int       `json:"total_facts" db:"total_facts"`
	AvgImportance  float64   `json:"avg_importance" db:"avg_importance"`
	AvgConfidence  float64   `json:"avg_confidence" db:"avg_confidence"`
	StaleFactCount int       `json:"stale_facts" db:"stale_facts"`
	LastConfirmed  time.Time `json:"last_confirmed" db:"last_confirmed"`
	HasSummary     bool      `json:"has_summary" db:"has_summary"`
}

// GetUserMemoryHealth returns the memory health snapshot for a user.
func (m *Metrics) GetUserMemoryHealth(ctx context.Context, userID uuid.UUID) (*UserMemoryHealth, error) {
	var health UserMemoryHealth
	err := m.db.GetContext(ctx, &health, `
		SELECT
			user_id,
			COUNT(*) AS total_facts,
			COALESCE(AVG(importance), 0) AS avg_importance,
			COALESCE(AVG(confidence), 0) AS avg_confidence,
			COUNT(*) FILTER (WHERE last_confirmed_at < $2) AS stale_facts,
			COALESCE(MAX(last_confirmed_at), created_at) AS last_confirmed
		FROM miriam_user_facts
		WHERE user_id = $1 AND superseded_by IS NULL
		GROUP BY user_id`, userID, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil {
		return nil, err
	}
	health.UserID = userID

	var summaryExists bool
	_ = m.db.GetContext(ctx, &summaryExists, `
		SELECT EXISTS(SELECT 1 FROM miriam_memory_summaries WHERE user_id = $1)`, userID)
	health.HasSummary = summaryExists

	return &health, nil
}

// GlobalMemoryStats is aggregate memory health across all users.
type GlobalMemoryStats struct {
	TotalUsers       int     `json:"total_users" db:"total_users"`
	TotalFacts       int     `json:"total_facts" db:"total_facts"`
	AvgFactsPerUser  float64 `json:"avg_facts_per_user" db:"avg_facts_per_user"`
	AvgImportance    float64 `json:"avg_importance" db:"avg_importance"`
	AvgConfidence    float64 `json:"avg_confidence" db:"avg_confidence"`
	StaleFactsTotal  int     `json:"stale_facts_total" db:"stale_facts_total"`
	UsersWithSummary int     `json:"users_with_summary" db:"users_with_summary"`
}

// GetGlobalStats returns aggregate memory health across all users.
func (m *Metrics) GetGlobalStats(ctx context.Context) (*GlobalMemoryStats, error) {
	var stats GlobalMemoryStats
	err := m.db.GetContext(ctx, &stats, `
		SELECT
			COUNT(DISTINCT user_id) AS total_users,
			COUNT(*) AS total_facts,
			COALESCE(AVG(fact_count), 0) AS avg_facts_per_user,
			COALESCE(AVG(importance), 0) AS avg_importance,
			COALESCE(AVG(confidence), 0) AS avg_confidence,
			COUNT(*) FILTER (WHERE last_confirmed_at < $1) AS stale_facts_total
		FROM miriam_user_facts
		WHERE superseded_by IS NULL`, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil {
		return nil, err
	}

	_ = m.db.GetContext(ctx, &stats.UsersWithSummary, `
		SELECT COUNT(*) FROM miriam_memory_summaries`)

	// Compute avg_facts_per_user from the totals.
	if stats.TotalUsers > 0 {
		stats.AvgFactsPerUser = float64(stats.TotalFacts) / float64(stats.TotalUsers)
	}

	return &stats, nil
}

// GetUsersNeedingCompression returns user IDs with more than the given fact threshold
// who haven't been summarized recently. Used by the compression worker.
func (m *Metrics) GetUsersNeedingCompression(ctx context.Context, threshold int, since time.Duration) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := m.db.SelectContext(ctx, &ids, `
		SELECT f.user_id
		FROM miriam_user_facts f
		LEFT JOIN miriam_memory_summaries s ON s.user_id = f.user_id
		WHERE f.superseded_by IS NULL
		GROUP BY f.user_id
		HAVING COUNT(*) >= $1
		   AND (s.last_summarized_at IS NULL OR s.last_summarized_at < $2)
		ORDER BY COUNT(*) DESC
		LIMIT 100`, threshold, time.Now().UTC().Add(-since))
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// CountActiveFacts returns the number of active facts for a user.
func (m *Metrics) CountActiveFacts(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := m.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM miriam_user_facts
		WHERE user_id = $1 AND superseded_by IS NULL`, userID)
	return count, err
}

// GetFactDistribution returns the count of facts per category for a user.
func (m *Metrics) GetFactDistribution(ctx context.Context, userID uuid.UUID) (map[string]int, error) {
	type row struct {
		Category string `db:"category"`
		Count    int    `db:"count"`
	}
	var rows []row
	err := m.db.SelectContext(ctx, &rows, `
		SELECT category, COUNT(*) AS count
		FROM miriam_user_facts
		WHERE user_id = $1 AND superseded_by IS NULL
		GROUP BY category
		ORDER BY count DESC`, userID)
	if err != nil {
		return nil, err
	}
	dist := make(map[string]int, len(rows))
	for _, r := range rows {
		dist[r.Category] = r.Count
	}
	return dist, nil
}
