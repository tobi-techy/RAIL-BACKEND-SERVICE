package travel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/brij"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

func TestValidateTravelDocument(t *testing.T) {
	valid := brij.PassengerInput{
		Nationality:    "GB",
		PassportNumber: "123456789",
		PassportExpiry: time.Now().AddDate(5, 0, 0).Format("2006-01-02"),
	}
	cases := []struct {
		name string
		pass func(*brij.PassengerInput)
	}{
		{"valid", func(*brij.PassengerInput) {}},
		{"missing nationality", func(p *brij.PassengerInput) { p.Nationality = "" }},
		{"missing passport number", func(p *brij.PassengerInput) { p.PassportNumber = "" }},
		{"empty expiry", func(p *brij.PassengerInput) { p.PassportExpiry = "" }},
		{"malformed expiry", func(p *brij.PassengerInput) { p.PassportExpiry = "12/04/2030" }},
		{"expired passport", func(p *brij.PassengerInput) { p.PassportExpiry = "2020-01-01" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.pass(&p)
			err := validateTravelDocument(&p)
			if tc.name == "valid" && err != nil {
				t.Fatalf("valid document rejected: %v", err)
			}
			if tc.name != "valid" && err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
	if err := validateTravelDocument(nil); err == nil {
		t.Fatal("nil passenger accepted")
	}
}

func TestEscrowAtomic(t *testing.T) {
	cases := map[decimal.Decimal]int64{
		decimal.NewFromFloat(1.00):  1_000_000,
		decimal.NewFromFloat(67.60): 67_600_000,
		decimal.NewFromFloat(0.01):  10_000,
	}
	for in, want := range cases {
		if got := escrowAtomic(in); got != want {
			t.Errorf("escrowAtomic(%s) = %d, want %d", in.String(), got, want)
		}
	}
}

// travelSearchServer records the search body and replies 200 (no x402 payment
// needed in the test) with a canned offer list.
func travelSearchServer(t *testing.T, body *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/air/search" {
			http.NotFound(w, r)
			return
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*body = req
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"search":{"request_id":"r1","offers":[
			{"id":"fare_a","owner_name":"Test Air","origin_iata":"LOS","destination_iata":"ABV",
			 "departing_at":"2026-12-01T08:00:00Z","arriving_at":"2026-12-01T09:30:00Z",
			 "total_amount":67600000,"total_amount_decimal":"67.600000","total_currency":"USDC",
			 "fare_brand_name":"Basic","cabin_class":"economy","checked_bags_included":0,"identity_documents_required":false}
		]}}`))
	}))
}

// TestSearchFlightsIsBounded verifies the service always asks BRIJ for a bounded,
// one-option-per-itinerary result set (fix for multi-hundred-KB search payloads).
func TestSearchFlightsIsBounded(t *testing.T) {
	var sent map[string]interface{}
	srv := travelSearchServer(t, &sent)
	defer srv.Close()

	priv, err := solana.NewRandomPrivateKey()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	client, err := brij.NewClient(brij.Config{BaseURL: srv.URL, FundingPrivateKey: priv.String()}, zap.NewNop())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	svc := NewService(nil, client, Config{}, zap.NewNop())

	offers, err := svc.SearchFlights(context.Background(), "los", "abv", "2026-12-01", 1)
	if err != nil {
		t.Fatalf("SearchFlights: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offers = %d, want 1", len(offers))
	}
	if sent["limit"] != float64(20) {
		t.Errorf("limit = %v, want 20", sent["limit"])
	}
	if sent["cheapest_per_itinerary"] != true {
		t.Errorf("cheapest_per_itinerary = %v, want true", sent["cheapest_per_itinerary"])
	}
	if sent["adults"] != float64(1) {
		t.Errorf("adults = %v, want 1", sent["adults"])
	}
	if sent["origin_iata"] != "LOS" {
		t.Errorf("origin_iata = %v, want LOS (uppercased)", sent["origin_iata"])
	}
	// Fare-brand field must surface for the model to distinguish fares.
	if offers[0].FareBrandName != "Basic" {
		t.Errorf("fare_brand_name = %q, want Basic", offers[0].FareBrandName)
	}
}

// TestSearchFlightsRejectsBadInput verifies malformed routes/dates fail before
// the x402-paid call (a 400 after payment still settles the micropayment).
func TestSearchFlightsRejectsBadInput(t *testing.T) {
	priv, err := solana.NewRandomPrivateKey()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	client, err := brij.NewClient(brij.Config{BaseURL: "http://127.0.0.1:1", FundingPrivateKey: priv.String()}, zap.NewNop())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	svc := NewService(nil, client, Config{}, zap.NewNop())
	cases := []struct {
		name               string
		origin, dest, date string
	}{
		{"short origin", "LO", "ABV", "2026-12-01"},
		{"numeric dest", "LOS", "AB1", "2026-12-01"},
		{"empty origin", "", "ABV", "2026-12-01"},
		{"malformed date", "LOS", "ABV", "12/01/2026"},
		{"empty date", "LOS", "ABV", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.SearchFlights(context.Background(), tc.origin, tc.dest, tc.date, 1); err == nil {
				t.Fatal("expected a validation error before any HTTP call")
			}
		})
	}
}

// TestValidatePassengerRejectsImpossibleDate verifies shape-valid but
// nonexistent dates (2026-13-45) fail before any money moves.
func TestValidatePassengerRejectsImpossibleDate(t *testing.T) {
	p := brij.PassengerInput{
		GivenName: "Ada", FamilyName: "Lovelace", BornOn: "2026-13-45",
		Title: "ms", Gender: "f", Email: "a@example.com", PhoneNumber: "+447400123456",
	}
	if err := validatePassenger(&p); err == nil {
		t.Fatal("impossible birth date accepted")
	}
	p.BornOn = "1990-04-12"
	if err := validatePassenger(&p); err != nil {
		t.Fatalf("valid passenger rejected: %v", err)
	}
}

// TestValidateTravelDocumentExpiryToday verifies a passport expiring today is
// still accepted (midnight-parsed expiry is always "before now" as an instant).
func TestValidateTravelDocumentExpiryToday(t *testing.T) {
	p := brij.PassengerInput{
		Nationality: "GB", PassportNumber: "123456789",
		PassportExpiry: time.Now().Format("2006-01-02"),
	}
	if err := validateTravelDocument(&p); err != nil {
		t.Fatalf("passport expiring today rejected: %v", err)
	}
}

// TestSearchFlightsDefaultsAdults verifies adults defaults to 1 when omitted.
func TestSearchFlightsDefaultsAdults(t *testing.T) {
	var sent map[string]interface{}
	srv := travelSearchServer(t, &sent)
	defer srv.Close()

	priv, err := solana.NewRandomPrivateKey()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	client, err := brij.NewClient(brij.Config{BaseURL: srv.URL, FundingPrivateKey: priv.String()}, zap.NewNop())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	svc := NewService(nil, client, Config{}, zap.NewNop())
	if _, err := svc.SearchFlights(context.Background(), "LOS", "ABV", "2026-12-01", 0); err != nil {
		t.Fatalf("SearchFlights: %v", err)
	}
	if sent["adults"] != float64(1) {
		t.Errorf("adults = %v, want 1", sent["adults"])
	}
}

// TestSearchFlightsRejectsMultiPassenger verifies the pre-paid guard: 2+
// adults must fail before any x402-paid OfferDetails/intent call, since
// BookFlight can never fulfill a multi-passenger intent.
func TestSearchFlightsRejectsMultiPassenger(t *testing.T) {
	var sent map[string]interface{}
	srv := travelSearchServer(t, &sent)
	defer srv.Close()

	priv, err := solana.NewRandomPrivateKey()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	client, err := brij.NewClient(brij.Config{BaseURL: srv.URL, FundingPrivateKey: priv.String()}, zap.NewNop())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	svc := NewService(nil, client, Config{}, zap.NewNop())
	if _, err := svc.SearchFlights(context.Background(), "LOS", "ABV", "2026-12-01", 2); err == nil {
		t.Fatal("expected multi-passenger rejection, got nil")
	}
}
