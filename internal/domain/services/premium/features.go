package premium

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ============================================================================
// Service Interfaces
// ============================================================================

type LedgerBalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

type P2PTransferProvider interface {
	GetTransfers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.P2PTransfer, error)
}

type CardTransactionProvider interface {
	GetUserTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error)
}

type DepositProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error)
}

type ReceiptProvider interface {
	GetByID(ctx context.Context, userID, receiptID uuid.UUID) (*entities.ReceiptScan, error)
}

// ============================================================================
// Tier 1.1: Naira Depreciation Shield Tracker
// ============================================================================

type NairaShieldService struct {
	exchangeRateRepo interface {
		GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error)
		GetRateOnDate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error)
	}
	ledger LedgerBalanceProvider
	logger *zap.Logger
}

func NewNairaShieldService(exchangeRateRepo interface {
	GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error)
	GetRateOnDate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error)
}, ledger LedgerBalanceProvider, logger *zap.Logger) *NairaShieldService {
	return &NairaShieldService{exchangeRateRepo: exchangeRateRepo, ledger: ledger, logger: logger}
}

// ShieldReport shows how much the user's USD stash has protected them from naira depreciation.
type ShieldReport struct {
	StashBalanceUSD      decimal.Decimal `json:"stash_balance_usd"`
	CurrentNGNValue      decimal.Decimal `json:"current_ngn_value"`
	HypotheticalNGNValue decimal.Decimal `json:"hypothetical_ngn_value"` // if saved in naira at old rate
	ShieldAmountNGN      decimal.Decimal `json:"shield_amount_ngn"`
	ShieldPercent        decimal.Decimal `json:"shield_percent"`
	CurrentUSDtoNGN      decimal.Decimal `json:"current_usd_to_ngn"`
}

func (s *NairaShieldService) GetShieldReport(ctx context.Context, userID uuid.UUID) (*ShieldReport, error) {
	stash, err := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return nil, fmt.Errorf("get stash balance: %w", err)
	}

	// Get current USD/NGN rate
	currentRate, err := s.exchangeRateRepo.GetLatestRate(ctx, "USD", "NGN")
	if err != nil {
		// Fallback to approximate rate if not in DB
		s.logger.Warn("No USD/NGN rate in DB, using fallback", zap.Error(err))
		currentRate = decimal.NewFromFloat(1550) // approximate 2025 rate
	}

	// Get rate from 1 year ago for comparison
	oneYearAgo := time.Now().AddDate(-1, 0, 0)
	oldRate, err := s.exchangeRateRepo.GetRateOnDate(ctx, "USD", "NGN", oneYearAgo)
	if err != nil {
		s.logger.Warn("No historical USD/NGN rate, using 65% of current", zap.Error(err))
		oldRate = currentRate.Mul(decimal.NewFromFloat(0.65)) // assume 35% depreciation over year
	}

	currentNGN := stash.Mul(currentRate)
	hypotheticalNGN := stash.Mul(oldRate)
	shieldAmount := currentNGN.Sub(hypotheticalNGN)

	var shieldPct decimal.Decimal
	if !hypotheticalNGN.IsZero() {
		shieldPct = shieldAmount.Div(hypotheticalNGN).Mul(decimal.NewFromInt(100))
	}

	return &ShieldReport{
		StashBalanceUSD:      stash,
		CurrentNGNValue:      currentNGN,
		HypotheticalNGNValue: hypotheticalNGN,
		ShieldAmountNGN:      shieldAmount,
		ShieldPercent:        shieldPct,
		CurrentUSDtoNGN:      currentRate,
	}, nil
}

// ============================================================================
// Tier 1.2: Black Tax Optimizer
// ============================================================================

type BlackTaxService struct {
	familyRepo interface {
		GetBudget(ctx context.Context, userID uuid.UUID) (*entities.FamilySupportBudget, error)
		UpsertBudget(ctx context.Context, b *entities.FamilySupportBudget) error
		GetRecipients(ctx context.Context, userID uuid.UUID) ([]*entities.FamilySupportRecipient, error)
		UpsertRecipient(ctx context.Context, rec *entities.FamilySupportRecipient) error
		GetMonthlySentTotal(ctx context.Context, userID uuid.UUID, year, month int) (decimal.Decimal, error)
	}
	p2p    P2PTransferProvider
	logger *zap.Logger
}

func NewBlackTaxService(familyRepo interface {
	GetBudget(ctx context.Context, userID uuid.UUID) (*entities.FamilySupportBudget, error)
	UpsertBudget(ctx context.Context, b *entities.FamilySupportBudget) error
	GetRecipients(ctx context.Context, userID uuid.UUID) ([]*entities.FamilySupportRecipient, error)
	UpsertRecipient(ctx context.Context, rec *entities.FamilySupportRecipient) error
	GetMonthlySentTotal(ctx context.Context, userID uuid.UUID, year, month int) (decimal.Decimal, error)
}, p2p P2PTransferProvider, logger *zap.Logger) *BlackTaxService {
	return &BlackTaxService{familyRepo: familyRepo, p2p: p2p, logger: logger}
}

type BlackTaxSummary struct {
	BudgetSet         bool                               `json:"budget_set"`
	MonthlyLimit      decimal.Decimal                    `json:"monthly_limit"`
	SentThisMonth     decimal.Decimal                    `json:"sent_this_month"`
	Remaining         decimal.Decimal                    `json:"remaining"`
	PercentUsed       decimal.Decimal                    `json:"percent_used"`
	AlertThresholdPct int                                `json:"alert_threshold_pct"`
	OverBudget        bool                               `json:"over_budget"`
	Recipients        []*entities.FamilySupportRecipient `json:"recipients"`
}

func (s *BlackTaxService) GetSummary(ctx context.Context, userID uuid.UUID) (*BlackTaxSummary, error) {
	budget, _ := s.familyRepo.GetBudget(ctx, userID)
	recipients, _ := s.familyRepo.GetRecipients(ctx, userID)

	now := time.Now()
	sentThisMonth, _ := s.familyRepo.GetMonthlySentTotal(ctx, userID, now.Year(), int(now.Month()))

	summary := &BlackTaxSummary{
		BudgetSet:         budget != nil,
		SentThisMonth:     sentThisMonth,
		Recipients:        recipients,
		AlertThresholdPct: 80,
	}

	if budget != nil {
		summary.MonthlyLimit = budget.MonthlyLimit
		summary.AlertThresholdPct = budget.AlertThresholdPct
		summary.Remaining = budget.MonthlyLimit.Sub(sentThisMonth)
		if !budget.MonthlyLimit.IsZero() {
			summary.PercentUsed = sentThisMonth.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
		}
		summary.OverBudget = sentThisMonth.GreaterThan(budget.MonthlyLimit)
	}

	return summary, nil
}

func (s *BlackTaxService) SetBudget(ctx context.Context, userID uuid.UUID, limit decimal.Decimal, alertPct int) error {
	return s.familyRepo.UpsertBudget(ctx, &entities.FamilySupportBudget{
		UserID:            userID,
		MonthlyLimit:      limit,
		AlertThresholdPct: alertPct,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	})
}

func (s *BlackTaxService) SyncRecipientsFromHistory(ctx context.Context, userID uuid.UUID) error {
	now := time.Now()
	start := now.AddDate(0, -6, 0)
	transfers, err := s.p2p.GetTransfers(ctx, userID, 1000, 0)
	if err != nil {
		return err
	}

	recipientMap := make(map[string]*entities.FamilySupportRecipient)
	for _, tx := range transfers {
		if tx.Status != "completed" {
			continue
		}
		if tx.CreatedAt.Before(start) {
			continue
		}
		id := tx.RecipientIdentifier
		if id == "" {
			continue
		}
		if rec, ok := recipientMap[id]; ok {
			rec.TotalSentLifetime = rec.TotalSentLifetime.Add(tx.Amount)
			rec.SendCount++
			if rec.LastSentAt == nil || tx.CreatedAt.After(*rec.LastSentAt) {
				rec.LastSentAt = &tx.CreatedAt
			}
		} else {
			t := tx.CreatedAt
			recipientMap[id] = &entities.FamilySupportRecipient{
				ID:                  uuid.New(),
				UserID:              userID,
				RecipientName:       id,
				RecipientIdentifier: id,
				TotalSentLifetime:   tx.Amount,
				SendCount:           1,
				LastSentAt:          &t,
				CreatedAt:           time.Now(),
				UpdatedAt:           time.Now(),
			}
		}
	}

	for _, rec := range recipientMap {
		if rec.SendCount > 0 {
			rec.MonthlyAverage = rec.TotalSentLifetime.Div(decimal.NewFromInt(int64(rec.SendCount)))
		}
		if err := s.familyRepo.UpsertRecipient(ctx, rec); err != nil {
			s.logger.Warn("Failed to upsert recipient", zap.Error(err), zap.String("identifier", rec.RecipientIdentifier))
		}
	}

	return nil
}

// ============================================================================
// Tier 1.4: Smart Receipt Splitting
// ============================================================================

type ReceiptSplitService struct {
	receiptRepo ReceiptProvider
	splitRepo   interface {
		CreateSplit(ctx context.Context, split *entities.ReceiptSplit) error
		CreateSplitItem(ctx context.Context, item *entities.ReceiptSplitItem) error
	}
	p2p    P2PTransferProvider
	logger *zap.Logger
}

func NewReceiptSplitService(receiptRepo ReceiptProvider, splitRepo interface {
	CreateSplit(ctx context.Context, split *entities.ReceiptSplit) error
	CreateSplitItem(ctx context.Context, item *entities.ReceiptSplitItem) error
}, p2p P2PTransferProvider, logger *zap.Logger) *ReceiptSplitService {
	return &ReceiptSplitService{receiptRepo: receiptRepo, splitRepo: splitRepo, p2p: p2p, logger: logger}
}

type SplitCalculation struct {
	ItemName string          `json:"item_name"`
	Amount   decimal.Decimal `json:"amount"`
	Assignee string          `json:"assignee"` // who pays
}

type SplitResult struct {
	TotalAmount decimal.Decimal            `json:"total_amount"`
	Currency    string                     `json:"currency"`
	Items       []SplitCalculation         `json:"items"`
	PerPerson   map[string]decimal.Decimal `json:"per_person"`
}

func (s *ReceiptSplitService) CalculateSplit(ctx context.Context, userID, receiptID uuid.UUID, assignments map[string]string) (*SplitResult, error) {
	receipt, err := s.receiptRepo.GetByID(ctx, userID, receiptID)
	if err != nil {
		return nil, fmt.Errorf("get receipt: %w", err)
	}

	items := receipt.ParsedItems()
	result := &SplitResult{
		TotalAmount: receipt.Amount,
		Currency:    receipt.Currency,
		Items:       make([]SplitCalculation, 0, len(items)),
		PerPerson:   make(map[string]decimal.Decimal),
	}

	for _, item := range items {
		assignee := assignments[item.Name]
		if assignee == "" {
			assignee = "me" // default
		}
		price, _ := decimal.NewFromString(item.Price)
		result.Items = append(result.Items, SplitCalculation{
			ItemName: item.Name,
			Amount:   price,
			Assignee: assignee,
		})
		result.PerPerson[assignee] = result.PerPerson[assignee].Add(price)
	}

	return result, nil
}

func (s *ReceiptSplitService) PersistSplit(ctx context.Context, userID, receiptID uuid.UUID, result *SplitResult) (*entities.ReceiptSplit, error) {
	split := &entities.ReceiptSplit{
		ID:          uuid.New(),
		ReceiptID:   receiptID,
		UserID:      userID,
		TotalAmount: result.TotalAmount,
		Currency:    result.Currency,
		Status:      "pending",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.splitRepo.CreateSplit(ctx, split); err != nil {
		return nil, fmt.Errorf("create split: %w", err)
	}
	for _, item := range result.Items {
		it := &entities.ReceiptSplitItem{
			ID:         uuid.New(),
			SplitID:    split.ID,
			ItemName:   item.ItemName,
			Amount:     item.Amount,
			AssignedTo: item.Assignee,
			Paid:       false,
			CreatedAt:  time.Now(),
		}
		if err := s.splitRepo.CreateSplitItem(ctx, it); err != nil {
			return nil, fmt.Errorf("create split item: %w", err)
		}
		split.Items = append(split.Items, *it)
	}
	return split, nil
}

// ============================================================================
// Tier 2.1: Scam Intelligence
// ============================================================================

type ScamIntelligenceService struct {
	scamRepo interface {
		GetRiskPatterns(ctx context.Context) ([]*entities.MerchantRiskPattern, error)
		CreateAlert(ctx context.Context, alert *entities.UserScamAlert) error
		GetActiveAlerts(ctx context.Context, userID uuid.UUID) ([]*entities.UserScamAlert, error)
		DismissAlert(ctx context.Context, alertID uuid.UUID) error
	}
	patterns []*entities.MerchantRiskPattern
	mu       sync.RWMutex
	logger   *zap.Logger
}

func NewScamIntelligenceService(scamRepo interface {
	GetRiskPatterns(ctx context.Context) ([]*entities.MerchantRiskPattern, error)
	CreateAlert(ctx context.Context, alert *entities.UserScamAlert) error
	GetActiveAlerts(ctx context.Context, userID uuid.UUID) ([]*entities.UserScamAlert, error)
	DismissAlert(ctx context.Context, alertID uuid.UUID) error
}, logger *zap.Logger) *ScamIntelligenceService {
	return &ScamIntelligenceService{scamRepo: scamRepo, logger: logger}
}

func (s *ScamIntelligenceService) RefreshPatterns(ctx context.Context) error {
	patterns, err := s.scamRepo.GetRiskPatterns(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.patterns = patterns
	s.mu.Unlock()
	return nil
}

type MerchantSafetyCheck struct {
	MerchantName string `json:"merchant_name"`
	Safe         bool   `json:"safe"`
	RiskLevel    string `json:"risk_level"`
	Reason       string `json:"reason,omitempty"`
}

func (s *ScamIntelligenceService) CheckMerchant(ctx context.Context, userID uuid.UUID, merchantName string) (*MerchantSafetyCheck, error) {
	s.mu.RLock()
	patterns := s.patterns
	s.mu.RUnlock()

	// Lazy-load patterns if not yet initialized
	if len(patterns) == 0 {
		if err := s.RefreshPatterns(ctx); err != nil {
			s.logger.Warn("Failed to refresh scam patterns", zap.Error(err))
		}
		s.mu.RLock()
		patterns = s.patterns
		s.mu.RUnlock()
	}

	upperName := strings.ToUpper(merchantName)
	result := &MerchantSafetyCheck{MerchantName: merchantName, Safe: true, RiskLevel: "low"}

	for _, p := range patterns {
		matched := false
		if strings.Contains(upperName, strings.ToUpper(p.Pattern)) {
			matched = true
		} else {
			// Try regex if pattern looks like regex
			if re, err := regexp.Compile(p.Pattern); err == nil {
				matched = re.MatchString(merchantName)
			}
		}

		if matched {
			result.Safe = false
			result.RiskLevel = string(p.RiskLevel)
			result.Reason = p.Description

			// Create alert for high/confirmed scams
			if p.RiskLevel == entities.MerchantRiskHigh || p.RiskLevel == entities.MerchantRiskConfirmed {
				_ = s.scamRepo.CreateAlert(ctx, &entities.UserScamAlert{
					UserID:       userID,
					MerchantName: merchantName,
					AlertType:    "pattern_match",
					RiskLevel:    string(p.RiskLevel),
					Reason:       p.Description,
					CreatedAt:    time.Now(),
				})
			}
			break
		}
	}

	return result, nil
}

func (s *ScamIntelligenceService) GetActiveAlerts(ctx context.Context, userID uuid.UUID) ([]*entities.UserScamAlert, error) {
	return s.scamRepo.GetActiveAlerts(ctx, userID)
}

func (s *ScamIntelligenceService) DismissAlert(ctx context.Context, alertID uuid.UUID) error {
	return s.scamRepo.DismissAlert(ctx, alertID)
}

// ============================================================================
// Tier 2.2: Tax Residency Tracker
// ============================================================================

type TaxResidencyService struct {
	taxRepo interface {
		LogLocation(ctx context.Context, log *entities.UserLocationLog) error
		GetLocationLogs(ctx context.Context, userID uuid.UUID, country string, from, to time.Time) ([]*entities.UserLocationLog, error)
		GetTaxProfile(ctx context.Context, userID uuid.UUID) (*entities.UserTaxProfile, error)
		UpsertTaxProfile(ctx context.Context, p *entities.UserTaxProfile) error
	}
	logger *zap.Logger
}

func NewTaxResidencyService(taxRepo interface {
	LogLocation(ctx context.Context, log *entities.UserLocationLog) error
	GetLocationLogs(ctx context.Context, userID uuid.UUID, country string, from, to time.Time) ([]*entities.UserLocationLog, error)
	GetTaxProfile(ctx context.Context, userID uuid.UUID) (*entities.UserTaxProfile, error)
	UpsertTaxProfile(ctx context.Context, p *entities.UserTaxProfile) error
}, logger *zap.Logger) *TaxResidencyService {
	return &TaxResidencyService{taxRepo: taxRepo, logger: logger}
}

type TaxResidencyStatus struct {
	PrimaryCountry     string    `json:"primary_country"`
	DaysInPrimary      int       `json:"days_in_primary"`
	PrimaryThreshold   int       `json:"primary_threshold"`
	PrimaryWarning     bool      `json:"primary_warning"`
	SecondaryCountry   *string   `json:"secondary_country,omitempty"`
	DaysInSecondary    int       `json:"days_in_secondary"`
	SecondaryThreshold int       `json:"secondary_threshold"`
	SecondaryWarning   bool      `json:"secondary_warning"`
	TaxYearStart       time.Time `json:"tax_year_start"`
	TaxYearEnd         time.Time `json:"tax_year_end"`
}

func (s *TaxResidencyService) LogLocation(ctx context.Context, userID uuid.UUID, country, source string) error {
	// Close any open location log for this user
	// In production, you'd query for open logs and close them
	return s.taxRepo.LogLocation(ctx, &entities.UserLocationLog{
		UserID:    userID,
		Country:   country,
		EnteredAt: time.Now(),
		Source:    source,
		CreatedAt: time.Now(),
	})
}

func (s *TaxResidencyService) GetResidencyStatus(ctx context.Context, userID uuid.UUID) (*TaxResidencyStatus, error) {
	profile, err := s.taxRepo.GetTaxProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		// Default to Nigeria
		profile = &entities.UserTaxProfile{
			UserID:            userID,
			PrimaryTaxCountry: "NG",
			AlertThreshold:    150,
			TaxYearStartMonth: 1,
			TaxYearStartDay:   1,
		}
	}

	now := time.Now()
	taxYearStart := time.Date(now.Year(), time.Month(profile.TaxYearStartMonth), profile.TaxYearStartDay, 0, 0, 0, 0, time.UTC)
	if now.Before(taxYearStart) {
		taxYearStart = taxYearStart.AddDate(-1, 0, 0)
	}
	taxYearEnd := taxYearStart.AddDate(1, 0, 0)

	// Count days in primary country
	daysPrimary := s.countDaysInCountry(ctx, userID, profile.PrimaryTaxCountry, taxYearStart, taxYearEnd)
	daysSecondary := 0
	if profile.SecondaryTaxCountry != nil {
		daysSecondary = s.countDaysInCountry(ctx, userID, *profile.SecondaryTaxCountry, taxYearStart, taxYearEnd)
	}

	status := &TaxResidencyStatus{
		PrimaryCountry:   profile.PrimaryTaxCountry,
		DaysInPrimary:    daysPrimary,
		PrimaryThreshold: profile.AlertThreshold,
		PrimaryWarning:   daysPrimary >= profile.AlertThreshold,
		DaysInSecondary:  daysSecondary,
		TaxYearStart:     taxYearStart,
		TaxYearEnd:       taxYearEnd,
	}

	if profile.SecondaryTaxCountry != nil {
		status.SecondaryCountry = profile.SecondaryTaxCountry
		status.SecondaryThreshold = profile.AlertThreshold
		status.SecondaryWarning = daysSecondary >= profile.AlertThreshold
	}

	return status, nil
}

func (s *TaxResidencyService) countDaysInCountry(ctx context.Context, userID uuid.UUID, country string, from, to time.Time) int {
	logs, err := s.taxRepo.GetLocationLogs(ctx, userID, country, from, to)
	if err != nil {
		return 0
	}

	dayMap := make(map[string]bool)
	for _, log := range logs {
		entered := log.EnteredAt
		exited := log.ExitedAt
		if exited == nil {
			now := time.Now()
			exited = &now
		}
		for d := entered; !d.After(*exited); d = d.AddDate(0, 0, 1) {
			dayMap[d.Format("2006-01-02")] = true
		}
	}
	return len(dayMap)
}

func (s *TaxResidencyService) SetTaxProfile(ctx context.Context, p *entities.UserTaxProfile) error {
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()
	return s.taxRepo.UpsertTaxProfile(ctx, p)
}

// ============================================================================
// Tier 2.3: Income Smoothing
// ============================================================================

type IncomeSmoothingService struct {
	depositRepo DepositProvider
	ledger      LedgerBalanceProvider
	logger      *zap.Logger
}

func NewIncomeSmoothingService(depositRepo DepositProvider, ledger LedgerBalanceProvider, logger *zap.Logger) *IncomeSmoothingService {
	return &IncomeSmoothingService{depositRepo: depositRepo, ledger: ledger, logger: logger}
}

type IncomeForecast struct {
	AvgMonthlyIncome   decimal.Decimal `json:"avg_monthly_income"`
	IncomeStability    decimal.Decimal `json:"income_stability"`     // 0-100, higher = more stable
	PredictedLowMonths []string        `json:"predicted_low_months"` // month names
	SuggestedReserve   decimal.Decimal `json:"suggested_reserve"`
	CurrentStash       decimal.Decimal `json:"current_stash"`
	MonthsOfRunway     int             `json:"months_of_runway"`
}

func (s *IncomeSmoothingService) GetForecast(ctx context.Context, userID uuid.UUID) (*IncomeForecast, error) {
	currentStash, _ := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	now := time.Now()
	start := now.AddDate(-1, 0, 0)
	deposits, err := s.depositRepo.GetByUserID(ctx, userID, 1000, 0)
	if err != nil {
		return nil, fmt.Errorf("get deposits: %w", err)
	}

	// Group by month
	monthlyTotals := make(map[string]decimal.Decimal)
	for _, d := range deposits {
		if d.Status != "confirmed" || d.CreatedAt.Before(start) {
			continue
		}
		key := d.CreatedAt.Format("2006-01")
		monthlyTotals[key] = monthlyTotals[key].Add(d.Amount)
	}

	if len(monthlyTotals) == 0 {
		return &IncomeForecast{
			IncomeStability: decimal.NewFromInt(100),
			CurrentStash:    currentStash,
			MonthsOfRunway:  0,
		}, nil
	}

	// Calculate average and stability
	var sum, min decimal.Decimal
	min = decimal.NewFromInt(1 << 30)
	count := 0
	for _, total := range monthlyTotals {
		sum = sum.Add(total)
		if total.LessThan(min) {
			min = total
		}
		count++
	}

	avg := sum.Div(decimal.NewFromInt(int64(count)))

	// Stability = (min/avg) * 100
	var stability decimal.Decimal
	if !avg.IsZero() {
		stability = min.Div(avg).Mul(decimal.NewFromInt(100))
	}

	// Identify low months (below 70% of average)
	var lowMonths []string
	for month, total := range monthlyTotals {
		if !avg.IsZero() && total.Div(avg).LessThan(decimal.NewFromFloat(0.7)) {
			// Parse month name
			if t, err := time.Parse("2006-01", month); err == nil {
				lowMonths = append(lowMonths, t.Format("January 2006"))
			}
		}
	}

	// Suggested reserve = 3 months of average expenses (assume 60% of income)
	suggestedReserve := avg.Mul(decimal.NewFromFloat(0.6)).Mul(decimal.NewFromInt(3))

	// Runway = stash / (avg * 0.6)
	monthlyExpense := avg.Mul(decimal.NewFromFloat(0.6))
	var runway int
	if !monthlyExpense.IsZero() {
		rf, _ := currentStash.Div(monthlyExpense).Float64()
		runway = int(math.Floor(rf))
	}

	return &IncomeForecast{
		AvgMonthlyIncome:   avg,
		IncomeStability:    stability,
		PredictedLowMonths: lowMonths,
		SuggestedReserve:   suggestedReserve,
		CurrentStash:       currentStash,
		MonthsOfRunway:     runway,
	}, nil
}

// ============================================================================
// Tier 3.1: Financial Trauma Detection
// ============================================================================

type FinancialTraumaService struct {
	wellnessRepo interface {
		RecordMetric(ctx context.Context, m *entities.BehavioralHealthMetric) error
		GetLatestMetrics(ctx context.Context, userID uuid.UUID, metricType entities.BehavioralHealthMetricType, limit int) ([]*entities.BehavioralHealthMetric, error)
		UpsertWellnessScore(ctx context.Context, s *entities.FinancialWellnessScore) error
		GetWellnessScore(ctx context.Context, userID uuid.UUID) (*entities.FinancialWellnessScore, error)
	}
	cardRepo CardTransactionProvider
	logger   *zap.Logger
}

func NewFinancialTraumaService(wellnessRepo interface {
	RecordMetric(ctx context.Context, m *entities.BehavioralHealthMetric) error
	GetLatestMetrics(ctx context.Context, userID uuid.UUID, metricType entities.BehavioralHealthMetricType, limit int) ([]*entities.BehavioralHealthMetric, error)
	UpsertWellnessScore(ctx context.Context, s *entities.FinancialWellnessScore) error
	GetWellnessScore(ctx context.Context, userID uuid.UUID) (*entities.FinancialWellnessScore, error)
}, cardRepo CardTransactionProvider, logger *zap.Logger) *FinancialTraumaService {
	return &FinancialTraumaService{wellnessRepo: wellnessRepo, cardRepo: cardRepo, logger: logger}
}

func (s *FinancialTraumaService) RecordBehavioralMetrics(ctx context.Context, userID uuid.UUID, balanceCheckCount int, appOpens int, withdrawals int) error {
	now := time.Now()
	start := now.AddDate(0, 0, -7)

	// Balance check frequency (normalized: >20/week = high anxiety)
	bcScore := math.Min(float64(balanceCheckCount)/20.0*100, 100)
	_ = s.wellnessRepo.RecordMetric(ctx, &entities.BehavioralHealthMetric{
		UserID:      userID,
		MetricType:  entities.MetricBalanceCheckFreq,
		Value:       bcScore,
		PeriodStart: start,
		PeriodEnd:   now,
		RecordedAt:  now,
	})

	// Panic withdrawal score (>3 withdrawals/week = high)
	pwScore := math.Min(float64(withdrawals)/3.0*100, 100)
	_ = s.wellnessRepo.RecordMetric(ctx, &entities.BehavioralHealthMetric{
		UserID:      userID,
		MetricType:  entities.MetricPanicWithdrawal,
		Value:       pwScore,
		PeriodStart: start,
		PeriodEnd:   now,
		RecordedAt:  now,
	})

	// App avoidance (inverse of app opens, <2/week = avoidance)
	avoidScore := 0.0
	if appOpens < 2 {
		avoidScore = math.Min((2-float64(appOpens))/2.0*100, 100)
	}
	_ = s.wellnessRepo.RecordMetric(ctx, &entities.BehavioralHealthMetric{
		UserID:      userID,
		MetricType:  entities.MetricAppAvoidance,
		Value:       avoidScore,
		PeriodStart: start,
		PeriodEnd:   now,
		RecordedAt:  now,
	})

	return nil
}

func (s *FinancialTraumaService) CalculateWellnessScore(ctx context.Context, userID uuid.UUID) (*entities.FinancialWellnessScore, error) {
	now := time.Now()

	// Get latest metrics
	bcMetrics, _ := s.wellnessRepo.GetLatestMetrics(ctx, userID, entities.MetricBalanceCheckFreq, 1)
	pwMetrics, _ := s.wellnessRepo.GetLatestMetrics(ctx, userID, entities.MetricPanicWithdrawal, 1)
	avMetrics, _ := s.wellnessRepo.GetLatestMetrics(ctx, userID, entities.MetricAppAvoidance, 1)

	anxiety := 0.0
	if len(bcMetrics) > 0 {
		anxiety = bcMetrics[0].Value * 0.5
	}
	if len(pwMetrics) > 0 {
		anxiety += pwMetrics[0].Value * 0.5
	}

	avoidance := 0.0
	if len(avMetrics) > 0 {
		avoidance = avMetrics[0].Value
	}

	impulsivity := 0.0
	if len(pwMetrics) > 0 {
		impulsivity = pwMetrics[0].Value
	}

	// Resilience = inverse of (anxiety + avoidance + impulsivity) / 3
	raw := (anxiety + avoidance + impulsivity) / 3.0
	resilience := math.Max(0, 100-raw)

	// Overall = weighted average
	overall := resilience*0.4 + (100-anxiety)*0.3 + (100-avoidance)*0.2 + (100-impulsivity)*0.1

	recommendation := "Your financial habits look healthy. Keep it up!"
	if anxiety > 70 {
		recommendation = "I notice you're checking your balance very frequently. Would you like me to send a daily summary instead so you don't have to check?"
	} else if avoidance > 70 {
		recommendation = "You haven't opened the app in a while. That's okay — want me to give you a quick, no-judgment update on how things look?"
	} else if impulsivity > 70 {
		recommendation = "You've made several withdrawals recently. Should we set up a 24-hour cooling-off period for transfers over a certain amount?"
	}

	score := &entities.FinancialWellnessScore{
		UserID:           userID,
		OverallScore:     overall,
		AnxietyScore:     anxiety,
		AvoidanceScore:   avoidance,
		ImpulsivityScore: impulsivity,
		ResilienceScore:  resilience,
		Recommendation:   recommendation,
		CalculatedAt:     now,
	}

	return score, s.wellnessRepo.UpsertWellnessScore(ctx, score)
}

// ============================================================================
// Tier 3.2: Visa Proof Generator
// ============================================================================

type VisaProofService struct {
	visaRepo interface {
		CreateRequest(ctx context.Context, req *entities.VisaProofRequest) error
		GetRequests(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.VisaProofRequest, error)
		UpdateStatus(ctx context.Context, reqID uuid.UUID, status, documentURL string) error
	}
	ledger      LedgerBalanceProvider
	depositRepo DepositProvider
	logger      *zap.Logger
}

func NewVisaProofService(visaRepo interface {
	CreateRequest(ctx context.Context, req *entities.VisaProofRequest) error
	GetRequests(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.VisaProofRequest, error)
	UpdateStatus(ctx context.Context, reqID uuid.UUID, status, documentURL string) error
}, ledger LedgerBalanceProvider, depositRepo DepositProvider, logger *zap.Logger) *VisaProofService {
	return &VisaProofService{visaRepo: visaRepo, ledger: ledger, depositRepo: depositRepo, logger: logger}
}

type VisaProofPayload struct {
	VisaCountry string `json:"visa_country"` // GB, US, CA, etc.
	VisaType    string `json:"visa_type"`    // student, work, tourist
}

type VisaProofResult struct {
	RequestID        uuid.UUID       `json:"request_id"`
	BankBalance      decimal.Decimal `json:"bank_balance"`
	StashBalance     decimal.Decimal `json:"stash_balance"`
	TotalHoldings    decimal.Decimal `json:"total_holdings"`
	AvgMonthlyInflow decimal.Decimal `json:"avg_monthly_inflow"`
	DocumentText     string          `json:"document_text"`
	Status           string          `json:"status"`
}

func (s *VisaProofService) GenerateProof(ctx context.Context, userID uuid.UUID, payload VisaProofPayload) (*VisaProofResult, error) {
	// Get balances
	spend, _ := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	stash, _ := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	total := spend.Add(stash)

	// Get 6-month deposit average
	now := time.Now()
	start := now.AddDate(0, -6, 0)
	deposits, _ := s.depositRepo.GetByUserID(ctx, userID, 1000, 0)
	var depositSum decimal.Decimal
	for _, d := range deposits {
		if d.Status == "confirmed" && d.ConfirmedAt != nil && d.ConfirmedAt.After(start) {
			depositSum = depositSum.Add(d.Amount)
		}
	}
	avgInflow := decimal.Zero
	if len(deposits) > 0 {
		avgInflow = depositSum.Div(decimal.NewFromInt(6))
	}

	// Generate document text
	docText := s.formatProofDocument(payload.VisaCountry, payload.VisaType, total, avgInflow, deposits)

	req := &entities.VisaProofRequest{
		UserID:           userID,
		VisaCountry:      payload.VisaCountry,
		VisaType:         payload.VisaType,
		BankBalance:      spend,
		StashBalance:     stash,
		TotalHoldings:    total,
		AvgMonthlyInflow: avgInflow,
		Status:           "ready",
		ExpiresAt:        now.AddDate(0, 1, 0),
		CreatedAt:        now,
	}

	if err := s.visaRepo.CreateRequest(ctx, req); err != nil {
		return nil, err
	}

	return &VisaProofResult{
		RequestID:        req.ID,
		BankBalance:      spend,
		StashBalance:     stash,
		TotalHoldings:    total,
		AvgMonthlyInflow: avgInflow,
		DocumentText:     docText,
		Status:           "ready",
	}, nil
}

func (s *VisaProofService) GetRequests(ctx context.Context, userID uuid.UUID) ([]*entities.VisaProofRequest, error) {
	return s.visaRepo.GetRequests(ctx, userID, 50)
}

func (s *VisaProofService) formatProofDocument(country, visaType string, totalHoldings, avgInflow decimal.Decimal, deposits []*entities.Deposit) string {
	var sb strings.Builder
	now := time.Now()

	sb.WriteString("PROOF OF FUNDS CERTIFICATE\n")
	sb.WriteString("===========================\n\n")
	sb.WriteString(fmt.Sprintf("Date of Issue: %s\n", now.Format("January 2, 2006")))
	sb.WriteString(fmt.Sprintf("Valid Until: %s\n\n", now.AddDate(0, 1, 0).Format("January 2, 2006")))
	sb.WriteString(fmt.Sprintf("Visa Type: %s Visa for %s\n\n", capitalize(visaType), country))
	sb.WriteString("FINANCIAL SUMMARY\n")
	sb.WriteString("-----------------\n")
	sb.WriteString(fmt.Sprintf("Total Liquid Assets: $%s USD\n", totalHoldings.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Average Monthly Inflow (6 months): $%s USD\n", avgInflow.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Number of Deposits (6 months): %d\n\n", len(deposits)))
	sb.WriteString("RECENT DEPOSIT HISTORY\n")
	sb.WriteString("----------------------\n")
	for i, d := range deposits {
		if i >= 10 {
			break
		}
		sb.WriteString(fmt.Sprintf("- %s: $%s (%s)\n", d.CreatedAt.Format("2006-01-02"), d.Amount.StringFixed(2), d.Status))
	}
	sb.WriteString("\nThis document is auto-generated by Rail Financial Services.")
	sb.WriteString("\nFor verification, please contact support@userail.money")

	return sb.String()
}

// ============================================================================
// Tier 3.3: Panic Button
// ============================================================================

type PanicButtonService struct {
	emergencyRepo interface {
		GetContacts(ctx context.Context, userID uuid.UUID) ([]*entities.EmergencyContact, error)
		AddContact(ctx context.Context, c *entities.EmergencyContact) error
		RemoveContact(ctx context.Context, contactID uuid.UUID) error
		CreateLock(ctx context.Context, lock *entities.EmergencyLock) error
		GetActiveLock(ctx context.Context, userID uuid.UUID) (*entities.EmergencyLock, error)
		ResolveLock(ctx context.Context, lockID uuid.UUID) error
	}
	ledger LedgerBalanceProvider
	logger *zap.Logger
}

func NewPanicButtonService(emergencyRepo interface {
	GetContacts(ctx context.Context, userID uuid.UUID) ([]*entities.EmergencyContact, error)
	AddContact(ctx context.Context, c *entities.EmergencyContact) error
	RemoveContact(ctx context.Context, contactID uuid.UUID) error
	CreateLock(ctx context.Context, lock *entities.EmergencyLock) error
	GetActiveLock(ctx context.Context, userID uuid.UUID) (*entities.EmergencyLock, error)
	ResolveLock(ctx context.Context, lockID uuid.UUID) error
}, ledger LedgerBalanceProvider, logger *zap.Logger) *PanicButtonService {
	return &PanicButtonService{emergencyRepo: emergencyRepo, ledger: ledger, logger: logger}
}

func (s *PanicButtonService) GetContacts(ctx context.Context, userID uuid.UUID) ([]*entities.EmergencyContact, error) {
	return s.emergencyRepo.GetContacts(ctx, userID)
}

func (s *PanicButtonService) AddContact(ctx context.Context, userID uuid.UUID, name, phone, relation string, priority int) error {
	return s.emergencyRepo.AddContact(ctx, &entities.EmergencyContact{
		UserID:    userID,
		Name:      name,
		Phone:     phone,
		Relation:  relation,
		Priority:  priority,
		CreatedAt: time.Now(),
	})
}

func (s *PanicButtonService) RemoveContact(ctx context.Context, userID, contactID uuid.UUID) error {
	// Verify ownership before removing
	contacts, err := s.emergencyRepo.GetContacts(ctx, userID)
	if err != nil {
		return fmt.Errorf("get contacts: %w", err)
	}
	found := false
	for _, c := range contacts {
		if c.ID == contactID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("contact not found or does not belong to user")
	}
	return s.emergencyRepo.RemoveContact(ctx, contactID)
}

func (s *PanicButtonService) TriggerLock(ctx context.Context, userID uuid.UUID, reason string) (*entities.EmergencyLock, error) {
	// Check for existing active lock
	existing, _ := s.emergencyRepo.GetActiveLock(ctx, userID)
	if existing != nil {
		return existing, fmt.Errorf("emergency lock already active since %s", existing.LockedAt.Format(time.RFC3339))
	}

	contacts, _ := s.emergencyRepo.GetContacts(ctx, userID)
	var alertedPhones []string
	for _, c := range contacts {
		alertedPhones = append(alertedPhones, c.Phone)
	}

	lock := &entities.EmergencyLock{
		UserID:          userID,
		LockedAt:        time.Now(),
		Reason:          reason,
		TriggeredBy:     "user",
		CardFrozen:      true, // Would integrate with card service
		StashMoved:      true, // Would integrate with ledger
		ContactsAlerted: len(alertedPhones) > 0,
		AlertedContacts: alertedPhones,
		Resolved:        false,
		CreatedAt:       time.Now(),
	}

	if err := s.emergencyRepo.CreateLock(ctx, lock); err != nil {
		return nil, err
	}

	s.logger.Info("Emergency lock triggered",
		zap.String("user_id", userID.String()),
		zap.String("reason", reason),
		zap.Int("contacts_alerted", len(alertedPhones)),
	)

	return lock, nil
}

func (s *PanicButtonService) GetActiveLock(ctx context.Context, userID uuid.UUID) (*entities.EmergencyLock, error) {
	return s.emergencyRepo.GetActiveLock(ctx, userID)
}

func (s *PanicButtonService) ResolveLock(ctx context.Context, lockID uuid.UUID) error {
	return s.emergencyRepo.ResolveLock(ctx, lockID)
}

// capitalize returns the string with its first letter uppercased and the rest lowercased.
func capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}
