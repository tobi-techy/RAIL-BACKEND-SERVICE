package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stack-service/stack_service/internal/zerog/compute"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// ChatSessionRepository implements the ChatSessionStore interface for PostgreSQL
type ChatSessionRepository struct {
	db     *sqlx.DB
	logger *zap.Logger
	tracer trace.Tracer
}

// NewChatSessionRepository creates a new chat session repository
func NewChatSessionRepository(db *sqlx.DB, logger *zap.Logger) *ChatSessionRepository {
	return &ChatSessionRepository{
		db:     db,
		logger: logger,
		tracer: otel.Tracer("chat-session-repository"),
	}
}

// SaveSession saves a new chat session
func (r *ChatSessionRepository) SaveSession(ctx context.Context, session *compute.ChatSession) error {
	ctx, span := r.tracer.Start(ctx, "chat_session_repo.save", trace.WithAttributes(
		attribute.String("session_id", session.ID.String()),
		attribute.String("user_id", session.UserID.String()),
	))
	defer span.End()

	messagesJSON, err := json.Marshal(session.Messages)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to marshal messages: %w", err)
	}

	contextJSON, err := json.Marshal(session.Context)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to marshal context: %w", err)
	}

	metadataJSON, err := json.Marshal(session.Metadata)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO chat_sessions (
			id, user_id, title, messages, context, created_at, updated_at, 
			last_accessed_at, message_count, tokens_used, status, metadata,
			provider_address, model, auto_summarize, summarize_interval
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		)
	`

	_, err = r.db.ExecContext(ctx, query,
		session.ID,
		session.UserID,
		session.Title,
		messagesJSON,
		contextJSON,
		session.CreatedAt,
		session.UpdatedAt,
		session.LastAccessedAt,
		session.MessageCount,
		session.TokensUsed,
		session.Status,
		metadataJSON,
		session.ProviderAddress,
		session.Model,
		session.AutoSummarize,
		session.SummarizeInterval,
	)

	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to save session: %w", err)
	}

	r.logger.Info("Chat session saved",
		zap.String("session_id", session.ID.String()),
		zap.String("user_id", session.UserID.String()),
	)

	return nil
}

// GetSession retrieves a chat session by ID
func (r *ChatSessionRepository) GetSession(ctx context.Context, sessionID uuid.UUID) (*compute.ChatSession, error) {
	ctx, span := r.tracer.Start(ctx, "chat_session_repo.get", trace.WithAttributes(
		attribute.String("session_id", sessionID.String()),
	))
	defer span.End()

	query := `
		SELECT 
			id, user_id, title, messages, context, created_at, updated_at, 
			last_accessed_at, message_count, tokens_used, status, metadata,
			provider_address, model, auto_summarize, summarize_interval
		FROM chat_sessions 
		WHERE id = $1
	`

	var (
		id                uuid.UUID
		userID            uuid.UUID
		title             string
		messagesJSON      []byte
		contextJSON       []byte
		createdAt         time.Time
		updatedAt         time.Time
		lastAccessedAt    time.Time
		messageCount      int
		tokensUsed        int
		status            string
		metadataJSON      []byte
		providerAddress   string
		model             string
		autoSummarize     bool
		summarizeInterval int
	)

	err := r.db.QueryRowContext(ctx, query, sessionID).Scan(
		&id, &userID, &title, &messagesJSON, &contextJSON, &createdAt, &updatedAt,
		&lastAccessedAt, &messageCount, &tokensUsed, &status, &metadataJSON,
		&providerAddress, &model, &autoSummarize, &summarizeInterval,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Unmarshal JSON fields
	var messages []compute.ChatMessage
	if err := json.Unmarshal(messagesJSON, &messages); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to unmarshal messages: %w", err)
	}

	var context compute.PortfolioContext
	if err := json.Unmarshal(contextJSON, &context); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to unmarshal context: %w", err)
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	session := &compute.ChatSession{
		ID:                id,
		UserID:            userID,
		Title:             title,
		Messages:          messages,
		Context:           &context,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		LastAccessedAt:    lastAccessedAt,
		MessageCount:      messageCount,
		TokensUsed:        tokensUsed,
		Status:            compute.ChatSessionStatus(status),
		Metadata:          metadata,
		ProviderAddress:   providerAddress,
		Model:             model,
		AutoSummarize:     autoSummarize,
		SummarizeInterval: summarizeInterval,
	}

	return session, nil
}

// ListUserSessions lists chat sessions for a user
func (r *ChatSessionRepository) ListUserSessions(ctx context.Context, userID uuid.UUID, status compute.ChatSessionStatus, limit int) ([]*compute.ChatSession, error) {
	ctx, span := r.tracer.Start(ctx, "chat_session_repo.list", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("status", string(status)),
		attribute.Int("limit", limit),
	))
	defer span.End()

	query := `
		SELECT 
			id, user_id, title, messages, context, created_at, updated_at, 
			last_accessed_at, message_count, tokens_used, status, metadata,
			provider_address, model, auto_summarize, summarize_interval
		FROM chat_sessions 
		WHERE user_id = $1
	`

	args := []interface{}{userID}
	argIndex := 2

	// Add status filter if not empty
	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, string(status))
		argIndex++
	}

	query += " ORDER BY last_accessed_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*compute.ChatSession

	for rows.Next() {
		var (
			id                uuid.UUID
			userID            uuid.UUID
			title             string
			messagesJSON      []byte
			contextJSON       []byte
			createdAt         time.Time
			updatedAt         time.Time
			lastAccessedAt    time.Time
			messageCount      int
			tokensUsed        int
			statusStr         string
			metadataJSON      []byte
			providerAddress   string
			model             string
			autoSummarize     bool
			summarizeInterval int
		)

		err := rows.Scan(
			&id, &userID, &title, &messagesJSON, &contextJSON, &createdAt, &updatedAt,
			&lastAccessedAt, &messageCount, &tokensUsed, &statusStr, &metadataJSON,
			&providerAddress, &model, &autoSummarize, &summarizeInterval,
		)
		if err != nil {
			span.RecordError(err)
			r.logger.Warn("Failed to scan session row", zap.Error(err))
			continue
		}

		var messages []compute.ChatMessage
		if err := json.Unmarshal(messagesJSON, &messages); err != nil {
			r.logger.Warn("Failed to unmarshal messages", zap.Error(err))
			continue
		}

		var context compute.PortfolioContext
		if err := json.Unmarshal(contextJSON, &context); err != nil {
			r.logger.Warn("Failed to unmarshal context", zap.Error(err))
			continue
		}

		var metadata map[string]interface{}
		if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
			r.logger.Warn("Failed to unmarshal metadata", zap.Error(err))
			continue
		}

		session := &compute.ChatSession{
			ID:                id,
			UserID:            userID,
			Title:             title,
			Messages:          messages,
			Context:           &context,
			CreatedAt:         createdAt,
			UpdatedAt:         updatedAt,
			LastAccessedAt:    lastAccessedAt,
			MessageCount:      messageCount,
			TokensUsed:        tokensUsed,
			Status:            compute.ChatSessionStatus(statusStr),
			Metadata:          metadata,
			ProviderAddress:   providerAddress,
			Model:             model,
			AutoSummarize:     autoSummarize,
			SummarizeInterval: summarizeInterval,
		}

		sessions = append(sessions, session)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("error iterating sessions: %w", err)
	}

	return sessions, nil
}

// UpdateSession updates an existing chat session
func (r *ChatSessionRepository) UpdateSession(ctx context.Context, session *compute.ChatSession) error {
	ctx, span := r.tracer.Start(ctx, "chat_session_repo.update", trace.WithAttributes(
		attribute.String("session_id", session.ID.String()),
	))
	defer span.End()

	messagesJSON, err := json.Marshal(session.Messages)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to marshal messages: %w", err)
	}

	contextJSON, err := json.Marshal(session.Context)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to marshal context: %w", err)
	}

	metadataJSON, err := json.Marshal(session.Metadata)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		UPDATE chat_sessions 
		SET 
			title = $1,
			messages = $2,
			context = $3,
			updated_at = $4,
			last_accessed_at = $5,
			message_count = $6,
			tokens_used = $7,
			status = $8,
			metadata = $9,
			auto_summarize = $10,
			summarize_interval = $11
		WHERE id = $12
	`

	result, err := r.db.ExecContext(ctx, query,
		session.Title,
		messagesJSON,
		contextJSON,
		session.UpdatedAt,
		session.LastAccessedAt,
		session.MessageCount,
		session.TokensUsed,
		session.Status,
		metadataJSON,
		session.AutoSummarize,
		session.SummarizeInterval,
		session.ID,
	)

	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to update session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("session not found: %s", session.ID)
	}

	r.logger.Debug("Chat session updated",
		zap.String("session_id", session.ID.String()),
		zap.Int("message_count", session.MessageCount),
	)

	return nil
}

// DeleteSession deletes a chat session
func (r *ChatSessionRepository) DeleteSession(ctx context.Context, sessionID uuid.UUID) error {
	ctx, span := r.tracer.Start(ctx, "chat_session_repo.delete", trace.WithAttributes(
		attribute.String("session_id", sessionID.String()),
	))
	defer span.End()

	query := `DELETE FROM chat_sessions WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to delete session: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	r.logger.Info("Chat session deleted",
		zap.String("session_id", sessionID.String()),
	)

	return nil
}

// GetActiveSessionForUser retrieves the most recent active session for a user
func (r *ChatSessionRepository) GetActiveSessionForUser(ctx context.Context, userID uuid.UUID) (*compute.ChatSession, error) {
	ctx, span := r.tracer.Start(ctx, "chat_session_repo.get_active", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	query := `
		SELECT 
			id, user_id, title, messages, context, created_at, updated_at, 
			last_accessed_at, message_count, tokens_used, status, metadata,
			provider_address, model, auto_summarize, summarize_interval
		FROM chat_sessions 
		WHERE user_id = $1 AND status = $2
		ORDER BY last_accessed_at DESC
		LIMIT 1
	`

	var (
		id                uuid.UUID
		title             string
		messagesJSON      []byte
		contextJSON       []byte
		createdAt         time.Time
		updatedAt         time.Time
		lastAccessedAt    time.Time
		messageCount      int
		tokensUsed        int
		status            string
		metadataJSON      []byte
		providerAddress   string
		model             string
		autoSummarize     bool
		summarizeInterval int
	)

	err := r.db.QueryRowContext(ctx, query, userID, string(compute.ChatSessionStatusActive)).Scan(
		&id, &userID, &title, &messagesJSON, &contextJSON, &createdAt, &updatedAt,
		&lastAccessedAt, &messageCount, &tokensUsed, &status, &metadataJSON,
		&providerAddress, &model, &autoSummarize, &summarizeInterval,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no active session found for user: %s", userID)
	}
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get active session: %w", err)
	}

	var messages []compute.ChatMessage
	if err := json.Unmarshal(messagesJSON, &messages); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to unmarshal messages: %w", err)
	}

	var context compute.PortfolioContext
	if err := json.Unmarshal(contextJSON, &context); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to unmarshal context: %w", err)
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	session := &compute.ChatSession{
		ID:                id,
		UserID:            userID,
		Title:             title,
		Messages:          messages,
		Context:           &context,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		LastAccessedAt:    lastAccessedAt,
		MessageCount:      messageCount,
		TokensUsed:        tokensUsed,
		Status:            compute.ChatSessionStatus(status),
		Metadata:          metadata,
		ProviderAddress:   providerAddress,
		Model:             model,
		AutoSummarize:     autoSummarize,
		SummarizeInterval: summarizeInterval,
	}

	return session, nil
}
