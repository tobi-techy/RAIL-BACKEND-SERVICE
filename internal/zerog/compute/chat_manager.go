package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// ChatSession represents an AI chat session for portfolio discussions
type ChatSession struct {
	ID                uuid.UUID              `json:"id"`
	UserID            uuid.UUID              `json:"user_id"`
	Title             string                 `json:"title"`
	Messages          []ChatMessage          `json:"messages"`
	Context           *PortfolioContext      `json:"context"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	LastAccessedAt    time.Time              `json:"last_accessed_at"`
	MessageCount      int                    `json:"message_count"`
	TokensUsed        int                    `json:"tokens_used"`
	Status            ChatSessionStatus      `json:"status"`
	Metadata          map[string]interface{} `json:"metadata"`
	ProviderAddress   string                 `json:"provider_address"`
	Model             string                 `json:"model"`
	AutoSummarize     bool                   `json:"auto_summarize"`
	SummarizeInterval int                    `json:"summarize_interval"` // Messages before auto-summarize
}

// ChatMessage represents a single message in the conversation
type ChatMessage struct {
	ID        uuid.UUID              `json:"id"`
	Role      string                 `json:"role"` // system, user, assistant
	Content   string                 `json:"content"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	TokensUsed int                   `json:"tokens_used,omitempty"`
}

// PortfolioContext contains portfolio data for AI context
type PortfolioContext struct {
	SnapshotTime       time.Time                     `json:"snapshot_time"`
	TotalValue         float64                       `json:"total_value"`
	TotalReturn        float64                       `json:"total_return"`
	TotalReturnPct     float64                       `json:"total_return_pct"`
	DayChange          float64                       `json:"day_change"`
	DayChangePct       float64                       `json:"day_change_pct"`
	Positions          []entities.PositionMetrics    `json:"positions"`
	RiskMetrics        *entities.RiskMetrics         `json:"risk_metrics,omitempty"`
	PerformanceHistory []entities.PerformancePoint   `json:"performance_history,omitempty"`
	RecentEvents       []PortfolioEvent              `json:"recent_events,omitempty"`
	UserPreferences    *entities.UserPreferences     `json:"user_preferences,omitempty"`
}

// PortfolioEvent represents a significant portfolio event
type PortfolioEvent struct {
	Type      string    `json:"type"` // trade, deposit, withdrawal, rebalance
	Timestamp time.Time `json:"timestamp"`
	Summary   string    `json:"summary"`
	Impact    float64   `json:"impact,omitempty"`
}

// ChatSessionStatus represents the status of a chat session
type ChatSessionStatus string

const (
	ChatSessionStatusActive   ChatSessionStatus = "active"
	ChatSessionStatusArchived ChatSessionStatus = "archived"
	ChatSessionStatusSummarized ChatSessionStatus = "summarized"
)

// ChatManager manages AI chat sessions for portfolio discussions
type ChatManager struct {
	computeClient    *Client
	storageClient    entities.ZeroGStorageClient
	logger           *zap.Logger
	tracer           trace.Tracer
	sessionStore     ChatSessionStore
	contextRefreshInterval time.Duration
	maxMessagesBeforeCompress int
}

// ChatSessionStore defines the interface for persisting chat sessions
type ChatSessionStore interface {
	SaveSession(ctx context.Context, session *ChatSession) error
	GetSession(ctx context.Context, sessionID uuid.UUID) (*ChatSession, error)
	ListUserSessions(ctx context.Context, userID uuid.UUID, status ChatSessionStatus, limit int) ([]*ChatSession, error)
	UpdateSession(ctx context.Context, session *ChatSession) error
	DeleteSession(ctx context.Context, sessionID uuid.UUID) error
	GetActiveSessionForUser(ctx context.Context, userID uuid.UUID) (*ChatSession, error)
}

// NewChatManager creates a new chat manager
func NewChatManager(
	computeClient *Client,
	storageClient entities.ZeroGStorageClient,
	sessionStore ChatSessionStore,
	logger *zap.Logger,
) *ChatManager {
	return &ChatManager{
		computeClient:              computeClient,
		storageClient:              storageClient,
		sessionStore:               sessionStore,
		logger:                     logger,
		tracer:                     otel.Tracer("chat-manager"),
		contextRefreshInterval:     15 * time.Minute,
		maxMessagesBeforeCompress:  20,
	}
}

// CreateSession creates a new chat session for a user
func (cm *ChatManager) CreateSession(ctx context.Context, userID uuid.UUID, title string, portfolioContext *PortfolioContext, providerAddress, model string) (*ChatSession, error) {
	ctx, span := cm.tracer.Start(ctx, "chat_manager.create_session", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("provider", providerAddress),
		attribute.String("model", model),
	))
	defer span.End()

	session := &ChatSession{
		ID:                uuid.New(),
		UserID:            userID,
		Title:             title,
		Messages:          []ChatMessage{},
		Context:           portfolioContext,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		LastAccessedAt:    time.Now(),
		MessageCount:      0,
		TokensUsed:        0,
		Status:            ChatSessionStatusActive,
		Metadata:          make(map[string]interface{}),
		ProviderAddress:   providerAddress,
		Model:             model,
		AutoSummarize:     true,
		SummarizeInterval: 20,
	}

	// Add system message with portfolio context
	systemMessage := cm.buildSystemMessage(portfolioContext)
	session.Messages = append(session.Messages, ChatMessage{
		ID:        uuid.New(),
		Role:      "system",
		Content:   systemMessage,
		Timestamp: time.Now(),
	})

	// Save session
	if err := cm.sessionStore.SaveSession(ctx, session); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to save session: %w", err)
	}

	cm.logger.Info("Chat session created",
		zap.String("session_id", session.ID.String()),
		zap.String("user_id", userID.String()),
		zap.String("title", title),
	)

	return session, nil
}

// SendMessage sends a message in a chat session and gets AI response
func (cm *ChatManager) SendMessage(ctx context.Context, sessionID uuid.UUID, userMessage string) (*ChatMessage, error) {
	ctx, span := cm.tracer.Start(ctx, "chat_manager.send_message", trace.WithAttributes(
		attribute.String("session_id", sessionID.String()),
	))
	defer span.End()

	// Load session
	session, err := cm.sessionStore.GetSession(ctx, sessionID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	if session.Status != ChatSessionStatusActive {
		return nil, fmt.Errorf("session is not active: %s", session.Status)
	}

	// Check if context needs refresh
	if time.Since(session.Context.SnapshotTime) > cm.contextRefreshInterval {
		cm.logger.Info("Portfolio context is stale, consider refreshing",
			zap.String("session_id", sessionID.String()),
			zap.Duration("age", time.Since(session.Context.SnapshotTime)),
		)
	}

	// Add user message
	userMsg := ChatMessage{
		ID:        uuid.New(),
		Role:      "user",
		Content:   userMessage,
		Timestamp: time.Now(),
	}
	session.Messages = append(session.Messages, userMsg)
	session.MessageCount++
	session.LastAccessedAt = time.Now()

	// Build inference request
	messages := cm.prepareMessagesForInference(session)

	request := &InferenceRequest{
		Model:       session.Model,
		Messages:    messages,
		MaxTokens:   2048,
		Temperature: 0.7,
		Stream:      false,
		Metadata: map[string]interface{}{
			"session_id": session.ID.String(),
			"user_id":    session.UserID.String(),
		},
	}

	// Get AI response
	response, err := cm.computeClient.GenerateInference(ctx, request)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to generate inference: %w", err)
	}

	// Extract assistant message
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no response from AI")
	}

	assistantMsg := ChatMessage{
		ID:         uuid.New(),
		Role:       "assistant",
		Content:    response.Choices[0].Message.Content,
		Timestamp:  time.Now(),
		TokensUsed: response.Usage.TotalTokens,
	}

	session.Messages = append(session.Messages, assistantMsg)
	session.MessageCount++
	session.TokensUsed += response.Usage.TotalTokens
	session.UpdatedAt = time.Now()

	// Check if summarization is needed
	if session.AutoSummarize && session.MessageCount >= session.SummarizeInterval {
		if err := cm.compressSession(ctx, session); err != nil {
			cm.logger.Warn("Failed to compress session", zap.Error(err))
			// Continue despite compression failure
		}
	}

	// Update session
	if err := cm.sessionStore.UpdateSession(ctx, session); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to update session: %w", err)
	}

	cm.logger.Info("Message processed successfully",
		zap.String("session_id", sessionID.String()),
		zap.Int("tokens_used", response.Usage.TotalTokens),
		zap.Int("total_messages", session.MessageCount),
	)

	return &assistantMsg, nil
}

// GetSession retrieves a chat session
func (cm *ChatManager) GetSession(ctx context.Context, sessionID uuid.UUID) (*ChatSession, error) {
	ctx, span := cm.tracer.Start(ctx, "chat_manager.get_session", trace.WithAttributes(
		attribute.String("session_id", sessionID.String()),
	))
	defer span.End()

	session, err := cm.sessionStore.GetSession(ctx, sessionID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Update last accessed time
	session.LastAccessedAt = time.Now()
	if err := cm.sessionStore.UpdateSession(ctx, session); err != nil {
		cm.logger.Warn("Failed to update last accessed time", zap.Error(err))
	}

	return session, nil
}

// ListUserSessions lists all sessions for a user
func (cm *ChatManager) ListUserSessions(ctx context.Context, userID uuid.UUID, status ChatSessionStatus, limit int) ([]*ChatSession, error) {
	ctx, span := cm.tracer.Start(ctx, "chat_manager.list_sessions", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("status", string(status)),
	))
	defer span.End()

	sessions, err := cm.sessionStore.ListUserSessions(ctx, userID, status, limit)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	return sessions, nil
}

// UpdatePortfolioContext updates the portfolio context for a session
func (cm *ChatManager) UpdatePortfolioContext(ctx context.Context, sessionID uuid.UUID, newContext *PortfolioContext) error {
	ctx, span := cm.tracer.Start(ctx, "chat_manager.update_context", trace.WithAttributes(
		attribute.String("session_id", sessionID.String()),
	))
	defer span.End()

	session, err := cm.sessionStore.GetSession(ctx, sessionID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to load session: %w", err)
	}

	// Update context
	session.Context = newContext
	session.UpdatedAt = time.Now()

	// Add a system message about the context update
	contextUpdateMsg := ChatMessage{
		ID:        uuid.New(),
		Role:      "system",
		Content:   fmt.Sprintf("Portfolio context updated at %s. Latest metrics: Total Value: $%.2f, Day Change: %.2f%%, Total Return: %.2f%%", newContext.SnapshotTime.Format("2006-01-02 15:04"), newContext.TotalValue, newContext.DayChangePct, newContext.TotalReturnPct),
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"context_update": true,
		},
	}
	session.Messages = append(session.Messages, contextUpdateMsg)

	if err := cm.sessionStore.UpdateSession(ctx, session); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to update session: %w", err)
	}

	cm.logger.Info("Portfolio context updated",
		zap.String("session_id", sessionID.String()),
		zap.Float64("total_value", newContext.TotalValue),
	)

	return nil
}

// ArchiveSession archives a chat session
func (cm *ChatManager) ArchiveSession(ctx context.Context, sessionID uuid.UUID) error {
	ctx, span := cm.tracer.Start(ctx, "chat_manager.archive_session", trace.WithAttributes(
		attribute.String("session_id", sessionID.String()),
	))
	defer span.End()

	session, err := cm.sessionStore.GetSession(ctx, sessionID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to load session: %w", err)
	}

	session.Status = ChatSessionStatusArchived
	session.UpdatedAt = time.Now()

	// Store full session in 0G storage for long-term archive
	if err := cm.archiveToStorage(ctx, session); err != nil {
		cm.logger.Warn("Failed to archive to storage", zap.Error(err))
		// Continue despite storage failure
	}

	if err := cm.sessionStore.UpdateSession(ctx, session); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to update session: %w", err)
	}

	cm.logger.Info("Session archived",
		zap.String("session_id", sessionID.String()),
	)

	return nil
}

// buildSystemMessage creates the initial system message with portfolio context
func (cm *ChatManager) buildSystemMessage(context *PortfolioContext) string {
	systemPrompt := `You are an AI financial advisor specialized in portfolio analysis and investment guidance. You have access to the user's current portfolio data and should provide personalized, actionable insights.

Key responsibilities:
- Analyze portfolio performance, risk, and allocation
- Provide clear, jargon-free explanations
- Offer actionable recommendations based on user's risk profile
- Help users understand market movements and their impact
- Be honest about limitations and avoid guaranteeing returns

Current Portfolio Snapshot (as of %s):
- Total Value: $%.2f
- Total Return: %.2f%% ($%.2f)
- Day Change: %.2f%%
`

	prompt := fmt.Sprintf(systemPrompt,
		context.SnapshotTime.Format("January 2, 2006 15:04 MST"),
		context.TotalValue,
		context.TotalReturnPct,
		context.TotalReturn,
		context.DayChangePct,
	)

	if context.RiskMetrics != nil {
		prompt += fmt.Sprintf(`
Risk Metrics:
- Volatility: %.2f%%
- Sharpe Ratio: %.2f
- Max Drawdown: %.2f%%
- Diversification Score: %.2f/1.0
`,
			context.RiskMetrics.Volatility*100,
			context.RiskMetrics.SharpeRatio,
			context.RiskMetrics.MaxDrawdown*100,
			context.RiskMetrics.Diversification,
		)
	}

	if len(context.Positions) > 0 {
		prompt += "\nTop Holdings:\n"
		for i, pos := range context.Positions {
			if i >= 5 { // Show top 5 positions
				break
			}
			prompt += fmt.Sprintf("- %s: $%.2f (%.1f%% of portfolio, P&L: %.2f%%)\n",
				pos.BasketName,
				pos.CurrentValue,
				pos.Weight*100,
				pos.UnrealizedPLPct,
			)
		}
	}

	prompt += "\nPlease provide helpful, personalized advice based on this portfolio context."

	return prompt
}

// prepareMessagesForInference prepares messages for inference API
func (cm *ChatManager) prepareMessagesForInference(session *ChatSession) []ChatMessage {
	// For long conversations, we may need to compress or summarize earlier messages
	// For now, return all messages
	// TODO: Implement intelligent message window management
	
	messages := make([]ChatMessage, 0, len(session.Messages))
	
	// Always include system message
	if len(session.Messages) > 0 && session.Messages[0].Role == "system" {
		messages = append(messages, session.Messages[0])
	}
	
	// Add recent user/assistant messages
	recentMessages := session.Messages[1:]
	if len(recentMessages) > 20 {
		// Keep last 20 messages
		recentMessages = recentMessages[len(recentMessages)-20:]
	}
	
	messages = append(messages, recentMessages...)
	
	return messages
}

// compressSession compresses the session by summarizing older messages
func (cm *ChatManager) compressSession(ctx context.Context, session *ChatSession) error {
	cm.logger.Info("Compressing session",
		zap.String("session_id", session.ID.String()),
		zap.Int("message_count", session.MessageCount),
	)

	// Create a summary of the conversation so far
	summaryRequest := &InferenceRequest{
		Model: session.Model,
		Messages: []ChatMessage{
			{
				Role: "system",
				Content: "Summarize the following conversation into key points and decisions. Focus on portfolio insights, user questions, and recommendations given. Be concise but preserve important details.",
			},
			{
				Role: "user",
				Content: fmt.Sprintf("Please summarize this conversation:\n\n%s", cm.formatMessagesForSummary(session.Messages)),
			},
		},
		MaxTokens:   1024,
		Temperature: 0.3,
	}

	response, err := cm.computeClient.GenerateInference(ctx, summaryRequest)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	if len(response.Choices) == 0 {
		return fmt.Errorf("no summary generated")
	}

	// Keep system message and recent 10 messages, replace older messages with summary
	systemMsg := session.Messages[0]
	recentMessages := session.Messages[len(session.Messages)-10:]

	summaryMsg := ChatMessage{
		ID:        uuid.New(),
		Role:      "system",
		Content:   fmt.Sprintf("Previous conversation summary (compressed at %s):\n%s", time.Now().Format("2006-01-02 15:04"), response.Choices[0].Message.Content),
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"compression": true,
			"original_message_count": len(session.Messages),
		},
	}

	session.Messages = append([]ChatMessage{systemMsg, summaryMsg}, recentMessages...)
	session.Status = ChatSessionStatusSummarized

	cm.logger.Info("Session compressed successfully",
		zap.String("session_id", session.ID.String()),
		zap.Int("new_message_count", len(session.Messages)),
	)

	return nil
}

// formatMessagesForSummary formats messages for summarization
func (cm *ChatManager) formatMessagesForSummary(messages []ChatMessage) string {
	var formatted string
	for _, msg := range messages {
		if msg.Role != "system" {
			formatted += fmt.Sprintf("[%s] %s: %s\n\n", msg.Timestamp.Format("15:04"), msg.Role, msg.Content)
		}
	}
	return formatted
}

// archiveToStorage archives the full session to 0G storage
func (cm *ChatManager) archiveToStorage(ctx context.Context, session *ChatSession) error {
	// Serialize session to JSON
	sessionData, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	// Store in 0G storage
	namespace := "chat-sessions/"
	metadata := map[string]string{
		"session_id":    session.ID.String(),
		"user_id":       session.UserID.String(),
		"archived_at":   time.Now().Format(time.RFC3339),
		"message_count": fmt.Sprintf("%d", session.MessageCount),
		"tokens_used":   fmt.Sprintf("%d", session.TokensUsed),
	}

	_, err = cm.storageClient.Store(ctx, namespace, sessionData, metadata)
	if err != nil {
		return fmt.Errorf("failed to store session in 0G storage: %w", err)
	}

	cm.logger.Info("Session archived to storage",
		zap.String("session_id", session.ID.String()),
		zap.Int("size_bytes", len(sessionData)),
	)

	return nil
}
