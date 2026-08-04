package di

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	travelservice "github.com/rail-service/rail_service/internal/domain/services/travel"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/brij"
	"github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

// --- TravelProvider ---

// buildTravelProvider wraps the travel service so Miriam's flight tools execute
// through the same booking pipeline (escrow holds, PNR issuance, ticket
// delivery) as the rest of the app.
func buildTravelProvider(c *Container) core.TravelProvider {
	if c.TravelService == nil {
		return nil
	}
	return &travelExecAdapter{svc: c.TravelService, logger: c.ZapLog}
}

// travelExecAdapter adapts *travel.Service to aicore.TravelProvider.
type travelExecAdapter struct {
	svc    *travelservice.Service
	logger *zap.Logger
}

func (a *travelExecAdapter) SearchFlights(ctx context.Context, origin, destination, departDate string, adults int) ([]map[string]interface{}, error) {
	offers, err := a.svc.SearchFlights(ctx, origin, destination, departDate, adults)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(offers))
	for _, o := range offers {
		out = append(out, structToMap(&o))
	}
	return out, nil
}

func (a *travelExecAdapter) CreateIntent(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	res, err := a.svc.CreateIntent(ctx, userID, travelservice.CreateIntentRequest{
		OfferID:     travelArgString(args, "offer_id"),
		Airline:     travelArgString(args, "airline"),
		Origin:      travelArgString(args, "origin"),
		Destination: travelArgString(args, "destination"),
		DepartingAt: travelArgString(args, "departing_at"),
		ArrivingAt:  travelArgString(args, "arriving_at"),
		AmountUSD:   travelArgString(args, "amount_usd"),
	})
	if err != nil {
		return nil, err
	}
	return structToMap(res), nil
}

func (a *travelExecAdapter) BookFlight(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	res, err := a.svc.BookFlight(ctx, userID, travelservice.BookFlightRequest{
		IntentID:  travelArgString(args, "intent_id"),
		Passenger: a.flightPassengerFromArg(args, "passenger"),
	})
	if err != nil {
		return nil, err
	}
	return structToMap(res), nil
}

func (a *travelExecAdapter) GetIntentStatus(ctx context.Context, userID uuid.UUID, intentID string) (map[string]interface{}, error) {
	intent, err := a.svc.GetIntentStatus(ctx, userID, intentID)
	if err != nil {
		return nil, err
	}
	return structToMap(intent), nil
}

func (a *travelExecAdapter) GetOrderStatus(ctx context.Context, userID uuid.UUID, intentID string) (map[string]interface{}, error) {
	order, err := a.svc.GetOrderStatus(ctx, userID, intentID)
	if err != nil {
		return nil, err
	}
	return structToMap(order), nil
}

func (a *travelExecAdapter) RequestRefund(ctx context.Context, userID uuid.UUID, intentID, reason, contact string) (map[string]interface{}, error) {
	res, err := a.svc.RequestRefund(ctx, userID, intentID, reason, contact)
	if err != nil {
		return nil, err
	}
	return structToMap(res), nil
}

func (a *travelExecAdapter) ListPassengers(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	list, err := a.svc.ListPassengers(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(list))
	for _, p := range list {
		out = append(out, structToMap(travelerSummary{
			ID:            p.ID,
			Label:         p.Label,
			IsPrimary:     p.IsPrimary,
			PassengerType: p.PassengerType,
			Title:         p.Title,
			FirstName:     p.FirstName,
			MiddleName:    p.MiddleName,
			LastName:      p.LastName,
		}))
	}
	return out, nil
}

// travelerSummary is the minimal traveler data exposed to the AI tool result —
// enough to select a saved profile without leaking passport numbers, contact
// details, dates of birth, or next-of-kin records. The full profile is resolved
// server-side at booking time.
type travelerSummary struct {
	ID            uuid.UUID `json:"id"`
	Label         string    `json:"label"`
	IsPrimary     bool      `json:"is_primary"`
	PassengerType string    `json:"passenger_type"`
	Title         string    `json:"title"`
	FirstName     string    `json:"first_name"`
	MiddleName    string    `json:"middle_name"`
	LastName      string    `json:"last_name"`
}

func (a *travelExecAdapter) GetBookingHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error) {
	history, err := a.svc.GetBookingHistory(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(history))
	for _, h := range history {
		out = append(out, structToMap(&h))
	}
	return out, nil
}

func (a *travelExecAdapter) SavePassenger(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	id, err := a.svc.SavePassenger(ctx, userID, travelservice.TravelPassenger{
		Label:           travelArgString(args, "label"),
		IsPrimary:       travelArgBool(args, "is_primary"),
		PassengerType:   travelArgString(args, "passenger_type"),
		Title:           travelArgString(args, "title"),
		FirstName:       travelArgString(args, "firstname"),
		MiddleName:      travelArgString(args, "middlename"),
		LastName:        travelArgString(args, "lastname"),
		DOB:             travelArgString(args, "dob"),
		Sex:             travelArgString(args, "sex"),
		Phone:           travelArgString(args, "phone"),
		Email:           travelArgString(args, "email"),
		PassportNumber:  travelArgString(args, "passport_number"),
		PassportCountry: travelArgString(args, "passport_country"),
		PassportExpiry:  travelArgString(args, "passport_expiry"),
		Nationality:     travelArgString(args, "nationality"),
		NextOfKin:       travelArgString(args, "next_of_kin"),
		NextOfKinPhone:  travelArgString(args, "next_of_kin_phone"),
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"id": id.String()}, nil
}

// flightPassengerFromArg decodes the book_flight passenger object into a BRIJ
// passenger, normalizing title/gender/date forms the model may emit. A malformed
// passenger payload is logged (validatePassenger surfaces the user-facing
// error), never silently passed downstream.
func (a *travelExecAdapter) flightPassengerFromArg(args map[string]interface{}, key string) brij.PassengerInput {
	raw, ok := args[key]
	if !ok {
		return brij.PassengerInput{}
	}
	var p map[string]interface{}
	switch t := raw.(type) {
	case map[string]interface{}:
		p = t
	case string:
		if err := json.Unmarshal([]byte(t), &p); err != nil {
			a.logger.Warn("book_flight passenger arg is malformed JSON", zap.Error(err))
		}
	}
	if p == nil {
		return brij.PassengerInput{}
	}
	gender := strings.ToLower(strings.TrimSpace(travelArgString(p, "gender")))
	switch gender {
	case "male":
		gender = "m"
	case "female":
		gender = "f"
	}
	return brij.PassengerInput{
		GivenName:   strings.TrimSpace(travelArgString(p, "given_name")),
		FamilyName:  strings.TrimSpace(travelArgString(p, "family_name")),
		BornOn:      travelDateToISO(travelArgString(p, "born_on")),
		Title:       strings.ToLower(strings.TrimSpace(travelArgString(p, "title"))),
		Gender:      gender,
		Email:       strings.TrimSpace(travelArgString(p, "email")),
		PhoneNumber: strings.TrimSpace(travelArgString(p, "phone_number")),
	}
}

// travelArgString extracts a string from the tool argument map.
func travelArgString(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	if s, ok := args[key].(string); ok {
		return s
	}
	return ""
}

// travelArgBool extracts a bool from the tool argument map.
func travelArgBool(args map[string]interface{}, key string) bool {
	if args == nil {
		return false
	}
	if b, ok := args[key].(bool); ok {
		return b
	}
	return false
}

var travelDateSlash = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)

// travelDateToISO normalizes MM/DD/YYYY to YYYY-MM-DD; other inputs pass through.
func travelDateToISO(s string) string {
	s = strings.TrimSpace(s)
	if m := travelDateSlash.FindStringSubmatch(s); m != nil {
		return m[3] + "-" + pad2(m[1]) + "-" + pad2(m[2])
	}
	return s
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

// structToMap round-trips a JSON-tagged struct into a map for the tool result.
func structToMap(v interface{}) map[string]interface{} {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

// --- TicketMessenger ---

// travelMessengerAdapter delivers booking receipts through the platform bridge
// dispatcher, folded into the iMessage thread as a critical receipt.
type travelMessengerAdapter struct {
	dispatcher *platform.BridgeDispatcher
}

func (m *travelMessengerAdapter) SendMessage(ctx context.Context, userID uuid.UUID, text string) error {
	return m.dispatcher.SendGenericNotification(ctx, userID, "Flight ticket", text)
}
