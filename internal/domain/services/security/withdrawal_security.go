package security

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	withdrawalConfirmPrefix = "withdrawal_confirm:"
	withdrawalVelocityPrefix = "withdrawal_velocity:"
	confirmationTTL         = 15 * time.Minute
	velocityWindow          = 24 * time.Hour
	maxWithdrawalsPerDay    = 5
	largeWithdrawalThreshold = 1000.00
)

type WithdrawalSecurityService struct {
	db     *sql.DB
	redis  *redis.Client
	logger *zap.Logger
}

type WithdrawalConfirmation struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	WithdrawalID    uuid.UUID
	Amount          decimal.Decimal
	DestinationAddr string
	Token           string
	ExpiresAt       time.Time
	Confirmed       bool
	CreatedAt       time.Time
}

type WithdrawalRiskAssessment struct {
	Allowed       bool
	RequiresMFA   bool
	RequiresEmail bool
	RiskScore     float64
	RiskFactors   []string
	DailyCount    int
	DailyLimit    int
}

func NewWithdrawalSecurityService(db *sql.DB, redisClient *redis.Client, logger *zap.Logger) *WithdrawalSecurityService {
	return &WithdrawalSecurityService{
		db:     db,
		redis:  redisClient,
		logger: logger,
	}
}

// AssessWithdrawalRisk evaluates risk for a withdrawal request
func (s *WithdrawalSecurityService) AssessWithdrawalRisk(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destAddress string) (*WithdrawalRiskAssessment, error) {
	assessment := &WithdrawalRiskAssessment{
		Allowed:     true,
		RiskFactors: []string{},
		DailyLimit:  maxWithdrawalsPerDay,
	}

	// Check velocity (withdrawals per day)
	dailyCount, err := s.getDailyWithdrawalCount(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get withdrawal count", zap.Error(err))
	}
	assessment.DailyCount = dailyCount

	if dailyCount >= maxWithdrawalsPerDay {
		assessment.Allowed = false
		assessment.RiskFactors = append(assessment.RiskFactors, "daily_limit_exceeded")
		return assessment, nil
	}

	// Check if large withdrawal
	if amount.GreaterThan(decimal.NewFromFloat(largeWithdrawalThreshold)) {
		assessment.RequiresMFA = true
		assessment.RequiresEmail = true
		assessment.RiskFactors = append(assessment.RiskFactors, "large_amount")
		assessment.RiskScore += 0.3
	}

	// Check if new destination address
	isNewAddr, err := s.isNewDestinationAddress(ctx, userID, destAddress)
	if err == nil && isNewAddr {
		assessment.RequiresMFA = true
		assessment.RequiresEmail = true
		assessment.RiskFactors = append(assessment.RiskFactors, "new_destination")
		assessment.RiskScore += 0.4
	}

	// Check withdrawal pattern anomaly
	avgAmount, err := s.getAverageWithdrawalAmount(ctx, userID)
	if err == nil && avgAmount.GreaterThan(decimal.Zero) {
		if amount.GreaterThan(avgAmount.Mul(decimal.NewFromFloat(3))) {
			assessment.RiskFactors = append(assessment.RiskFactors, "unusual_amount")
			assessment.RiskScore += 0.3
			assessment.RequiresMFA = true
		}
	}

	// Check time-based patterns (withdrawals at unusual hours)
	hour := time.Now().Hour()
	if hour >= 0 && hour < 6 {
		assessment.RiskFactors = append(assessment.RiskFactors, "unusual_time")
		assessment.RiskScore += 0.1
	}

	return assessment, nil
}

// CreateConfirmation creates a withdrawal confirmation request
func (s *WithdrawalSecurityService) CreateConfirmation(ctx context.Context, userID, withdrawalID uuid.UUID, amount decimal.Decimal, destAddress string) (*WithdrawalConfirmation, error) {
	token, err := generateSecureToken(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	confirmation := &WithdrawalConfirmation{
		ID:              uuid.New(),
		UserID:          userID,
		WithdrawalID:    withdrawalID,
		Amount:          amount,
		DestinationAddr: destAddress,
		Token:           token,
		ExpiresAt:       time.Now().Add(confirmationTTL),
		Confirmed:       false,
		CreatedAt:       time.Now(),
	}

	// Store in Redis for fast lookup
	key := withdrawalConfirmPrefix + token
	err = s.redis.HSet(ctx, key, map[string]interface{}{
		"id":           confirmation.ID.String(),
		"user_id":      userID.String(),
		"withdrawal_id": withdrawalID.String(),
		"amount":       amount.String(),
		"dest_address": destAddress,
		"created_at":   confirmation.CreatedAt.Unix(),
	}).Err()
	if err != nil {
		return nil, fmt.Errorf("failed to store confirmation: %w", err)
	}
	s.redis.Expire(ctx, key, confirmationTTL)

	// Also store in DB for audit
	query := `
		INSERT INTO withdrawal_confirmations (id, user_id, withdrawal_id, amount, destination_address, token_hash, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	tokenHash := hashToken(token)
	_, err = s.db.ExecContext(ctx, query, confirmation.ID, userID, withdrawalID, amount, destAddress, tokenHash, confirmation.ExpiresAt, confirmation.CreatedAt)
	if err != nil {
		s.logger.Error("Failed to store confirmation in DB", zap.Error(err))
	}

	return confirmation, nil
}

// VerifyConfirmation verifies a withdrawal confirmation token
func (s *WithdrawalSecurityService) VerifyConfirmation(ctx context.Context, token string, userID uuid.UUID) (*WithdrawalConfirmation, error) {
	key := withdrawalConfirmPrefix + token
	data, err := s.redis.HGetAll(ctx, key).Result()
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("confirmation not found or expired")
	}

	storedUserID, _ := uuid.Parse(data["user_id"])
	if storedUserID != userID {
		s.logger.Warn("Withdrawal confirmation user mismatch",
			zap.String("expected", userID.String()),
			zap.String("got", storedUserID.String()))
		return nil, fmt.Errorf("invalid confirmation")
	}

	withdrawalID, _ := uuid.Parse(data["withdrawal_id"])
	amount, _ := decimal.NewFromString(data["amount"])
	confirmationID, _ := uuid.Parse(data["id"])

	// Delete the token (one-time use)
	s.redis.Del(ctx, key)

	// Update DB record
	tokenHash := hashToken(token)
	s.db.ExecContext(ctx, "UPDATE withdrawal_confirmations SET confirmed = true, confirmed_at = NOW() WHERE token_hash = $1", tokenHash)

	return &WithdrawalConfirmation{
		ID:              confirmationID,
		UserID:          userID,
		WithdrawalID:    withdrawalID,
		Amount:          amount,
		DestinationAddr: data["dest_address"],
		Confirmed:       true,
	}, nil
}

// RecordWithdrawal records a withdrawal for velocity tracking
func (s *WithdrawalSecurityService) RecordWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destAddress string) error {
	key := fmt.Sprintf("%s%s:%s", withdrawalVelocityPrefix, userID.String(), time.Now().Format("2006-01-02"))
	
	pipe := s.redis.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 48*time.Hour)
	_, err := pipe.Exec(ctx)

	// Store destination address for future checks
	addrKey := fmt.Sprintf("withdrawal_addrs:%s", userID.String())
	s.redis.SAdd(ctx, addrKey, destAddress)

	return err
}

func (s *WithdrawalSecurityService) getDailyWithdrawalCount(ctx context.Context, userID uuid.UUID) (int, error) {
	key := fmt.Sprintf("%s%s:%s", withdrawalVelocityPrefix, userID.String(), time.Now().Format("2006-01-02"))
	count, err := s.redis.Get(ctx, key).Int()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

func (s *WithdrawalSecurityService) isNewDestinationAddress(ctx context.Context, userID uuid.UUID, address string) (bool, error) {
	key := fmt.Sprintf("withdrawal_addrs:%s", userID.String())
	isMember, err := s.redis.SIsMember(ctx, key, address).Result()
	if err != nil {
		return true, err
	}
	return !isMember, nil
}

func (s *WithdrawalSecurityService) getAverageWithdrawalAmount(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	query := `SELECT COALESCE(AVG(amount), 0) FROM withdrawals WHERE user_id = $1 AND status = 'completed' AND created_at > NOW() - INTERVAL '90 days'`
	var avg decimal.Decimal
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&avg)
	return avg, err
}

func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func hashToken(token string) string {
	// Use same hashing as session service
	hash := make([]byte, 32)
	copy(hash, token)
	return hex.EncodeToString(hash)
}
