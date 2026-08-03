package tools

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// RegisterTravelTools registers BRIJ flight booking tools. Read tools run
// directly; create_flight_intent/book_flight/save_travel_passenger/
// request_flight_refund are action tools staged for confirmation (book_flight is
// fund-moving and requires Face ID step-up). Miriam resolves trip fields from a
// prior search and passengers from the saved travel profile (topping up missing
// fields in chat) before calling the booking tools.
func RegisterTravelTools(r *Registry) {
	r.Register(NewTool(
		"search_flights",
		"Search live one-way flight offers for a route and date. origin and destination are 3-letter IATA airport codes (e.g. LOS, ABV, MAN); depart_date is YYYY-MM-DD. Returns offers with id, origin_iata, destination_iata, departing_at, arriving_at, total_amount_decimal (fare in USDC) and expires_at — all needed by create_flight_intent.",
		SimpleArgs(map[string]map[string]interface{}{
			"origin":       StringParam("Departure airport IATA code, e.g. LOS"),
			"destination":  StringParam("Arrival airport IATA code, e.g. ABV"),
			"depart_date":  StringParam("Travel date, YYYY-MM-DD"),
			"adults":       NumberParam("Number of adult passengers (default 1)"),
		}, []string{"origin", "destination", "depart_date"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			flights, err := deps.Travel.SearchFlights(ctx, GetArgString(args, "origin"), GetArgString(args, "destination"), GetArgString(args, "depart_date"), int(GetArgFloat(args, "adults")))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"flights": flights}}, nil
		},
	))

	r.Register(NewTool(
		"create_flight_intent",
		"Lock a flight offer with BRIJ and get its escrow amount before booking. Pass offer_id from search_flights plus the flight details for the receipt. Does not move the user's money — it only prepares the booking. Call this right before book_flight so the user sees the exact fare and escrow.",
		SimpleArgs(map[string]map[string]interface{}{
			"offer_id":     StringParam("Offer id from search_flights"),
			"airline":      StringParam("Airline/owner name from search_flights (optional)"),
			"origin":       StringParam("Departure airport IATA code, e.g. LOS"),
			"destination":  StringParam("Arrival airport IATA code, e.g. ABV"),
			"departing_at": StringParam("Departure time (ISO 8601) from search_flights"),
			"arriving_at":  StringParam("Arrival time (ISO 8601) from search_flights (optional)"),
			"amount_usd":   StringParam("Fare in USDC from search_flights (e.g. 67.60)"),
		}, []string{"offer_id"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			res, err := deps.Travel.CreateIntent(ctx, userID, args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"book_flight",
		"Book a locked flight for the user now. Pass intent_id from create_flight_intent and exactly one passenger with given_name, family_name, born_on (YYYY-MM-DD), title (mr/mrs/ms/miss/dr), gender (m/f), email, and phone_number (E.164, e.g. +447400123456). Reuse saved travelers from list_travel_passengers and ask for any missing fields. This holds the user's funds for the escrow + Rail fee and requires user confirmation with Face ID.",
		SimpleArgs(map[string]map[string]interface{}{
			"intent_id": StringParam("Intent id from create_flight_intent"),
			"passenger": map[string]interface{}{
				"type":        "object",
				"description": "The adult passenger: given_name, family_name, born_on (YYYY-MM-DD), title (mr/mrs/ms/miss/dr), gender (m/f), email, phone_number (E.164)",
			},
		}, []string{"intent_id", "passenger"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			res, err := deps.Travel.BookFlight(ctx, userID, args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"get_flight_status",
		"Check the live status of a flight intent: active, booked (ticketed) or refunded. Pass intent_id from create_flight_intent or a past booking.",
		SimpleArgs(map[string]map[string]interface{}{
			"intent_id": StringParam("Intent id from create_flight_intent or get_travel_bookings"),
		}, []string{"intent_id"}),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			res, err := deps.Travel.GetIntentStatus(ctx, userID, GetArgString(args, "intent_id"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"get_flight_order",
		"Get the airline order (PNR) for a ticketed flight. Pass intent_id of a booked flight.",
		SimpleArgs(map[string]map[string]interface{}{
			"intent_id": StringParam("Intent id of a booked flight"),
		}, []string{"intent_id"}),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			res, err := deps.Travel.GetOrderStatus(ctx, userID, GetArgString(args, "intent_id"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"list_travel_passengers",
		"List the user's saved traveler profiles (name, DOB, title, gender, passport). Use before booking to reuse a saved traveler; ask for any missing details.",
		SimpleArgs(nil, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			list, err := deps.Travel.ListPassengers(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"passengers": list}}, nil
		},
	))

	r.Register(NewTool(
		"get_travel_bookings",
		"Show the user's recent flight bookings with route, date, amount and status.",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": NumberParam("How many to return (1-50, default 20)"),
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			history, err := deps.Travel.GetBookingHistory(ctx, userID, int(GetArgFloat(args, "limit")))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"bookings": history}}, nil
		},
	))

	r.Register(NewTool(
		"save_travel_passenger",
		"Save a traveler profile so the user can book for them again without re-entering details. Include passport fields for flights. Does not move money.",
		SimpleArgs(map[string]map[string]interface{}{
			"label":            StringParam("Friendly name, e.g. \"Me\" or \"Wife\""),
			"is_primary":       BoolParam("Set as the default traveler for this user"),
			"passenger_type":   StringParam("Adult, Child or Infant (default Adult)"),
			"title":            StringParam("Title, e.g. Mr/Mrs/Ms"),
			"firstname":        StringParam("First name"),
			"middlename":       StringParam("Middle name (optional)"),
			"lastname":         StringParam("Last name"),
			"dob":              StringParam("Date of birth, MM/DD/YYYY"),
			"sex":              StringParam("Male or Female"),
			"phone":            StringParam("Phone number"),
			"email":            StringParam("Email address"),
			"passport_number":  StringParam("Passport number (for flights)"),
			"passport_country": StringParam("Passport issuing country (for flights)"),
			"passport_expiry":  StringParam("Passport expiry, MM/DD/YYYY (for flights)"),
			"nationality":      StringParam("Nationality (for flights)"),
			"next_of_kin":      StringParam("Next of kin full name (for bus)"),
			"next_of_kin_phone": StringParam("Next of kin phone (for bus)"),
		}, []string{"firstname", "lastname"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			res, err := deps.Travel.SavePassenger(ctx, userID, args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"request_flight_refund",
		"File a refund request for a booked flight. Refunds are reviewed by BRIJ and the airline; this is a request, not a guarantee. Pass intent_id of the booked flight and a reason.",
		SimpleArgs(map[string]map[string]interface{}{
			"intent_id": StringParam("Intent id of the booked flight"),
			"reason":    StringParam("Why the user wants a refund"),
			"contact":   StringParam("Contact for the refund review (email or phone, optional)"),
		}, []string{"intent_id", "reason"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			res, err := deps.Travel.RequestRefund(ctx, userID, GetArgString(args, "intent_id"), GetArgString(args, "reason"), GetArgString(args, "contact"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))
}
