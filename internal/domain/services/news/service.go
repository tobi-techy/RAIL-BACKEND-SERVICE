package news

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// AlpacaNewsClient interface for fetching news from Alpaca
type AlpacaNewsClient interface {
	GetNews(ctx context.Context, req *entities.AlpacaNewsRequest) (*entities.AlpacaNewsResponse, error)
}

// UserNewsRepository interface for news persistence
type UserNewsRepository interface {
	Create(ctx context.Context, news *entities.UserNews) error
	GetByUserID(ctx context.Context, userID uuid.UUID, isRead *bool, symbols []string, limit, offset int) ([]*entities.UserNews, error)
	GetWeeklyNews(ctx context.Context, userID uuid.UUID, weekStart, weekEnd time.Time) ([]*entities.UserNews, error)
	MarkAsRead(ctx context.Context, newsID uuid.UUID) error
	MarkMultipleAsRead(ctx context.Context, newsIDs []uuid.UUID) error
	GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error)
}

// PositionRepository interface for getting user holdings
type PositionRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Position, error)
}

// Service handles news operations
type Service struct {
	alpacaClient AlpacaNewsClient
	newsRepo     UserNewsRepository
	positionRepo PositionRepository
	logger       *zap.Logger
}

// NewService creates a new news service
func NewService(alpacaClient AlpacaNewsClient, newsRepo UserNewsRepository, positionRepo PositionRepository, logger *zap.Logger) *Service {
	return &Service{
		alpacaClient: alpacaClient,
		newsRepo:     newsRepo,
		positionRepo: positionRepo,
		logger:       logger,
	}
}

// GetFeed returns personalized news feed for a user
func (s *Service) GetFeed(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.UserNews, error) {
	if limit <= 0 {
		limit = 10
	}
	return s.newsRepo.GetByUserID(ctx, userID, nil, nil, limit, 0)
}

// GetWeeklyNews returns news for the current week
func (s *Service) GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]*entities.UserNews, error) {
	now := time.Now()
	weekStart := now.AddDate(0, 0, -int(now.Weekday()))
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, time.UTC)
	weekEnd := weekStart.AddDate(0, 0, 7)
	return s.newsRepo.GetWeeklyNews(ctx, userID, weekStart, weekEnd)
}

// MarkAsRead marks news articles as read
func (s *Service) MarkAsRead(ctx context.Context, newsIDs []uuid.UUID) error {
	if len(newsIDs) == 1 {
		return s.newsRepo.MarkAsRead(ctx, newsIDs[0])
	}
	return s.newsRepo.MarkMultipleAsRead(ctx, newsIDs)
}

// FetchAndStoreNews fetches news from Alpaca and stores relevant articles for a user
func (s *Service) FetchAndStoreNews(ctx context.Context, userID uuid.UUID) error {
	// Default to general market ETFs since positions are basket-based
	symbols := []string{"SPY", "QQQ", "VTI", "VOO", "IWM"}

	// Fetch news from Alpaca
	req := &entities.AlpacaNewsRequest{
		Symbols: symbols,
		Limit:   10,
	}

	resp, err := s.alpacaClient.GetNews(ctx, req)
	if err != nil {
		return err
	}

	// Store relevant news
	for _, article := range resp.News {
		news := &entities.UserNews{
			ID:             uuid.New(),
			UserID:         userID,
			Source:         article.Source,
			Title:          article.Headline,
			Summary:        article.Summary,
			URL:            article.URL,
			RelatedSymbols: article.Symbols,
			PublishedAt:    article.CreatedAt,
			IsRead:         false,
			RelevanceScore: s.calculateRelevance(article.Symbols, symbols),
			CreatedAt:      time.Now(),
		}

		if err := s.newsRepo.Create(ctx, news); err != nil {
			s.logger.Warn("Failed to store news article", zap.Error(err), zap.String("title", article.Headline))
		}
	}

	s.logger.Info("Fetched and stored news", zap.String("user_id", userID.String()), zap.Int("count", len(resp.News)))
	return nil
}

// calculateRelevance calculates relevance score based on symbol overlap
func (s *Service) calculateRelevance(articleSymbols, userSymbols []string) decimal.Decimal {
	if len(articleSymbols) == 0 || len(userSymbols) == 0 {
		return decimal.NewFromFloat(0.5)
	}

	userSymbolSet := make(map[string]bool)
	for _, sym := range userSymbols {
		userSymbolSet[sym] = true
	}

	matches := 0
	for _, sym := range articleSymbols {
		if userSymbolSet[sym] {
			matches++
		}
	}

	score := float64(matches) / float64(len(articleSymbols))
	return decimal.NewFromFloat(score)
}

// GetUnreadCount returns count of unread news for a user
func (s *Service) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	return s.newsRepo.GetUnreadCount(ctx, userID)
}
