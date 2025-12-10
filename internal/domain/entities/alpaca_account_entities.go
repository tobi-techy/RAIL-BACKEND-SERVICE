package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// AlpacaAccount represents a user's linked Alpaca brokerage account
type AlpacaAccount struct {
	ID                  uuid.UUID           `json:"id" db:"id"`
	UserID              uuid.UUID           `json:"user_id" db:"user_id"`
	AlpacaAccountID     string              `json:"alpaca_account_id" db:"alpaca_account_id"`
	AlpacaAccountNumber string              `json:"alpaca_account_number" db:"alpaca_account_number"`
	Status              AlpacaAccountStatus `json:"status" db:"status"`
	AccountType         AlpacaAccountType   `json:"account_type" db:"account_type"`
	Currency            string              `json:"currency" db:"currency"`
	BuyingPower         decimal.Decimal     `json:"buying_power" db:"buying_power"`
	Cash                decimal.Decimal     `json:"cash" db:"cash"`
	PortfolioValue      decimal.Decimal     `json:"portfolio_value" db:"portfolio_value"`
	TradingBlocked      bool                `json:"trading_blocked" db:"trading_blocked"`
	TransfersBlocked    bool                `json:"transfers_blocked" db:"transfers_blocked"`
	AccountBlocked      bool                `json:"account_blocked" db:"account_blocked"`
	LastSyncedAt        *time.Time          `json:"last_synced_at" db:"last_synced_at"`
	CreatedAt           time.Time           `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time           `json:"updated_at" db:"updated_at"`
}

// InvestmentOrder represents a tracked investment order
type InvestmentOrder struct {
	ID              uuid.UUID         `json:"id" db:"id"`
	UserID          uuid.UUID         `json:"user_id" db:"user_id"`
	AlpacaAccountID *uuid.UUID        `json:"alpaca_account_id" db:"alpaca_account_id"`
	AlpacaOrderID   *string           `json:"alpaca_order_id" db:"alpaca_order_id"`
	ClientOrderID   string            `json:"client_order_id" db:"client_order_id"`
	BasketID        *uuid.UUID        `json:"basket_id" db:"basket_id"`
	Symbol          string            `json:"symbol" db:"symbol"`
	Side            AlpacaOrderSide   `json:"side" db:"side"`
	OrderType       AlpacaOrderType   `json:"order_type" db:"order_type"`
	TimeInForce     AlpacaTimeInForce `json:"time_in_force" db:"time_in_force"`
	Qty             *decimal.Decimal  `json:"qty" db:"qty"`
	Notional        *decimal.Decimal  `json:"notional" db:"notional"`
	FilledQty       decimal.Decimal   `json:"filled_qty" db:"filled_qty"`
	FilledAvgPrice  *decimal.Decimal  `json:"filled_avg_price" db:"filled_avg_price"`
	LimitPrice      *decimal.Decimal  `json:"limit_price" db:"limit_price"`
	StopPrice       *decimal.Decimal  `json:"stop_price" db:"stop_price"`
	Status          AlpacaOrderStatus `json:"status" db:"status"`
	Commission      decimal.Decimal   `json:"commission" db:"commission"`
	SubmittedAt     *time.Time        `json:"submitted_at" db:"submitted_at"`
	FilledAt        *time.Time        `json:"filled_at" db:"filled_at"`
	CanceledAt      *time.Time        `json:"canceled_at" db:"canceled_at"`
	FailedAt        *time.Time        `json:"failed_at" db:"failed_at"`
	CreatedAt       time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at" db:"updated_at"`
}

// InvestmentPosition represents a user's position in a security
type InvestmentPosition struct {
	ID              uuid.UUID        `json:"id" db:"id"`
	UserID          uuid.UUID        `json:"user_id" db:"user_id"`
	AlpacaAccountID *uuid.UUID       `json:"alpaca_account_id" db:"alpaca_account_id"`
	Symbol          string           `json:"symbol" db:"symbol"`
	AssetID         string           `json:"asset_id" db:"asset_id"`
	Qty             decimal.Decimal  `json:"qty" db:"qty"`
	QtyAvailable    decimal.Decimal  `json:"qty_available" db:"qty_available"`
	AvgEntryPrice   decimal.Decimal  `json:"avg_entry_price" db:"avg_entry_price"`
	MarketValue     decimal.Decimal  `json:"market_value" db:"market_value"`
	CostBasis       decimal.Decimal  `json:"cost_basis" db:"cost_basis"`
	UnrealizedPL    decimal.Decimal  `json:"unrealized_pl" db:"unrealized_pl"`
	UnrealizedPLPC  decimal.Decimal  `json:"unrealized_plpc" db:"unrealized_plpc"`
	CurrentPrice    decimal.Decimal  `json:"current_price" db:"current_price"`
	LastdayPrice    decimal.Decimal  `json:"lastday_price" db:"lastday_price"`
	ChangeToday     decimal.Decimal  `json:"change_today" db:"change_today"`
	Side            string           `json:"side" db:"side"`
	LastSyncedAt    *time.Time       `json:"last_synced_at" db:"last_synced_at"`
	CreatedAt       time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at" db:"updated_at"`
}

// AlpacaInstantFunding represents an instant funding transfer
type AlpacaInstantFunding struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	UserID            uuid.UUID       `json:"user_id" db:"user_id"`
	AlpacaAccountID   *uuid.UUID      `json:"alpaca_account_id" db:"alpaca_account_id"`
	AlpacaTransferID  string          `json:"alpaca_transfer_id" db:"alpaca_transfer_id"`
	SourceAccountNo   string          `json:"source_account_no" db:"source_account_no"`
	Amount            decimal.Decimal `json:"amount" db:"amount"`
	RemainingPayable  decimal.Decimal `json:"remaining_payable" db:"remaining_payable"`
	TotalInterest     decimal.Decimal `json:"total_interest" db:"total_interest"`
	Status            string          `json:"status" db:"status"`
	Deadline          *time.Time      `json:"deadline" db:"deadline"`
	SystemDate        *time.Time      `json:"system_date" db:"system_date"`
	SettlementID      *string         `json:"settlement_id" db:"settlement_id"`
	SettledAt         *time.Time      `json:"settled_at" db:"settled_at"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" db:"updated_at"`
}

// AlpacaEvent represents an event from Alpaca for audit/processing
type AlpacaEvent struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	UserID          *uuid.UUID `json:"user_id" db:"user_id"`
	AlpacaAccountID *uuid.UUID `json:"alpaca_account_id" db:"alpaca_account_id"`
	EventType       string     `json:"event_type" db:"event_type"`
	EventID         string     `json:"event_id" db:"event_id"`
	Payload         []byte     `json:"payload" db:"payload"`
	Processed       bool       `json:"processed" db:"processed"`
	ProcessedAt     *time.Time `json:"processed_at" db:"processed_at"`
	ErrorMessage    *string    `json:"error_message" db:"error_message"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
}

// AlpacaOrderFillEvent represents an order fill event
type AlpacaOrderFillEvent struct {
	OrderID        string          `json:"order_id"`
	AccountID      string          `json:"account_id"`
	Symbol         string          `json:"symbol"`
	Side           string          `json:"side"`
	FilledQty      decimal.Decimal `json:"filled_qty"`
	FilledAvgPrice decimal.Decimal `json:"filled_avg_price"`
	Status         string          `json:"status"`
	FilledAt       time.Time       `json:"filled_at"`
}

// AlpacaAccountEvent represents an account status change event
type AlpacaAccountEvent struct {
	AccountID string              `json:"account_id"`
	Status    AlpacaAccountStatus `json:"status"`
	Reason    string              `json:"reason,omitempty"`
	UpdatedAt time.Time           `json:"updated_at"`
}

// AlpacaPositionEvent represents a position update event
type AlpacaPositionEvent struct {
	AccountID    string          `json:"account_id"`
	Symbol       string          `json:"symbol"`
	Qty          decimal.Decimal `json:"qty"`
	MarketValue  decimal.Decimal `json:"market_value"`
	UnrealizedPL decimal.Decimal `json:"unrealized_pl"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// AlpacaReconciliationReport represents portfolio reconciliation results
type AlpacaReconciliationReport struct {
	UserID            uuid.UUID                  `json:"user_id"`
	AlpacaAccountID   string                     `json:"alpaca_account_id"`
	ReconciledAt      time.Time                  `json:"reconciled_at"`
	PositionsMatched  int                        `json:"positions_matched"`
	PositionsAdded    int                        `json:"positions_added"`
	PositionsRemoved  int                        `json:"positions_removed"`
	PositionsUpdated  int                        `json:"positions_updated"`
	Discrepancies     []PositionDiscrepancy      `json:"discrepancies,omitempty"`
	BalanceDiscrepancy *BalanceDiscrepancy       `json:"balance_discrepancy,omitempty"`
}

// PositionDiscrepancy represents a mismatch between local and Alpaca positions
type PositionDiscrepancy struct {
	Symbol       string          `json:"symbol"`
	LocalQty     decimal.Decimal `json:"local_qty"`
	AlpacaQty    decimal.Decimal `json:"alpaca_qty"`
	Difference   decimal.Decimal `json:"difference"`
}

// BalanceDiscrepancy represents a mismatch in account balances
type BalanceDiscrepancy struct {
	LocalBuyingPower  decimal.Decimal `json:"local_buying_power"`
	AlpacaBuyingPower decimal.Decimal `json:"alpaca_buying_power"`
	LocalCash         decimal.Decimal `json:"local_cash"`
	AlpacaCash        decimal.Decimal `json:"alpaca_cash"`
}
