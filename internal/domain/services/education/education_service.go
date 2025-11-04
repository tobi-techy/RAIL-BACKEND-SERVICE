package education

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
)

// Service handles user education and risk warnings
type Service struct {
	educationRepo EducationRepository
	aiService     AIService
	userRepo      UserRepository
	logger        *logger.Logger
}

// EducationRepository interface for education content
type EducationRepository interface {
	GetEducationalContent(ctx context.Context, contentType, riskLevel string) ([]*entities.EducationalContent, error)
	GetUserProgress(ctx context.Context, userID uuid.UUID) (*entities.UserEducationProgress, error)
	UpdateUserProgress(ctx context.Context, progress *entities.UserEducationProgress) error
	LogEducationEvent(ctx context.Context, event *entities.EducationEvent) error
}

// AIService interface for AI-powered guidance
type AIService interface {
	GeneratePersonalizedAdvice(ctx context.Context, userID uuid.UUID, context string) (*entities.AIAdvice, error)
	AnalyzeRiskProfile(ctx context.Context, userID uuid.UUID) (*entities.RiskAnalysis, error)
}

// UserRepository interface for user operations
type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
}

// NewService creates a new education service
func NewService(
	educationRepo EducationRepository,
	aiService AIService,
	userRepo UserRepository,
	logger *logger.Logger,
) *Service {
	return &Service{
		educationRepo: educationRepo,
		aiService:     aiService,
		userRepo:      userRepo,
		logger:        logger,
	}
}

// GetRiskWarnings returns contextual risk warnings for a user action
func (s *Service) GetRiskWarnings(ctx context.Context, userID uuid.UUID, actionType string, context map[string]interface{}) ([]*entities.RiskWarning, error) {
	warnings := []*entities.RiskWarning{}

	// Get user information
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Get user's risk profile
	riskAnalysis, err := s.aiService.AnalyzeRiskProfile(ctx, userID)
	if err != nil {
		s.logger.Warnw("Failed to get risk analysis", "error", err, "user_id", userID)
		// Continue without AI analysis
	}

	// Generate contextual warnings based on action type
	switch actionType {
	case "large_withdrawal":
		warnings = append(warnings, s.getWithdrawalWarnings(context, riskAnalysis)...)
	case "new_investment":
		warnings = append(warnings, s.getInvestmentWarnings(context, riskAnalysis)...)
	case "wallet_connection":
		warnings = append(warnings, s.getWalletWarnings(context)...)
	case "high_value_transaction":
		warnings = append(warnings, s.getHighValueWarnings(context, riskAnalysis)...)
	}

	// Add general Web3 risks if user is new
	if user.CreatedAt.After(time.Now().Add(-30 * 24 * time.Hour)) { // New user (< 30 days)
		warnings = append(warnings, s.getNewUserWarnings()...)
	}

	// Log education event
	s.logEducationEvent(ctx, userID, "risk_warnings_displayed", map[string]interface{}{
		"action_type":   actionType,
		"warning_count": len(warnings),
	})

	return warnings, nil
}

// GetPersonalizedAdvice provides AI-powered personalized advice
func (s *Service) GetPersonalizedAdvice(ctx context.Context, userID uuid.UUID, context string) (*entities.AIAdvice, error) {
	advice, err := s.aiService.GeneratePersonalizedAdvice(ctx, userID, context)
	if err != nil {
		s.logger.Errorw("Failed to generate AI advice", "error", err, "user_id", userID)
		return nil, err
	}

	// Log education event
	s.logEducationEvent(ctx, userID, "ai_advice_received", map[string]interface{}{
		"advice_type": advice.AdviceType,
		"context":     context,
	})

	return advice, nil
}

// GetEducationalContent returns educational content for a user
func (s *Service) GetEducationalContent(ctx context.Context, userID uuid.UUID, contentType string) ([]*entities.EducationalContent, error) {
	// Get user's progress to determine appropriate content
	progress, err := s.educationRepo.GetUserProgress(ctx, userID)
	if err != nil {
		s.logger.Warnw("Failed to get user progress", "error", err, "user_id", userID)
		// Default to beginner level
	}

	riskLevel := "beginner"
	if progress != nil && progress.CompletedModules > 5 {
		riskLevel = "intermediate"
	}
	if progress != nil && progress.CompletedModules > 10 {
		riskLevel = "advanced"
	}

	content, err := s.educationRepo.GetEducationalContent(ctx, contentType, riskLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to get educational content: %w", err)
	}

	return content, nil
}

// UpdateUserProgress updates a user's education progress
func (s *Service) UpdateUserProgress(ctx context.Context, userID uuid.UUID, moduleID string, completed bool) error {
	progress, err := s.educationRepo.GetUserProgress(ctx, userID)
	if err != nil {
		// Create new progress record
		progress = &entities.UserEducationProgress{
			UserID:           userID,
			CompletedModules: 0,
			TotalModules:     15, // Assume 15 total modules
			CurrentStreak:    0,
			LongestStreak:    0,
			LastActivityAt:   time.Now(),
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}
	}

	if completed {
		progress.CompletedModules++
		if progress.CompletedModules > progress.TotalModules {
			progress.TotalModules = progress.CompletedModules
		}
		progress.CurrentStreak++
		if progress.CurrentStreak > progress.LongestStreak {
			progress.LongestStreak = progress.CurrentStreak
		}
	} else {
		progress.CurrentStreak = 0 // Reset streak on failure
	}

	progress.LastActivityAt = time.Now()
	progress.UpdatedAt = time.Now()

	if err := s.educationRepo.UpdateUserProgress(ctx, progress); err != nil {
		return fmt.Errorf("failed to update progress: %w", err)
	}

	// Log education event
	s.logEducationEvent(ctx, userID, "module_completed", map[string]interface{}{
		"module_id":       moduleID,
		"completed":       completed,
		"total_completed": progress.CompletedModules,
	})

	return nil
}

// GetUserProgress returns a user's education progress
func (s *Service) GetUserProgress(ctx context.Context, userID uuid.UUID) (*entities.UserEducationProgress, error) {
	return s.educationRepo.GetUserProgress(ctx, userID)
}

// getWithdrawalWarnings generates withdrawal-specific risk warnings
func (s *Service) getWithdrawalWarnings(context map[string]interface{}, riskAnalysis *entities.RiskAnalysis) []*entities.RiskWarning {
	warnings := []*entities.RiskWarning{}

	// Check withdrawal amount
	if amount, ok := context["amount"].(float64); ok && amount > 1000 {
		warnings = append(warnings, &entities.RiskWarning{
			WarningType:       "high_value_withdrawal",
			Severity:          "high",
			Title:             "Large Withdrawal Detected",
			Message:           "You're withdrawing a significant amount. Ensure the destination address is correct to avoid permanent loss.",
			RecommendedAction: "Double-check the recipient address and consider withdrawing in smaller amounts.",
			LearnMoreURL:      "/education/wallet-security",
		})
	}

	// Check if user is new to crypto
	if riskAnalysis != nil && riskAnalysis.RiskLevel == "high" {
		warnings = append(warnings, &entities.RiskWarning{
			WarningType:       "new_user_risk",
			Severity:          "medium",
			Title:             "First Large Transaction",
			Message:           "This appears to be one of your larger transactions. Web3 transactions are irreversible.",
			RecommendedAction: "Take time to review and consider starting with smaller amounts.",
			LearnMoreURL:      "/education/irreversible-transactions",
		})
	}

	// General withdrawal warnings
	warnings = append(warnings, &entities.RiskWarning{
		WarningType:       "general_withdrawal",
		Severity:          "low",
		Title:             "Transaction Confirmation",
		Message:           "Once confirmed, this transaction cannot be reversed. Ensure all details are correct.",
		RecommendedAction: "Verify recipient address and network selection.",
		LearnMoreURL:      "/education/transaction-safety",
	})

	return warnings
}

// getInvestmentWarnings generates investment-specific risk warnings
func (s *Service) getInvestmentWarnings(context map[string]interface{}, riskAnalysis *entities.RiskAnalysis) []*entities.RiskWarning {
	warnings := []*entities.RiskWarning{}

	if riskAnalysis != nil && riskAnalysis.Concerns != nil {
		for _, concern := range riskAnalysis.Concerns {
			if concern.Type == "over_concentration" {
				warnings = append(warnings, &entities.RiskWarning{
					WarningType:       "portfolio_concentration",
					Severity:          "medium",
					Title:             "Portfolio Concentration Risk",
					Message:           "Your portfolio is heavily concentrated in one asset. Consider diversification.",
					RecommendedAction: "Review your portfolio allocation and consider spreading investments.",
					LearnMoreURL:      "/education/portfolio-diversification",
				})
			}
		}
	}

	// Market volatility warning
	warnings = append(warnings, &entities.RiskWarning{
		WarningType:       "market_volatility",
		Severity:          "low",
		Title:             "Market Volatility",
		Message:           "Cryptocurrency markets can be highly volatile. Prices can change rapidly.",
		RecommendedAction: "Only invest what you can afford to lose.",
		LearnMoreURL:      "/education/market-volatility",
	})

	return warnings
}

// getWalletWarnings generates wallet connection warnings
func (s *Service) getWalletWarnings(context map[string]interface{}) []*entities.RiskWarning {
	return []*entities.RiskWarning{
		{
			WarningType:       "wallet_security",
			Severity:          "high",
			Title:             "Wallet Security Critical",
			Message:           "Never share your private keys or seed phrase. STACK manages custody for your security.",
			RecommendedAction: "Enable all available security features and use strong passwords.",
			LearnMoreURL:      "/education/wallet-security",
		},
		{
			WarningType:       "phishing_awareness",
			Severity:          "high",
			Title:             "Beware of Phishing",
			Message:           "Only connect to verified dApps and websites. Check URLs carefully.",
			RecommendedAction: "Bookmark official sites and enable browser security features.",
			LearnMoreURL:      "/education/phishing-protection",
		},
	}
}

// getHighValueWarnings generates warnings for high-value transactions
func (s *Service) getHighValueWarnings(context map[string]interface{}, riskAnalysis *entities.RiskAnalysis) []*entities.RiskWarning {
	warnings := []*entities.RiskWarning{}

	warnings = append(warnings, &entities.RiskWarning{
		WarningType:       "high_value_transaction",
		Severity:          "high",
		Title:             "High-Value Transaction",
		Message:           "This transaction involves significant value. Take extra care with verification.",
		RecommendedAction: "Consider breaking large transactions into smaller ones for safety.",
		LearnMoreURL:      "/education/large-transactions",
	})

	if riskAnalysis != nil && riskAnalysis.OverallRisk > 7 {
		warnings = append(warnings, &entities.RiskWarning{
			WarningType:       "risk_profile_warning",
			Severity:          "high",
			Title:             "Risk Profile Alert",
			Message:           "Your current risk profile suggests caution with high-value transactions.",
			RecommendedAction: "Review your risk tolerance and consider consulting with a financial advisor.",
			LearnMoreURL:      "/education/risk-assessment",
		})
	}

	return warnings
}

// getNewUserWarnings generates warnings for new users
func (s *Service) getNewUserWarnings() []*entities.RiskWarning {
	return []*entities.RiskWarning{
		{
			WarningType:       "new_user_education",
			Severity:          "medium",
			Title:             "Welcome to Web3",
			Message:           "Web3 transactions are different from traditional banking. Take time to learn.",
			RecommendedAction: "Complete our educational modules before making significant transactions.",
			LearnMoreURL:      "/education/getting-started",
		},
		{
			WarningType:       "learning_opportunity",
			Severity:          "low",
			Title:             "Learn as You Go",
			Message:           "Consider starting with small amounts to learn how Web3 works.",
			RecommendedAction: "Use our education center to build knowledge and confidence.",
			LearnMoreURL:      "/education",
		},
	}
}

// logEducationEvent logs an education-related event
func (s *Service) logEducationEvent(ctx context.Context, userID uuid.UUID, eventType string, details map[string]interface{}) {
	event := &entities.EducationEvent{
		UserID:    userID,
		EventType: eventType,
		Details:   details,
		CreatedAt: time.Now(),
	}

	if err := s.educationRepo.LogEducationEvent(ctx, event); err != nil {
		s.logger.Warnw("Failed to log education event", "error", err, "user_id", userID, "event_type", eventType)
	}
}

// GetEducationStats returns education system statistics
func (s *Service) GetEducationStats(ctx context.Context) (*entities.EducationStats, error) {
	// This would aggregate education data from the repository
	// For now, return mock stats
	return &entities.EducationStats{
		TotalUsers:            10000,
		ActiveLearners:        2500,
		CompletedModules:      15000,
		AverageCompletionRate: 0.75,
		PopularTopics:         []string{"wallet-security", "phishing-protection", "portfolio-diversification"},
		LastUpdated:           time.Now(),
	}, nil
}
