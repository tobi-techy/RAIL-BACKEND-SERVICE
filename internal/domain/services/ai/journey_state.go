package ai

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// Journey objectives: soft, conversational goals the backend tracks so Miriam
// always knows what this relationship is trying to accomplish next. Users never
// see these; they only shape the context Miriam reasons over.
const (
	JourneyObjectiveStory   = "hear_their_money_story" // learn what their money is actually for
	JourneyObjectivePicture = "see_the_real_picture"   // get eyes on their real cash flow
	JourneyObjectiveAha     = "deliver_first_insight"  // turn raw data into one genuine "oh" moment
	JourneyObjectivePath    = "name_the_path"          // diagnose which Freedom Step they're on
	JourneyObjectiveDeposit = "first_deposit"          // earn the right to ask, then ask
	JourneyObjectiveHabit   = "build_the_habit"        // post-funding reinforcement
)

// Fact keys stored in JourneyState.Facts. Every fact carries provenance so
// Miriam can distinguish hard data from things mentioned in passing.
const (
	JourneyFactName       = "name"
	JourneyFactMotivation = "motivation"
	JourneyFactIncome     = "income"
)

// Journey milestones mark funnel progression for Time-to-First-Value
// measurement. Keys map 1:1 to prometheus labels.
const (
	MilestoneGoalCaptured      = "goal_captured"
	MilestoneBankLinked        = "bank_linked"
	MilestoneStatementAnalyzed = "statement_analyzed"
	MilestoneFirstDeposit      = "first_deposit"
	MilestoneThirdDeposit      = "third_deposit"
	MilestoneAutomationCreated = "automation_created"
)

// Journey fact sources: where a piece of knowledge came from.
const (
	FactSourceSignup    = "signup"
	FactSourceChat      = "chat"
	FactSourceGoalTool  = "set_savings_goal"
	FactSourceMono      = "mono"
	FactSourceStatement = "statement"
)

// journeyStateTTL is how long journey state survives without activity.
// Long enough for dormant signups; short enough to self-clean abandoned ones.
const journeyStateTTL = 180 * 24 * time.Hour

// JourneyFact is a single thing Miriam's backend knows about the user, with
// provenance. The LLM never invents these values; it only reads them.
type JourneyFact struct {
	Value      string    `json:"value"`
	Source     string    `json:"source"`
	Confidence float64   `json:"confidence"`
	ObservedAt time.Time `json:"observed_at"`
}

// JourneyState is the durable record of where this user is in their Rail
// journey: which objective is active, what Miriam already knows (so she never
// re-asks), and which milestones have been reached. Backend-owned truth; the
// LLM cannot write to it except through tool calls that persist facts.
type JourneyState struct {
	UserID           string                 `json:"user_id"`
	CurrentObjective string                 `json:"current_objective"`
	Facts            map[string]JourneyFact `json:"facts,omitempty"`
	Milestones       map[string]time.Time   `json:"milestones,omitempty"`
	LastDepositCount int                    `json:"last_deposit_count,omitempty"`
	TurnCount        int                    `json:"turn_count,omitempty"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

// SetFact records or upgrades a fact. An existing fact wins if its confidence
// is higher and the new source isn't stronger (hard data > stated intent).
func (s *JourneyState) SetFact(key, value, source string, confidence float64) {
	if value == "" {
		return
	}
	if s.Facts == nil {
		s.Facts = make(map[string]JourneyFact)
	}
	sourceRank := journeySourceRank(source)
	if existing, ok := s.Facts[key]; ok {
		existingRank := journeySourceRank(existing.Source)
		if existingRank > sourceRank || (existingRank == sourceRank && existing.Confidence >= confidence) {
			return
		}
	}
	s.Facts[key] = JourneyFact{
		Value:      value,
		Source:     source,
		Confidence: confidence,
		ObservedAt: time.Now().UTC(),
	}
}

// HasFact reports whether a fact key is already known.
func (s *JourneyState) HasFact(key string) bool {
	_, ok := s.Facts[key]
	return ok
}

// ReachMilestone records a milestone timestamp. Returns true if it was newly
// reached (callers use this to fire metrics exactly once).
func (s *JourneyState) ReachMilestone(name string) bool {
	if s.Milestones == nil {
		s.Milestones = make(map[string]time.Time)
	}
	if _, ok := s.Milestones[name]; ok {
		return false
	}
	s.Milestones[name] = time.Now().UTC()
	return true
}

// HasMilestone reports whether a milestone has been reached.
func (s *JourneyState) HasMilestone(name string) bool {
	_, ok := s.Milestones[name]
	return ok
}

// journeySourceRank orders fact sources by trustworthiness. Hard financial
// data beats tool-persisted statements, which beat casual chat mentions.
func journeySourceRank(source string) int {
	switch source {
	case FactSourceStatement, FactSourceMono:
		return 3
	case FactSourceGoalTool:
		return 2
	case FactSourceChat:
		return 1
	case FactSourceSignup:
		return 1
	default:
		return 0
	}
}

// JourneyStore persists cross-session journey state.
type JourneyStore interface {
	Get(ctx context.Context, userID uuid.UUID) (*JourneyState, error)
	Save(ctx context.Context, state *JourneyState) error
}

const journeyKeyPrefix = "miriam_journey:"

// RedisJourneyStore stores journey state in Redis with a long TTL so a user
// who disappears mid-onboarding picks up exactly where they left off.
type RedisJourneyStore struct {
	redis  cache.RedisClient
	logger *zap.Logger
}

// NewRedisJourneyStore creates a Redis-backed journey store.
func NewRedisJourneyStore(redis cache.RedisClient, logger *zap.Logger) JourneyStore {
	return &RedisJourneyStore{redis: redis, logger: logger}
}

func (r *RedisJourneyStore) Get(ctx context.Context, userID uuid.UUID) (*JourneyState, error) {
	var state JourneyState
	if err := r.redis.Get(ctx, journeyKeyPrefix+userID.String(), &state); err != nil {
		return nil, err
	}
	state.UserID = userID.String()
	return &state, nil
}

func (r *RedisJourneyStore) Save(ctx context.Context, state *JourneyState) error {
	state.UpdatedAt = time.Now().UTC()
	return r.redis.Set(ctx, journeyKeyPrefix+state.UserID, state, journeyStateTTL)
}
