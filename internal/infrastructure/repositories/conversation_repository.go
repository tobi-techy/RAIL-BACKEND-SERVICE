package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ConversationRepository handles persistence for AI conversations and messages.
type ConversationRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewConversationRepository creates a new conversation repository.
func NewConversationRepository(db *sql.DB, logger *zap.Logger) *ConversationRepository {
	return &ConversationRepository{db: db, logger: logger}
}

const conversationColumns = `id, user_id, title, summary_context, message_count, total_tokens, total_estimated_cost_usd, created_at, updated_at`
const conversationListColumns = `id, user_id, title, '' as summary_context, message_count, total_tokens, total_estimated_cost_usd, created_at, updated_at`
const messageColumns = `id, conversation_id, role, content, token_count, estimated_cost_usd, model, metadata, created_at`

func scanConversation(row interface{ Scan(...interface{}) error }, conv *entities.AIConversation) error {
	return row.Scan(
		&conv.ID, &conv.UserID, &conv.Title, &conv.SummaryContext,
		&conv.MessageCount, &conv.TotalTokens, &conv.TotalEstimatedCost,
		&conv.CreatedAt, &conv.UpdatedAt,
	)
}

func scanMessage(row interface{ Scan(...interface{}) error }, m *entities.AIMessage) error {
	var metadataJSON []byte
	err := row.Scan(
		&m.ID, &m.ConversationID, &m.Role, &m.Content,
		&m.TokenCount, &m.EstimatedCost, &m.Model, &metadataJSON, &m.CreatedAt,
	)
	if err != nil {
		return err
	}
	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &m.Metadata)
	}
	return nil
}

// GetOrCreatePlatformConversation returns a stable conversation id for a
// messaging-platform thread (iMessage/WhatsApp/Telegram), creating one on first
// contact. The id keys the orchestrator's pending-action store, so tap-to-confirm
// postbacks resolve to the same conversation that staged the action.
func (r *ConversationRepository) GetOrCreatePlatformConversation(ctx context.Context, userID uuid.UUID, platform, threadID string, platformIdentityID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM ai_conversations WHERE platform = $1 AND platform_thread_id = $2 AND user_id = $3 LIMIT 1`,
		platform, threadID, userID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return uuid.Nil, fmt.Errorf("lookup platform conversation: %w", err)
	}

	id = uuid.New()
	now := time.Now()
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_conversations
			(id, user_id, title, summary_context, message_count, total_tokens, total_estimated_cost_usd,
			 created_at, updated_at, platform, platform_thread_id, platform_identity_id)
		 VALUES ($1, $2, $3, '', 0, 0, 0, $4, $4, $5, $6, $7)`,
		id, userID, "Miriam on "+platform, now, platform, threadID, platformIdentityID,
	); err != nil {
		return uuid.Nil, fmt.Errorf("create platform conversation: %w", err)
	}
	return id, nil
}

// GetLastPlatformThread returns the most recent platform_thread_id for a user
// on the given platform. Returns empty string if none found.
func (r *ConversationRepository) GetLastPlatformThread(ctx context.Context, userID uuid.UUID, platform string) (string, error) {
	var threadID string
	err := r.db.QueryRowContext(ctx,
		`SELECT platform_thread_id FROM ai_conversations WHERE user_id = $1 AND platform = $2 ORDER BY updated_at DESC LIMIT 1`,
		userID, platform,
	).Scan(&threadID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get last platform thread: %w", err)
	}
	return threadID, nil
}

// CreateConversation inserts a new conversation.
func (r *ConversationRepository) CreateConversation(ctx context.Context, conv *entities.AIConversation) error {
	query := `INSERT INTO ai_conversations (` + conversationColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	now := time.Now()
	if conv.ID == uuid.Nil {
		conv.ID = uuid.New()
	}
	conv.CreatedAt = now
	conv.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, query,
		conv.ID, conv.UserID, conv.Title, conv.SummaryContext,
		conv.MessageCount, conv.TotalTokens, conv.TotalEstimatedCost,
		conv.CreatedAt, conv.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create conversation: %w", err)
	}
	return nil
}

// GetConversation retrieves a conversation by ID.
func (r *ConversationRepository) GetConversation(ctx context.Context, id uuid.UUID) (*entities.AIConversation, error) {
	query := `SELECT ` + conversationColumns + ` FROM ai_conversations WHERE id = $1`
	conv := &entities.AIConversation{}
	err := scanConversation(r.db.QueryRowContext(ctx, query, id), conv)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	return conv, nil
}

// ListByUserID returns conversations for a user, most recent first.
// Excludes summary_context to keep the response lightweight.
func (r *ConversationRepository) ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AIConversation, error) {
	query := `SELECT ` + conversationListColumns + ` FROM ai_conversations WHERE user_id = $1 ORDER BY updated_at DESC LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var convs []*entities.AIConversation
	for rows.Next() {
		c := &entities.AIConversation{}
		if err := scanConversation(rows, c); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}

// DeleteConversation deletes a conversation and its messages (cascade).
func (r *ConversationRepository) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM ai_conversations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return nil
}

// UpdateSummary updates the summary context and bumps updated_at.
func (r *ConversationRepository) UpdateSummary(ctx context.Context, id uuid.UUID, summary string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE ai_conversations SET summary_context = $1, updated_at = $2 WHERE id = $3`,
		summary, time.Now(), id,
	)
	if err != nil {
		return fmt.Errorf("update summary: %w", err)
	}
	return nil
}

// UpdateTitle sets the conversation title.
func (r *ConversationRepository) UpdateTitle(ctx context.Context, id uuid.UUID, title string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE ai_conversations SET title = $1, updated_at = $2 WHERE id = $3`,
		title, time.Now(), id,
	)
	if err != nil {
		return fmt.Errorf("update title: %w", err)
	}
	return nil
}

// IncrementStats atomically increments counters after an exchange (user + assistant = 2 messages).
func (r *ConversationRepository) IncrementStats(ctx context.Context, id uuid.UUID, tokens int, cost decimal.Decimal) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE ai_conversations
		SET message_count = message_count + 2,
		    total_tokens = total_tokens + $1,
		    total_estimated_cost_usd = total_estimated_cost_usd + $2,
		    updated_at = $3
		WHERE id = $4`,
		tokens, cost, time.Now(), id,
	)
	if err != nil {
		return fmt.Errorf("increment stats: %w", err)
	}
	return nil
}

// --- Messages ---

// CreateMessage inserts a message into a conversation.
func (r *ConversationRepository) CreateMessage(ctx context.Context, msg *entities.AIMessage) error {
	metadataJSON, err := json.Marshal(msg.Metadata)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	msg.CreatedAt = time.Now()

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO ai_messages (`+messageColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		msg.ID, msg.ConversationID, msg.Role, msg.Content,
		msg.TokenCount, msg.EstimatedCost, msg.Model, metadataJSON, msg.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create message: %w", err)
	}
	return nil
}

// GetMessages returns messages for a conversation ordered by created_at ASC.
func (r *ConversationRepository) GetMessages(ctx context.Context, conversationID uuid.UUID, limit, offset int) ([]*entities.AIMessage, error) {
	query := `SELECT ` + messageColumns + ` FROM ai_messages WHERE conversation_id = $1 ORDER BY created_at ASC LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, conversationID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var msgs []*entities.AIMessage
	for rows.Next() {
		m := &entities.AIMessage{}
		if err := scanMessage(rows, m); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// GetRecentMessages returns the last N messages for a conversation (ordered ASC).
func (r *ConversationRepository) GetRecentMessages(ctx context.Context, conversationID uuid.UUID, n int) ([]*entities.AIMessage, error) {
	query := `
		SELECT ` + messageColumns + ` FROM (
			SELECT ` + messageColumns + ` FROM ai_messages WHERE conversation_id = $1
			ORDER BY created_at DESC LIMIT $2
		) sub ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, conversationID, n)
	if err != nil {
		return nil, fmt.Errorf("get recent messages: %w", err)
	}
	defer rows.Close()

	var msgs []*entities.AIMessage
	for rows.Next() {
		m := &entities.AIMessage{}
		if err := scanMessage(rows, m); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// CountMessages returns the total message count for a conversation.
func (r *ConversationRepository) CountMessages(ctx context.Context, conversationID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ai_messages WHERE conversation_id = $1`, conversationID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return count, nil
}
