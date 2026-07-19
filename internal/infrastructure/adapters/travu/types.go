package travu

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Trip type values for flight search.
const (
	FlightTypeOneway           = "Oneway"
	FlightTypeReturn           = "Return"
	FlightTypeMultidestination = "Multidestination"
)

// Order status values returned by Travu bookings.
const (
	OrderStatusConfirmed = "confirmed"
	OrderStatusFailed    = "failed"
	OrderStatusCanceled  = "canceled"
)

// flexString unmarshals a JSON value that Travu may return as either a string
// or a number into a Go string. Travu's docs explicitly warn that values come
// back in either form, so every user-facing field uses this.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	// Number (int or float) — trim a trailing .0 for clean ids.
	s := strings.TrimSuffix(string(b), ".0")
	*f = flexString(s)
	return nil
}

// String returns the underlying string value.
func (f flexString) String() string { return string(f) }

// --- Bus search (POST /check_trip) ---

// CheckTripRequest is the body for POST /check_trip.
type CheckTripRequest struct {
	DepartureState   string `json:"departure_state"`
	DestinationState string `json:"destination_state"`
	TripDate         string `json:"trip_date"` // YYYY-MM-DD
	Sort             string `json:"sort,omitempty"`
}

// Provider identifies a bus/flight operator.
type Provider struct {
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Logo      string `json:"logo,omitempty"`
}

// Trip is a single available bus (or flight) option.
type Trip struct {
	Provider            Provider    `json:"provider"`
	TripID              flexString  `json:"trip_id"`
	TripNo              flexString  `json:"trip_no"`
	TripDate            string      `json:"trip_date"`
	DepartureTime       string      `json:"departure_time"`
	DepartureTimeAlt    string      `json:"depature_time"` // Travu misspells this in some responses
	OriginID            flexString  `json:"origin_id"`
	DestinationID       flexString  `json:"destination_id"`
	Narration           string      `json:"narration"`
	Fare                flexString  `json:"fare"`
	TotalSeats          flexString  `json:"total_seats"`
	AvailableSeats      []int       `json:"available_seats"`
	BlockedSeats        []int       `json:"blocked_seats"`
	SpecialSeats        []int       `json:"special_seats"`
	OrderID             flexString  `json:"order_id"`
	DepartureTerminal   string      `json:"departure_terminal"`
	DestinationTerminal string      `json:"destination_terminal"`
	Vehicle             string      `json:"vehicle"`
	BoardingAt          flexString  `json:"boarding_at"`
	DepartureAddress    string      `json:"departure_address"`
	DestinationAddress  string      `json:"destination_address"`
	// SelectID holds the opaque itinerary id used to select/book flights.
	SelectID string `json:"id,omitempty"`
}

// providerResults is the per-operator block Travu returns for a route.
type providerResults struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
	Info    string `json:"info"`
	Data    []Trip `json:"data"`
}

// DepartureTimeValue returns the departure time, preferring the correctly
// spelled field and falling back to Travu's misspelled one.
func (t Trip) DepartureTimeValue() string {
	if t.DepartureTime != "" {
		return t.DepartureTime
	}
	return t.DepartureTimeAlt
}

// decodeTrips normalizes Travu's two search response shapes into a flat trip
// list. When sort="date" (or a single top-level result) the body is a single
// providerResults object; otherwise it is a map keyed by provider short_name.
func decodeTrips(body []byte) ([]Trip, error) {
	// Try the flat {error,message,info,data:[...]} shape first.
	var flat providerResults
	if err := json.Unmarshal(body, &flat); err == nil && flat.Data != nil {
		return flat.Data, nil
	}
	// Fall back to the provider-keyed map shape.
	var keyed map[string]providerResults
	if err := json.Unmarshal(body, &keyed); err != nil {
		return nil, err
	}
	var out []Trip
	for _, pr := range keyed {
		if pr.Error {
			continue
		}
		out = append(out, pr.Data...)
	}
	return out, nil
}

// --- Bus booking (POST /book_trip) ---

// Passenger is a single traveler on a bus booking request.
type Passenger struct {
	Title          string `json:"title"`
	Name           string `json:"name"`
	Age            string `json:"age"`
	Sex            string `json:"sex"`
	Phone          string `json:"phone"`
	Email          string `json:"email"`
	Blood          string `json:"blood,omitempty"`
	NextOfKin      string `json:"next_of_kin,omitempty"`
	NextOfKinPhone string `json:"next_of_kin_phone,omitempty"`
	IsPrimary      bool   `json:"is_primary"`
}

// BookTripRequest is the body for POST /book_trip.
type BookTripRequest struct {
	SeatNumbers   string      `json:"seat_numbers"`
	AmountPerSeat string      `json:"amount_per_seat"`
	AgentEmail    string      `json:"agent_email,omitempty"`
	Passengers    []Passenger `json:"passengers"`
	OriginID      string      `json:"origin_id"`
	DestinationID string      `json:"destination_id"`
	BoardingAt    string      `json:"boarding_at,omitempty"`
	TripID        string      `json:"trip_id"`
	TripDate      string      `json:"trip_date"`
	OrderID       string      `json:"order_id,omitempty"`
	Provider      string      `json:"provider"`
}

// --- Flight search (POST /search_flight) ---

// FlightItinerary is a single leg of a flight search.
type FlightItinerary struct {
	Departure     string `json:"Departure"`
	Destination   string `json:"Destination"`
	DepartureDate string `json:"DepartureDate"` // MM/DD/YYYY
}

// SearchFlightRequest is the body for POST /search_flight.
type SearchFlightRequest struct {
	Type        string            `json:"type"` // Oneway | Return | Multidestination
	Class       string            `json:"class"`
	Adult       int               `json:"adult"`
	Children    int               `json:"children"`
	Infant      int               `json:"infant"`
	Currency    string            `json:"currency"`
	Itineraries []FlightItinerary `json:"itineraries"`
}

// --- Flight select (POST /flight_select) ---

// SelectFlightRequest is the body for POST /flight_select.
type SelectFlightRequest struct {
	ID       string `json:"id"`
	Currency string `json:"currency"`
}

// --- Tentative flight booking (POST /flight_booking) ---

// FlightPassenger carries the passport-level detail a flight booking needs.
type FlightPassenger struct {
	PassengerType   string `json:"passenger_type"` // Adult | Child | Infant
	FirstName       string `json:"firstname"`
	MiddleName      string `json:"middlename,omitempty"`
	LastName        string `json:"lastname"`
	DOB             string `json:"dob"` // MM/DD/YYYY
	Phone           string `json:"phone"`
	PassportNumber  string `json:"passport_number"`
	ExpiryDate      string `json:"expiry_date"` // MM/DD/YYYY
	PassportCountry string `json:"passport_country"`
	Gender          string `json:"gender"`
	Title           string `json:"title"`
	Email           string `json:"email"`
	Address         string `json:"address"`
	Country         string `json:"country"`
	CountryCode     string `json:"country_code"`
	City            string `json:"city"`
	PostalCode      string `json:"postal_code"`
	ID              string `json:"id"`         // itinerary id from select
	BookingID       string `json:"booking_id"` // present on subsequent passengers
	Currency        string `json:"currency"`
	TotalAmount     int    `json:"total_amount"`
}

// --- Complete flight ticketing (POST /flight_ticket) ---

// TicketFlightRequest is the body for POST /flight_ticket.
type TicketFlightRequest struct {
	BookingID string `json:"booking_id"`
	PNRNumber string `json:"pnr_number"`
}

// --- Booking receipt (shared across bus + flight bookings) ---

// SeatDetail is a per-seat/passenger line on a booking receipt.
type SeatDetail struct {
	Fare           flexString `json:"fare"`
	Title          string     `json:"title"`
	Age            flexString `json:"age"`
	Sex            string     `json:"sex"`
	Name           string     `json:"name"`
	Email          string     `json:"email"`
	Phone          flexString `json:"phone"`
	Blood          string     `json:"blood,omitempty"`
	NextOfKin      string     `json:"next_of_kin,omitempty"`
	NextOfKinPhone flexString `json:"next_of_kin_phone,omitempty"`
	SeatNumber     flexString `json:"seat_number"`
}

// OrderReceipt is the confirmed booking Travu returns from book_trip,
// flight_booking (tentative) and flight_ticket (final). Numeric fields use
// flexString because Travu returns them as strings or numbers interchangeably.
type OrderReceipt struct {
	OrderStatus         string       `json:"order_status"`
	OrderID             flexString   `json:"order_id"`
	OrderName           string       `json:"order_name"`
	OrderEmail          string       `json:"order_email"`
	PhoneNumber         flexString   `json:"phone_number"`
	OrderAmount         flexString   `json:"order_amount"`
	TripID              flexString   `json:"trip_id"`
	OriginID            flexString   `json:"origin_id"`
	DestinationID       flexString   `json:"destination_id"`
	OrderTicketDate     string       `json:"order_ticket_date"`
	OrderTotalSeat      flexString   `json:"order_total_seat"`
	OrderSeats          flexString   `json:"order_seats"`
	AmountPerSeat       flexString   `json:"amount_per_seat"`
	OrderNumber         flexString   `json:"order_number"`
	VehicleNo           string       `json:"vehicle_no"`
	Narration           string       `json:"narration"`
	DepartureTerminal   string       `json:"departure_terminal"`
	DestinationTerminal string       `json:"destination_terminal"`
	SeatDetails         []SeatDetail `json:"seat_details"`
	Provider            string       `json:"provider"`
	// Flight-only fields carried through the tentative booking step.
	BookingID flexString `json:"booking_id"`
	PNRNumber flexString `json:"pnr_number"`
}

// Confirmed reports whether the receipt represents a confirmed booking.
func (r *OrderReceipt) Confirmed() bool {
	return r != nil && strings.EqualFold(r.OrderStatus, OrderStatusConfirmed)
}

// AmountNGN parses the order amount into a float. Returns 0 when absent.
func (r *OrderReceipt) AmountNGN() float64 {
	if r == nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(r.OrderAmount.String()), 64)
	if err != nil {
		return 0
	}
	return v
}

// --- Reference data ---

// State is a supported bus departure/destination state.
type State struct {
	ID   flexString `json:"id"`
	Name string     `json:"name"`
}

// Airport is a supported flight departure/destination airport.
type Airport struct {
	Code string `json:"code"`
	Name string `json:"name"`
	City string `json:"city,omitempty"`
}

// listEnvelope is the standard {error,message,info,data} wrapper.
type listEnvelope[T any] struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
	Info    string `json:"info"`
	Data    []T    `json:"data"`
}
