package obligation

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3,10}$`)

type Service struct {
	repo *repositories.FinancialObligationRepository
}

func NewService(repo *repositories.FinancialObligationRepository) *Service {
	return &Service{repo: repo}
}

type CreateRequest struct {
	Type         string                 `json:"type" binding:"required"`
	Name         string                 `json:"name" binding:"required"`
	Amount       decimal.Decimal        `json:"amount" binding:"required"`
	Currency     string                 `json:"currency" binding:"required"`
	Cadence      string                 `json:"cadence" binding:"required"`
	DueDate      *time.Time             `json:"due_date"`
	DueDay       *int                   `json:"due_day"`
	Priority     string                 `json:"priority"`
	Counterparty *string                `json:"counterparty"`
	Status       string                 `json:"status"`
	Metadata     map[string]interface{} `json:"metadata"`
}

type UpdateRequest struct {
	Type         *string                 `json:"type"`
	Name         *string                 `json:"name"`
	Amount       *decimal.Decimal        `json:"amount"`
	Currency     *string                 `json:"currency"`
	Cadence      *string                 `json:"cadence"`
	DueDate      *time.Time              `json:"due_date"`
	DueDay       *int                    `json:"due_day"`
	Priority     *string                 `json:"priority"`
	Counterparty *string                 `json:"counterparty"`
	Status       *string                 `json:"status"`
	Metadata     *map[string]interface{} `json:"metadata"`
}

type ListFilter struct {
	Status string
	Type   string
}

func (s *Service) Create(ctx context.Context, userID uuid.UUID, req CreateRequest) (*entities.FinancialObligation, error) {
	obligation := &entities.FinancialObligation{
		ID:           uuid.New(),
		UserID:       userID,
		Type:         strings.TrimSpace(req.Type),
		Name:         strings.TrimSpace(req.Name),
		Amount:       req.Amount,
		Currency:     strings.ToUpper(strings.TrimSpace(req.Currency)),
		Cadence:      strings.TrimSpace(req.Cadence),
		DueDate:      req.DueDate,
		DueDay:       req.DueDay,
		Priority:     coalesce(req.Priority, entities.ObligationPriorityMedium),
		Counterparty: cleanOptionalString(req.Counterparty),
		Status:       coalesce(req.Status, entities.ObligationStatusActive),
	}
	metadata, err := marshalMetadata(req.Metadata)
	if err != nil {
		return nil, err
	}
	obligation.Metadata = metadata
	if err := validate(obligation); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, obligation); err != nil {
		return nil, err
	}
	return obligation, nil
}

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*entities.FinancialObligation, error) {
	return s.repo.GetByID(ctx, userID, id)
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, filter ListFilter) ([]entities.FinancialObligation, error) {
	status := strings.TrimSpace(filter.Status)
	obligationType := strings.TrimSpace(filter.Type)
	if status != "" && !validStatus(status) {
		return nil, fmt.Errorf("unsupported obligation status: %s", status)
	}
	if obligationType != "" && !validType(obligationType) {
		return nil, fmt.Errorf("unsupported obligation type: %s", obligationType)
	}
	return s.repo.ListByUser(ctx, userID, status, obligationType)
}

func (s *Service) ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error) {
	return s.repo.ListByUser(ctx, userID, entities.ObligationStatusActive, "")
}

func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, req UpdateRequest) (*entities.FinancialObligation, error) {
	obligation, err := s.repo.GetByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if req.Type != nil {
		obligation.Type = strings.TrimSpace(*req.Type)
	}
	if req.Name != nil {
		obligation.Name = strings.TrimSpace(*req.Name)
	}
	if req.Amount != nil {
		obligation.Amount = *req.Amount
	}
	if req.Currency != nil {
		obligation.Currency = strings.ToUpper(strings.TrimSpace(*req.Currency))
	}
	if req.Cadence != nil {
		obligation.Cadence = strings.TrimSpace(*req.Cadence)
	}
	if req.DueDate != nil {
		obligation.DueDate = req.DueDate
	}
	if req.DueDay != nil {
		obligation.DueDay = req.DueDay
	}
	if req.Priority != nil {
		obligation.Priority = strings.TrimSpace(*req.Priority)
	}
	if req.Counterparty != nil {
		obligation.Counterparty = cleanOptionalString(req.Counterparty)
	}
	if req.Status != nil {
		obligation.Status = strings.TrimSpace(*req.Status)
	}
	if req.Metadata != nil {
		metadata, err := marshalMetadata(*req.Metadata)
		if err != nil {
			return nil, err
		}
		obligation.Metadata = metadata
	}
	if err := validate(obligation); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, obligation); err != nil {
		return nil, err
	}
	return obligation, nil
}

func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return s.repo.Delete(ctx, userID, id)
}

func validate(o *entities.FinancialObligation) error {
	if !validType(o.Type) {
		return fmt.Errorf("unsupported obligation type: %s", o.Type)
	}
	if strings.TrimSpace(o.Name) == "" || len(o.Name) > 200 {
		return fmt.Errorf("obligation name must be between 1 and 200 characters")
	}
	if !o.Amount.IsPositive() {
		return fmt.Errorf("amount must be greater than zero")
	}
	if !currencyPattern.MatchString(o.Currency) {
		return fmt.Errorf("currency must be an uppercase code such as USD or NGN")
	}
	if !validCadence(o.Cadence) {
		return fmt.Errorf("unsupported obligation cadence: %s", o.Cadence)
	}
	if o.DueDay != nil && (*o.DueDay < 1 || *o.DueDay > 31) {
		return fmt.Errorf("due_day must be between 1 and 31")
	}
	if !validPriority(o.Priority) {
		return fmt.Errorf("unsupported obligation priority: %s", o.Priority)
	}
	if !validStatus(o.Status) {
		return fmt.Errorf("unsupported obligation status: %s", o.Status)
	}
	return nil
}

func validType(value string) bool {
	switch value {
	case entities.ObligationTypeDebt, entities.ObligationTypeInvoice, entities.ObligationTypePayroll,
		entities.ObligationTypeInsurance, entities.ObligationTypeEducation, entities.ObligationTypeRent,
		entities.ObligationTypeFamilySupport, entities.ObligationTypeTax, entities.ObligationTypeSubscription,
		entities.ObligationTypeVendorBill, entities.ObligationTypeOther:
		return true
	default:
		return false
	}
}

func validCadence(value string) bool {
	switch value {
	case entities.ObligationCadenceOneTime, entities.ObligationCadenceWeekly, entities.ObligationCadenceBiweekly,
		entities.ObligationCadenceMonthly, entities.ObligationCadenceQuarterly, entities.ObligationCadenceAnnual:
		return true
	default:
		return false
	}
}

func validPriority(value string) bool {
	switch value {
	case entities.ObligationPriorityCritical, entities.ObligationPriorityHigh, entities.ObligationPriorityMedium, entities.ObligationPriorityLow:
		return true
	default:
		return false
	}
}

func validStatus(value string) bool {
	switch value {
	case entities.ObligationStatusActive, entities.ObligationStatusPaused, entities.ObligationStatusPaid, entities.ObligationStatusCancelled:
		return true
	default:
		return false
	}
}

func coalesce(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func cleanOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	cleaned := strings.TrimSpace(*value)
	if cleaned == "" {
		return nil
	}
	return &cleaned
}

func marshalMetadata(metadata map[string]interface{}) ([]byte, error) {
	if metadata == nil {
		return []byte("{}"), nil
	}
	out, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("metadata must be valid JSON: %w", err)
	}
	return out, nil
}
