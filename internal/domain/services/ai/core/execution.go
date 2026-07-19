package core

import (
	"context"

	"github.com/google/uuid"
)

// Execution Engine providers (spec 5.2). Each interface wraps an existing
// domain service; implementations live in the DI layer. All mutating methods
// are dispatched through action tools, so they inherit Monitor-mode blocking
// and the pending-action confirmation flow.

// BillPayProvider surfaces upcoming bills and sets up auto-pay automations.
type BillPayProvider interface {
	GetUpcomingBills(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	// SetupAutoPay creates an automation for the obligation's due date. With a
	// payee (Rail tag, email, or phone) the bill amount is actually sent to
	// that payee on the due day and the user gets a payment receipt; without
	// one, funds are set aside from Stash to Spend ahead of the due date.
	SetupAutoPay(ctx context.Context, userID uuid.UUID, obligationID, payeeIdentifier, payeeName string) (map[string]interface{}, error)
}

// BillsProvider powers Nigerian bill payments through Airbills (airtime, data,
// electricity, cable TV, betting, transport). Read methods surface catalog and
// history; the mutating PayBill/AutomateBill/SaveBeneficiary methods are
// dispatched via action tools so they inherit Monitor-mode blocking and the
// pending-action confirmation flow (with Face ID step-up for the fund-moving
// ones).
type BillsProvider interface {
	ListProviders(ctx context.Context, category string) ([]map[string]interface{}, error)
	GetDataPlans(ctx context.Context, networkID string) ([]map[string]interface{}, error)
	GetCablePackages(ctx context.Context) ([]map[string]interface{}, error)
	ValidateMeter(ctx context.Context, meterNo, electID string) (map[string]interface{}, error)
	DetectNetwork(ctx context.Context, phone string) (map[string]interface{}, error)
	ListBeneficiaries(ctx context.Context, userID uuid.UUID, category string) ([]map[string]interface{}, error)
	GetPaymentHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
	PayBill(ctx context.Context, userID uuid.UUID, category, recipient, networkID, prodID, electID string, amountNGN float64) (map[string]interface{}, error)
	AutomateBill(ctx context.Context, userID uuid.UUID, category, recipient, networkID, prodID, electID string, amountNGN float64, schedule string) (map[string]interface{}, error)
	SaveBeneficiary(ctx context.Context, userID uuid.UUID, label, category, recipient, networkID, prodID, electID string) (map[string]interface{}, error)
}

// SubscriptionAuditProvider detects recurring subscriptions and cancels them.
type SubscriptionAuditProvider interface {
	// AuditSubscriptions returns detected recurring charges with waste flags
	// (unused, duplicate-category) and a monthly total.
	AuditSubscriptions(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	// CancelSubscription marks a subscription obligation cancelled and
	// optionally blocks the merchant so future card charges decline.
	CancelSubscription(ctx context.Context, userID uuid.UUID, name string, blockMerchant bool) (map[string]interface{}, error)
}

// InvestmentExecutionProvider places buy/sell orders per allocation rules.
type InvestmentExecutionProvider interface {
	ListInvestmentOptions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	ExecuteInvestment(ctx context.Context, userID uuid.UUID, basketID, side, amount string) (map[string]interface{}, error)
}

// YieldOptimizationProvider moves idle stablecoins into the yield-bearing stash.
type YieldOptimizationProvider interface {
	GetYieldStatus(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	OptimizeYield(ctx context.Context, userID uuid.UUID, amount string) (map[string]interface{}, error)
}

// MerchantBlockProvider blocks problematic merchants at the card layer.
type MerchantBlockProvider interface {
	BlockMerchant(ctx context.Context, userID uuid.UUID, merchant string) (map[string]interface{}, error)
	UnblockMerchant(ctx context.Context, userID uuid.UUID, merchant string) (map[string]interface{}, error)
	ListBlockedMerchants(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

// TradeCopyProvider mirrors trades from vetted conductors (copy trading).
// conductor arguments accept either a conductor UUID or a display name
// ("copy Nancy's trades") — implementations resolve names case-insensitively.
type TradeCopyProvider interface {
	ListConductors(ctx context.Context) ([]map[string]interface{}, error)
	ResearchTrader(ctx context.Context, userID uuid.UUID, conductor string) (map[string]interface{}, error)
	GetCopyStatus(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	StartCopying(ctx context.Context, userID uuid.UUID, conductor, amount string) (map[string]interface{}, error)
	PauseCopying(ctx context.Context, userID uuid.UUID, draftID string) (map[string]interface{}, error)
	ResumeCopying(ctx context.Context, userID uuid.UUID, draftID string) (map[string]interface{}, error)
	StopCopying(ctx context.Context, userID uuid.UUID, draftID string) (map[string]interface{}, error)
}

// TravelProvider powers bus + flight booking through Travu. Read methods surface
// search results, reference data, saved travelers, and history; the mutating
// BookBus/BookFlight/SavePassenger methods are dispatched via action tools so
// they inherit Monitor-mode blocking and the pending-action confirmation flow
// (with Face ID step-up for the fund-moving BookBus/BookFlight).
type TravelProvider interface {
	SearchBusTrips(ctx context.Context, departureState, destinationState, tripDate string) ([]map[string]interface{}, error)
	SearchFlights(ctx context.Context, args map[string]interface{}) ([]map[string]interface{}, error)
	ListStates(ctx context.Context) ([]map[string]interface{}, error)
	ListAirports(ctx context.Context) ([]map[string]interface{}, error)
	ListPassengers(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	GetBookingHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
	SelectFlight(ctx context.Context, itineraryID, currency string) (map[string]interface{}, error)
	BookBus(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error)
	BookFlight(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error)
	SavePassenger(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error)
}
