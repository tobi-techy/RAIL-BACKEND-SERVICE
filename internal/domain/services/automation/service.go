package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BalanceProvider fetches user balances for threshold checks.
type BalanceProvider interface {
	GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// TransferExecutor executes stash<->spend transfers.
type TransferExecutor interface {
	TransferBetweenStashes(ctx context.Context, userID uuid.UUID, from, to string, amount decimal.Decimal) error
}

// Service manages automation CRUD and execution.
type Service struct {
	repo     *repositories.AutomationRepository
	balance  BalanceProvider
	transfer TransferExecutor
	logger   *zap.Logger
}

func NewService(repo *repositories.AutomationRepository, balance BalanceProvider, transfer TransferExecutor, logger *zap.Logger) *Service {
	return &Service{repo: repo, balance: balance, transfer: transfer, logger: logger}
}

// Create creates a new automation rule.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, req *CreateAutomationRequest) (*entities.MiriamAutomation, error) {
	triggerConfig, _ := json.Marshal(req.TriggerConfig)
	actionConfig, _ := json.Marshal(req.ActionConfig)

	a := &entities.MiriamAutomation{
		ID:               uuid.New(),
		UserID:           userID,
		Name:             req.Name,
		Description:      req.Description,
		TriggerType:      req.TriggerType,
		TriggerConfig:    triggerConfig,
		ActionType:       req.ActionType,
		ActionConfig:     actionConfig,
		IsActive:         true,
		MaxTriggersPerDay: coalesce(req.MaxTriggersPerDay, 3),
		CooldownMinutes:  coalesce(req.CooldownMinutes, 60),
	}

	if err := s.repo.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("create automation: %w", err)
	}
	return a, nil
}

// List returns all automations for a user.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error) {
	return s.repo.ListByUser(ctx, userID)
}

// Get returns a single automation.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*entities.MiriamAutomation, error) {
	return s.repo.GetByID(ctx, userID, id)
}

// Update modifies an automation.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, req *UpdateAutomationRequest) (*entities.MiriamAutomation, error) {
	a, err := s.repo.GetByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		a.Name = *req.Name
	}
	if req.Description != nil {
		a.Description = req.Description
	}
	if req.IsActive != nil {
		a.IsActive = *req.IsActive
	}
	if req.TriggerConfig != nil {
		tc, _ := json.Marshal(req.TriggerConfig)
		a.TriggerConfig = tc
	}
	if req.ActionConfig != nil {
		ac, _ := json.Marshal(req.ActionConfig)
		a.ActionConfig = ac
	}
	if err := s.repo.Update(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// Delete removes an automation.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return s.repo.Delete(ctx, userID, id)
}

// GetLogs returns execution history.
func (s *Service) GetLogs(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamAutomationLog, error) {
	return s.repo.GetLogs(ctx, userID, limit)
}

// EvaluateScheduled checks all schedule-based automations and executes eligible ones.
func (s *Service) EvaluateScheduled(ctx context.Context) error {
	automations, err := s.repo.ListActiveByTrigger(ctx, entities.TriggerSchedule)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, a := range automations {
		if !s.shouldTrigger(ctx, &a, now) {
			continue
		}
		go s.execute(context.Background(), &a)
	}
	return nil
}

// EvaluateBalanceThresholds checks balance-triggered automations.
func (s *Service) EvaluateBalanceThresholds(ctx context.Context, userID uuid.UUID) error {
	automations, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return err
	}

	for _, a := range automations {
		if !a.IsActive || a.TriggerType != entities.TriggerBalanceThreshold {
			continue
		}
		if s.checkBalanceThreshold(ctx, &a) {
			go s.execute(context.Background(), &a)
		}
	}
	return nil
}

func (s *Service) shouldTrigger(ctx context.Context, a *entities.MiriamAutomation, now time.Time) bool {
	// Cooldown check
	if a.LastTriggeredAt != nil && now.Sub(*a.LastTriggeredAt) < time.Duration(a.CooldownMinutes)*time.Minute {
		return false
	}
	// Daily limit check
	count, err := s.repo.GetTodayTriggerCount(ctx, a.ID)
	if err == nil && count >= a.MaxTriggersPerDay {
		return false
	}

	// Parse schedule config
	var cfg entities.ScheduleTriggerConfig
	json.Unmarshal(a.TriggerConfig, &cfg)

	// Simple weekday + hour matching
	if len(cfg.Weekdays) > 0 {
		todayWeekday := int(now.Weekday())
		matched := false
		for _, wd := range cfg.Weekdays {
			if wd == todayWeekday {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if cfg.Hour > 0 && now.Hour() != cfg.Hour {
		return false
	}
	return true
}

func (s *Service) checkBalanceThreshold(ctx context.Context, a *entities.MiriamAutomation) bool {
	var cfg entities.BalanceThresholdConfig
	json.Unmarshal(a.TriggerConfig, &cfg)

	var balance decimal.Decimal
	var err error
	if cfg.Wallet == "spend" {
		balance, err = s.balance.GetSpendBalance(ctx, a.UserID)
	} else {
		balance, err = s.balance.GetStashBalance(ctx, a.UserID)
	}
	if err != nil {
		return false
	}

	threshold := decimal.NewFromFloat(cfg.Threshold)
	switch cfg.Operator {
	case "below":
		return balance.LessThan(threshold)
	case "above":
		return balance.GreaterThan(threshold)
	}
	return false
}

func (s *Service) execute(ctx context.Context, a *entities.MiriamAutomation) {
	log := &entities.MiriamAutomationLog{
		ID:           uuid.New(),
		AutomationID: a.ID,
		UserID:       a.UserID,
		ExecutedAt:   time.Now().UTC(),
	}

	err := s.executeAction(ctx, a)
	if err != nil {
		log.Status = "failed"
		errMsg := err.Error()
		log.ErrorMessage = &errMsg
		s.logger.Warn("automation execution failed", zap.String("id", a.ID.String()), zap.Error(err))
	} else {
		log.Status = "success"
		s.repo.MarkTriggered(ctx, a.ID)
	}

	s.repo.LogExecution(ctx, log)
}

func (s *Service) executeAction(ctx context.Context, a *entities.MiriamAutomation) error {
	var cfg entities.TransferActionConfig
	json.Unmarshal(a.ActionConfig, &cfg)

	switch a.ActionType {
	case entities.ActionTransferToStash:
		amount := decimal.NewFromFloat(cfg.Amount)
		return s.transfer.TransferBetweenStashes(ctx, a.UserID, "spend", "stash", amount)
	case entities.ActionTransferToSpend:
		amount := decimal.NewFromFloat(cfg.Amount)
		return s.transfer.TransferBetweenStashes(ctx, a.UserID, "stash", "spend", amount)
	case entities.ActionNotify:
		// Notification-only automations are logged but don't execute transfers
		return nil
	default:
		return fmt.Errorf("unsupported action type: %s", a.ActionType)
	}
}

// --- Request types ---

type CreateAutomationRequest struct {
	Name             string                 `json:"name" binding:"required"`
	Description      *string                `json:"description"`
	TriggerType      string                 `json:"trigger_type" binding:"required"`
	TriggerConfig    map[string]interface{} `json:"trigger_config" binding:"required"`
	ActionType       string                 `json:"action_type" binding:"required"`
	ActionConfig     map[string]interface{} `json:"action_config" binding:"required"`
	MaxTriggersPerDay int                   `json:"max_triggers_per_day"`
	CooldownMinutes  int                    `json:"cooldown_minutes"`
}

type UpdateAutomationRequest struct {
	Name          *string                `json:"name"`
	Description   *string                `json:"description"`
	IsActive      *bool                  `json:"is_active"`
	TriggerConfig map[string]interface{} `json:"trigger_config"`
	ActionConfig  map[string]interface{} `json:"action_config"`
}

func coalesce(val, def int) int {
	if val > 0 {
		return val
	}
	return def
}
