package security

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type IPWhitelistService struct {
	db     *sql.DB
	redis  *redis.Client
	logger *zap.Logger
}

type WhitelistedIP struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	IPAddress   string
	Label       string
	IsActive    bool
	VerifiedAt  *time.Time
	CreatedAt   time.Time
}

func NewIPWhitelistService(db *sql.DB, redisClient *redis.Client, logger *zap.Logger) *IPWhitelistService {
	return &IPWhitelistService{
		db:     db,
		redis:  redisClient,
		logger: logger,
	}
}

// IsIPWhitelisted checks if an IP is whitelisted for a user
func (s *IPWhitelistService) IsIPWhitelisted(ctx context.Context, userID uuid.UUID, ipAddress string) (bool, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("ip_whitelist:%s", userID.String())
	isMember, err := s.redis.SIsMember(ctx, cacheKey, ipAddress).Result()
	if err == nil && isMember {
		return true, nil
	}

	// Check database
	query := `SELECT EXISTS(SELECT 1 FROM ip_whitelist WHERE user_id = $1 AND ip_address = $2 AND is_active = true)`
	var exists bool
	err = s.db.QueryRowContext(ctx, query, userID, ipAddress).Scan(&exists)
	if err != nil {
		return false, err
	}

	// Cache if whitelisted
	if exists {
		s.redis.SAdd(ctx, cacheKey, ipAddress)
		s.redis.Expire(ctx, cacheKey, 1*time.Hour)
	}

	return exists, nil
}

// AddIP adds an IP to user's whitelist (requires verification)
func (s *IPWhitelistService) AddIP(ctx context.Context, userID uuid.UUID, ipAddress, label string) (*WhitelistedIP, error) {
	// Validate IP format
	if net.ParseIP(ipAddress) == nil {
		return nil, fmt.Errorf("invalid IP address format")
	}

	// Check if already exists
	var existingID uuid.UUID
	err := s.db.QueryRowContext(ctx, 
		"SELECT id FROM ip_whitelist WHERE user_id = $1 AND ip_address = $2", 
		userID, ipAddress).Scan(&existingID)
	if err == nil {
		return nil, fmt.Errorf("IP already in whitelist")
	}

	ip := &WhitelistedIP{
		ID:        uuid.New(),
		UserID:    userID,
		IPAddress: ipAddress,
		Label:     label,
		IsActive:  false, // Requires verification
		CreatedAt: time.Now(),
	}

	query := `INSERT INTO ip_whitelist (id, user_id, ip_address, label, is_active, created_at) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err = s.db.ExecContext(ctx, query, ip.ID, ip.UserID, ip.IPAddress, ip.Label, ip.IsActive, ip.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to add IP: %w", err)
	}

	return ip, nil
}

// VerifyIP activates a whitelisted IP after verification
func (s *IPWhitelistService) VerifyIP(ctx context.Context, userID, ipID uuid.UUID) error {
	now := time.Now()
	query := `UPDATE ip_whitelist SET is_active = true, verified_at = $1 WHERE id = $2 AND user_id = $3`
	result, err := s.db.ExecContext(ctx, query, now, ipID, userID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("IP not found")
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("ip_whitelist:%s", userID.String())
	s.redis.Del(ctx, cacheKey)

	return nil
}

// RemoveIP removes an IP from whitelist
func (s *IPWhitelistService) RemoveIP(ctx context.Context, userID, ipID uuid.UUID) error {
	query := `DELETE FROM ip_whitelist WHERE id = $1 AND user_id = $2`
	_, err := s.db.ExecContext(ctx, query, ipID, userID)
	if err != nil {
		return err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("ip_whitelist:%s", userID.String())
	s.redis.Del(ctx, cacheKey)

	return nil
}

// GetUserWhitelist returns all whitelisted IPs for a user
func (s *IPWhitelistService) GetUserWhitelist(ctx context.Context, userID uuid.UUID) ([]*WhitelistedIP, error) {
	query := `SELECT id, user_id, ip_address, label, is_active, verified_at, created_at FROM ip_whitelist WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ips []*WhitelistedIP
	for rows.Next() {
		ip := &WhitelistedIP{}
		err := rows.Scan(&ip.ID, &ip.UserID, &ip.IPAddress, &ip.Label, &ip.IsActive, &ip.VerifiedAt, &ip.CreatedAt)
		if err != nil {
			return nil, err
		}
		ips = append(ips, ip)
	}

	return ips, nil
}

// HasWhitelistEnabled checks if user has IP whitelist enabled
func (s *IPWhitelistService) HasWhitelistEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM ip_whitelist WHERE user_id = $1 AND is_active = true)`
	var exists bool
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&exists)
	return exists, err
}

// AutoWhitelistCurrentIP automatically whitelists current IP on first login
func (s *IPWhitelistService) AutoWhitelistCurrentIP(ctx context.Context, userID uuid.UUID, ipAddress string) error {
	// Check if user has any whitelisted IPs
	hasWhitelist, err := s.HasWhitelistEnabled(ctx, userID)
	if err != nil {
		return err
	}

	// If no whitelist, auto-add current IP as trusted
	if !hasWhitelist {
		ip, err := s.AddIP(ctx, userID, ipAddress, "Auto-added on first login")
		if err != nil {
			return err
		}
		return s.VerifyIP(ctx, userID, ip.ID)
	}

	return nil
}
