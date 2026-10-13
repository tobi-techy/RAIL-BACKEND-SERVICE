package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testInternalKey = "test-only-internal-ops-key-fedcba9876543210"

func serve(t *testing.T, internalKey, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	c := newRouterContainer()
	c.Config.Security.InternalAPIKey = internalKey
	router := SetupRoutes(c)
	req := httptest.NewRequest(method, path, strings.NewReader(`{"reason":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// /admin/paj/complete/:order_id used to accept the JWT signing secret as its
// X-Internal-Key. It must now answer only to the dedicated internal key, and
// that gate runs before the admin session checks.
func TestPajCompleteRefusesSigningSecretAsInternalKey(t *testing.T) {
	const path = "/api/v1/admin/paj/complete/order-1"

	old := serve(t, testInternalKey, http.MethodPost, path, map[string]string{"X-Internal-Key": testSigningSecret})
	if old.Code != http.StatusUnauthorized || !strings.Contains(old.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("signing secret as X-Internal-Key: status %d body %q, want the internal-key gate's 401 unauthorized", old.Code, old.Body.String())
	}

	missing := serve(t, testInternalKey, http.MethodPost, path, nil)
	if missing.Code != http.StatusUnauthorized || !strings.Contains(missing.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("no X-Internal-Key: status %d body %q, want 401 unauthorized", missing.Code, missing.Body.String())
	}

	good := serve(t, testInternalKey, http.MethodPost, path, map[string]string{"X-Internal-Key": testInternalKey})
	if good.Code != http.StatusUnauthorized || !strings.Contains(good.Body.String(), "Authorization header required") {
		t.Fatalf("correct internal key without an admin session: status %d body %q, want the admin session gate's 401", good.Code, good.Body.String())
	}
}

func TestPajCompleteFailsClosedWithoutConfiguredKey(t *testing.T) {
	for _, key := range []string{"", testSigningSecret} {
		w := serve(t, "", http.MethodPost, "/api/v1/admin/paj/complete/order-1", map[string]string{"X-Internal-Key": key})
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("unconfigured internal key, presented %q: status %d, want 503", key, w.Code)
		}
	}
}

// Every internal-key route (/metrics and /internal/*) refuses the signing
// secret, whether it is sent as a bearer token or as X-Internal-Key.
func TestInternalKeyRoutesRefuseSigningSecret(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/metrics"},
		{http.MethodGet, "/internal/users/lookup?email=a@example.com"},
		{http.MethodPost, "/internal/paj-orders/complete-stuck"},
		{http.MethodDelete, "/internal/users/00000000-0000-0000-0000-000000000000"},
	}
	for _, tc := range cases {
		for _, headers := range []map[string]string{
			{"Authorization": "Bearer " + testSigningSecret},
			{"X-Internal-Key": testSigningSecret},
		} {
			w := serve(t, testInternalKey, tc.method, tc.path, headers)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s with the signing secret %v: status %d, want 401", tc.method, tc.path, headers, w.Code)
			}
		}
	}
}

// /internal/stash/transfer-to-spend moved ledger funds for any caller-named
// user_id on a bare key header. Nothing called it; the route is gone.
func TestInternalStashTransferRouteIsGone(t *testing.T) {
	c := newRouterContainer()
	c.Config.Security.InternalAPIKey = testInternalKey
	router := SetupRoutes(c)
	for _, route := range router.Routes() {
		if strings.Contains(route.Path, "transfer-to-spend") {
			t.Fatalf("route %s %s must not be registered", route.Method, route.Path)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/stash/transfer-to-spend",
		strings.NewReader(`{"user_id":"00000000-0000-0000-0000-000000000001","amount":"10","reason":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testInternalKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d with the valid internal key, want 404", w.Code)
	}
}
