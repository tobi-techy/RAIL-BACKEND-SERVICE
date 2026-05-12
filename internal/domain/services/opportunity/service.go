package opportunity

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/superteam"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type Service struct {
	repo            *repositories.OpportunityRepository
	superteamClient *superteam.Client
	logger          *zap.Logger
}

func NewService(repo *repositories.OpportunityRepository, superteamClient *superteam.Client, logger *zap.Logger) *Service {
	return &Service{repo: repo, superteamClient: superteamClient, logger: logger}
}

// SyncFromSuperteam fetches all listing types and upserts them.
func (s *Service) SyncFromSuperteam(ctx context.Context) error {
	source, err := s.repo.GetSourceByName(ctx, "superteam_earn")
	if err != nil {
		return fmt.Errorf("get source: %w", err)
	}

	var allListings []superteam.Listing
	for _, t := range []string{"bounty", "project", "hackathon"} {
		listings, err := s.superteamClient.FetchListings(ctx, t, 50)
		if err != nil {
			s.logger.Warn("Failed to fetch listings", zap.String("type", t), zap.Error(err))
			continue
		}
		allListings = append(allListings, listings...)
	}

	ingested := 0
	for _, l := range allListings {
		entity := s.convertListing(source.ID, l)
		if err := s.repo.UpsertListing(ctx, entity); err != nil {
			s.logger.Warn("Failed to upsert listing", zap.String("title", l.Title), zap.Error(err))
			continue
		}
		ingested++
	}

	// Expire old listings
	expired, _ := s.repo.ExpireOldListings(ctx)

	_ = s.repo.UpdateSourceLastSynced(ctx, source.ID)
	s.logger.Info("Superteam sync complete", zap.Int("ingested", ingested), zap.Int64("expired", expired))
	return nil
}

// GenerateWeeklyRecommendations scores all open listings for a user and picks top 3.
func (s *Service) GenerateWeeklyRecommendations(ctx context.Context, userID uuid.UUID) error {
	profile, err := s.repo.GetUserProfile(ctx, userID)
	if err != nil {
		// No profile = use empty defaults
		profile = &entities.UserOpportunityProfile{
			UserID:       userID,
			Skills:       pq.StringArray{},
			Interests:    pq.StringArray{},
			HoursPerWeek: 5,
		}
	}

	listings, err := s.repo.GetOpenListings(ctx, 100)
	if err != nil {
		return fmt.Errorf("get open listings: %w", err)
	}

	// Get hidden listings to exclude
	hiddenIDs, _ := s.repo.GetHiddenListingIDs(ctx, userID)
	hiddenSet := make(map[uuid.UUID]bool, len(hiddenIDs))
	for _, id := range hiddenIDs {
		hiddenSet[id] = true
	}

	// Score each listing
	type scored struct {
		listing *entities.OpportunityListing
		score   decimal.Decimal
	}
	var candidates []scored
	for _, l := range listings {
		if hiddenSet[l.ID] {
			continue
		}
		score := s.scoreListing(l, profile)
		candidates = append(candidates, scored{listing: l, score: score})
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score.GreaterThan(candidates[j].score)
	})

	// Pick top 3
	weekStart := currentWeekStart()
	top := 3
	if len(candidates) < top {
		top = len(candidates)
	}

	for i := 0; i < top; i++ {
		rank := i + 1
		explanation := s.buildExplanation(candidates[i].listing, profile, candidates[i].score)
		match := &entities.OpportunityMatch{
			ID:          uuid.New(),
			UserID:      userID,
			ListingID:   candidates[i].listing.ID,
			FitScore:    candidates[i].score,
			WeekStart:   weekStart,
			Rank:        &rank,
			Explanation: &explanation,
			Status:      entities.OpportunityStatusRecommended,
			CreatedAt:   time.Now().UTC(),
		}
		if err := s.repo.UpsertMatch(ctx, match); err != nil {
			s.logger.Warn("Failed to upsert match", zap.Error(err))
		}
	}

	return nil
}

// GetWeeklyRecommendations returns the current week's top 3 for a user.
func (s *Service) GetWeeklyRecommendations(ctx context.Context, userID uuid.UUID) ([]*entities.OpportunityMatch, error) {
	return s.repo.GetWeeklyRecommendations(ctx, userID, currentWeekStart())
}

// GetListing returns a single listing by ID.
func (s *Service) GetListing(ctx context.Context, id uuid.UUID) (*entities.OpportunityListing, error) {
	return s.repo.GetListingByID(ctx, id)
}

// SaveOpportunity marks an opportunity as saved.
func (s *Service) SaveOpportunity(ctx context.Context, userID, listingID uuid.UUID) error {
	return s.recordFeedback(ctx, userID, listingID, entities.OpportunityFeedbackSaved)
}

// HideOpportunity marks an opportunity as hidden.
func (s *Service) HideOpportunity(ctx context.Context, userID, listingID uuid.UUID) error {
	return s.recordFeedback(ctx, userID, listingID, entities.OpportunityFeedbackHidden)
}

// UpdateProfile creates or updates a user's opportunity profile.
func (s *Service) UpdateProfile(ctx context.Context, p *entities.UserOpportunityProfile) error {
	return s.repo.UpsertUserProfile(ctx, p)
}

// GetProfile returns a user's opportunity profile.
func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (*entities.UserOpportunityProfile, error) {
	return s.repo.GetUserProfile(ctx, userID)
}

// --- Scoring ---

func (s *Service) scoreListing(l *entities.OpportunityListing, profile *entities.UserOpportunityProfile) decimal.Decimal {
	score := decimal.Zero

	// 30% skill match
	skillScore := s.skillMatchScore(l.Skills, profile.Skills)
	score = score.Add(skillScore.Mul(decimal.NewFromInt(30)))

	// 20% interest/type match
	typeScore := s.typeMatchScore(l.Type, profile.PreferredTypes)
	score = score.Add(typeScore.Mul(decimal.NewFromInt(20)))

	// 20% reward quality (higher reward = better, capped)
	rewardScore := s.rewardScore(l.RewardAmount, profile.MinReward)
	score = score.Add(rewardScore.Mul(decimal.NewFromInt(20)))

	// 15% deadline urgency (closer deadline with enough time = better)
	deadlineScore := s.deadlineScore(l.Deadline)
	score = score.Add(deadlineScore.Mul(decimal.NewFromInt(15)))

	// 15% sponsor quality / freshness
	freshnessScore := s.freshnessScore(l.CreatedAt)
	score = score.Add(freshnessScore.Mul(decimal.NewFromInt(15)))

	return score.Round(2)
}

func (s *Service) skillMatchScore(listingSkills, userSkills pq.StringArray) decimal.Decimal {
	if len(listingSkills) == 0 || len(userSkills) == 0 {
		return decimal.NewFromFloat(0.5) // neutral if no data
	}
	matches := 0
	for _, ls := range listingSkills {
		for _, us := range userSkills {
			if strings.EqualFold(ls, us) {
				matches++
				break
			}
		}
	}
	if matches == 0 {
		return decimal.NewFromFloat(0.2)
	}
	ratio := float64(matches) / float64(len(listingSkills))
	return decimal.NewFromFloat(ratio)
}

func (s *Service) typeMatchScore(listingType string, preferredTypes pq.StringArray) decimal.Decimal {
	if len(preferredTypes) == 0 {
		return decimal.NewFromFloat(0.5)
	}
	for _, pt := range preferredTypes {
		if strings.EqualFold(pt, listingType) {
			return decimal.NewFromInt(1)
		}
	}
	return decimal.NewFromFloat(0.3)
}

func (s *Service) rewardScore(reward, minReward decimal.Decimal) decimal.Decimal {
	if reward.IsZero() {
		return decimal.NewFromFloat(0.3)
	}
	if reward.LessThan(minReward) {
		return decimal.NewFromFloat(0.1)
	}
	// Scale: $100=0.5, $500=0.8, $1000+=1.0
	r, _ := reward.Float64()
	switch {
	case r >= 1000:
		return decimal.NewFromInt(1)
	case r >= 500:
		return decimal.NewFromFloat(0.8)
	case r >= 100:
		return decimal.NewFromFloat(0.6)
	default:
		return decimal.NewFromFloat(0.4)
	}
}

func (s *Service) deadlineScore(deadline *time.Time) decimal.Decimal {
	if deadline == nil {
		return decimal.NewFromFloat(0.5) // no deadline = neutral
	}
	daysLeft := time.Until(*deadline).Hours() / 24
	switch {
	case daysLeft < 1:
		return decimal.NewFromFloat(0.1) // too late
	case daysLeft < 3:
		return decimal.NewFromFloat(0.6) // urgent but doable
	case daysLeft < 7:
		return decimal.NewFromFloat(1.0) // sweet spot
	case daysLeft < 14:
		return decimal.NewFromFloat(0.8)
	default:
		return decimal.NewFromFloat(0.5) // far out
	}
}

func (s *Service) freshnessScore(createdAt time.Time) decimal.Decimal {
	age := time.Since(createdAt).Hours() / 24
	switch {
	case age < 2:
		return decimal.NewFromInt(1)
	case age < 7:
		return decimal.NewFromFloat(0.8)
	case age < 14:
		return decimal.NewFromFloat(0.5)
	default:
		return decimal.NewFromFloat(0.3)
	}
}

func (s *Service) buildExplanation(l *entities.OpportunityListing, profile *entities.UserOpportunityProfile, score decimal.Decimal) string {
	parts := []string{}

	if len(l.Skills) > 0 && len(profile.Skills) > 0 {
		matched := []string{}
		for _, ls := range l.Skills {
			for _, us := range profile.Skills {
				if strings.EqualFold(ls, us) {
					matched = append(matched, ls)
				}
			}
		}
		if len(matched) > 0 {
			parts = append(parts, fmt.Sprintf("Matches your skills: %s", strings.Join(matched, ", ")))
		}
	}

	if !l.RewardAmount.IsZero() {
		parts = append(parts, fmt.Sprintf("Pays %s %s", l.RewardAmount.StringFixed(0), l.RewardCurrency))
	}

	if l.Deadline != nil {
		days := int(time.Until(*l.Deadline).Hours() / 24)
		if days > 0 {
			parts = append(parts, fmt.Sprintf("Deadline in %d days", days))
		}
	}

	parts = append(parts, fmt.Sprintf("Fit score: %s/100", score.StringFixed(0)))

	if len(parts) == 0 {
		return "Good opportunity based on your profile"
	}
	return strings.Join(parts, ". ") + "."
}

// --- Helpers ---

func (s *Service) recordFeedback(ctx context.Context, userID, listingID uuid.UUID, action string) error {
	f := &entities.OpportunityFeedback{
		ID:        uuid.New(),
		UserID:    userID,
		ListingID: listingID,
		Action:    action,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.CreateFeedback(ctx, f); err != nil {
		return err
	}
	// Also update match status
	status := entities.OpportunityStatusSaved
	if action == entities.OpportunityFeedbackHidden || action == entities.OpportunityFeedbackNotInterested {
		status = entities.OpportunityStatusHidden
	} else if action == entities.OpportunityFeedbackApplied {
		status = entities.OpportunityStatusApplied
	}
	_ = s.repo.UpdateMatchStatus(ctx, userID, listingID, status)
	return nil
}

func (s *Service) convertListing(sourceID uuid.UUID, l superteam.Listing) *entities.OpportunityListing {
	now := time.Now().UTC()

	skills := make(pq.StringArray, 0, len(l.Skills))
	for _, sk := range l.Skills {
		if sk.Skills != "" {
			skills = append(skills, sk.Skills)
		}
	}

	reward := decimal.Zero
	if l.USDValue != nil {
		reward = decimal.NewFromFloat(*l.USDValue)
	} else if l.RewardAmount != nil {
		reward = decimal.NewFromFloat(*l.RewardAmount)
	}

	currency := "USDC"
	if l.Token != "" {
		currency = l.Token
	}

	var deadline *time.Time
	if l.Deadline != nil && *l.Deadline != "" {
		if t, err := time.Parse(time.RFC3339, *l.Deadline); err == nil {
			deadline = &t
		}
	}

	var sponsor *string
	if l.Sponsor != nil && l.Sponsor.Name != "" {
		sponsor = &l.Sponsor.Name
	}

	slug := l.Slug
	url := fmt.Sprintf("https://earn.superteam.fun/listings/%s/%s", l.Type, l.Slug)

	var rawJSON []byte
	if l.Raw != nil {
		rawJSON, _ = json.Marshal(l.Raw)
	}

	agentAccess := l.AgentAccess

	return &entities.OpportunityListing{
		ID:             uuid.New(),
		SourceID:       sourceID,
		ExternalID:     l.ID,
		Slug:           &slug,
		Title:          l.Title,
		Description:    &l.Description,
		Type:           l.Type,
		Skills:         skills,
		RewardAmount:   reward,
		RewardCurrency: currency,
		Deadline:       deadline,
		Sponsor:        sponsor,
		URL:            url,
		Remote:         true,
		Status:         "open",
		AgentAccess:    &agentAccess,
		RawJSON:        rawJSON,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func currentWeekStart() time.Time {
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	monday := now.AddDate(0, 0, -(weekday - 1))
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)
}
