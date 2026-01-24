package ratelimit

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// AdaptiveRateLimiter provides risk-based rate limiting
type AdaptiveRateLimiter struct {
	redis       *redis.Client
	riskScorer  *RiskScoringEngine
	baseLimits  map[string]RateLimit
	logger      *zap.Logger
}

// RateLimit defines a rate limit configuration
type RateLimit struct {
	MaxRequests int64
	Window      time.Duration
}

// RateLimitResult contains the result of a rate limit check
type RateLimitResult struct {
	Allowed     bool
	Remaining   int64
	Limit       int64
	ResetTime   time.Time
	RiskScore   float64
	Adjustments []string
	LimitedBy   string
}

// AdaptiveRateLimiterConfig holds configuration
type AdaptiveRateLimiterConfig struct {
	BaseLimits       map[string]RateLimit
	DefaultLimit     RateLimit
	EnableRiskScoring bool
}

// DefaultAdaptiveConfig returns sensible defaults
func DefaultAdaptiveConfig() AdaptiveRateLimiterConfig {
	return AdaptiveRateLimiterConfig{
		BaseLimits: map[string]RateLimit{
			"default":     {MaxRequests: 100, Window: time.Minute},
			"auth":        {MaxRequests: 10, Window: time.Minute},
			"transaction": {MaxRequests: 30, Window: time.Minute},
			"webhook":     {MaxRequests: 1000, Window: time.Minute},
		},
		DefaultLimit:      RateLimit{MaxRequests: 100, Window: time.Minute},
		EnableRiskScoring: true,
	}
}

// NewAdaptiveRateLimiter creates a new adaptive rate limiter
func NewAdaptiveRateLimiter(
	redisClient *redis.Client,
	riskScorer *RiskScoringEngine,
	config AdaptiveRateLimiterConfig,
	logger *zap.Logger,
) *AdaptiveRateLimiter {
	return &AdaptiveRateLimiter{
		redis:      redisClient,
		riskScorer: riskScorer,
		baseLimits: config.BaseLimits,
		logger:     logger,
	}
}

// CheckAdaptiveRateLimit performs risk-adjusted rate limiting
func (a *AdaptiveRateLimiter) CheckAdaptiveRateLimit(
	ctx context.Context,
	userID string,
	endpoint string,
	ipAddress string,
	userAgent string,
) (*RateLimitResult, error) {
	// Calculate risk score
	var riskScore float64
	var anomalies []string

	if a.riskScorer != nil {
		score, err := a.riskScorer.CalculateRiskScore(ctx, userID, ipAddress, userAgent)
		if err != nil {
			a.logger.Warn("Failed to calculate risk score", zap.Error(err))
		} else {
			riskScore = score
		}

		detected, _ := a.riskScorer.DetectAnomalies(ctx, userID, endpoint)
		anomalies = detected
	}

	// Get base limit
	baseLimit := a.baseLimits[endpoint]
	if baseLimit.MaxRequests == 0 {
		baseLimit = a.baseLimits["default"]
	}

	// Adjust limit based on risk
	adjustedLimit := a.calculateAdjustedLimit(baseLimit.MaxRequests, riskScore, anomalies)

	// Check current usage
	currentUsage, err := a.getCurrentUsage(ctx, userID, endpoint, baseLimit.Window)
	if err != nil {
		a.logger.Warn("Failed to get current usage", zap.Error(err))
		return &RateLimitResult{Allowed: true, Limit: adjustedLimit}, nil
	}

	allowed := currentUsage < adjustedLimit
	remaining := max(0, adjustedLimit-currentUsage)
	resetTime := time.Now().Add(baseLimit.Window)

	// Record request
	if allowed {
		a.recordRequest(ctx, userID, endpoint, baseLimit.Window)
	}

	return &RateLimitResult{
		Allowed:     allowed,
		Remaining:   remaining,
		Limit:       adjustedLimit,
		ResetTime:   resetTime,
		RiskScore:   riskScore,
		Adjustments: anomalies,
		LimitedBy:   a.getLimitationReason(riskScore, anomalies, !allowed),
	}, nil
}

func (a *AdaptiveRateLimiter) calculateAdjustedLimit(baseLimit int64, riskScore float64, anomalies []string) int64 {
	adjustment := 1.0

	// Risk-based adjustment
	switch {
	case riskScore > 0.8:
		adjustment *= 0.1 // 90% reduction
	case riskScore > 0.6:
		adjustment *= 0.3 // 70% reduction
	case riskScore > 0.4:
		adjustment *= 0.6 // 40% reduction
	case riskScore > 0.2:
		adjustment *= 0.8 // 20% reduction
	}

	// Anomaly-based adjustment
	for _, anomaly := range anomalies {
		switch anomaly {
		case "burst_pattern":
			adjustment *= 0.5
		case "unusual_time":
			adjustment *= 0.7
		case "geo_anomaly":
			adjustment *= 0.4
		case "new_device":
			adjustment *= 0.8
		case "velocity_spike":
			adjustment *= 0.3
		}
	}

	// Ensure minimum limit
	adjusted := int64(float64(baseLimit) * adjustment)
	if adjusted < 1 {
		adjusted = 1
	}

	return adjusted
}

func (a *AdaptiveRateLimiter) getCurrentUsage(ctx context.Context, userID, endpoint string, window time.Duration) (int64, error) {
	key := fmt.Sprintf("ratelimit:adaptive:%s:%s", userID, endpoint)
	
	count, err := a.redis.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

func (a *AdaptiveRateLimiter) recordRequest(ctx context.Context, userID, endpoint string, window time.Duration) {
	key := fmt.Sprintf("ratelimit:adaptive:%s:%s", userID, endpoint)
	
	pipe := a.redis.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	pipe.Exec(ctx)
}

func (a *AdaptiveRateLimiter) getLimitationReason(riskScore float64, anomalies []string, limited bool) string {
	if !limited {
		return ""
	}

	if riskScore > 0.6 {
		return "high_risk_score"
	}

	if len(anomalies) > 0 {
		return fmt.Sprintf("anomaly:%s", anomalies[0])
	}

	return "rate_limit_exceeded"
}

// RiskScoringEngine calculates risk scores for requests
type RiskScoringEngine struct {
	redis   *redis.Client
	weights RiskWeights
	logger  *zap.Logger
}

// RiskWeights defines weights for risk factors
type RiskWeights struct {
	Geographic float64
	Time       float64
	Device     float64
	Behavior   float64
	Velocity   float64
	Reputation float64
}

// DefaultRiskWeights returns balanced risk weights
func DefaultRiskWeights() RiskWeights {
	return RiskWeights{
		Geographic: 0.20,
		Time:       0.10,
		Device:     0.20,
		Behavior:   0.20,
		Velocity:   0.20,
		Reputation: 0.10,
	}
}

// NewRiskScoringEngine creates a new risk scoring engine
func NewRiskScoringEngine(redisClient *redis.Client, weights RiskWeights, logger *zap.Logger) *RiskScoringEngine {
	return &RiskScoringEngine{
		redis:   redisClient,
		weights: weights,
		logger:  logger,
	}
}

// RiskFactors holds individual risk factor scores
type RiskFactors struct {
	Geographic float64
	Time       float64
	Device     float64
	Behavior   float64
	Velocity   float64
	Reputation float64
}

// CalculateRiskScore computes overall risk score for a request
func (r *RiskScoringEngine) CalculateRiskScore(
	ctx context.Context,
	userID string,
	ipAddress string,
	userAgent string,
) (float64, error) {
	factors := RiskFactors{}

	// Geographic risk
	factors.Geographic = r.calculateGeographicRisk(ctx, userID, ipAddress)

	// Time pattern risk
	factors.Time = r.calculateTimeRisk(ctx, userID)

	// Device risk
	factors.Device = r.calculateDeviceRisk(ctx, userID, userAgent)

	// Behavior risk
	factors.Behavior = r.calculateBehaviorRisk(ctx, userID)

	// Velocity risk
	factors.Velocity = r.calculateVelocityRisk(ctx, userID)

	// Reputation risk
	factors.Reputation = r.calculateReputationRisk(ctx, ipAddress)

	// Weighted sum
	totalRisk := factors.Geographic*r.weights.Geographic +
		factors.Time*r.weights.Time +
		factors.Device*r.weights.Device +
		factors.Behavior*r.weights.Behavior +
		factors.Velocity*r.weights.Velocity +
		factors.Reputation*r.weights.Reputation

	// Store for analysis
	r.storeRiskFactors(ctx, userID, factors, totalRisk)

	return math.Min(totalRisk, 1.0), nil
}

func (r *RiskScoringEngine) calculateGeographicRisk(ctx context.Context, userID, ipAddress string) float64 {
	// Check if IP is from a new location
	key := fmt.Sprintf("user:locations:%s", userID)
	
	exists, _ := r.redis.SIsMember(ctx, key, ipAddress).Result()
	if !exists {
		// New IP, add it and return elevated risk
		r.redis.SAdd(ctx, key, ipAddress)
		r.redis.Expire(ctx, key, 30*24*time.Hour)
		return 0.4
	}

	return 0.0
}

func (r *RiskScoringEngine) calculateTimeRisk(ctx context.Context, userID string) float64 {
	hour := time.Now().Hour()
	
	// Get user's typical active hours
	key := fmt.Sprintf("user:hours:%s", userID)
	hourStr := fmt.Sprintf("%d", hour)
	
	count, _ := r.redis.HGet(ctx, key, hourStr).Int64()
	
	// Record this hour
	r.redis.HIncrBy(ctx, key, hourStr, 1)
	r.redis.Expire(ctx, key, 30*24*time.Hour)

	// If this hour has low activity historically, it's unusual
	if count < 5 {
		return 0.3
	}

	return 0.0
}

func (r *RiskScoringEngine) calculateDeviceRisk(ctx context.Context, userID, userAgent string) float64 {
	key := fmt.Sprintf("user:devices:%s", userID)
	
	exists, _ := r.redis.SIsMember(ctx, key, userAgent).Result()
	if !exists {
		r.redis.SAdd(ctx, key, userAgent)
		r.redis.Expire(ctx, key, 30*24*time.Hour)
		return 0.5 // New device
	}

	return 0.0
}

func (r *RiskScoringEngine) calculateBehaviorRisk(ctx context.Context, userID string) float64 {
	// Check for unusual request patterns
	key := fmt.Sprintf("user:requests:%s:%d", userID, time.Now().Unix()/60)
	
	count, _ := r.redis.Get(ctx, key).Int64()
	
	// High request volume in short time
	if count > 50 {
		return 0.6
	} else if count > 20 {
		return 0.3
	}

	return 0.0
}

func (r *RiskScoringEngine) calculateVelocityRisk(ctx context.Context, userID string) float64 {
	// Check request velocity (requests per second)
	key := fmt.Sprintf("user:velocity:%s", userID)
	
	now := time.Now().Unix()
	lastRequest, _ := r.redis.Get(ctx, key).Int64()
	
	r.redis.Set(ctx, key, now, time.Minute)

	if lastRequest > 0 && now-lastRequest < 1 {
		return 0.4 // Multiple requests per second
	}

	return 0.0
}

func (r *RiskScoringEngine) calculateReputationRisk(ctx context.Context, ipAddress string) float64 {
	// Check if IP has been flagged
	key := fmt.Sprintf("ip:reputation:%s", ipAddress)
	
	score, err := r.redis.Get(ctx, key).Float64()
	if err == redis.Nil {
		return 0.0
	}

	return score
}

func (r *RiskScoringEngine) storeRiskFactors(ctx context.Context, userID string, factors RiskFactors, total float64) {
	key := fmt.Sprintf("user:risk:%s", userID)
	
	r.redis.HSet(ctx, key, map[string]interface{}{
		"geographic": factors.Geographic,
		"time":       factors.Time,
		"device":     factors.Device,
		"behavior":   factors.Behavior,
		"velocity":   factors.Velocity,
		"reputation": factors.Reputation,
		"total":      total,
		"updated_at": time.Now().Unix(),
	})
	r.redis.Expire(ctx, key, time.Hour)
}

// DetectAnomalies identifies anomalous patterns
func (r *RiskScoringEngine) DetectAnomalies(ctx context.Context, userID, endpoint string) ([]string, error) {
	var anomalies []string

	// Burst pattern detection
	if r.detectBurstPattern(ctx, userID, endpoint) {
		anomalies = append(anomalies, "burst_pattern")
	}

	// Velocity spike detection
	if r.detectVelocitySpike(ctx, userID) {
		anomalies = append(anomalies, "velocity_spike")
	}

	return anomalies, nil
}

func (r *RiskScoringEngine) detectBurstPattern(ctx context.Context, userID, endpoint string) bool {
	key := fmt.Sprintf("user:burst:%s:%s", userID, endpoint)
	
	count, _ := r.redis.Incr(ctx, key).Result()
	if count == 1 {
		r.redis.Expire(ctx, key, 10*time.Second)
	}

	return count > 20 // More than 20 requests in 10 seconds
}

func (r *RiskScoringEngine) detectVelocitySpike(ctx context.Context, userID string) bool {
	key := fmt.Sprintf("user:velocity_history:%s", userID)
	
	// Get recent request counts
	now := time.Now().Unix()
	currentMinute := fmt.Sprintf("%d", now/60)
	
	current, _ := r.redis.HGet(ctx, key, currentMinute).Int64()
	
	// Record current
	r.redis.HIncrBy(ctx, key, currentMinute, 1)
	r.redis.Expire(ctx, key, 10*time.Minute)

	// Compare to average
	vals, _ := r.redis.HGetAll(ctx, key).Result()
	if len(vals) < 3 {
		return false
	}

	var total int64
	for _, v := range vals {
		var count int64
		fmt.Sscanf(v, "%d", &count)
		total += count
	}
	avg := total / int64(len(vals))

	return current > avg*3 // 3x spike
}

// FlagIP marks an IP as suspicious
func (r *RiskScoringEngine) FlagIP(ctx context.Context, ipAddress string, score float64) error {
	key := fmt.Sprintf("ip:reputation:%s", ipAddress)
	return r.redis.Set(ctx, key, score, 24*time.Hour).Err()
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
