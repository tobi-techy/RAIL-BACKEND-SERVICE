package routes

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/twofa"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// dashAdminDriver is a one-admin users table. It answers the old email-only
// lookup (id, role) and the credential lookup (id, email, role, password_hash,
// is_active) alike, and fails every other query, including the user_2fa read.
type dashAdminDriver struct {
	id   uuid.UUID
	hash string
}

type dashAdminConn struct{ d *dashAdminDriver }

func (d *dashAdminDriver) Open(string) (driver.Conn, error) { return &dashAdminConn{d}, nil }
func (c *dashAdminConn) Prepare(q string) (driver.Stmt, error) {
	return &dashAdminStmt{c: c, q: q}, nil
}
func (c *dashAdminConn) Close() error              { return nil }
func (c *dashAdminConn) Begin() (driver.Tx, error) { return nil, errors.New("no tx") }

type dashAdminStmt struct {
	c *dashAdminConn
	q string
}

func (s *dashAdminStmt) Close() error  { return nil }
func (s *dashAdminStmt) NumInput() int { return -1 }
func (s *dashAdminStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("no exec")
}
func (s *dashAdminStmt) Query(args []driver.Value) (driver.Rows, error) {
	if !strings.Contains(s.q, "FROM users") {
		return nil, errors.New("table not available")
	}
	if len(args) == 1 && args[0] != "boss@example.com" {
		return &dashAdminRows{}, nil
	}
	if strings.Contains(s.q, "password_hash") {
		return &dashAdminRows{cols: []string{"id", "email", "role", "password_hash", "is_active"},
			row: []driver.Value{s.c.d.id.String(), "boss@example.com", "super_admin", s.c.d.hash, true}}, nil
	}
	return &dashAdminRows{cols: []string{"id", "role"}, row: []driver.Value{s.c.d.id.String(), "super_admin"}}, nil
}

type dashAdminRows struct {
	cols []string
	row  []driver.Value
	done bool
}

func (r *dashAdminRows) Columns() []string { return r.cols }
func (r *dashAdminRows) Close() error      { return nil }
func (r *dashAdminRows) Next(dest []driver.Value) error {
	if r.done || r.row == nil {
		return io.EOF
	}
	r.done = true
	copy(dest, r.row)
	return nil
}

type dashNoRedis struct{ cache.RedisClient }

func (dashNoRedis) Client() *redis.Client { return nil }

const (
	dashSecret   = "dashboard-router-signing-secret-0123456789abc"
	dashPassword = "correct horse battery staple"
)

func newDashboardRouter(t *testing.T, withTwoFA bool) http.Handler {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(dashPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	name := "dashadmin-" + uuid.NewString()
	sql.Register(name, &dashAdminDriver{id: uuid.New(), hash: string(hash)})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("closing db: %v", err)
		}
	})

	c := &di.Container{
		Config: &config.Config{
			JWT:    config.JWTConfig{Secret: dashSecret},
			Server: config.ServerConfig{SupportedVersions: []string{"v1"}},
		},
		Logger:      logger.New("error", "test"),
		ZapLog:      zap.NewNop(),
		RedisClient: dashNoRedis{},
		UserRepo:    &repositories.UserRepository{},
		DB:          db,
		JWTService:  auth.NewJWTService(dashSecret, 900, 900, nil),
	}
	if withTwoFA {
		c.TwoFAService = twofa.NewService(db, zap.NewNop(), "dashboard-test-encryption-key", nil)
	}
	return SetupRoutes(c)
}

func postDashboardAuth(router http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dashboard/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func dashboardToken(w *httptest.ResponseRecorder) string {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		return ""
	}
	return body.Token
}

// An admin's email address alone used to return a signed admin JWT.
func TestDashboardAuthEmailAloneIsRefusedThroughTheRouter(t *testing.T) {
	router := newDashboardRouter(t, true)
	w := postDashboardAuth(router, `{"email":"boss@example.com"}`)
	if w.Code == http.StatusOK || dashboardToken(w) != "" {
		t.Fatalf("email alone: status %d body %s, want a refusal and no token", w.Code, w.Body.String())
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("email alone: status %d, want 400", w.Code)
	}
}

func TestDashboardAuthPasswordWithoutSecondFactorIsRefusedThroughTheRouter(t *testing.T) {
	router := newDashboardRouter(t, true)
	w := postDashboardAuth(router, `{"email":"boss@example.com","password":"`+dashPassword+`"}`)
	if w.Code != http.StatusBadRequest || dashboardToken(w) != "" {
		t.Fatalf("password without totp_code: status %d body %s", w.Code, w.Body.String())
	}
}

// Right email and password, but the second factor is absent from the wiring or
// not enrolled for the account: the route must refuse, not skip the factor.
func TestDashboardAuthSecondFactorMisconfigurationFailsClosedThroughTheRouter(t *testing.T) {
	good := `{"email":"boss@example.com","password":"` + dashPassword + `","totp_code":"123456"}`

	absent := postDashboardAuth(newDashboardRouter(t, false), good)
	if absent.Code != http.StatusServiceUnavailable || dashboardToken(absent) != "" {
		t.Fatalf("no 2FA service wired: status %d body %s, want 503 and no token", absent.Code, absent.Body.String())
	}

	notEnrolled := postDashboardAuth(newDashboardRouter(t, true), good)
	if notEnrolled.Code != http.StatusUnauthorized || dashboardToken(notEnrolled) != "" {
		t.Fatalf("2FA service cannot find an enrolment: status %d body %s, want 401 and no token", notEnrolled.Code, notEnrolled.Body.String())
	}

	wrongPassword := postDashboardAuth(newDashboardRouter(t, true), `{"email":"boss@example.com","password":"nope","totp_code":"123456"}`)
	if wrongPassword.Code != http.StatusUnauthorized || dashboardToken(wrongPassword) != "" {
		t.Fatalf("wrong password: status %d body %s", wrongPassword.Code, wrongPassword.Body.String())
	}
}
