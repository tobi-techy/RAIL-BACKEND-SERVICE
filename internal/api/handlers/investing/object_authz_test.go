package investing

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/authz"
	"github.com/rail-service/rail_service/internal/domain/services/sharedgoal"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// These tests pin object-level authorization at the HTTP boundary for every
// path the seam covers: a member is served, a non-member is refused with 404
// (never 403, which would disclose that the object exists). They need Postgres
// because the member-served half reads real rows; without DATABASE_URL they
// skip, like the repository integration tests.

const authzTestDDL = `
CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY, rail_tag TEXT);
CREATE TABLE IF NOT EXISTS shared_goals (
	id UUID PRIMARY KEY,
	creator_id UUID NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	description TEXT,
	target_amount NUMERIC(20,6) NOT NULL DEFAULT 0,
	current_amount NUMERIC(20,6) NOT NULL DEFAULT 0,
	currency TEXT NOT NULL DEFAULT 'USD',
	deadline TIMESTAMPTZ,
	status TEXT NOT NULL DEFAULT 'active',
	visibility TEXT NOT NULL DEFAULT 'members',
	icon_name TEXT NOT NULL DEFAULT 'target-02',
	celebration_message TEXT,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS shared_goal_members (
	id UUID PRIMARY KEY,
	goal_id UUID NOT NULL,
	user_id UUID NOT NULL,
	role TEXT NOT NULL DEFAULT 'member',
	target_contribution NUMERIC(20,6),
	total_contributed NUMERIC(20,6) NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'active',
	invited_by UUID,
	joined_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE (goal_id, user_id));
CREATE TABLE IF NOT EXISTS shared_goal_contributions (
	id UUID PRIMARY KEY,
	goal_id UUID NOT NULL,
	user_id UUID NOT NULL,
	amount NUMERIC(20,6) NOT NULL,
	note TEXT,
	source TEXT NOT NULL DEFAULT 'manual',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS receipt_scans (
	id UUID PRIMARY KEY,
	user_id UUID NOT NULL,
	amount NUMERIC(20,6) NOT NULL DEFAULT 0,
	image_hash TEXT,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS household_members (
	group_id UUID NOT NULL,
	user_id UUID NOT NULL,
	PRIMARY KEY (group_id, user_id));
CREATE TABLE IF NOT EXISTS household_receipts (
	receipt_id UUID NOT NULL,
	group_id UUID NOT NULL,
	shared_by UUID NOT NULL,
	PRIMARY KEY (receipt_id, group_id));
`

func authzTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: object-authz handler test needs Postgres (CI provides it)")
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
	if _, err := db.Exec(authzTestDDL); err != nil {
		t.Fatalf("ensure authz test tables: %v", err)
	}
	return db
}

// withActor injects the acting user id the way the JWT middleware does, chosen
// per request so one router can be driven as several users.
func withActor() gin.HandlerFunc {
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

func doRequest(t *testing.T, r http.Handler, method, path string, actor uuid.UUID) int {
	t.Helper()
	return doRequestBody(t, r, method, path, actor, nil)
}

func doRequestBody(t *testing.T, r http.Handler, method, path string, actor uuid.UUID, body []byte) int {
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

func TestSharedGoalReadsRequireMembership(t *testing.T) {
	db := authzTestDB(t)
	ctx := context.Background()

	svc := sharedgoal.NewService(
		repositories.NewSharedGoalRepository(db),
		nil,
		authz.NewGuard(repositories.NewAuthzRepository(db)),
		zap.NewNop(),
	)
	h := NewSharedGoalHandler(svc, zap.NewNop())

	member, outsider := uuid.New(), uuid.New()
	goalID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id) VALUES ($1),($2) ON CONFLICT DO NOTHING`, member, outsider); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO shared_goals (id, creator_id, name, target_amount) VALUES ($1,$2,'Uche restock fund',100000)`, goalID, member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO shared_goal_members (id, goal_id, user_id, role, status) VALUES ($1,$2,$3,'creator','active')`, uuid.New(), goalID, member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO shared_goal_contributions (id, goal_id, user_id, amount) VALUES ($1,$2,$3,500)`, uuid.New(), goalID, member); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/v1/goals/shared", withActor())
	g.GET("/:id", h.GetGoal)
	g.GET("/:id/leaderboard", h.GetLeaderboard)
	g.GET("/:id/contributions", h.GetContributions)

	paths := []string{
		"/v1/goals/shared/" + goalID.String(),
		"/v1/goals/shared/" + goalID.String() + "/leaderboard",
		"/v1/goals/shared/" + goalID.String() + "/contributions",
	}

	for _, path := range paths {
		if code := doRequest(t, r, http.MethodGet, path, member); code != http.StatusOK {
			t.Errorf("%s: member got %d, want 200", path, code)
		}
		if code := doRequest(t, r, http.MethodGet, path, outsider); code != http.StatusNotFound {
			t.Errorf("%s: non-member got %d, want 404", path, code)
		}
	}

	// A non-member response must not carry the goal's data.
	req := httptest.NewRequest(http.MethodGet, paths[0], nil)
	req.Header.Set("X-Test-Actor", outsider.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if body := w.Body.String(); strings.Contains(body, "Uche restock fund") || strings.Contains(body, "current_amount") {
		t.Errorf("non-member response leaked goal data: %s", body)
	}
}

func TestHouseholdReadsRequireMembership(t *testing.T) {
	db := authzTestDB(t)
	ctx := context.Background()

	h := NewHouseholdHandler(
		db,
		nil,
		authz.NewGuard(repositories.NewAuthzRepository(db)),
		zap.NewNop(),
	)

	member, outsider := uuid.New(), uuid.New()
	groupID := uuid.New()
	receiptID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id) VALUES ($1),($2) ON CONFLICT DO NOTHING`, member, outsider); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO household_members (group_id, user_id) VALUES ($1,$2)`, groupID, member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO receipt_scans (id, user_id, amount) VALUES ($1,$2,2500)`, receiptID, member); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/v1/household/groups", withActor())
	g.GET("/:id/summary", h.GetSummary)
	g.POST("/:id/receipts", h.ShareReceipt)

	summary := "/v1/household/groups/" + groupID.String() + "/summary"
	share := "/v1/household/groups/" + groupID.String() + "/receipts"
	shareBody, _ := json.Marshal(map[string]string{"receipt_id": receiptID.String()})

	if code := doRequest(t, r, http.MethodGet, summary, member); code != http.StatusOK {
		t.Errorf("summary: member got %d, want 200", code)
	}
	if code := doRequest(t, r, http.MethodGet, summary, outsider); code != http.StatusNotFound {
		t.Errorf("summary: non-member got %d, want 404", code)
	}
	if code := doRequestBody(t, r, http.MethodPost, share, member, shareBody); code != http.StatusOK {
		t.Errorf("share: member got %d, want 200", code)
	}
	if code := doRequestBody(t, r, http.MethodPost, share, outsider, shareBody); code != http.StatusNotFound {
		t.Errorf("share: non-member got %d, want 404", code)
	}
}
