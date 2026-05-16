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

// CardController freezes/unfreezes cards for cooldown automations.
type CardController interface {
	FreezeCard(ctx context.Context, userID, cardID uuid.UUID) error
	UnfreezeCard(ctx context.Context, userID, cardID uuid.UUID) error
	GetCardsByUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
}

// NotificationSender sends push/in-app notifications.
type NotificationSender interface {
	SendPush(ctx context.Context, userID uuid.UUID, title, message string, data map[string]interface{}) error
}

// ObligationProvider lists active obligations for bill shield evaluation.
type ObligationProvider interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
}

// GoalChecker checks if a savings goal has been reached.
type GoalChecker interface {
	IsGoalReached(ctx context.Context, userID uuid.UUID, goalID uuid.UUID) (bool, error)
}

// Service manages automation CRUD and execution.
type Service struct {
	repo        *repositories.AutomationRepository
	balance     BalanceProvider
	transfer    TransferExecutor
	card        CardController
	notifier    NotificationSender
	obligations ObligationProvider
	goals       GoalChecker
	logger      *zap.Logger
}

func NewService(repo *repositories.AutomationRepository, balance BalanceProvider, transfer TransferExecutor, logger *zap.Logger) *Service {
	return &Service{repo: repo, balance: balance, transfer: transfer, logger: logger}
}

// SetCardController sets the card controller for pause/resume automations.
func (s *Service) SetCardController(c CardController) { s.card = c }

// SetNotificationSender sets the notification sender for notify automations.
func (s *Service) SetNotificationSender(n NotificationSender) { s.notifier = n }

// SetObligationProvider sets the obligation provider for bill shield automations.
func (s *Service) SetObligationProvider(o ObligationProvider) { s.obligations = o }

// SetGoalChecker sets the goal checker for goal-linked automations.
func (s *Service) SetGoalChecker(g GoalChecker) { s.goals = g }

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
		SavingsGoalID:     req.SavingsGoalID,
		ObligationID:      req.ObligationID,
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
	case entities.TriggerSchedule, entities.TriggerBalanceThreshold, entities.TriggerIncomeDetected, entities.TriggerSpendingSpike, entities.TriggerPayday, entities.TriggerCustom,
		entities.TriggerObligationDue, entities.TriggerLifeEvent:
		return true
	default:
		return false
	}
}

func validAction(value string) bool {
	switch value {
	case entities.ActionTransferToStash, entities.ActionTransferToSpend, entities.ActionNotify, entities.ActionCustom,
		entities.ActionPauseCard, entities.ActionResumeCard, entities.ActionPauseCardCooldown:
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
		automation := a
		if !s.shouldTrigger(ctx, &automation, now) {
			continue
		}
		go s.execute(context.Background(), &automation)
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
		automation := a
		if s.checkBalanceThreshold(ctx, &automation) {
			go s.execute(context.Background(), &automation)
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
		automation := a
		if s.checkBalanceThreshold(ctx, &automation) {
			go s.execute(context.Background(), &automation)
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
		return s.executeNotify(ctx, a)
	case entities.ActionPauseCard:
		return s.executePauseCard(ctx, a.UserID)
	case entities.ActionResumeCard:
		return s.executeResumeCard(ctx, a.UserID)
	case entities.ActionPauseCardCooldown:
		return s.executePauseCardCooldown(ctx, a)
	default:
		return fmt.Errorf("unsupported action type: %s", a.ActionType)
	}
}

func (s *Service) executeNotify(ctx context.Context, a *entities.MiriamAutomation) error {
	if s.notifier == nil {
		s.logger.Warn("notify action skipped: no notification sender configured", zap.String("id", a.ID.String()))
		return nil
	}
	var cfg entities.NotifyActionConfig
	json.Unmarshal(a.ActionConfig, &cfg)
	title := cfg.Title
	if title == "" {
		title = "Rail Automation"
	}
	msg := cfg.Message
	if msg == "" {
		msg = a.Name
	}
	return s.notifier.SendPush(ctx, a.UserID, title, msg,
		automationPushData("notify", fmt.Sprintf("My automation '%s' just triggered. What should I do?", a.Name)))
}

func (s *Service) executePauseCard(ctx context.Context, userID uuid.UUID) error {
	if s.card == nil {
		return fmt.Errorf("card controller not configured")
	}
	cardIDs, err := s.card.GetCardsByUser(ctx, userID)
	if err != nil || len(cardIDs) == 0 {
		return fmt.Errorf("no cards found for user")
	}
	return s.card.FreezeCard(ctx, userID, cardIDs[0])
}

func (s *Service) executeResumeCard(ctx context.Context, userID uuid.UUID) error {
	if s.card == nil {
		return fmt.Errorf("card controller not configured")
	}
	cardIDs, err := s.card.GetCardsByUser(ctx, userID)
	if err != nil || len(cardIDs) == 0 {
		return fmt.Errorf("no cards found for user")
	}
	return s.card.UnfreezeCard(ctx, userID, cardIDs[0])
}

func (s *Service) executePauseCardCooldown(ctx context.Context, a *entities.MiriamAutomation) error {
	if s.card == nil {
		return fmt.Errorf("card controller not configured")
	}
	var cfg entities.PauseCardCooldownConfig
	json.Unmarshal(a.ActionConfig, &cfg)
	cooldown := cfg.CooldownMinutes
	if cooldown <= 0 {
		cooldown = 30
	}

	cardIDs, err := s.card.GetCardsByUser(ctx, a.UserID)
	if err != nil || len(cardIDs) == 0 {
		return fmt.Errorf("no cards found for user")
	}
	cardID := cardIDs[0]

	if err := s.card.FreezeCard(ctx, a.UserID, cardID); err != nil {
		return fmt.Errorf("freeze card: %w", err)
	}

	// Notify user about the cooldown
	if s.notifier != nil {
		msg := cfg.Message
		if msg == "" {
			msg = fmt.Sprintf("Spending spike detected. Your card is paused for %d minutes to cool down.", cooldown)
		}
		_ = s.notifier.SendPush(ctx, a.UserID, "Card Paused — Cooldown Active", msg,
			automationPushData("spending_spike", "My card was just paused because of a spending spike. Can you review my recent spending?"))
	}

	// Persist the scheduled unfreeze so it survives service restarts.
	unfreezeAt := time.Now().Add(time.Duration(cooldown) * time.Minute)
	if err := s.repo.InsertPendingUnfreeze(ctx, a.UserID, cardID, a.ID, unfreezeAt); err != nil {
		s.logger.Error("failed to persist pending card unfreeze",
			zap.Error(err),
			zap.String("automation_id", a.ID.String()),
			zap.String("user_id", a.UserID.String()),
			zap.String("card_id", cardID.String()))
	}

	return nil
}

// PauseUserCards freezes the user's first card and schedules an automatic
// PauseUserCards freezes all user cards with a scheduled auto-unfreeze.
// Authorization: must only be called by internal services (Money Guard) after validating context.
func (s *Service) PauseUserCards(ctx context.Context, userID uuid.UUID, cooldownMinutes int, reason string) error {
	if s.card == nil {
		return fmt.Errorf("card controller not configured")
	}
	if cooldownMinutes <= 0 {
		cooldownMinutes = 30
	}
	cardIDs, err := s.card.GetCardsByUser(ctx, userID)
	if err != nil || len(cardIDs) == 0 {
		return fmt.Errorf("no cards found for user")
	}
	unfreezeAt := time.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
	var frozenCards []uuid.UUID
	for _, cardID := range cardIDs {
		if err := s.card.FreezeCard(ctx, userID, cardID); err != nil {
			for _, fid := range frozenCards {
				if ufErr := s.card.UnfreezeCard(ctx, userID, fid); ufErr != nil {
					s.logger.Error("rollback unfreeze failed", zap.String("card_id", fid.String()), zap.Error(ufErr))
				}
			}
			return fmt.Errorf("freeze card %s: %w", cardID, err)
		}
		frozenCards = append(frozenCards, cardID)
		if err := s.repo.InsertPendingUnfreeze(ctx, userID, cardID, uuid.Nil, unfreezeAt); err != nil {
			for _, fid := range frozenCards {
				if ufErr := s.card.UnfreezeCard(ctx, userID, fid); ufErr != nil {
					s.logger.Error("rollback unfreeze failed", zap.String("card_id", fid.String()), zap.Error(ufErr))
				}
			}
			return fmt.Errorf("schedule card unfreeze: %w", err)
		}
	}
	if s.notifier != nil {
		msg := reason
		if strings.TrimSpace(msg) == "" {
			msg = fmt.Sprintf("Money Guard paused your card for %d minutes.", cooldownMinutes)
		}
		_ = s.notifier.SendPush(ctx, userID, "Money Guard cooldown", msg,
			automationPushData("money_guard_cooldown", "Money Guard paused my card. Show me my recovery plan."))
	}
	return nil
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

// EvaluateObligationDue checks obligation_due triggered automations.
// For each automation linked to an obligation, it checks if the obligation is due
// within the configured days_before_due window and triggers the action.
func (s *Service) EvaluateObligationDue(ctx context.Context) error {
	if s.obligations == nil {
		return nil
	}
	automations, err := s.repo.ListActiveByTrigger(ctx, entities.TriggerObligationDue)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, a := range automations {
		if a.ObligationID == nil {
			continue
		}
		if a.LastTriggeredAt != nil && now.Sub(*a.LastTriggeredAt) < time.Duration(a.CooldownMinutes)*time.Minute {
			continue
		}
		if s.isObligationDueSoon(ctx, &a, now) {
			automation := a
			go s.execute(context.Background(), &automation)
		}
	}
	return nil
}

func (s *Service) isObligationDueSoon(ctx context.Context, a *entities.MiriamAutomation, now time.Time) bool {
	if s.obligations == nil || a.ObligationID == nil {
		return false
	}
	obligations, err := s.obligations.ListActive(ctx, a.UserID)
	if err != nil {
		return false
	}
	var cfg entities.ObligationDueTriggerConfig
	json.Unmarshal(a.TriggerConfig, &cfg)
	daysWindow := cfg.DaysBeforeDue
	if daysWindow <= 0 {
		daysWindow = 3
	}
	for _, ob := range obligations {
		if ob.ID != *a.ObligationID {
			continue
		}
		if ob.DueDay == nil {
			continue
		}
		dueThisMonth := time.Date(now.Year(), now.Month(), *ob.DueDay, 0, 0, 0, 0, time.UTC)
		if dueThisMonth.Before(now) {
			dueThisMonth = dueThisMonth.AddDate(0, 1, 0)
		}
		daysUntilDue := int(dueThisMonth.Sub(now).Hours() / 24)
		return daysUntilDue <= daysWindow
	}
	return false
}

// EvaluateBillShield checks if spend balance covers upcoming obligations for a user.
// If not, auto-transfers from stash to cover the shortfall.
func (s *Service) EvaluateBillShield(ctx context.Context, userID uuid.UUID) error {
	if s.obligations == nil || s.balance == nil {
		return nil
	}
	obligations, err := s.obligations.ListActive(ctx, userID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var totalDueSoon decimal.Decimal
	for _, ob := range obligations {
		if ob.DueDay == nil {
			continue
		}
		dueThisMonth := time.Date(now.Year(), now.Month(), *ob.DueDay, 0, 0, 0, 0, time.UTC)
		if dueThisMonth.Before(now) {
			dueThisMonth = dueThisMonth.AddDate(0, 1, 0)
		}
		if int(dueThisMonth.Sub(now).Hours()/24) <= 7 {
			totalDueSoon = totalDueSoon.Add(ob.Amount)
		}
	}
	if !totalDueSoon.IsPositive() {
		return nil
	}
	spendBal, err := s.balance.GetSpendBalance(ctx, userID)
	if err != nil {
		return err
	}
	shortfall := totalDueSoon.Sub(spendBal)
	if !shortfall.IsPositive() {
		return nil
	}
	stashBal, err := s.balance.GetStashBalance(ctx, userID)
	if err != nil {
		return err
	}
	transferAmt := decimal.Min(shortfall, stashBal)
	if !transferAmt.IsPositive() {
		if s.notifier != nil {
			_ = s.notifier.SendPush(ctx, userID, "Bill Shield Alert",
				fmt.Sprintf("You have $%s in bills due within 7 days but insufficient balance.", totalDueSoon.StringFixed(2)),
				automationPushData("bill_shield", "I got a bill shield alert about insufficient balance. What are my options?"))
		}
		return nil
	}
	if err := s.transfer.TransferBetweenStashes(ctx, userID, "stash", "spend", transferAmt); err != nil {
		return fmt.Errorf("bill shield transfer: %w", err)
	}
	if s.notifier != nil {
		_ = s.notifier.SendPush(ctx, userID, "Bill Shield Activated",
			fmt.Sprintf("Moved $%s from Stash to Spend to cover upcoming bills.", transferAmt.StringFixed(2)),
			automationPushData("bill_shield", fmt.Sprintf("Bill Shield just moved $%s to cover my upcoming bills. Can you show me what's due?", transferAmt.StringFixed(2))))
	}
	s.logger.Info("bill shield transfer executed",
		zap.String("user_id", userID.String()),
		zap.String("amount", transferAmt.StringFixed(2)))
	return nil
}

// DeactivateCompletedGoalAutomations deactivates automations whose savings goal is reached.
func (s *Service) DeactivateCompletedGoalAutomations(ctx context.Context) error {
	if s.goals == nil {
		return nil
	}
	automations, err := s.repo.ListActive(ctx)
	if err != nil {
		return err
	}
	for _, a := range automations {
		if a.SavingsGoalID == nil {
			continue
		}
		reached, err := s.goals.IsGoalReached(ctx, a.UserID, *a.SavingsGoalID)
		if err != nil {
			s.logger.Warn("goal check failed", zap.Error(err), zap.String("automation_id", a.ID.String()))
			continue
		}
		if reached {
			a.IsActive = false
			if err := s.repo.Update(ctx, &a); err != nil {
				s.logger.Error("failed to deactivate goal-linked automation", zap.Error(err))
				continue
			}
			if s.notifier != nil {
				_ = s.notifier.SendPush(ctx, a.UserID, "Goal Reached! 🎉",
					fmt.Sprintf("Your automation '%s' has been deactivated because your savings goal was reached.", a.Name),
					automationPushData("goal_reached", fmt.Sprintf("I just reached my savings goal! The '%s' automation was deactivated. What should I do with my stash now?", a.Name)))
			}
			s.logger.Info("goal-linked automation deactivated",
				zap.String("automation_id", a.ID.String()),
				zap.String("goal_id", a.SavingsGoalID.String()))
		}
	}
	return nil
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
	SavingsGoalID     *uuid.UUID             `json:"savings_goal_id,omitempty"`
	ObligationID      *uuid.UUID             `json:"obligation_id,omitempty"`
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

// automationPushData builds a push notification data map that deep-links into
// the Miriam AI chat with a contextual pre-loaded message.
func automationPushData(automationType, preloadedMessage string) map[string]interface{} {
	return map[string]interface{}{
		"type":              "automation",
		"screen":            "ai-chat",
		"automation_type":   automationType,
		"preloaded_message": preloadedMessage,
	}
}

// ProcessPendingUnfreezes polls for due card unfreeze operations and executes them.
func (s *Service) ProcessPendingUnfreezes(ctx context.Context) error {
	if s.card == nil {
		return nil
	}
	due, err := s.repo.GetDueUnfreezes(ctx, time.Now(), 50)
	if err != nil {
		return fmt.Errorf("get due unfreezes: %w", err)
	}
	for _, job := range due {
		unfreezeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.card.UnfreezeCard(unfreezeCtx, job.UserID, job.CardID)
		cancel()
		if err != nil {
			s.logger.Error("failed to unfreeze card",
				zap.Error(err),
				zap.String("user_id", job.UserID.String()),
				zap.String("card_id", job.CardID.String()))
			_ = s.repo.ReinsertFailedUnfreeze(ctx, job, err.Error())
			continue
		}
		if s.notifier != nil {
			_ = s.notifier.SendPush(ctx, job.UserID, "Card Resumed", "Your cooldown period is over. Your card is active again.",
				automationPushData("card_resumed", "My card cooldown just ended. How's my spending looking today?"))
		}
	}
	return nil
}
