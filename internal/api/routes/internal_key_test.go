package routes

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
		{http.MethodPost, "/internal/stash/transfer-to-spend"},
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

// With a signing secret configured, a request carrying the right internal key
// but no valid HMAC is refused before it reaches the handler; a correctly
// signed one reaches it. /internal/users/lookup answers 400 for a missing
// query before touching the database, which makes "reached the handler"
// observable.
func TestInternalRequestSigningIsEnforcedOnInternalRoutes(t *testing.T) {
	const signingSecret = "test-only-request-signing-secret-0123456789ab"
	c := newRouterContainer()
	c.Config.Security.InternalAPIKey = testInternalKey
	c.Config.Security.InternalRequestSigningSecret = signingSecret
	router := SetupRoutes(c)

	do := func(headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/internal/users/lookup", nil)
		req.Header.Set("Authorization", "Bearer "+testInternalKey)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	sign := func(secret, ts string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(fmt.Sprintf("%s.GET./internal/users/lookup.-.", ts)))
		return hex.EncodeToString(mac.Sum(nil))
	}
	ts := fmt.Sprintf("%d", time.Now().Unix())

	for name, headers := range map[string]map[string]string{
		"no signature":    nil,
		"wrong secret":    {"X-Internal-Timestamp": ts, "X-Internal-Signature": sign("some-other-secret-0123456789abcdef-xyz", ts)},
		"stale timestamp": {"X-Internal-Timestamp": "1", "X-Internal-Signature": sign(signingSecret, "1")},
	} {
		w := do(headers)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "internal_signature_invalid") {
			t.Fatalf("%s: status %d body %q, want 401 internal_signature_invalid", name, w.Code, w.Body.String())
		}
	}

	w := do(map[string]string{"X-Internal-Timestamp": ts, "X-Internal-Signature": sign(signingSecret, ts)})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("correctly signed request: status %d body %q, want 400 from the handler", w.Code, w.Body.String())
	}
}
