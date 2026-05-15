package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
)

// OpportunitySource represents a platform we ingest from.
type OpportunitySource struct {
	ID           uuid.UUID  `db:"id" json:"id"`
	Name         string     `db:"name" json:"name"`
	BaseURL      string     `db:"base_url" json:"base_url"`
	APIKeyRef    *string    `db:"api_key_ref" json:"-"`
	Enabled      bool       `db:"enabled" json:"enabled"`
	LastSyncedAt *time.Time `db:"last_synced_at" json:"last_synced_at"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
}

// OpportunityListing is a normalized bounty/project/hackathon from any source.
type OpportunityListing struct {
	ID             uuid.UUID       `db:"id" json:"id"`
	SourceID       uuid.UUID       `db:"source_id" json:"source_id"`
	ExternalID     string          `db:"external_id" json:"external_id"`
	Slug           *string         `db:"slug" json:"slug,omitempty"`
	Title          string          `db:"title" json:"title"`
	Description    *string         `db:"description" json:"description,omitempty"`
	Type           string          `db:"type" json:"type"` // bounty, project, hackathon
	Skills         pq.StringArray  `db:"skills" json:"skills"`
	RewardAmount   decimal.Decimal `db:"reward_amount" json:"reward_amount"`
	RewardCurrency string          `db:"reward_currency" json:"reward_currency"`
	Deadline       *time.Time      `db:"deadline" json:"deadline,omitempty"`
	Sponsor        *string         `db:"sponsor" json:"sponsor,omitempty"`
	URL            string          `db:"url" json:"url"`
	Remote         bool            `db:"remote" json:"remote"`
	Status         string          `db:"status" json:"status"`
	AgentAccess    *string         `db:"agent_access" json:"agent_access,omitempty"`
	RawJSON        []byte          `db:"raw_json" json:"-"`
	CreatedAt      time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at" json:"updated_at"`
}

// UserOpportunityProfile stores a user's skills and preferences for matching.
type UserOpportunityProfile struct {
	UserID            uuid.UUID       `db:"user_id" json:"user_id"`
	Skills            pq.StringArray  `db:"skills" json:"skills"`
	Interests         pq.StringArray  `db:"interests" json:"interests"`
	PreferredTypes    pq.StringArray  `db:"preferred_types" json:"preferred_types"`
	HoursPerWeek      int             `db:"hours_per_week" json:"hours_per_week"`
	MinReward         decimal.Decimal `db:"min_reward" json:"min_reward"`
	PreferredCurrency string          `db:"preferred_currency" json:"preferred_currency"`
	Bio               *string         `db:"bio" json:"bio,omitempty"`
	PortfolioLinks    pq.StringArray  `db:"portfolio_links" json:"portfolio_links"`
	UpdatedAt         time.Time       `db:"updated_at" json:"updated_at"`
}

// OpportunityMatch is a scored recommendation for a user.
type OpportunityMatch struct {
	ID          uuid.UUID       `db:"id" json:"id"`
	UserID      uuid.UUID       `db:"user_id" json:"user_id"`
	ListingID   uuid.UUID       `db:"listing_id" json:"listing_id"`
	FitScore    decimal.Decimal `db:"fit_score" json:"fit_score"`
	WeekStart   time.Time       `db:"week_start" json:"week_start"`
	Rank        *int            `db:"rank" json:"rank,omitempty"`
	Explanation *string         `db:"explanation" json:"explanation,omitempty"`
	Status      string          `db:"status" json:"status"`
	CreatedAt   time.Time       `db:"created_at" json:"created_at"`

	// Joined fields (not stored in opportunity_matches table)
	Listing *OpportunityListing `db:"-" json:"listing,omitempty"`
}

// OpportunityFeedback records user actions on opportunities.
type OpportunityFeedback struct {
	ID        uuid.UUID `db:"id" json:"id"`
	UserID    uuid.UUID `db:"user_id" json:"user_id"`
	ListingID uuid.UUID `db:"listing_id" json:"listing_id"`
	Action    string    `db:"action" json:"action"` // saved, hidden, applied, not_interested, won, lost
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// Opportunity listing types
const (
	OpportunityTypeBounty    = "bounty"
	OpportunityTypeProject   = "project"
	OpportunityTypeHackathon = "hackathon"
)

// Opportunity match statuses
const (
	OpportunityStatusRecommended = "recommended"
	OpportunityStatusSaved       = "saved"
	OpportunityStatusHidden      = "hidden"
	OpportunityStatusApplied     = "applied"
)

// Feedback actions
const (
	OpportunityFeedbackSaved         = "saved"
	OpportunityFeedbackHidden        = "hidden"
	OpportunityFeedbackApplied       = "applied"
	OpportunityFeedbackNotInterested = "not_interested"
	OpportunityFeedbackWon           = "won"
	OpportunityFeedbackLost          = "lost"
)
