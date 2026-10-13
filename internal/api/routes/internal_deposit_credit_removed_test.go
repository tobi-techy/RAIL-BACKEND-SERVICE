package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

type depositCreditNoRedis struct{ cache.RedisClient }

func (depositCreditNoRedis) Client() *redis.Client { return nil }

// POST /internal/deposit/credit registered whenever a FundingService was wired
// and credited up to $10,000 from a caller-supplied address, tx_hash and
// amount, none of it checked against the chain. Real deposits arrive through
// the signed provider webhooks and the on-chain reconciliation worker.
func TestInternalDepositCreditRouteIsGone(t *testing.T) {
	const internalKey = "test-only-internal-ops-key-0123456789abcdef"
	router := SetupRoutes(&di.Container{
		Config: &config.Config{
			Security: config.SecurityConfig{InternalAPIKey: internalKey},
			Server:   config.ServerConfig{SupportedVersions: []string{"v1"}},
		},
		Logger:         logger.New("error", "test"),
		ZapLog:         zap.NewNop(),
		RedisClient:    depositCreditNoRedis{},
		UserRepo:       &repositories.UserRepository{},
		FundingService: &funding.Service{},
	})

	for _, route := range router.Routes() {
		if strings.Contains(route.Path, "deposit/credit") {
			t.Fatalf("route %s %s must not be registered", route.Method, route.Path)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/deposit/credit",
		strings.NewReader(`{"address":"0xabc","amount":"10000","tx_hash":"0xdef","chain":"BASE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+internalKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /internal/deposit/credit with the valid internal key: status %d body %s, want 404", w.Code, w.Body.String())
	}
}
