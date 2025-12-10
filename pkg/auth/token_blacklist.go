package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	blacklistPrefix = "token:blacklist:"
	refreshPrefix   = "token:refresh:"
)

// TokenBlacklist manages revoked tokens using Redis
type TokenBlacklist struct {
	redis *redis.Client
}

// NewTokenBlacklist creates a new token blacklist service
func NewTokenBlacklist(redisClient *redis.Client) *TokenBlacklist {
	return &TokenBlacklist{redis: redisClient}
}

// Blacklist adds a token to the blacklist until its expiration
func (b *TokenBlacklist) Blacklist(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil // Token already expired
	}

	key := blacklistPrefix + tokenHash
	return b.redis.Set(ctx, key, "1", ttl).Err()
}

// IsBlacklisted checks if a token is blacklisted
func (b *TokenBlacklist) IsBlacklisted(ctx context.Context, tokenHash string) (bool, error) {
	key := blacklistPrefix + tokenHash
	exists, err := b.redis.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check blacklist: %w", err)
	}
	return exists > 0, nil
}

// BlacklistUserTokens blacklists all tokens for a user (logout all sessions)
func (b *TokenBlacklist) BlacklistUserTokens(ctx context.Context, userID string, ttl time.Duration) error {
	key := blacklistPrefix + "user:" + userID
	return b.redis.Set(ctx, key, time.Now().Unix(), ttl).Err()
}

// IsUserBlacklisted checks if all user tokens are blacklisted
func (b *TokenBlacklist) IsUserBlacklisted(ctx context.Context, userID string, tokenIssuedAt time.Time) (bool, error) {
	key := blacklistPrefix + "user:" + userID
	val, err := b.redis.Get(ctx, key).Int64()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check user blacklist: %w", err)
	}
	// Token is blacklisted if it was issued before the blacklist timestamp
	return tokenIssuedAt.Unix() < val, nil
}

// StoreRefreshToken stores refresh token metadata for rotation tracking
func (b *TokenBlacklist) StoreRefreshToken(ctx context.Context, tokenHash, userID string, ttl time.Duration) error {
	key := refreshPrefix + tokenHash
	return b.redis.Set(ctx, key, userID, ttl).Err()
}

// ValidateRefreshToken validates and invalidates a refresh token (one-time use)
func (b *TokenBlacklist) ValidateRefreshToken(ctx context.Context, tokenHash string) (string, error) {
	key := refreshPrefix + tokenHash
	
	// Get and delete atomically
	userID, err := b.redis.GetDel(ctx, key).Result()
	if err == redis.Nil {
		return "", fmt.Errorf("refresh token not found or already used")
	}
	if err != nil {
		return "", fmt.Errorf("failed to validate refresh token: %w", err)
	}
	
	return userID, nil
}

// RevokeRefreshToken explicitly revokes a refresh token
func (b *TokenBlacklist) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	key := refreshPrefix + tokenHash
	return b.redis.Del(ctx, key).Err()
}
