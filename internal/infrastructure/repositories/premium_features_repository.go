package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ============================================================================
// Family Support (Black Tax)
// ============================================================================

type FamilySupportRepository struct {
	db *sqlx.DB
}

func NewFamilySupportRepository(db *sqlx.DB) *FamilySupportRepository {
	return &FamilySupportRepository{db: db}
}

func (r *FamilySupportRepository) GetBudget(ctx context.Context, userID uuid.UUID) (*entities.FamilySupportBudget, error) {
	var b entities.FamilySupportBudget
	err := r.db.GetContext(ctx, &b, `SELECT user_id, monthly_limit, alert_threshold_pct, created_at, updated_at FROM family_support_budgets WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *FamilySupportRepository) UpsertBudget(ctx context.Context, b *entities.FamilySupportBudget) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO family_support_budgets (user_id, monthly_limit, alert_threshold_pct, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			monthly_limit = EXCLUDED.monthly_limit,
			alert_threshold_pct = EXCLUDED.alert_threshold_pct,
			updated_at = EXCLUDED.updated_at`,
		b.UserID, b.MonthlyLimit, b.AlertThresholdPct, b.CreatedAt, b.UpdatedAt)
	return err
}

func (r *FamilySupportRepository) GetRecipients(ctx context.Context, userID uuid.UUID) ([]*entities.FamilySupportRecipient, error) {
	var recs []*entities.FamilySupportRecipient
	err := r.db.SelectContext(ctx, &recs, `
		SELECT id, user_id, recipient_name, recipient_identifier, relationship, monthly_average, total_sent_lifetime, last_sent_at, send_count, created_at, updated_at
		FROM family_support_recipients WHERE user_id = $1 ORDER BY total_sent_lifetime DESC`, userID)
	return recs, err
}

func (r *FamilySupportRepository) UpsertRecipient(ctx context.Context, rec *entities.FamilySupportRecipient) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO family_support_recipients (id, user_id, recipient_name, recipient_identifier, relationship, monthly_average, total_sent_lifetime, last_sent_at, send_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (user_id, recipient_identifier) DO UPDATE SET
			recipient_name = EXCLUDED.recipient_name,
			relationship = EXCLUDED.relationship,
			monthly_average = EXCLUDED.monthly_average,
			total_sent_lifetime = EXCLUDED.total_sent_lifetime,
			last_sent_at = EXCLUDED.last_sent_at,
			send_count = EXCLUDED.send_count,
			updated_at = EXCLUDED.updated_at`,
		rec.ID, rec.UserID, rec.RecipientName, rec.RecipientIdentifier, rec.Relationship, rec.MonthlyAverage, rec.TotalSentLifetime, rec.LastSentAt, rec.SendCount, rec.CreatedAt, rec.UpdatedAt)
	return err
}

func (r *FamilySupportRepository) GetMonthlySentTotal(ctx context.Context, userID uuid.UUID, year int, month int) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.GetContext(ctx, &total, `
		SELECT COALESCE(SUM(amount), 0) FROM p2p_transfers 
		WHERE sender_id = $1 AND status = 'completed'
			AND EXTRACT(YEAR FROM created_at) = $2 AND EXTRACT(MONTH FROM created_at) = $3`,
		userID, year, month)
	return total, err
}

// ============================================================================
// Scam Intelligence
// ============================================================================

type ScamRepository struct {
	db *sqlx.DB
}

func NewScamRepository(db *sqlx.DB) *ScamRepository {
	return &ScamRepository{db: db}
}

func (r *ScamRepository) GetRiskPatterns(ctx context.Context) ([]*entities.MerchantRiskPattern, error) {
	var patterns []*entities.MerchantRiskPattern
	err := r.db.SelectContext(ctx, &patterns, `SELECT id, pattern, risk_level, category, description, report_count, created_at FROM merchant_risk_patterns ORDER BY report_count DESC`)
	return patterns, err
}

func (r *ScamRepository) CreateAlert(ctx context.Context, alert *entities.UserScamAlert) error {
	if alert.ID == uuid.Nil {
		alert.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_scam_alerts (id, user_id, merchant_name, transaction_id, alert_type, risk_level, reason, dismissed, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		alert.ID, alert.UserID, alert.MerchantName, alert.TransactionID, alert.AlertType, alert.RiskLevel, alert.Reason, alert.Dismissed, alert.CreatedAt)
	return err
}

func (r *ScamRepository) GetActiveAlerts(ctx context.Context, userID uuid.UUID) ([]*entities.UserScamAlert, error) {
	var alerts []*entities.UserScamAlert
	err := r.db.SelectContext(ctx, &alerts, `
		SELECT id, user_id, merchant_name, transaction_id, alert_type, risk_level, reason, dismissed, created_at
		FROM user_scam_alerts WHERE user_id = $1 AND dismissed = FALSE ORDER BY created_at DESC`, userID)
	return alerts, err
}

func (r *ScamRepository) DismissAlert(ctx context.Context, alertID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE user_scam_alerts SET dismissed = TRUE WHERE id = $1`, alertID)
	return err
}

// ============================================================================
// Tax Residency
// ============================================================================

type TaxResidencyRepository struct {
	db *sqlx.DB
}

func NewTaxResidencyRepository(db *sqlx.DB) *TaxResidencyRepository {
	return &TaxResidencyRepository{db: db}
}

func (r *TaxResidencyRepository) LogLocation(ctx context.Context, log *entities.UserLocationLog) error {
	if log.ID == uuid.Nil {
		log.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_location_logs (id, user_id, country, entered_at, exited_at, source, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		log.ID, log.UserID, log.Country, log.EnteredAt, log.ExitedAt, log.Source, log.CreatedAt)
	return err
}

func (r *TaxResidencyRepository) GetLocationLogs(ctx context.Context, userID uuid.UUID, country string, from, to time.Time) ([]*entities.UserLocationLog, error) {
	var logs []*entities.UserLocationLog
	err := r.db.SelectContext(ctx, &logs, `
		SELECT id, user_id, country, entered_at, exited_at, source, created_at
		FROM user_location_logs
		WHERE user_id = $1 AND country = $2 AND entered_at >= $3 AND entered_at <= $4
		ORDER BY entered_at DESC`, userID, country, from, to)
	return logs, err
}

func (r *TaxResidencyRepository) GetTaxProfile(ctx context.Context, userID uuid.UUID) (*entities.UserTaxProfile, error) {
	var p entities.UserTaxProfile
	err := r.db.GetContext(ctx, &p, `SELECT * FROM user_tax_profiles WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

func (r *TaxResidencyRepository) UpsertTaxProfile(ctx context.Context, p *entities.UserTaxProfile) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_tax_profiles (user_id, primary_tax_country, secondary_tax_country, days_in_primary, days_in_secondary, alert_threshold, tax_year_start_month, tax_year_start_day, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (user_id) DO UPDATE SET
			primary_tax_country = EXCLUDED.primary_tax_country,
			secondary_tax_country = EXCLUDED.secondary_tax_country,
			alert_threshold = EXCLUDED.alert_threshold,
			tax_year_start_month = EXCLUDED.tax_year_start_month,
			tax_year_start_day = EXCLUDED.tax_year_start_day,
			updated_at = EXCLUDED.updated_at`,
		p.UserID, p.PrimaryTaxCountry, p.SecondaryTaxCountry, p.DaysInPrimary, p.DaysInSecondary, p.AlertThreshold, p.TaxYearStartMonth, p.TaxYearStartDay, p.CreatedAt, p.UpdatedAt)
	return err
}

// ============================================================================
// Financial Trauma / Wellness
// ============================================================================

type WellnessRepository struct {
	db *sqlx.DB
}

func NewWellnessRepository(db *sqlx.DB) *WellnessRepository {
	return &WellnessRepository{db: db}
}

func (r *WellnessRepository) RecordMetric(ctx context.Context, m *entities.BehavioralHealthMetric) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO behavioral_health_metrics (id, user_id, metric_type, value, period_start, period_end, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		m.ID, m.UserID, m.MetricType, m.Value, m.PeriodStart, m.PeriodEnd, m.RecordedAt)
	return err
}

func (r *WellnessRepository) GetLatestMetrics(ctx context.Context, userID uuid.UUID, metricType entities.BehavioralHealthMetricType, limit int) ([]*entities.BehavioralHealthMetric, error) {
	var metrics []*entities.BehavioralHealthMetric
	err := r.db.SelectContext(ctx, &metrics, `
		SELECT id, user_id, metric_type, value, period_start, period_end, recorded_at
		FROM behavioral_health_metrics
		WHERE user_id = $1 AND metric_type = $2
		ORDER BY recorded_at DESC LIMIT $3`, userID, metricType, limit)
	return metrics, err
}

func (r *WellnessRepository) GetWellnessScore(ctx context.Context, userID uuid.UUID) (*entities.FinancialWellnessScore, error) {
	var s entities.FinancialWellnessScore
	err := r.db.GetContext(ctx, &s, `SELECT * FROM financial_wellness_scores WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

func (r *WellnessRepository) UpsertWellnessScore(ctx context.Context, s *entities.FinancialWellnessScore) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO financial_wellness_scores (user_id, overall_score, anxiety_score, avoidance_score, impulsivity_score, resilience_score, recommendation, calculated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id) DO UPDATE SET
			overall_score = EXCLUDED.overall_score,
			anxiety_score = EXCLUDED.anxiety_score,
			avoidance_score = EXCLUDED.avoidance_score,
			impulsivity_score = EXCLUDED.impulsivity_score,
			resilience_score = EXCLUDED.resilience_score,
			recommendation = EXCLUDED.recommendation,
			calculated_at = EXCLUDED.calculated_at`,
		s.UserID, s.OverallScore, s.AnxietyScore, s.AvoidanceScore, s.ImpulsivityScore, s.ResilienceScore, s.Recommendation, s.CalculatedAt)
	return err
}

// ============================================================================
// Emergency / Panic Button
// ============================================================================

type EmergencyRepository struct {
	db *sqlx.DB
}

func NewEmergencyRepository(db *sqlx.DB) *EmergencyRepository {
	return &EmergencyRepository{db: db}
}

func (r *EmergencyRepository) GetContacts(ctx context.Context, userID uuid.UUID) ([]*entities.EmergencyContact, error) {
	var contacts []*entities.EmergencyContact
	err := r.db.SelectContext(ctx, &contacts, `
		SELECT id, user_id, name, phone, email, relation, priority, created_at
		FROM emergency_contacts WHERE user_id = $1 ORDER BY priority ASC`, userID)
	return contacts, err
}

func (r *EmergencyRepository) AddContact(ctx context.Context, c *entities.EmergencyContact) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO emergency_contacts (id, user_id, name, phone, email, relation, priority, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		c.ID, c.UserID, c.Name, c.Phone, c.Email, c.Relation, c.Priority, c.CreatedAt)
	return err
}

func (r *EmergencyRepository) RemoveContact(ctx context.Context, contactID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM emergency_contacts WHERE id = $1`, contactID)
	return err
}

func (r *EmergencyRepository) CreateLock(ctx context.Context, lock *entities.EmergencyLock) error {
	if lock.ID == uuid.Nil {
		lock.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO emergency_locks (id, user_id, locked_at, unlocked_at, reason, triggered_by, card_frozen, stash_moved, contacts_alerted, alerted_contacts, resolved, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		lock.ID, lock.UserID, lock.LockedAt, lock.UnlockedAt, lock.Reason, lock.TriggeredBy, lock.CardFrozen, lock.StashMoved, lock.ContactsAlerted, lock.AlertedContacts, lock.Resolved, lock.CreatedAt)
	return err
}

func (r *EmergencyRepository) GetActiveLock(ctx context.Context, userID uuid.UUID) (*entities.EmergencyLock, error) {
	var lock entities.EmergencyLock
	err := r.db.GetContext(ctx, &lock, `
		SELECT id, user_id, locked_at, unlocked_at, reason, triggered_by, card_frozen, stash_moved, contacts_alerted, alerted_contacts, resolved, created_at
		FROM emergency_locks WHERE user_id = $1 AND resolved = FALSE ORDER BY locked_at DESC LIMIT 1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &lock, err
}

func (r *EmergencyRepository) ResolveLock(ctx context.Context, lockID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE emergency_locks SET resolved = TRUE, unlocked_at = NOW() WHERE id = $1`, lockID)
	return err
}

// ============================================================================
// Receipt Split
// ============================================================================

type ReceiptSplitRepository struct {
	db *sqlx.DB
}

func NewReceiptSplitRepository(db *sqlx.DB) *ReceiptSplitRepository {
	return &ReceiptSplitRepository{db: db}
}

func (r *ReceiptSplitRepository) CreateSplit(ctx context.Context, split *entities.ReceiptSplit) error {
	if split.ID == uuid.Nil {
		split.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO receipt_splits (id, receipt_id, user_id, total_amount, currency, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		split.ID, split.ReceiptID, split.UserID, split.TotalAmount, split.Currency, split.Status, split.CreatedAt, split.UpdatedAt)
	return err
}

func (r *ReceiptSplitRepository) CreateSplitItem(ctx context.Context, item *entities.ReceiptSplitItem) error {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO receipt_split_items (id, split_id, item_name, amount, assigned_to, paid, p2p_transfer_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		item.ID, item.SplitID, item.ItemName, item.Amount, item.AssignedTo, item.Paid, item.P2PTransferID, item.CreatedAt)
	return err
}

func (r *ReceiptSplitRepository) GetSplitByReceipt(ctx context.Context, userID, receiptID uuid.UUID) (*entities.ReceiptSplit, error) {
	var split entities.ReceiptSplit
	err := r.db.GetContext(ctx, &split, `
		SELECT id, receipt_id, user_id, total_amount, currency, status, created_at, updated_at
		FROM receipt_splits WHERE user_id = $1 AND receipt_id = $2`, userID, receiptID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []*entities.ReceiptSplitItem
	items, err = r.GetSplitItems(ctx, split.ID)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		split.Items = append(split.Items, *it)
	}
	return &split, nil
}

func (r *ReceiptSplitRepository) GetSplitItems(ctx context.Context, splitID uuid.UUID) ([]*entities.ReceiptSplitItem, error) {
	var items []*entities.ReceiptSplitItem
	err := r.db.SelectContext(ctx, &items, `
		SELECT id, split_id, item_name, amount, assigned_to, paid, p2p_transfer_id, created_at
		FROM receipt_split_items WHERE split_id = $1`, splitID)
	return items, err
}

func (r *ReceiptSplitRepository) AddParticipant(ctx context.Context, p *entities.ReceiptSplitParticipant) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO receipt_split_participants (id, split_id, rail_tag, participant_user_id, amount, status, p2p_transfer_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`,
		p.ID, p.SplitID, p.RailTag, p.ParticipantUID, p.Amount, p.Status, p.P2PTransferID)
	return err
}

func (r *ReceiptSplitRepository) GetByID(ctx context.Context, userID, splitID uuid.UUID) (*entities.ReceiptSplit, error) {
	var s entities.ReceiptSplit
	err := r.db.GetContext(ctx, &s, `SELECT * FROM receipt_splits WHERE id = $1 AND user_id = $2`, splitID, userID)
	if err != nil {
		return nil, err
	}
	var participants []entities.ReceiptSplitParticipant
	_ = r.db.SelectContext(ctx, &participants, `SELECT * FROM receipt_split_participants WHERE split_id = $1 ORDER BY created_at`, splitID)
	s.Participants = participants
	return &s, nil
}

func (r *ReceiptSplitRepository) ListByUser(ctx context.Context, userID uuid.UUID, status string, limit int) ([]entities.ReceiptSplit, error) {
	var splits []entities.ReceiptSplit
	if status != "" {
		err := r.db.SelectContext(ctx, &splits, `SELECT * FROM receipt_splits WHERE user_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3`, userID, status, limit)
		return splits, err
	}
	err := r.db.SelectContext(ctx, &splits, `SELECT * FROM receipt_splits WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	return splits, err
}

func (r *ReceiptSplitRepository) UpdateParticipantStatus(ctx context.Context, participantID uuid.UUID, status string) error {
	query := `UPDATE receipt_split_participants SET status = $1`
	if status == entities.ParticipantPaid {
		query += `, paid_at = NOW()`
	}
	query += ` WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, status, participantID)
	return err
}

func (r *ReceiptSplitRepository) IncrementReminder(ctx context.Context, participantID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE receipt_split_participants SET reminder_count = reminder_count + 1, last_reminded_at = NOW() WHERE id = $1`, participantID)
	return err
}

func (r *ReceiptSplitRepository) UpdateSplitStatus(ctx context.Context, splitID uuid.UUID, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE receipt_splits SET status = $1, updated_at = NOW() WHERE id = $2`, status, splitID)
	return err
}

func (r *ReceiptSplitRepository) GetPendingParticipants(ctx context.Context, splitID uuid.UUID) ([]entities.ReceiptSplitParticipant, error) {
	var participants []entities.ReceiptSplitParticipant
	err := r.db.SelectContext(ctx, &participants, `SELECT * FROM receipt_split_participants WHERE split_id = $1 AND status IN ('pending', 'requested')`, splitID)
	return participants, err
}

// ============================================================================
// Currency Exchange Rates
// ============================================================================

type ExchangeRateRepository struct {
	db *sqlx.DB
}

func NewExchangeRateRepository(db *sqlx.DB) *ExchangeRateRepository {
	return &ExchangeRateRepository{db: db}
}

func (r *ExchangeRateRepository) SaveRate(ctx context.Context, rate *entities.CurrencyExchangeRate) error {
	if rate.ID == uuid.Nil {
		rate.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO currency_exchange_rates (id, from_code, to_code, rate, date, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (from_code, to_code, date) DO UPDATE SET rate = EXCLUDED.rate, created_at = EXCLUDED.created_at`,
		rate.ID, rate.FromCode, rate.ToCode, rate.Rate, rate.Date, rate.CreatedAt)
	return err
}

func (r *ExchangeRateRepository) GetRateOnDate(ctx context.Context, from, to string, date time.Time) (decimal.Decimal, error) {
	var rate decimal.Decimal
	err := r.db.GetContext(ctx, &rate, `
		SELECT rate FROM currency_exchange_rates 
		WHERE from_code = $1 AND to_code = $2 AND date <= $3
		ORDER BY date DESC LIMIT 1`, from, to, date)
	if err == sql.ErrNoRows {
		return decimal.Zero, fmt.Errorf("no rate found for %s/%s on or before %s", from, to, date.Format("2006-01-02"))
	}
	return rate, err
}

func (r *ExchangeRateRepository) GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error) {
	var rate decimal.Decimal
	err := r.db.GetContext(ctx, &rate, `
		SELECT rate FROM currency_exchange_rates 
		WHERE from_code = $1 AND to_code = $2
		ORDER BY date DESC LIMIT 1`, from, to)
	if err == sql.ErrNoRows {
		return decimal.Zero, fmt.Errorf("no rate found for %s/%s", from, to)
	}
	return rate, err
}

// ============================================================================
// Visa Proof
// ============================================================================

type VisaProofRepository struct {
	db *sqlx.DB
}

func NewVisaProofRepository(db *sqlx.DB) *VisaProofRepository {
	return &VisaProofRepository{db: db}
}

func (r *VisaProofRepository) CreateRequest(ctx context.Context, req *entities.VisaProofRequest) error {
	if req.ID == uuid.Nil {
		req.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO visa_proof_requests (id, user_id, visa_country, visa_type, bank_balance, stash_balance, total_holdings, avg_monthly_inflow, document_url, status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		req.ID, req.UserID, req.VisaCountry, req.VisaType, req.BankBalance, req.StashBalance, req.TotalHoldings, req.AvgMonthlyInflow, req.DocumentURL, req.Status, req.ExpiresAt, req.CreatedAt)
	return err
}

func (r *VisaProofRepository) GetRequests(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.VisaProofRequest, error) {
	var reqs []*entities.VisaProofRequest
	err := r.db.SelectContext(ctx, &reqs, `
		SELECT id, user_id, visa_country, visa_type, bank_balance, stash_balance, total_holdings, avg_monthly_inflow, document_url, status, expires_at, created_at
		FROM visa_proof_requests WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	return reqs, err
}

func (r *VisaProofRepository) UpdateStatus(ctx context.Context, reqID uuid.UUID, status, documentURL string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE visa_proof_requests SET status = $2, document_url = $3 WHERE id = $1`, reqID, status, documentURL)
	return err
}
