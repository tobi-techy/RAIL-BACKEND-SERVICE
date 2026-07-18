package tools

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// RegisterTravelTools registers Travu bus + flight booking tools. Read tools run
// directly; book_bus/book_flight/save_travel_passenger are action tools staged
// for confirmation (book_bus/book_flight are fund-moving and require Face ID
// step-up). Miriam resolves trip fields from a prior search and passengers from
// the saved travel profile (topping up missing fields in chat) before calling
// the booking tools.
func RegisterTravelTools(r *Registry) {
	r.Register(NewTool(
		"search_bus_trips",
		"Search available interstate/intra-city bus trips for a route and date. States are UPPERCASE Nigerian state names (e.g. LAGOS, ABIA, FCT (ABUJA)). Returns trips with trip_id, order_id, provider short_name, fare, available_seats, boarding_at, origin_id/destination_id and terminals — all needed by book_bus.",
		SimpleArgs(map[string]map[string]interface{}{
			"departure_state":   StringParam("Departure state, UPPERCASE (e.g. LAGOS)"),
			"destination_state": StringParam("Destination state, UPPERCASE (e.g. ABIA)"),
			"trip_date":         StringParam("Travel date, YYYY-MM-DD"),
		}, []string{"departure_state", "destination_state", "trip_date"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			trips, err := deps.Travel.SearchBusTrips(ctx, GetArgString(args, "departure_state"), GetArgString(args, "destination_state"), GetArgString(args, "trip_date"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"trips": trips}}, nil
		},
	))

	r.Register(NewTool(
		"search_flights",
		"Search available domestic or international flights. type is Oneway, Return or Multidestination; class is a fare class code (e.g. Y). itineraries is a list of legs each with Departure, Destination (airport codes like LOS, ABV, MAN) and DepartureDate (MM/DD/YYYY). Returns flight options with an itinerary id used by book_flight.",
		SimpleArgs(map[string]map[string]interface{}{
			"type":        EnumParam("Trip type", []string{"Oneway", "Return", "Multidestination"}),
			"class":       StringParam("Fare class code, e.g. Y"),
			"adult":       NumberParam("Number of adult passengers (default 1)"),
			"children":    NumberParam("Number of child passengers (default 0)"),
			"infant":      NumberParam("Number of infant passengers (default 0)"),
			"currency":    StringParam("Fare currency (default NGN)"),
			"itineraries": ArrayOfObjectsParam("Flight legs: each has Departure, Destination (airport codes) and DepartureDate (MM/DD/YYYY)"),
		}, []string{"type", "itineraries"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			trips, err := deps.Travel.SearchFlights(ctx, args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"flights": trips}}, nil
		},
	))

	r.Register(NewTool(
		"list_travel_states",
		"List the Nigerian states Travu supports for bus travel (names to use for search_bus_trips).",
		SimpleArgs(nil, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			states, err := deps.Travel.ListStates(ctx)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"states": states}}, nil
		},
	))

	r.Register(NewTool(
		"list_airports",
		"List the airports Travu supports for flights, with their codes (used in search_flights itineraries).",
		SimpleArgs(nil, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			airports, err := deps.Travel.ListAirports(ctx)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"airports": airports}}, nil
		},
	))

	r.Register(NewTool(
		"list_travel_passengers",
		"List the user's saved traveler profiles (name, DOB, passport, contact). Use before booking to reuse a saved traveler; ask for any missing passport details for flights.",
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
		"Show the user's recent bus and flight bookings with route, date, amount and status.",
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
		"select_flight",
		"Refresh a chosen flight itinerary to get the latest price before booking. Pass the itinerary id from search_flights. Do this right before book_flight so the user confirms the current fare.",
		SimpleArgs(map[string]map[string]interface{}{
			"itinerary_id": StringParam("Itinerary id from search_flights"),
			"currency":     StringParam("Fare currency (default NGN)"),
		}, []string{"itinerary_id"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			res, err := deps.Travel.SelectFlight(ctx, GetArgString(args, "itinerary_id"), GetArgString(args, "currency"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	// --- Action tools (staged for confirmation) ---

	r.Register(NewTool(
		"book_bus",
		"Book bus seats for the user now. Pass the trip fields from search_bus_trips (provider short_name, trip_id, order_id, origin_id, destination_id, boarding_at, trip_date), seat_numbers as a comma-separated string (e.g. \"1,2\"), amount_per_seat (the fare from the trip), and passengers (one per seat, with title, name, age, sex, phone, email, next_of_kin). This moves money and requires user confirmation with Face ID.",
		SimpleArgs(map[string]map[string]interface{}{
			"provider":        StringParam("Operator short_name from search_bus_trips (e.g. GUO)"),
			"trip_id":         StringParam("trip_id from search_bus_trips"),
			"order_id":        StringParam("order_id from search_bus_trips"),
			"origin_id":       StringParam("origin_id from search_bus_trips"),
			"destination_id":  StringParam("destination_id from search_bus_trips"),
			"boarding_at":     StringParam("boarding_at from search_bus_trips (optional)"),
			"trip_date":       StringParam("Travel date, YYYY-MM-DD"),
			"seat_numbers":    StringParam("Comma-separated seat numbers, e.g. \"1,2\""),
			"amount_per_seat": NumberParam("Fare per seat in NGN (from the trip)"),
			"route":           StringParam("Route narration for the receipt (optional)"),
			"passengers":      ArrayOfObjectsParam("One passenger per seat: title, name, age, sex, phone, email, next_of_kin, next_of_kin_phone"),
		}, []string{"provider", "trip_id", "trip_date", "seat_numbers", "amount_per_seat", "passengers"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Travel == nil {
				return &core.ToolResult{Error: "travel booking not available"}, nil
			}
			res, err := deps.Travel.BookBus(ctx, userID, args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"book_flight",
		"Book a flight for the user now. Pass itinerary_id (from select_flight/search_flights), amount_ngn (the confirmed fare), and passengers each with passport-level detail: title, firstname, lastname, dob (MM/DD/YYYY), sex, phone, email, passport_number, passport_country, passport_expiry (MM/DD/YYYY), nationality. Reuse saved travelers from list_travel_passengers and ask for any missing passport fields. This moves money and requires user confirmation with Face ID.",
		SimpleArgs(map[string]map[string]interface{}{
			"itinerary_id": StringParam("Itinerary id from select_flight/search_flights"),
			"currency":     StringParam("Fare currency (default NGN)"),
			"amount_ngn":   NumberParam("Confirmed total fare in NGN"),
			"route":        StringParam("Route for the receipt, e.g. LOS to ABV (optional)"),
			"trip_date":    StringParam("Departure date for the receipt (optional)"),
			"passengers":   ArrayOfObjectsParam("Passengers with passport detail: title, firstname, middlename, lastname, dob, sex, phone, email, passport_number, passport_country, passport_expiry, nationality, address, city, country, country_code, postal_code"),
		}, []string{"itinerary_id", "amount_ngn", "passengers"}),
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
}
