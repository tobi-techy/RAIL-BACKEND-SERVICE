package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// AIConversation represents a persistent AI chat session.
type AIConversation struct {
	ID                 uuid.UUID       `json:"id" db:"id"`
	UserID             uuid.UUID       `json:"user_id" db:"user_id"`
	Title              string          `json:"title" db:"title"`
	SummaryContext     string          `json:"-" db:"summary_context"` // internal LLM context, not exposed to clients
	MessageCount       int             `json:"message_count" db:"message_count"`
	TotalTokens        int             `json:"total_tokens" db:"total_tokens"`
	TotalEstimatedCost decimal.Decimal `json:"total_estimated_cost_usd" db:"total_estimated_cost_usd"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
}

// AIMessage represents a single message in a conversation.
type AIMessage struct {
	ID             uuid.UUID              `json:"id" db:"id"`
	ConversationID uuid.UUID              `json:"conversation_id" db:"conversation_id"`
	Role           string                 `json:"role" db:"role"`
	Content        string                 `json:"content" db:"content"`
	TokenCount     int                    `json:"token_count" db:"token_count"`
	EstimatedCost  decimal.Decimal        `json:"estimated_cost_usd" db:"estimated_cost_usd"`
	Model          string                 `json:"model" db:"model"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt      time.Time              `json:"created_at" db:"created_at"`
}

// SummarizationThreshold is the number of actual messages (user + assistant)
// after which conversation history gets compressed into a summary.
// Since each exchange produces 2 messages, this triggers every 5 exchanges.
const SummarizationThreshold = 10

// RecentMessageWindow is the number of recent messages kept alongside the
// summary when building context for the LLM.
const RecentMessageWindow = 10

// MaxSummarizationMessages caps how many messages are fed to the summarizer
// to avoid exceeding the LLM context window.
const MaxSummarizationMessages = 30

// MaxChatMessageLength is the maximum allowed length for a user chat message.
const MaxChatMessageLength = 4000
