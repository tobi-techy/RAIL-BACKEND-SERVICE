package auth

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	blacklistPrefix = "token:blacklist:"
	refreshPrefix   = "token:refresh:"

	// negCacheTTL is how long a "not blacklisted" result is trusted locally
	// before re-checking Redis. This collapses the per-request Redis Exists into
	// roughly one call per token per window — a large Upstash cost reduction for
	// the frontend's frequent polling. Tradeoff: a freshly-revoked token can
	// remain valid for up to this window. 60s is safe: revocation clears the
	// local cache entry immediately via negCacheDel.
	negCacheTTL     = 60 * time.Second
	negCacheMaxSize = 50000 // purge threshold to bound memory
)

// TokenBlacklist manages revoked tokens using Redis, with a short in-process
// negative cache to cut Redis traffic and stay available during Redis blips.
type TokenBlacklist struct {
	redis *redis.Client

	mu       sync.RWMutex
	negCache map[string]int64 // tokenHash -> unix-nano expiry of the "not blacklisted" result
}

// NewTokenBlacklist creates a new token blacklist service
func NewTokenBlacklist(redisClient *redis.Client) *TokenBlacklist {
	return &TokenBlacklist{redis: redisClient, negCache: make(map[string]int64)}
}

func (b *TokenBlacklist) negCacheGet(tokenHash string) bool {
	b.mu.RLock()
	exp, ok := b.negCache[tokenHash]
	b.mu.RUnlock()
	return ok && time.Now().UnixNano() < exp
}

func (b *TokenBlacklist) negCachePut(tokenHash string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.negCache) >= negCacheMaxSize {
		now := time.Now().UnixNano()
		for k, exp := range b.negCache {
			if now >= exp {
				delete(b.negCache, k)
			}
		}
		// If still at capacity after removing expired entries, evict the oldest 10%
		// (those with the earliest expiry) to enforce the hard limit deterministically.
		if len(b.negCache) >= negCacheMaxSize {
			type entry struct {
				key string
				exp int64
			}
			entries := make([]entry, 0, len(b.negCache))
			for k, exp := range b.negCache {
				entries = append(entries, entry{k, exp})
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].exp < entries[j].exp })
			evict := negCacheMaxSize / 10
			for i := 0; i < evict && i < len(entries); i++ {
				delete(b.negCache, entries[i].key)
			}
		}
	}
	b.negCache[tokenHash] = time.Now().Add(negCacheTTL).UnixNano()
}

func (b *TokenBlacklist) negCacheDel(tokenHash string) {
	b.mu.Lock()
	delete(b.negCache, tokenHash)
	b.mu.Unlock()
}

// Blacklist adds a token to the blacklist until its expiration
func (b *TokenBlacklist) Blacklist(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil // Token already expired
	}

	key := blacklistPrefix + tokenHash
	if err := b.redis.Set(ctx, key, "1", ttl).Err(); err != nil {
		return err
	}
	b.negCacheDel(tokenHash) // a now-revoked token must not be served from the negative cache
	return nil
}

// IsBlacklisted checks if a token is blacklisted. It first consults a short
// in-process negative cache (skips Redis entirely on a hit). On Redis error
// the error is returned to the caller so the middleware's AUTH_BLACKLIST_FAIL_OPEN
// policy controls the security decision (fail-open vs. fail-closed).
func (b *TokenBlacklist) IsBlacklisted(ctx context.Context, tokenHash string) (bool, error) {
	if b.negCacheGet(tokenHash) {
		return false, nil
	}

	key := blacklistPrefix + tokenHash
	exists, err := b.redis.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check blacklist: %w", err)
	}
	if exists > 0 {
		b.negCacheDel(tokenHash)
		return true, nil
	}
	b.negCachePut(tokenHash)
	return false, nil
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
