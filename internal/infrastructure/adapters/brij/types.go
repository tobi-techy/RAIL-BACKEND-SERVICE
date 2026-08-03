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
//   - GET  /air/orders/{id}      — PNR + order status, needs the support code.
//
// Ids always travel in the request body, never in the path (except the two GET
// read endpoints above).
package brij

import (
	"fmt"
)

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
type SearchRequest struct {
	OriginIATA      string `json:"origin_iata"`
	DestinationIATA string `json:"destination_iata"`
	DepartDate      string `json:"depart_date"` // YYYY-MM-DD
	Adults          int    `json:"adults"`      // default 1
}

// SearchResponse is the 200 body of POST /air/search.
type SearchResponse struct {
	Search SearchResult `json:"search"`
}

// SearchResult carries the request id plus the matched offers.
type SearchResult struct {
	RequestID string         `json:"request_id"`
	Offers    []OfferSummary `json:"offers"`
}

// OfferSummary is a single flight offer. TotalAmount is the atomic amount the
// API uses for money comparisons; TotalAmountDecimal is the human form.
type OfferSummary struct {
	ID                      string   `json:"id"`
	OwnerName               string   `json:"owner_name"`
	OriginIATA              string   `json:"origin_iata"`
	DestinationIATA         string   `json:"destination_iata"`
	DepartingAt             string   `json:"departing_at"`
	ArrivingAt              string   `json:"arriving_at"`
	TotalAmount             int64    `json:"total_amount"`
	TotalAmountDecimal      string   `json:"total_amount_decimal"`
	TotalCurrency           string   `json:"total_currency"`
	ExpiresAt               string   `json:"expires_at"`
	RequiresInstantPayment  bool     `json:"requires_instant_payment"`
	PriceGuaranteeExpiresAt string   `json:"price_guarantee_expires_at"`
	PaymentRequiredBy       string   `json:"payment_required_by"`
	PassengerIDs            []string `json:"passenger_ids"`
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
// order or file a refund.
type BookingIntent struct {
	ID                     string `json:"id"`
	CustomerSupportCode    string `json:"customer_support_code"`
	FundingWallet          string `json:"funding_wallet"`
	RefundWallet           string `json:"refund_wallet"`
	OfferID                string `json:"offer_id"`
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

// PassengerInput is a single adult passenger as accepted by /air/book. Values
// mirror the upstream airline contract: title is mr/mrs/ms/miss/dr and gender
// is exactly m or f. Booking is one-way, one adult per booking.
type PassengerInput struct {
	GivenName   string `json:"given_name"`
	FamilyName  string `json:"family_name"`
	BornOn      string `json:"born_on"` // YYYY-MM-DD
	Title       string `json:"title"`
	Gender      string `json:"gender"` // m | f
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"` // E.164, e.g. +447400123456
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
// decimal string, e.g. 67600000 -> "67.600000".
func formatAtomicAmount(atomic int64) string {
	whole := atomic / 1_000_000
	frac := atomic % 1_000_000
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%06d", whole, frac)
}
