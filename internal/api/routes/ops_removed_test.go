package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

type noRedis struct{ cache.RedisClient }

func (noRedis) Client() *redis.Client { return nil }

const testSigningSecret = "test-only-signing-secret-0123456789abcdef"

func newRouterContainer() *di.Container {
	return &di.Container{
		Config: &config.Config{
			JWT:    config.JWTConfig{Secret: testSigningSecret},
			Server: config.ServerConfig{SupportedVersions: []string{"v1"}},
		},
		Logger:        logger.New("error", "test"),
		ZapLog:        zap.NewNop(),
		RedisClient:   noRedis{},
		UserRepo:      &repositories.UserRepository{},
		CircleAdapter: circle.NewAdapter(nil, nil),
	}
}

// The route used to register whenever a Circle adapter was wired (always, in
// production) and swept a custodied wallet to a caller-chosen address behind a
// header compared to the JWT signing secret. It must not exist.
func TestOpsReverseSolRouteIsGone(t *testing.T) {
	router := SetupRoutes(newRouterContainer())

	for _, route := range router.Routes() {
		if strings.Contains(route.Path, "reverse-sol") || strings.HasPrefix(route.Path, "/api/v1/ops/") {
			t.Fatalf("route %s %s must not be registered", route.Method, route.Path)
		}
	}

	body := `{"wallet_id":"w","destination":"d","lamports":1}`
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, path := range []string{"/api/v1/ops/reverse-sol", "/ops/reverse-sol"} {
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Internal-Key", testSigningSecret)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s %s with the signing secret as X-Internal-Key: status %d, want 404 (body %q)", method, path, w.Code, w.Body.String())
			}
		}
	}
}
