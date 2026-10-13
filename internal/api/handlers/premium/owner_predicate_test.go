package premium

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"go.uber.org/zap"

	premiumservice "github.com/rail-service/rail_service/internal/domain/services/premium"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// Pins the owner predicate on the scam-alert dismissal: the owner is served, a
// second authenticated user is refused with 404, never 403, and the owner's
// alert is left untouched. Needs Postgres for the owner half; skips without
// DATABASE_URL.

const scamAlertTestDDL = `
CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY);
CREATE TABLE IF NOT EXISTS user_scam_alerts (
	id UUID PRIMARY KEY,
	user_id UUID NOT NULL,
	merchant_name TEXT NOT NULL,
	transaction_id UUID,
	alert_type TEXT NOT NULL DEFAULT '',
	risk_level TEXT NOT NULL DEFAULT '',
	reason TEXT,
	dismissed BOOLEAN NOT NULL DEFAULT FALSE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
`

func scamAlertTestDB(t *testing.T) *sqlx.DB {
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
	if _, err := db.Exec(scamAlertTestDDL); err != nil {
		t.Fatalf("ensure test tables: %v", err)
	}
	return db
}

func TestDismissScamAlertOwnerPredicate(t *testing.T) {
	db := scamAlertTestDB(t)
	ctx := context.Background()

	owner, stranger := uuid.New(), uuid.New()
	alertID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id) VALUES ($1),($2) ON CONFLICT DO NOTHING`, owner, stranger); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO user_scam_alerts (id, user_id, merchant_name) VALUES ($1,$2,'Uche Ventures')`, alertID, owner); err != nil {
		t.Fatal(err)
	}

	scamIntel := premiumservice.NewScamIntelligenceService(repositories.NewScamRepository(db), zap.NewNop())
	h := NewHandlers(nil, nil, nil, scamIntel, nil, nil, nil, nil, nil, zap.NewNop())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/v1/premium/scam/alerts", actorMiddleware())
	g.POST("/:alertId/dismiss", h.DismissScamAlert)

	path := "/v1/premium/scam/alerts/" + alertID.String() + "/dismiss"

	if code := callAs(t, r, http.MethodPost, path, stranger); code != http.StatusNotFound {
		t.Fatalf("stranger got %d, want 404", code)
	}

	var stillOpen bool
	if err := db.GetContext(ctx, &stillOpen, `SELECT NOT dismissed FROM user_scam_alerts WHERE id = $1 AND user_id = $2`, alertID, owner); err != nil {
		t.Fatal(err)
	}
	if !stillOpen {
		t.Fatal("the stranger's refusal dismissed the owner's alert")
	}

	if code := callAs(t, r, http.MethodPost, path, owner); code != http.StatusOK {
		t.Fatalf("owner got %d, want 200", code)
	}

	var dismissed bool
	if err := db.GetContext(ctx, &dismissed, `SELECT dismissed FROM user_scam_alerts WHERE id = $1 AND user_id = $2`, alertID, owner); err != nil {
		t.Fatal(err)
	}
	if !dismissed {
		t.Fatal("owner's dismissal did not take effect")
	}
}

// actorMiddleware injects the acting user id the way the JWT middleware does.
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

func callAs(t *testing.T, r http.Handler, method, path string, actor uuid.UUID) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Test-Actor", actor.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}
