package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

type userDeleteNoRedis struct{ cache.RedisClient }

func (userDeleteNoRedis) Client() *redis.Client { return nil }

// DELETE /internal/users/:id deleted a user plus their ledger_entries,
// ledger_transactions, ledger_accounts, wallets and deposits, with no balance
// check, no transaction, and swallowed per-table errors, on the internal key
// alone. Account deletion belongs to the authenticated DELETE /users/me flow.
func TestInternalUserDeleteRouteIsGone(t *testing.T) {
	const internalKey = "test-only-internal-ops-key-0123456789abcdef"
	router := SetupRoutes(&di.Container{
		Config: &config.Config{
			Security: config.SecurityConfig{InternalAPIKey: internalKey},
			Server:   config.ServerConfig{SupportedVersions: []string{"v1"}},
		},
		Logger:      logger.New("error", "test"),
		ZapLog:      zap.NewNop(),
		RedisClient: userDeleteNoRedis{},
		UserRepo:    &repositories.UserRepository{},
	})

	for _, route := range router.Routes() {
		if route.Method == http.MethodDelete && strings.HasPrefix(route.Path, "/internal/users") {
			t.Fatalf("route %s %s must not be registered", route.Method, route.Path)
		}
	}

	req := httptest.NewRequest(http.MethodDelete, "/internal/users/00000000-0000-0000-0000-000000000001", nil)
	req.Header.Set("Authorization", "Bearer "+internalKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("DELETE /internal/users/:id with the valid internal key: status %d body %s, want 404", w.Code, w.Body.String())
	}
}
