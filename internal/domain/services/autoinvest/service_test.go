package autoinvest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

type stubOrderPlacer struct{}

func (stubOrderPlacer) PlaceMarketOrder(context.Context, uuid.UUID, string, decimal.Decimal, string) (*entities.AlpacaOrderResponse, error) {
	return nil, nil
}

// The KYC auto-invest worker stands itself down when no execution venue is
// wired, because TriggerAutoInvestment fails closed in that state — without
// this the worker re-scanned the same candidates and logged a skip for each one
// every 30 seconds, forever. Production wires a nil placer (the Alpaca
// brokerage was removed), so this is the state the worker has to detect.
func TestHasOrderVenue(t *testing.T) {
	var nilService *Service
	require.False(t, nilService.HasOrderVenue(), "a nil service has no venue")

	withoutVenue := NewService(nil, nil, Config{}, logger.New("error", "test"))
	require.False(t, withoutVenue.HasOrderVenue())

	withVenue := NewService(nil, stubOrderPlacer{}, Config{}, logger.New("error", "test"))
	require.True(t, withVenue.HasOrderVenue())

	withoutVenue.SetOrderPlacer(stubOrderPlacer{})
	require.True(t, withoutVenue.HasOrderVenue(), "wiring a venue later must re-enable it")
}
