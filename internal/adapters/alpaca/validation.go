package alpaca

import (
	"fmt"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ValidateOrderRequest validates an order request before submission
func ValidateOrderRequest(req *entities.AlpacaCreateOrderRequest) error {
	if req.Symbol == "" {
		return fmt.Errorf("symbol is required")
	}

	// Qty and Notional are mutually exclusive
	if req.Qty != nil && req.Notional != nil {
		return fmt.Errorf("qty and notional are mutually exclusive")
	}

	if req.Qty == nil && req.Notional == nil {
		return fmt.Errorf("either qty or notional must be specified")
	}

	// Fractional shares only supported for day orders
	if req.Qty != nil && !req.Qty.IsInteger() && req.TimeInForce != entities.AlpacaTimeInForceDay {
		return fmt.Errorf("fractional shares only supported for day orders")
	}

	// Validate commission type
	if req.Commission != nil && req.CommissionType != "" {
		switch req.CommissionType {
		case "notional", "qty", "bps":
			// Valid commission types
		default:
			return fmt.Errorf("invalid commission_type: %s", req.CommissionType)
		}
	}

	// Validate order type specific fields
	switch req.Type {
	case entities.AlpacaOrderTypeLimit:
		if req.LimitPrice == nil {
			return fmt.Errorf("limit_price required for limit orders")
		}
	case entities.AlpacaOrderTypeStop:
		if req.StopPrice == nil {
			return fmt.Errorf("stop_price required for stop orders")
		}
	case entities.AlpacaOrderTypeStopLimit:
		if req.LimitPrice == nil || req.StopPrice == nil {
			return fmt.Errorf("both limit_price and stop_price required for stop_limit orders")
		}
	case entities.AlpacaOrderTypeTrailingStop:
		if req.TrailPrice == nil && req.TrailPercent == nil {
			return fmt.Errorf("either trail_price or trail_percent required for trailing_stop orders")
		}
		if req.TrailPrice != nil && req.TrailPercent != nil {
			return fmt.Errorf("trail_price and trail_percent are mutually exclusive")
		}
	}

	return nil
}
