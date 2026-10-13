package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/auth"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/bcrypt"
)

const (
	testSecret   = "dashboard-test-signing-secret-0123456789abcdef"
	testPassword = "correct horse battery staple"
	testCode     = "123456"
)

type fakeStore struct {
	cred  *AdminCredential
	err   error
	calls int
}

func (f *fakeStore) FindAdminByEmail(_ context.Context, email string) (*AdminCredential, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.cred == nil || !strings.EqualFold(f.cred.Email, email) {
		return nil, sql.ErrNoRows
	}
	return f.cred, nil
}

type fakeSecondFactor struct {
	ok    bool
	err   error
	calls int
}

func (f *fakeSecondFactor) Verify(_ context.Context, _ uuid.UUID, code string) (bool, error) {
	f.calls++
	return f.err == nil && f.ok && code == testCode, f.err
}

func adminCred(t *testing.T) *AdminCredential {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &AdminCredential{ID: uuid.New(), Email: "boss@example.com", Role: "super_admin", PasswordHash: string(hash), IsActive: true}
}

type rig struct {
	handler *DashboardAuthHandler
	store   *fakeStore
	factor  *fakeSecondFactor
	logs    *observer.ObservedLogs
}

func newRig(t *testing.T, cred *AdminCredential) *rig {
	t.Helper()
	core, logs := observer.New(zap.InfoLevel)
	store := &fakeStore{cred: cred}
	factor := &fakeSecondFactor{ok: true}
	return &rig{
		handler: NewDashboardAuthHandler(store, factor, auth.NewJWTService(testSecret, 900, 900, nil), zap.New(core)),
		store:   store,
		factor:  factor,
		logs:    logs,
	}
}

func (r *rig) post(body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/dashboard/auth", r.handler.Auth)
	req := httptest.NewRequest(http.MethodPost, "/dashboard/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.9:4444"
	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		panic(err)
	}
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	if err != nil {
		panic(err)
	}
	req = req.WithContext(trace.ContextWithSpanContext(req.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID})))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func assertNoToken(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if _, ok := body["token"]; ok {
		t.Fatalf("response carried a token: %s", w.Body.String())
	}
}

func TestEmailAloneNeverMintsAToken(t *testing.T) {
	r := newRig(t, adminCred(t))
	for _, body := range []string{
		`{"email":"boss@example.com"}`,
		`{"email":"boss@example.com","password":""}`,
		`{"email":"boss@example.com","password":"` + testPassword + `"}`,
		`{"email":"boss@example.com","totp_code":"` + testCode + `"}`,
	} {
		w := r.post(body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status %d, want 400", body, w.Code)
		}
		assertNoToken(t, w)
	}
	if r.store.calls != 0 || r.factor.calls != 0 {
		t.Fatalf("an incomplete request reached the store (%d) or second factor (%d)", r.store.calls, r.factor.calls)
	}
}

func TestPasswordAndSecondFactorMintAndAudit(t *testing.T) {
	cred := adminCred(t)
	r := newRig(t, cred)
	w := r.post(`{"email":"BOSS@example.com","password":"` + testPassword + `","totp_code":"` + testCode + `"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	var body struct{ Token, Role string }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	claims, err := auth.NewJWTService(testSecret, 900, 900, nil).ValidateEnhancedToken(body.Token)
	if err != nil || claims.UserID != cred.ID || claims.Role != "super_admin" {
		t.Fatalf("minted token does not validate as the admin: claims=%+v err=%v", claims, err)
	}

	entries := r.logs.FilterMessage("dashboard admin token minted").All()
	if len(entries) != 1 {
		t.Fatalf("want exactly one mint audit entry, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["actor_id"] != cred.ID.String() || fields["actor_email"] != cred.Email ||
		fields["source_ip"] != "203.0.113.9" || fields["trace_id"] != "0102030405060708090a0b0c0d0e0f10" {
		t.Fatalf("mint audit entry is missing actor/source/trace: %v", fields)
	}
	if strings.Contains(entries[0].Message+strings.Join(mapValues(fields), " "), body.Token) {
		t.Fatal("the minted token was written to the log")
	}
}

func mapValues(m map[string]any) []string {
	var out []string
	for _, v := range m {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestRefusals(t *testing.T) {
	good := `{"email":"boss@example.com","password":"` + testPassword + `","totp_code":"` + testCode + `"}`

	t.Run("wrong password", func(t *testing.T) {
		r := newRig(t, adminCred(t))
		w := r.post(`{"email":"boss@example.com","password":"nope","totp_code":"` + testCode + `"}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
		assertNoToken(t, w)
		if r.factor.calls != 0 {
			t.Fatal("second factor was consulted after a bad password")
		}
	})
	t.Run("right password, wrong code", func(t *testing.T) {
		r := newRig(t, adminCred(t))
		w := r.post(`{"email":"boss@example.com","password":"` + testPassword + `","totp_code":"000000"}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
		assertNoToken(t, w)
	})
	t.Run("second factor not enrolled or erroring fails closed", func(t *testing.T) {
		r := newRig(t, adminCred(t))
		r.factor.err = errors.New("2FA not enabled for this user")
		w := r.post(good)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
		assertNoToken(t, w)
	})
	t.Run("absent second-factor service fails closed", func(t *testing.T) {
		r := newRig(t, adminCred(t))
		r.handler.secondFact = nil
		w := r.post(good)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d", w.Code)
		}
		assertNoToken(t, w)
	})
	t.Run("absent token minter fails closed", func(t *testing.T) {
		r := newRig(t, adminCred(t))
		r.handler.tokens = nil
		if w := r.post(good); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("admin with no password set", func(t *testing.T) {
		cred := adminCred(t)
		cred.PasswordHash = ""
		r := newRig(t, cred)
		w := r.post(good)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
		assertNoToken(t, w)
		if r.factor.calls != 0 {
			t.Fatal("second factor consulted for an account with no password")
		}
	})
	t.Run("inactive admin", func(t *testing.T) {
		cred := adminCred(t)
		cred.IsActive = false
		if w := newRig(t, cred).post(good); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("unknown email or non-admin", func(t *testing.T) {
		r := newRig(t, nil)
		w := r.post(good)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
		assertNoToken(t, w)
	})
	t.Run("store failure", func(t *testing.T) {
		r := newRig(t, adminCred(t))
		r.store.err = errors.New("db down")
		if w := r.post(good); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestMintOfflineAdminToken(t *testing.T) {
	cred := adminCred(t)
	store := &fakeStore{cred: cred}

	token, got, expires, err := MintOfflineAdminToken(context.Background(), store, testSecret, " Boss@Example.com ", 0)
	if err != nil || got.ID != cred.ID {
		t.Fatalf("mint: %v", err)
	}
	if d := time.Until(expires); d > DefaultOfflineAdminTokenTTL || d < DefaultOfflineAdminTokenTTL-time.Minute {
		t.Fatalf("default ttl not applied: expires in %s", d)
	}
	if _, err := auth.NewJWTService(testSecret, 900, 900, nil).ValidateEnhancedToken(token); err != nil {
		t.Fatalf("token does not validate: %v", err)
	}

	if _, _, _, err := MintOfflineAdminToken(context.Background(), store, testSecret, "boss@example.com", 2*time.Hour); err == nil {
		t.Fatal("a 2h ttl must be refused")
	}
	if _, _, _, err := MintOfflineAdminToken(context.Background(), store, "", "boss@example.com", time.Minute); err == nil {
		t.Fatal("a missing signing secret must be refused")
	}
	if _, _, _, err := MintOfflineAdminToken(context.Background(), store, testSecret, "someone@example.com", time.Minute); err == nil {
		t.Fatal("a non-admin email must be refused")
	}
	inactive := adminCred(t)
	inactive.IsActive = false
	if _, _, _, err := MintOfflineAdminToken(context.Background(), &fakeStore{cred: inactive}, testSecret, inactive.Email, time.Minute); err == nil {
		t.Fatal("an inactive admin must be refused")
	}
}
