// Package brij is a typed client for the BRIJ Travel flight API
// (https://travel.brij.fi). Search, intent creation, booking, and refunds are
// paid per call with x402 micropayments settled in USDC on Solana mainnet; the
// x402 flow lives in client.go and is invisible to callers.
//
// Endpoint model:
//   - POST /air/search      — live flight offers (0.10 USDC, load-scaled).
//   - POST /air/intents     — lock an offer and derive its escrow (0.10 USDC).
//     Returns intent_id + customer_support_code ONCE; both must be persisted.
//   - POST /air/book        — pay the intent's escrow + submit one passenger.
//     Async: poll GET /air/intents/{id} until status is booked or refunded.
//   - POST /air/refund-requests — 0.10 USDC, files a manual refund request.
//   - GET  /air/intents/{id}     — intent status (no payment required).
//   - GET  /air/orders/{id}      — PNR + order status, paid 0.01 USDC via x402
//     and still gated by the customer support code. Refusals are never charged.
//   - POST /air/offer-details    — drill into one offer (fresh price, fare menu,
//     bag prices); browser-tier offers 0.10, fastbooking 0.01.
//
// Ids always travel in the request body, never in the path (except the two GET
// read endpoints above).
package brij

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// BrowserTierOffer prefixes. These offers book through a browser-driven
// fulfiller (trip.com, ryanair.com) rather than an airline API: they need a
// fresh fare menu (POST /air/offer-details) before intent creation and the
// passenger's travel document at book time.
const (
	BrowserTierTripcom = "trip.com:"
	BrowserTierRyanair = "ryanair:"
)

// x402Amount is an x402 payment amount. BRIJ serializes atomic amounts as
// decimal strings in the PAYMENT-REQUIRED challenge (e.g. "100000"); be lenient
// and accept a plain JSON number too, so a server-side format change can never
// break the whole payment handshake again.
type x402Amount int64

// Int64 returns the atomic amount.
func (a x402Amount) Int64() int64 { return int64(a) }

// UnmarshalJSON accepts a JSON string ("100000"), a JSON number (100000), or
// a whole float (100000.0) — BRIJ sends strings, but a server-side format
// change must never break the whole payment handshake again.
func (a *x402Amount) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return fmt.Errorf("x402 amount %q is not an integer", s)
		}
		*a = x402Amount(v)
		return nil
	}
	var v int64
	if err := json.Unmarshal(b, &v); err == nil {
		*a = x402Amount(v)
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("x402 amount is not an integer: %w", err)
	}
	if f != float64(int64(f)) {
		return fmt.Errorf("x402 amount %v is not an integer", f)
	}
	*a = x402Amount(int64(f))
	return nil
}

// IsBrowserTierOffer reports whether the offer books through a browser-driven
// fulfiller that requires a travel document at book time.
func IsBrowserTierOffer(offerID string) bool {
	id := strings.ToLower(offerID)
	return strings.HasPrefix(id, BrowserTierTripcom) || strings.HasPrefix(id, BrowserTierRyanair)
}

// Booking intent status values returned by the BRIJ API.
const (
	StatusActive   = "active"
	StatusBooked   = "booked"
	StatusRefunded = "refunded"
)

// Solana mainnet payment constants used by the x402 exact scheme.
const (
	MainnetNetwork = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
	USDCAccount    = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	USDCDecimals   = 6
	// BasePriceUSDC is the fixed per-call fee (0.10 USDC) for search/intents/
	// refunds before load scaling is applied to search.
	BasePriceUSDC = 100_000 // 0.10 USDC in 6-decimal units
)

// SearchRequest searches live flight offers. All fares are one-way, per adult.
// Limit and CheapestPerItinerary default to "no cap / cheapest instance" on the
// BRIJ side, but full search responses can exceed 300 KB; LLM-driven callers
// should always bound the result set (see travel.Service.SearchFlights).
type SearchRequest struct {
	OriginIATA           string `json:"origin_iata"`
	DestinationIATA      string `json:"destination_iata"`
	DepartDate           string `json:"depart_date"` // YYYY-MM-DD
	Adults               int    `json:"adults"`      // default 1
	ReturnDate           string `json:"return_date,omitempty"`
	CabinClass           string `json:"cabin_class,omitempty"`
	Limit                *int   `json:"limit,omitempty"`
	CheapestPerItinerary *bool  `json:"cheapest_per_itinerary,omitempty"`
	Sort                 string `json:"sort,omitempty"`
	MaxStops             *int   `json:"max_stops,omitempty"`
	DepartureAfter       string `json:"departure_after,omitempty"`
	DepartureBefore      string `json:"departure_before,omitempty"`
	MaxPrice             string `json:"max_price,omitempty"`
}

// SearchResponse is the 200 body of POST /air/search.
type SearchResponse struct {
	Search SearchResult `json:"search"`
}

// SearchResult carries the request id plus the matched offers. Progressive
// searches may return Status "enriching" with a SearchID to poll via
// /air/search-updates; a SearchID alone is a capability token, not all offers.
type SearchResult struct {
	RequestID string         `json:"request_id"`
	SearchID  string         `json:"search_id,omitempty"`
	Status    string         `json:"status,omitempty"`
	Offers    []OfferSummary `json:"offers"`
}

// OfferSummary is a single flight offer. TotalAmount is the atomic amount the
// API uses for money comparisons; TotalAmountDecimal is the human form. Fare
// fields (brand, cabin, bags, conditions) distinguish the same physical flight
// sold under different fares — the model needs them to avoid answering "there
// is no refundable/business fare" from a truncated list.
type OfferSummary struct {
	ID                        string            `json:"id"`
	OwnerName                 string            `json:"owner_name"`
	OriginIATA                string            `json:"origin_iata"`
	DestinationIATA           string            `json:"destination_iata"`
	DepartingAt               string            `json:"departing_at"`
	ArrivingAt                string            `json:"arriving_at"`
	Stops                     *int              `json:"stops,omitempty"`
	Duration                  string            `json:"duration,omitempty"`
	FareBrandName             string            `json:"fare_brand_name,omitempty"`
	CabinClass                string            `json:"cabin_class,omitempty"`
	CheckedBagsIncluded       *int              `json:"checked_bags_included,omitempty"`
	CarryOnBagsIncluded       *int              `json:"carry_on_bags_included,omitempty"`
	Conditions                *FareConditions   `json:"conditions,omitempty"`
	IdentityDocumentsRequired bool              `json:"identity_documents_required"`
	TotalEmissionsKG          string            `json:"total_emissions_kg,omitempty"`
	TotalAmount               int64             `json:"total_amount"`
	TotalAmountDecimal        string            `json:"total_amount_decimal"`
	TotalCurrency             string            `json:"total_currency"`
	ExpiresAt                 string            `json:"expires_at,omitempty"`
	RequiresInstantPayment    bool              `json:"requires_instant_payment"`
	PriceGuaranteeExpiresAt   string            `json:"price_guarantee_expires_at,omitempty"`
	PaymentRequiredBy         string            `json:"payment_required_by,omitempty"`
	PassengerIDs              []string          `json:"passenger_ids,omitempty"`
	FareOptions               []OfferFareOption `json:"fare_options,omitempty"`
}

// FareConditions is the machine-readable change/refund ruleset of a fare. Null
// means the airline did not disclose the value — unknown, never zero/false.
type FareConditions struct {
	ChangeAllowed         *bool  `json:"change_allowed,omitempty"`
	ChangePenaltyAmount   string `json:"change_penalty_amount,omitempty"`
	ChangePenaltyCurrency string `json:"change_penalty_currency,omitempty"`
	RefundAllowed         *bool  `json:"refund_allowed,omitempty"`
	RefundPenaltyAmount   string `json:"refund_penalty_amount,omitempty"`
	RefundPenaltyCurrency string `json:"refund_penalty_currency,omitempty"`
}

// OfferFareOption is one bookable fare of a flight, as returned by grouped
// searches and POST /air/offer-details (cheapest first).
type OfferFareOption struct {
	OfferID             string              `json:"offer_id,omitempty"`
	TotalAmountDecimal  string              `json:"total_amount_decimal,omitempty"`
	TotalCurrency       string              `json:"total_currency,omitempty"`
	FareBrandName       string              `json:"fare_brand_name,omitempty"`
	Cabin               string              `json:"cabin,omitempty"`
	Refundable          bool                `json:"refundable"`
	Changeable          bool                `json:"changeable"`
	CheckedBagsIncluded int                 `json:"checked_bags_included"`
	FareIndex           *int                `json:"fare_index,omitempty"`
	SeatsLeft           *int                `json:"seats_left,omitempty"`
	Conditions          []FareTextCondition `json:"conditions,omitempty"`
}

// FareTextCondition is a supplier verbatim wording pairing (browser tier only).
type FareTextCondition struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

// OfferDetailsRequest drills into one offer (fresh price, fare menu, bags).
type OfferDetailsRequest struct {
	OfferID string `json:"offer_id"`
}

// OfferDetailsResponse is the 200 body of POST /air/offer-details.
type OfferDetailsResponse struct {
	Offer             OfferSummary   `json:"offer"`
	AvailableServices []OfferService `json:"available_services"`
}

// OfferService is a purchasable extra (e.g. checked bag) on the offer's fare.
type OfferService struct {
	Type               string `json:"type"`
	MaximumQuantity    int    `json:"maximum_quantity"`
	TotalAmountDecimal string `json:"total_amount_decimal"`
	TotalCurrency      string `json:"total_currency"`
	BaggageType        string `json:"baggage_type,omitempty"`
	MaximumWeightKg    *int   `json:"maximum_weight_kg,omitempty"`
}

// CreateIntentRequest locks an offer against the Rail funding wallet.
type CreateIntentRequest struct {
	FundingWallet string `json:"funding_wallet"`
	RefundWallet  string `json:"refund_wallet,omitempty"`
	OfferID       string `json:"offer_id"`
}

// IntentResponse wraps a booking intent (used by intent GET/POST responses).
type IntentResponse struct {
	Intent BookingIntent `json:"intent"`
}

// BookingIntent is the full intent projection. CustomerSupportCode is returned
// exactly once — at intent creation — and again inside the /book response; the
// GET /air/intents projection omits it. Persist it; it is required to read an
// order or file a refund. PassengerCount is how many passengers /air/book must
// supply (the offer is priced for that many).
type BookingIntent struct {
	ID                     string `json:"id"`
	CustomerSupportCode    string `json:"customer_support_code"`
	FundingWallet          string `json:"funding_wallet"`
	RefundWallet           string `json:"refund_wallet"`
	OfferID                string `json:"offer_id"`
	PassengerCount         int    `json:"passenger_count"`
	ExpectedTicketAmount   int64  `json:"expected_ticket_amount"`
	ExpectedTicketCurrency string `json:"expected_ticket_currency"`
	ExpectedEscrowAmount   int64  `json:"expected_escrow_amount"`
	ExpectedEscrowMint     string `json:"expected_escrow_mint"`
	FeeAmount              int64  `json:"fee_amount"`
	PassengerGivenName     string `json:"passenger_given_name"`
	PassengerFamilyName    string `json:"passenger_family_name"`
	PassengerBornOn        string `json:"passenger_born_on"`
	PassengerTitle         string `json:"passenger_title"`
	PassengerGender        string `json:"passenger_gender"`
	PassengerEmail         string `json:"passenger_email"`
	PassengerPhoneNumber   string `json:"passenger_phone_number"`
	Status                 string `json:"status"`
	ExpiresAt              string `json:"expires_at"`
	EscrowSlotID           int64  `json:"escrow_slot_id"`
	EscrowAddress          string `json:"escrow_address"`
	VaultAddress           string `json:"vault_address"`
	EscrowInitSignature    string `json:"escrow_init_signature"`
	EscrowInitializedAt    string `json:"escrow_initialized_at"`
	ObservedEscrowAmount   int64  `json:"observed_escrow_amount"`
	ObservedEscrowMint     string `json:"observed_escrow_mint"`
	EscrowFundedAt         string `json:"escrow_funded_at"`
	AirlineOrderID         string `json:"airline_order_id"`
	PaymentRequiredBy      string `json:"payment_required_by"`
	TicketedAt             string `json:"ticketed_at"`
	CaptureTxHash          string `json:"capture_tx_hash"`
	CapturedAt             string `json:"captured_at"`
	RefundReason           string `json:"refund_reason"`
	RefundTxHash           string `json:"refund_tx_hash"`
	RefundedAt             string `json:"refunded_at"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

// EscrowAmountDecimal returns the expected escrow amount in USDC as a decimal
// string (the API expresses amounts atomically with 6 decimals).
func (i *BookingIntent) EscrowAmountDecimal() string {
	return formatAtomicAmount(i.ExpectedEscrowAmount)
}

// IsTerminal reports whether the intent reached a final state.
func (i *BookingIntent) IsTerminal() bool {
	return i.Status == StatusBooked || i.Status == StatusRefunded
}

// PassengerInput is a passenger as accepted by /air/book. Values mirror the
// upstream airline contract: title is mr/mrs/ms/miss/dr and gender is exactly m
// or f. Booking is one-way, one adult per booking. Passport fields are ignored
// by fastbooking fares but are REQUIRED for browser-tier (trip.com:/ryanair:)
// offers — refused before payment when missing.
type PassengerInput struct {
	GivenName      string `json:"given_name"`
	FamilyName     string `json:"family_name"`
	BornOn         string `json:"born_on"` // YYYY-MM-DD
	Title          string `json:"title"`
	Gender         string `json:"gender"` // m | f
	Email          string `json:"email"`
	PhoneNumber    string `json:"phone_number"` // E.164, e.g. +447400123456
	Nationality    string `json:"nationality,omitempty"`
	PassportNumber string `json:"passport_number,omitempty"`
	PassportExpiry string `json:"passport_expiry,omitempty"` // YYYY-MM-DD, in the future
}

// BookRequest carries the intent id (in the body, never the path) plus exactly
// one passenger.
type BookRequest struct {
	IntentID   string           `json:"intent_id"`
	Passengers []PassengerInput `json:"passengers"`
}

// RequestBookingResponse is the 200 body of POST /air/book. Booking proceeds
// asynchronously; poll GET /air/intents/{id} until the intent is booked.
type RequestBookingResponse struct {
	Intent  BookingIntent `json:"intent"`
	Booking BookResult    `json:"booking"`
}

// BookResult summarizes the accepted booking request.
type BookResult struct {
	OrderID           string `json:"order_id"`
	TotalAmount       int64  `json:"total_amount"`
	TotalCurrency     string `json:"total_currency"`
	AwaitingPayment   bool   `json:"awaiting_payment"`
	DocumentsIssued   int    `json:"documents_issued"`
	PaymentRequiredBy string `json:"payment_required_by"`
}

// OrderResponse wraps the airline order status.
type OrderResponse struct {
	Order OrderStatus `json:"order"`
}

// OrderStatus is the airline order projection. BookingReference is the PNR.
type OrderStatus struct {
	OrderID                 string `json:"order_id"`
	BookingReference        string `json:"booking_reference"`
	TotalAmountDecimal      string `json:"total_amount_decimal"`
	TotalAmount             int64  `json:"total_amount"`
	TotalCurrency           string `json:"total_currency"`
	AwaitingPayment         bool   `json:"awaiting_payment"`
	DocumentsIssued         int    `json:"documents_issued"`
	PriceGuaranteeExpiresAt string `json:"price_guarantee_expires_at"`
	PaymentRequiredBy       string `json:"payment_required_by"`
	CreatedAt               string `json:"created_at"`
}

// RefundRequest files a manual refund request for a booked intent. Requires the
// X-Customer-Support-Code and X-Passenger-Family-Name headers on the wire.
type RefundRequest struct {
	IntentID string `json:"intent_id"`
	Reason   string `json:"reason"`
	Contact  string `json:"contact,omitempty"`
}

// RefundResponse is the 202 body of POST /air/refund-requests. It is not a
// refund guarantee; eligibility and carrier penalties are reviewed manually.
type RefundResponse struct {
	IntentID    string `json:"intent_id"`
	RequestedAt string `json:"requested_at"`
	Status      string `json:"status"`
}

// formatAtomicAmount renders an integer atomic amount (6 decimals) as a
// decimal string, e.g. 67600000 -> "67.600000" and -1500000 -> "-1.500000".
func formatAtomicAmount(atomic int64) string {
	sign := ""
	if atomic < 0 {
		sign = "-"
		atomic = -atomic
	}
	whole := atomic / 1_000_000
	frac := atomic % 1_000_000
	return fmt.Sprintf("%s%d.%06d", sign, whole, frac)
}
