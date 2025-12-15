package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ContributionType represents the type of user contribution
type ContributionType string

const (
	ContributionTypeDeposit  ContributionType = "deposit"
	ContributionTypeRoundup  ContributionType = "roundup"
	ContributionTypeCashback ContributionType = "cashback"
	ContributionTypeReferral ContributionType = "referral"
)

// UserContribution represents a user's financial contribution
type UserContribution struct {
	ID        uuid.UUID       `json:"id"`
	UserID    uuid.UUID       `json:"user_id"`
	Type      ContributionType `json:"type"`
	Amount    decimal.Decimal `json:"amount"`
	Source    string          `json:"source,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// InvestmentStreak represents a user's investment streak
type InvestmentStreak struct {
	UserID             uuid.UUID `json:"user_id"`
	CurrentStreak      int       `json:"current_streak"`
	LongestStreak      int       `json:"longest_streak"`
	LastInvestmentDate *time.Time `json:"last_investment_date,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// UserNews represents a personalized news article for a user
type UserNews struct {
	ID              uuid.UUID       `json:"id"`
	UserID          uuid.UUID       `json:"user_id"`
	Source          string          `json:"source"`
	Title           string          `json:"title"`
	Summary         string          `json:"summary,omitempty"`
	URL             string          `json:"url"`
	RelatedSymbols  []string        `json:"related_symbols"`
	PublishedAt     time.Time       `json:"published_at"`
	IsRead          bool            `json:"is_read"`
	RelevanceScore  decimal.Decimal `json:"relevance_score"`
	CreatedAt       time.Time       `json:"created_at"`
}

// AIChatSession represents an AI chat session
type AIChatSession struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	StartedAt     time.Time `json:"started_at"`
	LastMessageAt time.Time `json:"last_message_at"`
}

// ChatRole represents the role of a chat message sender
type ChatRole string

const (
	ChatRoleUser      ChatRole = "user"
	ChatRoleAssistant ChatRole = "assistant"
	ChatRoleSystem    ChatRole = "system"
)

// AIChatMessage represents a message in an AI chat session
type AIChatMessage struct {
	ID        uuid.UUID              `json:"id"`
	SessionID uuid.UUID              `json:"session_id"`
	Role      ChatRole               `json:"role"`
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// BasketRecommendation represents an AI-generated basket recommendation
type BasketRecommendation struct {
	ID                   uuid.UUID       `json:"id"`
	UserID               uuid.UUID       `json:"user_id"`
	RecommendedBasketID  uuid.UUID       `json:"recommended_basket_id"`
	Reason               string          `json:"reason"`
	ExpectedReturn       decimal.Decimal `json:"expected_return,omitempty"`
	RiskChange           string          `json:"risk_change,omitempty"`
	ConfidenceScore      decimal.Decimal `json:"confidence_score"`
	IsApplied            bool            `json:"is_applied"`
	CreatedAt            time.Time       `json:"created_at"`
	ExpiresAt            time.Time       `json:"expires_at"`
}

// RebalancePreviewStatus represents the status of a rebalance preview
type RebalancePreviewStatus string

const (
	RebalancePreviewStatusPending   RebalancePreviewStatus = "pending"
	RebalancePreviewStatusExecuted  RebalancePreviewStatus = "executed"
	RebalancePreviewStatusExpired   RebalancePreviewStatus = "expired"
	RebalancePreviewStatusCancelled RebalancePreviewStatus = "cancelled"
)

// RebalancePreview represents a preview of portfolio rebalancing
type RebalancePreview struct {
	ID                uuid.UUID              `json:"id"`
	UserID            uuid.UUID              `json:"user_id"`
	TargetAllocation  map[string]interface{} `json:"target_allocation"`
	TradesPreview     []TradePreview         `json:"trades_preview"`
	ExpectedFees      decimal.Decimal        `json:"expected_fees"`
	ExpectedTaxImpact decimal.Decimal        `json:"expected_tax_impact"`
	Status            RebalancePreviewStatus `json:"status"`
	CreatedAt         time.Time              `json:"created_at"`
	ExpiresAt         time.Time              `json:"expires_at"`
}

// TradePreview represents a single trade in a rebalance preview
type TradePreview struct {
	Symbol   string          `json:"symbol"`
	Action   string          `json:"action"` // "buy" or "sell"
	Quantity decimal.Decimal `json:"quantity"`
	Price    decimal.Decimal `json:"price,omitempty"`
}

// WrappedCard represents a single card in a Weekly Wrapped summary
type WrappedCard struct {
	Type    string                 `json:"type"`
	Title   string                 `json:"title"`
	Content string                 `json:"content"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Action  *CardAction            `json:"action,omitempty"`
}

// CardAction represents an actionable element in a card
type CardAction struct {
	Type     string                 `json:"type"`
	Label    string                 `json:"label,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// WeeklySummary represents enhanced AI summary with cards
type WeeklySummary struct {
	ID          uuid.UUID     `json:"id"`
	UserID      uuid.UUID     `json:"user_id"`
	WeekStart   time.Time     `json:"week_start"`
	SummaryMD   string        `json:"summary_md"`
	SummaryType string        `json:"summary_type"`
	Cards       []WrappedCard `json:"cards,omitempty"`
	Insights    map[string]interface{} `json:"insights,omitempty"`
	ArtifactURI string        `json:"artifact_uri,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
}
