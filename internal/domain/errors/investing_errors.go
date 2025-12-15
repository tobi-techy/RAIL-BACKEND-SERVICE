package errors

import "errors"

// Investment and trading errors
var (
	// Basket errors
	ErrBasketNotFound       = errors.New("basket not found")
	ErrBasketInactive       = errors.New("basket is inactive")
	ErrInvalidBasketConfig  = errors.New("invalid basket configuration")

	// Order errors
	ErrOrderNotFound        = errors.New("order not found")
	ErrOrderCancelled       = errors.New("order has been cancelled")
	ErrOrderFilled          = errors.New("order has been filled")
	ErrOrderExpired         = errors.New("order has expired")
	ErrInvalidOrderType     = errors.New("invalid order type")
	ErrInvalidOrderSide     = errors.New("invalid order side")
	ErrOrderMinimumNotMet   = errors.New("order minimum not met")
	ErrOrderMaximumExceeded = errors.New("order maximum exceeded")
	ErrDuplicateOrder       = errors.New("duplicate order detected")

	// Position errors
	ErrPositionNotFound     = errors.New("position not found")
	ErrInsufficientPosition = errors.New("insufficient position")
	ErrPositionClosed       = errors.New("position is closed")

	// Portfolio errors
	ErrPortfolioNotFound    = errors.New("portfolio not found")
	ErrPortfolioEmpty       = errors.New("portfolio is empty")

	// Market errors
	ErrMarketClosed         = errors.New("market is closed")
	ErrMarketHalted         = errors.New("trading is halted")
	ErrSymbolNotFound       = errors.New("symbol not found")
	ErrSymbolNotTradable    = errors.New("symbol is not tradable")
	ErrPriceUnavailable     = errors.New("price data unavailable")

	// Brokerage errors
	ErrBrokerageUnavailable = errors.New("brokerage service unavailable")
	ErrBrokerageAccountNotActive = errors.New("brokerage account not active")
	ErrBrokerageRejected    = errors.New("order rejected by brokerage")

	// Allocation errors
	ErrInvalidAllocation    = errors.New("invalid allocation")
	ErrAllocationMismatch   = errors.New("allocation mismatch")
)

// BasketNotFoundError creates a basket not found error
func BasketNotFoundError(basketID string) *DomainError {
	return &DomainError{
		Err:     ErrBasketNotFound,
		Code:    "BASKET_NOT_FOUND",
		Message: "basket not found",
		Details: map[string]interface{}{
			"basket_id": basketID,
		},
	}
}

// OrderNotFoundError creates an order not found error
func OrderNotFoundError(orderID string) *DomainError {
	return &DomainError{
		Err:     ErrOrderNotFound,
		Code:    "ORDER_NOT_FOUND",
		Message: "order not found",
		Details: map[string]interface{}{
			"order_id": orderID,
		},
	}
}

// InsufficientPositionError creates an insufficient position error
func InsufficientPositionError(symbol string, available, required string) *DomainError {
	return &DomainError{
		Err:     ErrInsufficientPosition,
		Code:    "INSUFFICIENT_POSITION",
		Message: "insufficient position for this sell order",
		Details: map[string]interface{}{
			"symbol":    symbol,
			"available": available,
			"required":  required,
		},
	}
}

// OrderMinimumError creates an order minimum not met error
func OrderMinimumError(minimum, provided string) *DomainError {
	return &DomainError{
		Err:     ErrOrderMinimumNotMet,
		Code:    "ORDER_MINIMUM_NOT_MET",
		Message: "order amount is below minimum",
		Details: map[string]interface{}{
			"minimum":  minimum,
			"provided": provided,
		},
	}
}

// MarketClosedError creates a market closed error
func MarketClosedError(nextOpen string) *DomainError {
	return &DomainError{
		Err:     ErrMarketClosed,
		Code:    "MARKET_CLOSED",
		Message: "market is currently closed",
		Details: map[string]interface{}{
			"next_open": nextOpen,
		},
	}
}

// BrokerageRejectedError creates a brokerage rejection error
func BrokerageRejectedError(reason string) *DomainError {
	return &DomainError{
		Err:     ErrBrokerageRejected,
		Code:    "BROKERAGE_REJECTED",
		Message: "order was rejected by the brokerage",
		Details: map[string]interface{}{
			"reason": reason,
		},
	}
}

// SymbolNotFoundError creates a symbol not found error
func SymbolNotFoundError(symbol string) *DomainError {
	return &DomainError{
		Err:     ErrSymbolNotFound,
		Code:    "SYMBOL_NOT_FOUND",
		Message: "symbol not found",
		Details: map[string]interface{}{
			"symbol": symbol,
		},
	}
}

// OrderCannotBeCancelledError creates an error for orders that can't be cancelled
func OrderCannotBeCancelledError(orderID, status string) *DomainError {
	return &DomainError{
		Err:     ErrOrderFilled,
		Code:    "ORDER_CANNOT_BE_CANCELLED",
		Message: "order cannot be cancelled in current status",
		Details: map[string]interface{}{
			"order_id": orderID,
			"status":   status,
		},
	}
}

// Error checking helpers

// IsBasketNotFound checks if error is basket not found
func IsBasketNotFound(err error) bool {
	return errors.Is(err, ErrBasketNotFound)
}

// IsOrderNotFound checks if error is order not found
func IsOrderNotFound(err error) bool {
	return errors.Is(err, ErrOrderNotFound)
}

// IsInsufficientPosition checks if error is insufficient position
func IsInsufficientPosition(err error) bool {
	return errors.Is(err, ErrInsufficientPosition)
}

// IsMarketClosed checks if error is market closed
func IsMarketClosed(err error) bool {
	return errors.Is(err, ErrMarketClosed)
}

// IsBrokerageUnavailable checks if brokerage is unavailable
func IsBrokerageUnavailable(err error) bool {
	return errors.Is(err, ErrBrokerageUnavailable)
}
