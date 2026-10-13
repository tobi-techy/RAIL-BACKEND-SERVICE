package trading

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// These tests pin the owner predicate on rebalancing_configs at the HTTP
// boundary: the owner is served, a second authenticated user is refused with
// 404 (never 403), on every path that names the config by id. They need
// Postgres because the owner half reads a real row; without DATABASE_URL they
// skip, like the repository integration tests.

const rebalancingTestDDL = `
CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY);
CREATE TABLE IF NOT EXISTS rebalancing_configs (
	id UUID PRIMARY KEY,
	user_id UUID NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	target_allocations JSONB NOT NULL DEFAULT '{}',
	threshold_pct NUMERIC(5,2) NOT NULL DEFAULT 5.0,
	frequency TEXT,
	last_rebalanced_at TIMESTAMPTZ,
	status TEXT NOT NULL DEFAULT 'active',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
`

func rebalancingTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: owner-predicate handler test needs Postgres (CI provides it)")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Skipf("no Postgres available: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("close test db: %v", err)
		}
	})
	if _, err := db.Exec(rebalancingTestDDL); err != nil {
		t.Fatalf("ensure test tables: %v", err)
	}
	return db
}

type stubPositionProvider struct {
	positions []*entities.InvestmentPosition
}

func (s stubPositionProvider) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error) {
	var out []*entities.InvestmentPosition
	for _, p := range s.positions {
		if p.UserID == userID {
			out = append(out, p)
		}
	}
	return out, nil
}

type stubQuoteProvider struct{}

func (stubQuoteProvider) GetQuotes(ctx context.Context, symbols []string) (map[string]*entities.MarketQuote, error) {
	out := make(map[string]*entities.MarketQuote, len(symbols))
	for _, s := range symbols {
		out[s] = &entities.MarketQuote{Symbol: s, Price: decimal.NewFromInt(100)}
	}
	return out, nil
}

type stubOrderPlacer struct{}

func (stubOrderPlacer) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, notional decimal.Decimal) (*entities.InvestmentOrder, error) {
	return &entities.InvestmentOrder{}, nil
}

func newRebalancingTestRouter(t *testing.T, db *sqlx.DB, owner uuid.UUID) http.Handler {
	t.Helper()
	svc := investing.NewRebalancingService(
		repositories.NewRebalancingConfigRepository(db),
		stubPositionProvider{positions: []*entities.InvestmentPosition{
			{UserID: owner, Symbol: "AAPL", MarketValue: decimal.NewFromInt(1000)},
		}},
		stubQuoteProvider{},
		stubOrderPlacer{},
		zap.NewNop(),
	)
	h := NewRebalancingHandlers(svc, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/v1/rebalancing/configs", actorMiddleware())
	g.GET("/:id", h.GetRebalancingConfig)
	g.PATCH("/:id", h.UpdateRebalancingConfig)
	g.DELETE("/:id", h.DeleteRebalancingConfig)
	g.GET("/:id/plan", h.GenerateRebalancingPlan)
	g.POST("/:id/execute", h.ExecuteRebalancing)
	g.GET("/:id/drift", h.CheckDrift)
	return r
}

// actorMiddleware injects the acting user id the way the JWT middleware does,
// chosen per request so one router can be driven as several users.
func actorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.GetHeader("X-Test-Actor"))
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set("user_id", id)
		c.Next()
	}
}

func callAs(t *testing.T, r http.Handler, method, path string, actor uuid.UUID, body []byte) int {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("X-Test-Actor", actor.String())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRebalancingConfigOwnerPredicate(t *testing.T) {
	db := rebalancingTestDB(t)
	ctx := context.Background()

	owner, stranger := uuid.New(), uuid.New()
	configID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id) VALUES ($1),($2) ON CONFLICT DO NOTHING`, owner, stranger); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO rebalancing_configs (id, user_id, name, target_allocations, threshold_pct) VALUES ($1,$2,'Owner only','{"AAPL": 100}',5)`, configID, owner); err != nil {
		t.Fatal(err)
	}

	r := newRebalancingTestRouter(t, db, owner)
	base := "/v1/rebalancing/configs/" + configID.String()

	// Every path that names the config by id: owner served, stranger 404.
	cases := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{"get", http.MethodGet, base, nil},
		{"update", http.MethodPatch, base, []byte(`{"allocations":{"AAPL":"100"}}`)},
		{"delete", http.MethodDelete, base, nil},
		{"plan", http.MethodGet, base + "/plan", nil},
		{"execute", http.MethodPost, base + "/execute", nil},
		{"drift", http.MethodGet, base + "/drift", nil},
	}

	for _, tc := range cases {
		ownerCase := tc

		t.Run(ownerCase.name+"/owner_served", func(t *testing.T) {
			// Restore the row first: the delete case removes it, so make each
			// owner case independent of the others' order.
			if _, err := db.ExecContext(ctx, `INSERT INTO rebalancing_configs (id, user_id, name, target_allocations, threshold_pct) VALUES ($1,$2,'Owner only','{"AAPL": 100}',5) ON CONFLICT (id) DO UPDATE SET user_id = EXCLUDED.user_id`, configID, owner); err != nil {
				t.Fatal(err)
			}
			if code := callAs(t, r, ownerCase.method, ownerCase.path, owner, ownerCase.body); code != http.StatusOK {
				t.Fatalf("owner got %d, want 200", code)
			}
		})
	}

	// Restore the row: the delete case above removed it.
	if _, err := db.ExecContext(ctx, `INSERT INTO rebalancing_configs (id, user_id, name, target_allocations, threshold_pct) VALUES ($1,$2,'Owner only','{"AAPL": 100}',5) ON CONFLICT (id) DO UPDATE SET user_id = EXCLUDED.user_id`, configID, owner); err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		t.Run(tc.name+"/stranger_refused", func(t *testing.T) {
			if code := callAs(t, r, tc.method, tc.path, stranger, tc.body); code != http.StatusNotFound {
				t.Fatalf("stranger got %d, want 404", code)
			}
		})
	}

	// The UPDATE statement is owner-scoped too: a config carrying another user's
	// id writes nothing even when the id matches.
	if err := repositories.NewRebalancingConfigRepository(db).Update(ctx, &entities.RebalancingConfig{
		ID:     configID,
		UserID: stranger,
		Name:   "Hijacked",
	}); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.GetContext(ctx, &name, `SELECT name FROM rebalancing_configs WHERE id = $1`, configID); err != nil {
		t.Fatal(err)
	}
	if name != "Owner only" {
		t.Fatalf("a mismatched owner rewrote the config: name is %q", name)
	}

	// The stranger's refusals must not have mutated or removed the owner's row.
	var remaining int
	if err := db.GetContext(ctx, &remaining, `SELECT COUNT(*) FROM rebalancing_configs WHERE id = $1 AND user_id = $2`, configID, owner); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("owner's config was affected by a stranger: %d rows remain, want 1", remaining)
	}
}
