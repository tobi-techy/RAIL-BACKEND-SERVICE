package brij

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"
	"go.uber.org/zap"
)

// testKeypair returns a fresh random funding keypair for a test client.
func testKeypair(t *testing.T) (solana.PrivateKey, solana.PublicKey) {
	t.Helper()
	priv, err := solana.NewRandomPrivateKey()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return priv, priv.PublicKey()
}

// testChallenge builds a PAYMENT-REQUIRED header payload usable off-chain: the
// challenge's recentBlockhash short-circuits the RPC round trip and the fee
// payer avoids any SOL requirement. It mirrors the live BRIJ shape — resource is
// an OBJECT and amount is a STRING — which used to break the Go decoder.
func testChallenge(t *testing.T, amount int64) string {
	t.Helper()
	_, payTo := testKeypair(t)
	_, feePayer := testKeypair(t)
	blockhash := "11111111111111111111111111111111"
	payload := map[string]interface{}{
		"x402Version": 2,
		"error":       nil,
		"resource": map[string]interface{}{
			"url":         "https://travel.brij.fi/air/search",
			"description": "Live flight search",
			"mimeType":    "application/vnd.brij+json",
		},
		"accepts": []interface{}{
			map[string]interface{}{
				"scheme":            "exact",
				"network":           "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
				"asset":             USDCAccount,
				"amount":            strconv.FormatInt(amount, 10), // atomic amount as a string, as BRIJ sends it
				"payTo":             payTo.String(),
				"maxTimeoutSeconds": 60,
				"extra": map[string]interface{}{
					"feePayer":        feePayer.String(),
					"recentBlockhash": blockhash,
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal challenge: %v", err)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

// newTestClient wires a BRIJ client to the given API and RPC endpoints with a
// fresh funding keypair.
func newTestClient(t *testing.T, apiURL, rpcURL string, maxPayment int64) *Client {
	t.Helper()
	priv, _ := testKeypair(t)
	c, err := NewClient(Config{
		BaseURL:             apiURL,
		SolanaRPC:           rpcURL,
		FundingPrivateKey:   priv.String(),
		MaxPaymentBaseUnits: maxPayment,
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

// fixHeaders binds the recorded request headers for later assertions.
type recordedRequest struct {
	method, path string
	headers      http.Header
}

// TestPaymentRequirementParseLive pins the fix for the two x402 decode bugs
// found against the live BRIJ endpoint: resource is an OBJECT (the old decoder
// demanded a string) and amount is a STRING (the old decoder demanded int64).
func TestPaymentRequirementParseLive(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(testChallenge(t, 100_000))
	if err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	var pr paymentRequirement
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatalf("parse PAYMENT-REQUIRED with live shape: %v", err)
	}
	if pr.X402Version != 2 {
		t.Errorf("x402Version = %d, want 2", pr.X402Version)
	}
	if len(pr.Resource) == 0 || !json.Valid(pr.Resource) {
		t.Errorf("resource should be a raw JSON object, got %q", string(pr.Resource))
	}
	if len(pr.Accepts) != 1 {
		t.Fatalf("accepts = %d entries, want 1", len(pr.Accepts))
	}
	ac := pr.Accepts[0]
	if ac.Amount.Int64() != 100_000 {
		t.Errorf("amount = %d, want 100000", ac.Amount.Int64())
	}
	if ac.PayTo == "" || ac.Extra["feePayer"] == nil {
		t.Errorf("payTo/feePayer should be populated: %+v", ac)
	}
}

// TestX402AmountUnmarshal covers the lenient amount decoder: string, number,
// null, and malformed inputs.
func TestX402AmountUnmarshal(t *testing.T) {
	parse := func(t *testing.T, raw string) (x402Amount, bool) {
		t.Helper()
		var a x402Amount
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return 0, false
		}
		return a, true
	}
	if a, ok := parse(t, `"100000"`); !ok || a.Int64() != 100_000 {
		t.Errorf(`"100000" -> %d, %v`, a.Int64(), ok)
	}
	if a, ok := parse(t, `150050`); !ok || a.Int64() != 150_050 {
		t.Errorf("150050 -> %d, %v", a.Int64(), ok)
	}
	if a, ok := parse(t, `100000.0`); !ok || a.Int64() != 100_000 {
		t.Errorf("100000.0 -> %d, %v", a.Int64(), ok)
	}
	if _, ok := parse(t, `100000.5`); ok {
		t.Error("100000.5 should not parse as an integer")
	}
	if a, ok := parse(t, `-5`); !ok || a.Int64() != -5 {
		t.Errorf("-5 -> %d, %v", a.Int64(), ok)
	}
	if _, ok := parse(t, `null`); !ok {
		t.Error("null should parse as zero")
	}
	if _, ok := parse(t, `"12.5"`); ok {
		t.Error(`"12.5" should not parse as an integer`)
	}
	if _, ok := parse(t, `"abc"`); ok {
		t.Error(`"abc" should not parse`)
	}
}

// TestGetOrderIsPaid verifies the PNR endpoint now runs the x402 payment path:
// the 402 challenge is answered with a PAYMENT-SIGNATURE header, the support
// code rides along, and the real order is returned.
func TestGetOrderIsPaid(t *testing.T) {
	var calls []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordedRequest{method: r.Method, path: r.URL.Path, headers: r.Header.Clone()})
		if len(calls) == 1 {
			w.Header().Set("PAYMENT-REQUIRED", testChallenge(t, 10_000))
			w.WriteHeader(http.StatusPaymentRequired)
			return
		}
		if r.Header.Get("PAYMENT-SIGNATURE") == "" {
			http.Error(w, "missing payment signature", http.StatusPaymentRequired)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"order":{"order_id":"ord_123","booking_reference":"ABC123","total_amount_decimal":"0.010000"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, "", 0)
	order, err := c.GetOrder(context.Background(), "ord_123", "SUP-42")
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if order.BookingReference != "ABC123" {
		t.Errorf("booking_reference = %q, want ABC123", order.BookingReference)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 HTTP calls (402 then signed), got %d", len(calls))
	}
	if calls[1].method != http.MethodGet || calls[1].path != "/air/orders/ord_123" {
		t.Errorf("second call = %s %s, want GET /air/orders/ord_123", calls[1].method, calls[1].path)
	}
	if calls[1].headers.Get("X-Customer-Support-Code") != "SUP-42" {
		t.Errorf("support code header missing on the paid call")
	}
	if calls[1].headers.Get("PAYMENT-SIGNATURE") == "" {
		t.Errorf("the second call should carry a PAYMENT-SIGNATURE header")
	}
}

// TestAmountOverCap verifies the funding-wallet safety cap rejects a challenge
// that demands more than the configured per-request ceiling — before any
// transfer is built.
func TestAmountOverCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("PAYMENT-REQUIRED", testChallenge(t, 500_000_000)) // $500
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, "", 100_000_000) // cap $100
	_, err := c.Search(context.Background(), SearchRequest{OriginIATA: "LOS", DestinationIATA: "ABV", DepartDate: "2026-12-01"})
	if err == nil {
		t.Fatal("expected an amount_over_cap error")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("error should mention the cap, got %v", err)
	}
}

// TestOfferDetailsPending verifies an empty-activity 202 decodes into a typed
// fares_pending error instead of a silent zero-value success.
func TestOfferDetailsPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/air/offer-details" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"offer":{},"status":"fares_pending"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, "", 0)
	_, err := c.OfferDetails(context.Background(), OfferDetailsRequest{OfferID: "trip.com:123"})
	if err == nil {
		t.Fatal("expected fares_pending error for an empty offer")
	}
	pve, ok := err.(*PaymentVerificationError)
	if !ok || pve.Code != "fares_pending" {
		t.Errorf("error = %v, want PaymentVerificationError{fares_pending}", err)
	}
}

// mockRPC serves a canned getTokenAccountBalance result so the funding-wallet
// preflight can be exercised without a live RPC node.
func mockRPC(t *testing.T, amount int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		idJSON, _ := json.Marshal(req.ID)
		switch req.Method {
		case "getTokenAccountBalance":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"context":{"slot":1},"value":{"amount":"` +
				strconv.FormatInt(amount, 10) + `","decimals":6,"uiAmount":1,"uiAmountString":"1"}},"id":` + string(idJSON) + `}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"method not found"},"id":` + string(idJSON) + `}`))
		}
	}))
	return srv
}

// TestUSDCBalanceAtomic reads the funding wallet's USDC balance via the RPC
// client's token-account-balance call and returns it in atomic units.
func TestUSDCBalanceAtomic(t *testing.T) {
	rpcSrv := mockRPC(t, 1_234_567_890)
	defer rpcSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer apiSrv.Close()

	c := newTestClient(t, apiSrv.URL, rpcSrv.URL, 0)
	bal, err := c.USDCBalanceAtomic(context.Background())
	if err != nil {
		t.Fatalf("USDCBalanceAtomic: %v", err)
	}
	if bal != 1_234_567_890 {
		t.Errorf("balance = %d, want 1234567890", bal)
	}
}

// TestIsBrowserTierOffer pins the browser-tier prefix detection.
func TestIsBrowserTierOffer(t *testing.T) {
	cases := map[string]bool{
		"trip.com:CRL7QTI":      true,
		"ryanair:xyz123":        true,
		"TRIP.COM:crl":          true, // case-insensitive
		"some-airline-fare-123": false,
		"":                      false,
	}
	for id, want := range cases {
		if got := IsBrowserTierOffer(id); got != want {
			t.Errorf("IsBrowserTierOffer(%q) = %v, want %v", id, got, want)
		}
	}
}
