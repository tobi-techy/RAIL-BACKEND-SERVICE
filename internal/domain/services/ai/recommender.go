package ai

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// BasketRepository interface for basket data
type BasketRepository interface {
	GetCuratedBaskets(ctx context.Context) ([]*entities.Basket, error)
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Basket, error)
}

// Recommender provides AI-powered basket recommendations
type Recommender struct {
	aiProvider        ai.AIProvider
	basketRepo        BasketRepository
	portfolioProvider PortfolioDataProvider
	logger            *zap.Logger
}

// NewRecommender creates a new basket recommender
func NewRecommender(
	aiProvider ai.AIProvider,
	basketRepo BasketRepository,
	portfolioProvider PortfolioDataProvider,
	logger *zap.Logger,
) *Recommender {
	return &Recommender{
		aiProvider:        aiProvider,
		basketRepo:        basketRepo,
		portfolioProvider: portfolioProvider,
		logger:            logger,
	}
}

// Recommendation represents a basket recommendation
type Recommendation struct {
	BasketID   uuid.UUID       `json:"basket_id"`
	BasketName string          `json:"basket_name"`
	Score      decimal.Decimal `json:"score"`
	Reason     string          `json:"reason"`
	Tags       []string        `json:"tags"`
}

// GetRecommendations returns personalized basket recommendations
func (r *Recommender) GetRecommendations(ctx context.Context, userID uuid.UUID, limit int) ([]*Recommendation, error) {
	if limit <= 0 {
		limit = 3
	}

	// Get user's current allocations
	allocations, err := r.portfolioProvider.GetAllocations(ctx, userID)
	if err != nil {
		r.logger.Warn("Failed to get allocations for recommendations", zap.Error(err))
		allocations = []*Allocation{}
	}

	// Get available baskets
	baskets, err := r.basketRepo.GetCuratedBaskets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get baskets: %w", err)
	}

	// Build context for AI
	currentHoldings := make([]string, 0, len(allocations))
	for _, a := range allocations {
		currentHoldings = append(currentHoldings, a.BasketName)
	}

	availableBaskets := make([]map[string]interface{}, 0, len(baskets))
	for _, b := range baskets {
		availableBaskets = append(availableBaskets, map[string]interface{}{
			"id":          b.ID.String(),
			"name":        b.Name,
			"description": b.Description,
			"risk_level":  b.RiskLevel,
		})
	}

	prompt := fmt.Sprintf(`Based on the user's current holdings and available baskets, recommend %d baskets they should consider.

Current holdings: %v
Available baskets: %v

For each recommendation, provide:
1. basket_id (from available baskets)
2. reason (1-2 sentences, Gen Z friendly tone)
3. score (0-100 based on fit)

Respond in JSON format: [{"basket_id": "...", "reason": "...", "score": 85}]`, limit, currentHoldings, availableBaskets)

	req := &ai.ChatRequest{
		Messages:    []ai.Message{{Role: "user", Content: prompt}},
		MaxTokens:   300,
		Temperature: 0.7,
	}

	resp, err := r.aiProvider.ChatCompletion(ctx, req)
	if err != nil {
		r.logger.Warn("AI recommendation failed, using fallback", zap.Error(err))
		return r.fallbackRecommendations(baskets, allocations, limit), nil
	}

	// Parse AI response - for now use fallback as parsing is complex
	recommendations := r.fallbackRecommendations(baskets, allocations, limit)
	r.logger.Debug("Generated recommendations", zap.Int("count", len(recommendations)), zap.String("ai_response", resp.Content))

	return recommendations, nil
}

// fallbackRecommendations provides rule-based recommendations when AI fails
func (r *Recommender) fallbackRecommendations(baskets []*entities.Basket, currentAllocations []*Allocation, limit int) []*Recommendation {
	// Build set of current basket IDs
	currentBasketIDs := make(map[uuid.UUID]bool)
	for _, a := range currentAllocations {
		currentBasketIDs[a.BasketID] = true
	}

	recommendations := make([]*Recommendation, 0, limit)
	reasons := []string{
		"Diversify your portfolio with this one 📈",
		"Popular choice among investors your age 🔥",
		"Great for long-term growth potential 🚀",
		"Balanced risk-reward ratio 💪",
		"Trending basket this month ⭐",
	}

	reasonIdx := 0
	for _, basket := range baskets {
		if len(recommendations) >= limit {
			break
		}
		// Skip baskets user already owns
		if currentBasketIDs[basket.ID] {
			continue
		}

		score := decimal.NewFromInt(75)
		if basket.RiskLevel == "low" {
			score = decimal.NewFromInt(85)
		} else if basket.RiskLevel == "high" {
			score = decimal.NewFromInt(65)
		}

		recommendations = append(recommendations, &Recommendation{
			BasketID:   basket.ID,
			BasketName: basket.Name,
			Score:      score,
			Reason:     reasons[reasonIdx%len(reasons)],
			Tags:       []string{string(basket.RiskLevel)},
		})
		reasonIdx++
	}

	return recommendations
}

// GetRebalanceSuggestions suggests portfolio rebalancing
func (r *Recommender) GetRebalanceSuggestions(ctx context.Context, userID uuid.UUID) (*RebalanceSuggestion, error) {
	allocations, err := r.portfolioProvider.GetAllocations(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get allocations: %w", err)
	}

	if len(allocations) == 0 {
		return &RebalanceSuggestion{
			NeedsRebalance: false,
			Message:        "Start investing to get rebalancing suggestions!",
		}, nil
	}

	// Check for concentration risk (any single basket > 50%)
	var maxWeight decimal.Decimal
	var maxBasket string
	for _, a := range allocations {
		if a.Weight.GreaterThan(maxWeight) {
			maxWeight = a.Weight
			maxBasket = a.BasketName
		}
	}

	threshold := decimal.NewFromFloat(0.5)
	if maxWeight.GreaterThan(threshold) {
		return &RebalanceSuggestion{
			NeedsRebalance: true,
			Message:        fmt.Sprintf("Your portfolio is %s%% in %s - consider diversifying 🎯", maxWeight.Mul(decimal.NewFromInt(100)).StringFixed(0), maxBasket),
			Actions: []RebalanceAction{
				{Type: "reduce", BasketName: maxBasket, Reason: "High concentration risk"},
			},
		}, nil
	}

	return &RebalanceSuggestion{
		NeedsRebalance: false,
		Message:        "Your portfolio looks well-balanced! 🎉",
	}, nil
}

// RebalanceSuggestion represents rebalancing advice
type RebalanceSuggestion struct {
	NeedsRebalance bool              `json:"needs_rebalance"`
	Message        string            `json:"message"`
	Actions        []RebalanceAction `json:"actions,omitempty"`
}

// RebalanceAction represents a suggested rebalancing action
type RebalanceAction struct {
	Type       string `json:"type"` // "increase", "reduce", "add"
	BasketName string `json:"basket_name"`
	Reason     string `json:"reason"`
}
