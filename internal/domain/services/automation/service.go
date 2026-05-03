package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const transferAutomationReauthorizationWindow = 90 * 24 * time.Hour

var ErrTransferAutomationReauthorizationRequired = errors.New("transfer automation requires passcode reauthorization")

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
	if err := validateCreateRequest(req); err != nil {
		return nil, err
	}
	triggerConfig, _ := json.Marshal(req.TriggerConfig)
	actionConfig, _ := json.Marshal(req.ActionConfig)

	a := &entities.MiriamAutomation{
		ID:                uuid.New(),
		UserID:            userID,
		Name:              req.Name,
		Description:       req.Description,
		TriggerType:       req.TriggerType,
		TriggerConfig:     triggerConfig,
		ActionType:        req.ActionType,
		ActionConfig:      actionConfig,
		IsActive:          true,
		MaxTriggersPerDay: coalesce(req.MaxTriggersPerDay, 3),
		CooldownMinutes:   coalesce(req.CooldownMinutes, 60),
	}

	if err := s.repo.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("create automation: %w", err)
	}
	return a, nil
}

func validateCreateRequest(req *CreateAutomationRequest) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		return fmt.Errorf("automation name must be between 1 and 200 characters")
	}
	if !validTrigger(req.TriggerType) {
		return fmt.Errorf("unsupported trigger type: %s", req.TriggerType)
	}
	if !validAction(req.ActionType) {
		return fmt.Errorf("unsupported action type: %s", req.ActionType)
	}
	if req.TriggerConfig == nil {
		return fmt.Errorf("trigger_config is required")
	}
	if req.ActionConfig == nil {
		return fmt.Errorf("action_config is required")
	}
	if req.MaxTriggersPerDay < 0 || req.MaxTriggersPerDay > 24 {
		return fmt.Errorf("max_triggers_per_day must be between 1 and 24")
	}
	if req.CooldownMinutes < 0 || req.CooldownMinutes > 10080 {
		return fmt.Errorf("cooldown_minutes must be between 1 and 10080")
	}
	if req.ActionType == entities.ActionTransferToStash || req.ActionType == entities.ActionTransferToSpend {
		amount, ok := numericConfig(req.ActionConfig, "amount")
		if !ok || amount <= 0 {
			return fmt.Errorf("transfer automation requires a positive action_config.amount")
		}
		if amount > 10000 {
			return fmt.Errorf("transfer automation amount exceeds maximum of 10000")
		}
		ack, _ := req.ActionConfig["acknowledged_future_transfer"].(bool)
		if !ack {
			return fmt.Errorf("transfer automation requires acknowledged_future_transfer consent")
		}
		if _, err := transferConsentWindow(req.ActionConfig, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func validTrigger(value string) bool {
	switch value {
	case entities.TriggerSchedule, entities.TriggerBalanceThreshold, entities.TriggerIncomeDetected, entities.TriggerSpendingSpike, entities.TriggerPayday, entities.TriggerCustom:
		return true
	default:
		return false
	}
}

func validAction(value string) bool {
	switch value {
	case entities.ActionTransferToStash, entities.ActionTransferToSpend, entities.ActionNotify, entities.ActionCustom:
		return true
	default:
		return false
	}
}

func numericConfig(config map[string]interface{}, key string) (float64, bool) {
	switch value := config[key].(type) {
	case float64:
		return value, true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
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
	if err := validateAutomation(a); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func validateAutomation(a *entities.MiriamAutomation) error {
	triggerConfig := map[string]interface{}{}
	actionConfig := map[string]interface{}{}
	_ = json.Unmarshal(a.TriggerConfig, &triggerConfig)
	_ = json.Unmarshal(a.ActionConfig, &actionConfig)
	return validateCreateRequest(&CreateAutomationRequest{
		Name:              a.Name,
		Description:       a.Description,
		TriggerType:       a.TriggerType,
		TriggerConfig:     triggerConfig,
		ActionType:        a.ActionType,
		ActionConfig:      actionConfig,
		MaxTriggersPerDay: a.MaxTriggersPerDay,
		CooldownMinutes:   a.CooldownMinutes,
	})
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

// EvaluateBalanceThresholds checks balance-triggered automations for a specific user.
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

// EvaluateAllBalanceThresholds checks balance-triggered automations across all users.
func (s *Service) EvaluateAllBalanceThresholds(ctx context.Context) error {
	automations, err := s.repo.ListActiveByTrigger(ctx, entities.TriggerBalanceThreshold)
	if err != nil {
		return err
	}
	for _, a := range automations {
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

	// Weekday matching
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
	// Hour matching — cfg.Hour==0 means "any hour" (zero value = not set due to omitempty)
	if cfg.Hour != 0 && now.Hour() != cfg.Hour {
		return false
	}
	// Prevent re-firing within the same hour if already triggered today
	if a.LastTriggeredAt != nil {
		sameHour := a.LastTriggeredAt.Year() == now.Year() &&
			a.LastTriggeredAt.YearDay() == now.YearDay() &&
			a.LastTriggeredAt.Hour() == now.Hour()
		if sameHour {
			return false
		}
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
		if errors.Is(err, ErrTransferAutomationReauthorizationRequired) {
			a.IsActive = false
			_ = s.repo.Update(ctx, a)
		}
	} else {
		log.Status = "success"
		s.repo.MarkTriggered(ctx, a.ID)
	}

	s.repo.LogExecution(ctx, log)
}

func (s *Service) executeAction(ctx context.Context, a *entities.MiriamAutomation) error {
	var cfg entities.TransferActionConfig
	var raw map[string]interface{}
	_ = json.Unmarshal(a.ActionConfig, &raw)
	json.Unmarshal(a.ActionConfig, &cfg)

	switch a.ActionType {
	case entities.ActionTransferToStash:
		if err := ensureTransferReauthorization(raw, time.Now().UTC()); err != nil {
			return err
		}
		amount := decimal.NewFromFloat(cfg.Amount)
		return s.transfer.TransferBetweenStashes(ctx, a.UserID, "spend", "stash", amount)
	case entities.ActionTransferToSpend:
		if err := ensureTransferReauthorization(raw, time.Now().UTC()); err != nil {
			return err
		}
		amount := decimal.NewFromFloat(cfg.Amount)
		return s.transfer.TransferBetweenStashes(ctx, a.UserID, "stash", "spend", amount)
	case entities.ActionNotify:
		// Notification-only automations are logged but don't execute transfers
		return nil
	default:
		return fmt.Errorf("unsupported action type: %s", a.ActionType)
	}
}

func StampTransferConsent(actionConfig map[string]interface{}, now time.Time) map[string]interface{} {
	if actionConfig == nil {
		actionConfig = map[string]interface{}{}
	}
	actionConfig["acknowledged_future_transfer"] = true
	actionConfig["passcode_session_verified_at"] = now.UTC().Format(time.RFC3339)
	actionConfig["reauthorization_due_at"] = now.UTC().Add(transferAutomationReauthorizationWindow).Format(time.RFC3339)
	actionConfig["reauthorization_window_days"] = int(transferAutomationReauthorizationWindow.Hours() / 24)
	return actionConfig
}

func ensureTransferReauthorization(actionConfig map[string]interface{}, now time.Time) error {
	dueAt, err := transferConsentWindow(actionConfig, now)
	if err != nil {
		return err
	}
	if !now.Before(dueAt) {
		return fmt.Errorf("%w: passcode consent expired on %s", ErrTransferAutomationReauthorizationRequired, dueAt.Format(time.RFC3339))
	}
	return nil
}

func transferConsentWindow(actionConfig map[string]interface{}, now time.Time) (time.Time, error) {
	verifiedRaw, _ := actionConfig["passcode_session_verified_at"].(string)
	if verifiedRaw == "" {
		return time.Time{}, fmt.Errorf("%w: passcode_session_verified_at is required", ErrTransferAutomationReauthorizationRequired)
	}
	verifiedAt, err := time.Parse(time.RFC3339, verifiedRaw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid passcode_session_verified_at", ErrTransferAutomationReauthorizationRequired)
	}
	if verifiedAt.After(now.Add(2 * time.Minute)) {
		return time.Time{}, fmt.Errorf("%w: passcode consent timestamp is in the future", ErrTransferAutomationReauthorizationRequired)
	}
	dueRaw, _ := actionConfig["reauthorization_due_at"].(string)
	if dueRaw == "" {
		return time.Time{}, fmt.Errorf("%w: reauthorization_due_at is required", ErrTransferAutomationReauthorizationRequired)
	}
	dueAt, err := time.Parse(time.RFC3339, dueRaw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid reauthorization_due_at", ErrTransferAutomationReauthorizationRequired)
	}
	if dueAt.Before(now.Add(-transferAutomationReauthorizationWindow)) {
		return time.Time{}, fmt.Errorf("%w: reauthorization_due_at is stale", ErrTransferAutomationReauthorizationRequired)
	}
	if dueAt.After(verifiedAt.Add(transferAutomationReauthorizationWindow + time.Minute)) {
		return time.Time{}, fmt.Errorf("%w: reauthorization window is too long", ErrTransferAutomationReauthorizationRequired)
	}
	return dueAt, nil
}

// --- Request types ---

type CreateAutomationRequest struct {
	Name              string                 `json:"name" binding:"required"`
	Description       *string                `json:"description"`
	TriggerType       string                 `json:"trigger_type" binding:"required"`
	TriggerConfig     map[string]interface{} `json:"trigger_config" binding:"required"`
	ActionType        string                 `json:"action_type" binding:"required"`
	ActionConfig      map[string]interface{} `json:"action_config" binding:"required"`
	MaxTriggersPerDay int                    `json:"max_triggers_per_day"`
	CooldownMinutes   int                    `json:"cooldown_minutes"`
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
