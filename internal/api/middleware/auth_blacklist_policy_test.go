package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
)

// The token-revocation check needs Redis. Whether that makes an authenticated
// request fail is a policy switch, and getting it wrong is expensive in both
// directions: strict means a Redis outage 503s every authenticated route
// (including withdrawals) for every user once a token falls out of the 60s
// local negative cache; loose means a revoked token can ride for its remaining
// lifetime. These tests pin both behaviours.
func TestAuthentication_BlacklistPolicyWhenRedisIsDown(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	secret := "middleware-test-secret"
	token, _, err := auth.GenerateAccessToken(uuid.New(), "user@example.com", "user", secret, 3600)
	require.NoError(t, err)

	serve := func(failOpen bool) *httptest.ResponseRecorder {
		cfg := &config.Config{
			JWT:      config.JWTConfig{Secret: secret},
			Security: config.SecurityConfig{AuthBlacklistFailOpen: failOpen},
		}
		router := gin.New()
		router.GET("/x", Authentication(cfg, logger.New("error", "test"), nil, auth.NewTokenBlacklist(rdb)),
			func(c *gin.Context) { c.Status(http.StatusOK) })

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// Healthy Redis: the check runs and the request passes.
	require.Equal(t, http.StatusOK, serve(true).Code)

	// Now the exact production failure: every Redis command is rejected.
	mr.SetError("ERR max requests limit exceeded. Limit: 500000, Usage: 500000")

	require.Equal(t, http.StatusOK, serve(true).Code,
		"fail-open must not block a logged-in user when revocation cannot be checked")

	require.Equal(t, http.StatusServiceUnavailable, serve(false).Code,
		"fail-closed must refuse the request when revocation cannot be checked")
}

// The check is skipped entirely for a token seen recently, so a Redis blip is
// invisible to active sessions even under the strict policy.
func TestAuthentication_RecentlySeenTokenSkipsRedis(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	secret := "middleware-test-secret"
	token, _, err := auth.GenerateAccessToken(uuid.New(), "user@example.com", "user", secret, 3600)
	require.NoError(t, err)

	cfg := &config.Config{
		JWT:      config.JWTConfig{Secret: secret},
		Security: config.SecurityConfig{AuthBlacklistFailOpen: false},
	}
	router := gin.New()
	router.GET("/x", Authentication(cfg, logger.New("error", "test"), nil, auth.NewTokenBlacklist(rdb)),
		func(c *gin.Context) { c.Status(http.StatusOK) })

	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, http.StatusOK, call(), "first call warms the cache")

	// Redis is unreachable from here on, but this token was just checked.
	mr.SetError("ERR max request limit exceeded")
	require.Equal(t, http.StatusOK, call(),
		"a recently validated token must ride through a Redis outage under the strict policy too")
}
