package security

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type FraudDetectionService struct {
	db     *sql.DB
	redis  *redis.Client
	logger *zap.Logger
}

type FraudSignal struct {
	Type      string
	Value     float64
	Metadata  map[string]interface{}
	Timestamp time.Time
}

type FraudCheckResult struct {
	Score         float64
	Signals       []FraudSignal
	Action        FraudAction
	RequiresMFA   bool
	RequiresReview bool
	BlockReason   string
}

type FraudAction string

const (
	FraudActionAllow   FraudAction = "allow"
	FraudActionMFA     FraudAction = "require_mfa"
	FraudActionReview  FraudAction = "manual_review"
	FraudActionBlock   FraudAction = "block"
)

type TransactionContext struct {
	UserID        uuid.UUID
	Amount        decimal.Decimal
	Type          string // deposit, withdrawal, trade, transfer
	Destination   string
	IPAddress     string
	DeviceID      string
	SessionID     string
}

type UserBehaviorPattern struct {
	UserID               uuid.UUID
	TypicalLoginHours    []int
	TypicalCountries     []string
	TypicalDevices       int
	AvgSessionDuration   int
	AvgTransactionsPerDay float64
	AvgTransactionAmount decimal.Decimal
	LastAnalyzedAt       *time.Time
}

func NewFraudDetectionService(db *sql.DB, redis *redis.Client, logger *zap.Logger) *FraudDetectionService {
	return &FraudDetectionService{
		db:     db,
		redis:  redis,
		logger: logger,
	}
}

// CheckTransaction performs comprehensive fraud analysis on a transaction
func (s *FraudDetectionService) CheckTransaction(ctx context.Context, txCtx *TransactionContext) (*FraudCheckResult, error) {
	result := &FraudCheckResult{
		Signals: []FraudSignal{},
		Action:  FraudActionAllow,
	}

	// Get user's behavior pattern
	pattern, err := s.getUserBehaviorPattern(ctx, txCtx.UserID)
	if err != nil {
		s.logger.Warn("Failed to get behavior pattern", zap.Error(err))
	}

	// Run all fraud checks
	signals := []FraudSignal{}

	// 1. Velocity check
	if signal := s.checkVelocity(ctx, txCtx); signal != nil {
		signals = append(signals, *signal)
	}

	// 2. Amount anomaly check
	if signal := s.checkAmountAnomaly(ctx, txCtx, pattern); signal != nil {
		signals = append(signals, *signal)
	}

	// 3. Time-based anomaly
	if signal := s.checkTimeAnomaly(ctx, txCtx, pattern); signal != nil {
		signals = append(signals, *signal)
	}

	// 4. Device anomaly
	if signal := s.checkDeviceAnomaly(ctx, txCtx); signal != nil {
		signals = append(signals, *signal)
	}

	// 5. Destination risk (for withdrawals/transfers)
	if signal := s.checkDestinationRisk(ctx, txCtx); signal != nil {
		signals = append(signals, *signal)
	}

	// 6. Account age check
	if signal := s.checkAccountAge(ctx, txCtx); signal != nil {
		signals = append(signals, *signal)
	}

	// Calculate composite score
	result.Signals = signals
	result.Score = s.calculateCompositeScore(signals)

	// Store signals for analysis
	s.storeSignals(ctx, txCtx.UserID, txCtx.SessionID, signals)

	// Determine action based on score
	result.Action, result.RequiresMFA, result.RequiresReview = s.determineAction(result.Score)

	if result.Action == FraudActionBlock {
		result.BlockReason = "high_fraud_score"
	}

	// Update user's fraud score
	s.updateUserFraudScore(ctx, txCtx.UserID, result.Score)

	s.logger.Info("Fraud check completed",
		zap.String("user_id", txCtx.UserID.String()),
		zap.Float64("score", result.Score),
		zap.String("action", string(result.Action)))

	return result, nil
}

// checkVelocity detects unusual transaction frequency
func (s *FraudDetectionService) checkVelocity(ctx context.Context, txCtx *TransactionContext) *FraudSignal {
	// Count transactions in last hour
	var hourlyCount int
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM transactions 
		WHERE user_id = $1 AND created_at > NOW() - INTERVAL '1 hour'`,
		txCtx.UserID).Scan(&hourlyCount)

	// Count transactions in last 24 hours
	var dailyCount int
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM transactions 
		WHERE user_id = $1 AND created_at > NOW() - INTERVAL '24 hours'`,
		txCtx.UserID).Scan(&dailyCount)

	// High velocity thresholds
	if hourlyCount > 10 || dailyCount > 50 {
		value := math.Min(float64(hourlyCount)/10.0, 1.0)
		return &FraudSignal{
			Type:      "velocity",
			Value:     value,
			Metadata:  map[string]interface{}{"hourly": hourlyCount, "daily": dailyCount},
			Timestamp: time.Now(),
		}
	}

	return nil
}

// checkAmountAnomaly detects unusual transaction amounts
func (s *FraudDetectionService) checkAmountAnomaly(ctx context.Context, txCtx *TransactionContext, pattern *UserBehaviorPattern) *FraudSignal {
	if pattern == nil || pattern.AvgTransactionAmount.IsZero() {
		return nil
	}

	// Calculate deviation from average
	avgAmount := pattern.AvgTransactionAmount.InexactFloat64()
	currentAmount := txCtx.Amount.InexactFloat64()

	if avgAmount == 0 {
		return nil
	}

	deviation := (currentAmount - avgAmount) / avgAmount

	// Flag if >3x average
	if deviation > 3.0 {
		return &FraudSignal{
			Type:      "amount_anomaly",
			Value:     math.Min(deviation/5.0, 1.0),
			Metadata:  map[string]interface{}{"avg": avgAmount, "current": currentAmount, "deviation": deviation},
			Timestamp: time.Now(),
		}
	}

	return nil
}

// checkTimeAnomaly detects unusual login/transaction times
func (s *FraudDetectionService) checkTimeAnomaly(ctx context.Context, txCtx *TransactionContext, pattern *UserBehaviorPattern) *FraudSignal {
	if pattern == nil || len(pattern.TypicalLoginHours) == 0 {
		return nil
	}

	currentHour := time.Now().Hour()
	isTypical := false
	for _, h := range pattern.TypicalLoginHours {
		if h == currentHour || h == (currentHour+1)%24 || h == (currentHour+23)%24 {
			isTypical = true
			break
		}
	}

	if !isTypical {
		return &FraudSignal{
			Type:      "time_anomaly",
			Value:     0.3,
			Metadata:  map[string]interface{}{"current_hour": currentHour, "typical_hours": pattern.TypicalLoginHours},
			Timestamp: time.Now(),
		}
	}

	return nil
}

// checkDeviceAnomaly detects new or suspicious devices
func (s *FraudDetectionService) checkDeviceAnomaly(ctx context.Context, txCtx *TransactionContext) *FraudSignal {
	if txCtx.DeviceID == "" {
		return &FraudSignal{
			Type:      "device_anomaly",
			Value:     0.2,
			Metadata:  map[string]interface{}{"reason": "no_device_id"},
			Timestamp: time.Now(),
		}
	}

	// Check if device is known
	var isTrusted bool
	err := s.db.QueryRowContext(ctx, `
		SELECT is_trusted FROM known_devices 
		WHERE user_id = $1 AND fingerprint = $2`,
		txCtx.UserID, txCtx.DeviceID).Scan(&isTrusted)

	if err == sql.ErrNoRows {
		return &FraudSignal{
			Type:      "device_anomaly",
			Value:     0.4,
			Metadata:  map[string]interface{}{"reason": "new_device"},
			Timestamp: time.Now(),
		}
	}

	if !isTrusted {
		return &FraudSignal{
			Type:      "device_anomaly",
			Value:     0.2,
			Metadata:  map[string]interface{}{"reason": "untrusted_device"},
			Timestamp: time.Now(),
		}
	}

	return nil
}

// checkDestinationRisk checks withdrawal/transfer destinations
func (s *FraudDetectionService) checkDestinationRisk(ctx context.Context, txCtx *TransactionContext) *FraudSignal {
	if txCtx.Destination == "" || (txCtx.Type != "withdrawal" && txCtx.Type != "transfer") {
		return nil
	}

	// Check if destination is new
	var usedBefore bool
	s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM transactions 
		WHERE user_id = $1 AND destination_address = $2 AND created_at < NOW() - INTERVAL '24 hours')`,
		txCtx.UserID, txCtx.Destination).Scan(&usedBefore)

	if !usedBefore {
		return &FraudSignal{
			Type:      "destination_risk",
			Value:     0.3,
			Metadata:  map[string]interface{}{"reason": "new_destination"},
			Timestamp: time.Now(),
		}
	}

	return nil
}

// checkAccountAge flags new accounts with high-value transactions
func (s *FraudDetectionService) checkAccountAge(ctx context.Context, txCtx *TransactionContext) *FraudSignal {
	var createdAt time.Time
	err := s.db.QueryRowContext(ctx, "SELECT created_at FROM users WHERE id = $1", txCtx.UserID).Scan(&createdAt)
	if err != nil {
		return nil
	}

	accountAge := time.Since(createdAt)
	amount := txCtx.Amount.InexactFloat64()

	// New account (<7 days) with high value transaction (>$1000)
	if accountAge < 7*24*time.Hour && amount > 1000 {
		return &FraudSignal{
			Type:      "account_age",
			Value:     0.4,
			Metadata:  map[string]interface{}{"age_days": accountAge.Hours() / 24, "amount": amount},
			Timestamp: time.Now(),
		}
	}

	// Very new account (<24 hours) with any significant transaction (>$100)
	if accountAge < 24*time.Hour && amount > 100 {
		return &FraudSignal{
			Type:      "account_age",
			Value:     0.5,
			Metadata:  map[string]interface{}{"age_hours": accountAge.Hours(), "amount": amount},
			Timestamp: time.Now(),
		}
	}

	return nil
}

// calculateCompositeScore combines all signals into a single score
func (s *FraudDetectionService) calculateCompositeScore(signals []FraudSignal) float64 {
	if len(signals) == 0 {
		return 0
	}

	// Weighted combination with diminishing returns
	var score float64
	weights := map[string]float64{
		"velocity":         1.0,
		"amount_anomaly":   1.2,
		"time_anomaly":     0.5,
		"device_anomaly":   0.8,
		"destination_risk": 1.0,
		"account_age":      0.9,
		"geo_anomaly":      1.1,
	}

	for _, signal := range signals {
		weight := weights[signal.Type]
		if weight == 0 {
			weight = 1.0
		}
		score += signal.Value * weight
	}

	// Normalize to 0-1 range with diminishing returns
	return math.Min(1.0, 1.0-math.Exp(-score))
}

// determineAction decides what action to take based on fraud score
func (s *FraudDetectionService) determineAction(score float64) (FraudAction, bool, bool) {
	switch {
	case score >= 0.8:
		return FraudActionBlock, false, true
	case score >= 0.6:
		return FraudActionReview, true, true
	case score >= 0.4:
		return FraudActionMFA, true, false
	default:
		return FraudActionAllow, false, false
	}
}

// getUserBehaviorPattern retrieves user's established behavior pattern
func (s *FraudDetectionService) getUserBehaviorPattern(ctx context.Context, userID uuid.UUID) (*UserBehaviorPattern, error) {
	pattern := &UserBehaviorPattern{UserID: userID}

	var typicalHoursJSON, typicalCountriesJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT typical_login_hours, typical_countries, typical_devices, 
		       avg_session_duration, avg_transactions_per_day, avg_transaction_amount, last_analyzed_at
		FROM user_behavior_patterns WHERE user_id = $1`,
		userID).Scan(
		&typicalHoursJSON, &typicalCountriesJSON, &pattern.TypicalDevices,
		&pattern.AvgSessionDuration, &pattern.AvgTransactionsPerDay,
		&pattern.AvgTransactionAmount, &pattern.LastAnalyzedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal(typicalHoursJSON, &pattern.TypicalLoginHours)
	json.Unmarshal(typicalCountriesJSON, &pattern.TypicalCountries)

	return pattern, nil
}

// UpdateBehaviorPattern analyzes and updates user's behavior pattern
func (s *FraudDetectionService) UpdateBehaviorPattern(ctx context.Context, userID uuid.UUID) error {
	// Analyze login hours from last 30 days
	rows, err := s.db.QueryContext(ctx, `
		SELECT EXTRACT(HOUR FROM created_at) as hour, COUNT(*) as cnt
		FROM sessions WHERE user_id = $1 AND created_at > NOW() - INTERVAL '30 days'
		GROUP BY hour ORDER BY cnt DESC LIMIT 5`,
		userID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var typicalHours []int
	for rows.Next() {
		var hour int
		var cnt int
		if rows.Scan(&hour, &cnt) == nil {
			typicalHours = append(typicalHours, hour)
		}
	}

	// Analyze typical countries
	var typicalCountries []string
	countryRows, _ := s.db.QueryContext(ctx, `
		SELECT country_code, COUNT(*) as cnt
		FROM geo_locations WHERE user_id = $1 AND created_at > NOW() - INTERVAL '30 days'
		GROUP BY country_code ORDER BY cnt DESC LIMIT 3`,
		userID)
	if countryRows != nil {
		defer countryRows.Close()
		for countryRows.Next() {
			var country string
			var cnt int
			if countryRows.Scan(&country, &cnt) == nil && country != "" {
				typicalCountries = append(typicalCountries, country)
			}
		}
	}

	// Count typical devices
	var deviceCount int
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT fingerprint) FROM known_devices 
		WHERE user_id = $1 AND last_used_at > NOW() - INTERVAL '30 days'`,
		userID).Scan(&deviceCount)

	// Calculate average transaction metrics
	var avgAmount decimal.Decimal
	var avgPerDay float64
	s.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(amount), 0), COALESCE(COUNT(*)::float / 30, 0)
		FROM transactions WHERE user_id = $1 AND created_at > NOW() - INTERVAL '30 days'`,
		userID).Scan(&avgAmount, &avgPerDay)

	// Upsert behavior pattern
	typicalHoursJSON, _ := json.Marshal(typicalHours)
	typicalCountriesJSON, _ := json.Marshal(typicalCountries)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO user_behavior_patterns 
		(user_id, typical_login_hours, typical_countries, typical_devices, avg_transactions_per_day, avg_transaction_amount, last_analyzed_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			typical_login_hours = EXCLUDED.typical_login_hours,
			typical_countries = EXCLUDED.typical_countries,
			typical_devices = EXCLUDED.typical_devices,
			avg_transactions_per_day = EXCLUDED.avg_transactions_per_day,
			avg_transaction_amount = EXCLUDED.avg_transaction_amount,
			last_analyzed_at = NOW(),
			updated_at = NOW()`,
		userID, typicalHoursJSON, typicalCountriesJSON, deviceCount, avgPerDay, avgAmount)

	return err
}

// storeSignals saves fraud signals for analysis
func (s *FraudDetectionService) storeSignals(ctx context.Context, userID uuid.UUID, sessionID string, signals []FraudSignal) {
	for _, signal := range signals {
		metadata, _ := json.Marshal(signal.Metadata)
		s.db.ExecContext(ctx, `
			INSERT INTO fraud_signals (user_id, session_id, signal_type, signal_value, metadata)
			VALUES ($1, $2, $3, $4, $5)`,
			userID, sessionID, signal.Type, signal.Value, metadata)
	}
}

// updateUserFraudScore updates the user's overall fraud score
func (s *FraudDetectionService) updateUserFraudScore(ctx context.Context, userID uuid.UUID, score float64) {
	// Use exponential moving average
	s.db.ExecContext(ctx, `
		UPDATE users SET 
			fraud_score = COALESCE(fraud_score * 0.7 + $1 * 0.3, $1),
			last_fraud_check = NOW()
		WHERE id = $2`,
		score, userID)
}

// GetUserFraudHistory returns recent fraud signals for a user
func (s *FraudDetectionService) GetUserFraudHistory(ctx context.Context, userID uuid.UUID, limit int) ([]FraudSignal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT signal_type, signal_value, metadata, created_at
		FROM fraud_signals WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var signals []FraudSignal
	for rows.Next() {
		var signal FraudSignal
		var metadataJSON []byte
		if rows.Scan(&signal.Type, &signal.Value, &metadataJSON, &signal.Timestamp) == nil {
			json.Unmarshal(metadataJSON, &signal.Metadata)
			signals = append(signals, signal)
		}
	}

	return signals, nil
}
