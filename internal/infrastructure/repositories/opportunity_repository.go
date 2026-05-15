package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type OpportunityRepository struct {
	db *sqlx.DB
}

func NewOpportunityRepository(db *sqlx.DB) *OpportunityRepository {
	return &OpportunityRepository{db: db}
}

// --- Sources ---

func (r *OpportunityRepository) GetSourceByName(ctx context.Context, name string) (*entities.OpportunitySource, error) {
	var src entities.OpportunitySource
	err := r.db.GetContext(ctx, &src, `SELECT * FROM opportunity_sources WHERE name = $1`, name)
	if err != nil {
		return nil, fmt.Errorf("get source by name: %w", err)
	}
	return &src, nil
}

func (r *OpportunityRepository) UpdateSourceLastSynced(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE opportunity_sources SET last_synced_at = NOW() WHERE id = $1`, id)
	return err
}

// --- Listings ---

func (r *OpportunityRepository) UpsertListing(ctx context.Context, l *entities.OpportunityListing) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO opportunity_listings (id, source_id, external_id, slug, title, description, type, skills, reward_amount, reward_currency, deadline, sponsor, url, remote, status, agent_access, raw_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (source_id, external_id) DO UPDATE SET
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			skills = EXCLUDED.skills,
			reward_amount = EXCLUDED.reward_amount,
			deadline = EXCLUDED.deadline,
			sponsor = EXCLUDED.sponsor,
			status = EXCLUDED.status,
			agent_access = EXCLUDED.agent_access,
			raw_json = EXCLUDED.raw_json,
			updated_at = NOW()`,
		l.ID, l.SourceID, l.ExternalID, l.Slug, l.Title, l.Description, l.Type,
		l.Skills, l.RewardAmount, l.RewardCurrency, l.Deadline, l.Sponsor,
		l.URL, l.Remote, l.Status, l.AgentAccess, l.RawJSON, l.CreatedAt, l.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert listing: %w", err)
	}
	return nil
}

func (r *OpportunityRepository) GetOpenListings(ctx context.Context, limit int) ([]*entities.OpportunityListing, error) {
	if limit <= 0 {
		limit = 100
	}
	var listings []*entities.OpportunityListing
	err := r.db.SelectContext(ctx, &listings, `
		SELECT * FROM opportunity_listings
		WHERE status = 'open' AND (deadline IS NULL OR deadline > NOW())
		ORDER BY reward_amount DESC NULLS LAST, created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("get open listings: %w", err)
	}
	return listings, nil
}

func (r *OpportunityRepository) GetListingByID(ctx context.Context, id uuid.UUID) (*entities.OpportunityListing, error) {
	var l entities.OpportunityListing
	err := r.db.GetContext(ctx, &l, `SELECT * FROM opportunity_listings WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("get listing: %w", err)
	}
	return &l, nil
}

func (r *OpportunityRepository) ExpireOldListings(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE opportunity_listings SET status = 'expired', updated_at = NOW()
		WHERE status = 'open' AND deadline IS NOT NULL AND deadline < NOW()`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// --- User Profiles ---

func (r *OpportunityRepository) GetUserProfile(ctx context.Context, userID uuid.UUID) (*entities.UserOpportunityProfile, error) {
	var p entities.UserOpportunityProfile
	err := r.db.GetContext(ctx, &p, `SELECT * FROM user_opportunity_profiles WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("get user profile: %w", err)
	}
	return &p, nil
}

func (r *OpportunityRepository) UpsertUserProfile(ctx context.Context, p *entities.UserOpportunityProfile) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_opportunity_profiles (user_id, skills, interests, preferred_types, hours_per_week, min_reward, preferred_currency, bio, portfolio_links, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			skills = EXCLUDED.skills,
			interests = EXCLUDED.interests,
			preferred_types = EXCLUDED.preferred_types,
			hours_per_week = EXCLUDED.hours_per_week,
			min_reward = EXCLUDED.min_reward,
			preferred_currency = EXCLUDED.preferred_currency,
			bio = EXCLUDED.bio,
			portfolio_links = EXCLUDED.portfolio_links,
			updated_at = NOW()`,
		p.UserID, p.Skills, p.Interests, p.PreferredTypes, p.HoursPerWeek,
		p.MinReward, p.PreferredCurrency, p.Bio, p.PortfolioLinks)
	if err != nil {
		return fmt.Errorf("upsert user profile: %w", err)
	}
	return nil
}

// --- Matches ---

func (r *OpportunityRepository) UpsertMatch(ctx context.Context, m *entities.OpportunityMatch) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO opportunity_matches (id, user_id, listing_id, fit_score, week_start, rank, explanation, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, listing_id, week_start) DO UPDATE SET
			fit_score = EXCLUDED.fit_score,
			rank = EXCLUDED.rank,
			explanation = EXCLUDED.explanation,
			status = EXCLUDED.status`,
		m.ID, m.UserID, m.ListingID, m.FitScore, m.WeekStart, m.Rank, m.Explanation, m.Status, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert match: %w", err)
	}
	return nil
}

func (r *OpportunityRepository) GetWeeklyRecommendations(ctx context.Context, userID uuid.UUID, weekStart time.Time) ([]*entities.OpportunityMatch, error) {
	var matches []*entities.OpportunityMatch
	err := r.db.SelectContext(ctx, &matches, `
		SELECT m.* FROM opportunity_matches m
		WHERE m.user_id = $1 AND m.week_start = $2 AND m.rank IS NOT NULL AND m.status != 'hidden'
		ORDER BY m.rank ASC
		LIMIT 3`, userID, weekStart)
	if err != nil {
		return nil, fmt.Errorf("get weekly recommendations: %w", err)
	}

	// Hydrate listings
	for _, m := range matches {
		l, err := r.GetListingByID(ctx, m.ListingID)
		if err == nil {
			m.Listing = l
		}
	}
	return matches, nil
}

func (r *OpportunityRepository) UpdateMatchStatus(ctx context.Context, userID, listingID uuid.UUID, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE opportunity_matches SET status = $3
		WHERE user_id = $1 AND listing_id = $2`,
		userID, listingID, status)
	return err
}

// --- Feedback ---

func (r *OpportunityRepository) CreateFeedback(ctx context.Context, f *entities.OpportunityFeedback) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO opportunity_feedback (id, user_id, listing_id, action, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		f.ID, f.UserID, f.ListingID, f.Action, f.CreatedAt)
	if err != nil {
		return fmt.Errorf("create feedback: %w", err)
	}
	return nil
}

func (r *OpportunityRepository) GetUserFeedback(ctx context.Context, userID uuid.UUID) ([]*entities.OpportunityFeedback, error) {
	var feedback []*entities.OpportunityFeedback
	err := r.db.SelectContext(ctx, &feedback, `
		SELECT * FROM opportunity_feedback WHERE user_id = $1 ORDER BY created_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, fmt.Errorf("get user feedback: %w", err)
	}
	return feedback, nil
}

func (r *OpportunityRepository) GetHiddenListingIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.SelectContext(ctx, &ids, `
		SELECT DISTINCT listing_id FROM opportunity_feedback
		WHERE user_id = $1 AND action IN ('hidden', 'not_interested')`, userID)
	if err != nil {
		return nil, fmt.Errorf("get hidden listings: %w", err)
	}
	return ids, nil
}
