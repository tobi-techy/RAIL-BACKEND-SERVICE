// Package entities — Alpaca brokerage entity stubs.
//
// Alpaca brokerage onboarding and order execution have been removed from the
// live code path (investment execution now routes through the Glider/Solana
// sleeve). These types are retained as compile-time stubs so that the domain
// and infrastructure packages that still reference them (balance, station,
// portfolio analytics, investing, account deletion) continue to build. They
// carry no runtime behavior and are not populated from a live Alpaca backend.
package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// AlpacaAccountType represents the type of brokerage account.
type AlpacaAccountType string

const (
	AlpacaAccountTypeTradingCash   AlpacaAccountType = "trading_cash"
	AlpacaAccountTypeTradingMargin AlpacaAccountType = "trading_margin"
)

// AlpacaAccountStatus represents the status of an account.
type AlpacaAccountStatus string

const (
	AlpacaAccountStatusActive          AlpacaAccountStatus = "ACTIVE"
	AlpacaAccountStatusAccountUpdated  AlpacaAccountStatus = "ACCOUNT_UPDATED"
	AlpacaAccountStatusApprovalPending AlpacaAccountStatus = "APPROVAL_PENDING"
	AlpacaAccountStatusApproved        AlpacaAccountStatus = "APPROVED"
	AlpacaAccountStatusDisabled        AlpacaAccountStatus = "DISABLED"
	AlpacaAccountStatusRejected        AlpacaAccountStatus = "REJECTED"
	AlpacaAccountStatusSubmitted       AlpacaAccountStatus = "SUBMITTED"
)

// AlpacaCreateAccountRequest is a deprecated request shape retained for the
// optional KYC-side account creation stub.
type AlpacaCreateAccountRequest struct {
	Contact        AlpacaContact         `json:"contact"`
	Identity       AlpacaIdentity        `json:"identity"`
	Disclosures    AlpacaDisclosures     `json:"disclosures"`
	Agreements     []AlpacaAgreement     `json:"agreements"`
	Documents      []AlpacaDocument      `json:"documents,omitempty"`
	TrustedContact *AlpacaTrustedContact `json:"trusted_contact,omitempty"`
}

type AlpacaContact struct {
	EmailAddress  string   `json:"email_address"`
	PhoneNumber   string   `json:"phone_number"`
	StreetAddress []string `json:"street_address"`
	City          string   `json:"city"`
	State         string   `json:"state,omitempty"`
	PostalCode    string   `json:"postal_code"`
	Country       string   `json:"country,omitempty"`
}

type AlpacaIdentity struct {
	GivenName             string `json:"given_name"`
	MiddleName            string `json:"middle_name,omitempty"`
	FamilyName            string `json:"family_name"`
	DateOfBirth           string `json:"date_of_birth"`
	TaxID                 string `json:"tax_id,omitempty"`
	TaxIDType             string `json:"tax_id_type,omitempty"`
	CountryOfTaxResidence string `json:"country_of_tax_residence,omitempty"`
}

type AlpacaDisclosures struct {
	DayTradingAsMargin          bool             `json:"day_trading_as_margin,omitempty"`
	IsControlPerson             bool             `json:"is_control_person,omitempty"`
	FinancialStatus             *FinancialStatus `json:"financial_status,omitempty"`
	AnnualIncome                string           `json:"annual_income,omitempty"`
	Networth                    *NetWorth        `json:"net_worth,omitempty"`
	VoluntaryDayTrading         bool             `json:"voluntary_day_trading,omitempty"`
	UnderstandsSpeculation      bool             `json:"understands_speculation,omitempty"`
	UnderstandsRisk             bool             `json:"understands_risk,omitempty"`
	IntentToReserve             *IntentToReserve `json:"intent_to_reserve,omitempty"`
	Employment                  *Employment      `json:"employment,omitempty"`
	MailingAddress              *MailingAddress  `json:"mailing_address,omitempty"`
	MarginPercentage            string           `json:"margin_percentage,omitempty"`
	IsAffiliatedExchangeOrFINRA bool             `json:"is_affiliated_exchange_or_finra,omitempty"`
	IsPoliticallyExposed        bool             `json:"is_politically_exposed,omitempty"`
	ImmediateFamilyExposed      bool             `json:"immediate_family_exposed,omitempty"`
}

type FinancialStatus string
type NetWorth string
type IntentToReserve string
type Employment struct {
	Status           string `json:"status"`
	EmployerName     string `json:"employer_name,omitempty"`
	EmployerAddr     string `json:"employer_address,omitempty"`
	EmploymentStatus string `json:"employment_status,omitempty"`
}
type MailingAddress struct {
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
}

type AlpacaAgreement struct {
	AgreementType string `json:"agreement_type"` // customer, margin, account
	SigningDate   string `json:"signing_date,omitempty"`
	Content       string `json:"content,omitempty"`
	Agreement     string `json:"agreement,omitempty"`
	SignedAt      string `json:"signed_at,omitempty"`
	IPAddress     string `json:"ip_address,omitempty"`
}

type AlpacaDocument struct {
	DocumentType    string `json:"document_type"` // IDENTITY_VERIFICATION, ADDRESS_VERIFICATION
	DocumentSubType string `json:"document_sub_type,omitempty"`
	Content         string `json:"content,omitempty"`
}

type AlpacaTrustedContact struct {
	GivenName   string         `json:"given_name,omitempty"`
	MiddleName  string         `json:"middle_name,omitempty"`
	FamilyName  string         `json:"family_name,omitempty"`
	Email       string         `json:"email,omitempty"`
	PhoneNumber string         `json:"phone_number,omitempty"`
	Address     *AlpacaContact `json:"address,omitempty"`
}

// AlpacaAccountResponse is the account response shape (stub).
type AlpacaAccountResponse struct {
	ID                   string              `json:"id"`
	AccountNumber        string              `json:"account_number"`
	Status               AlpacaAccountStatus `json:"status"`
	CryptoStatus         string              `json:"crypto_status,omitempty"`
	Currency             string              `json:"currency"`
	Equity               decimal.Decimal     `json:"equity"`
	BuyingPower          decimal.Decimal     `json:"buying_power"`
	Cash                 decimal.Decimal     `json:"cash"`
	PortfolioValue       decimal.Decimal     `json:"portfolio_value"`
	PatternDayTrader     bool                `json:"pattern_day_trader"`
	TradeSuspendedByUser bool                `json:"trade_suspended_by_user"`
	TradingBlocked       bool                `json:"trading_blocked"`
	TransfersBlocked     bool                `json:"transfers_blocked"`
	AccountBlocked       bool                `json:"account_blocked"`
	CreatedAt            time.Time           `json:"created_at"`
	Contact              AlpacaContact       `json:"contact,omitempty"`
	Identity             AlpacaIdentity      `json:"identity,omitempty"`
	Disclosures          AlpacaDisclosures   `json:"disclosures,omitempty"`
}

// AlpacaOrderSide is the side of an order.
type AlpacaOrderSide string

const (
	AlpacaOrderSideBuy  AlpacaOrderSide = "buy"
	AlpacaOrderSideSell AlpacaOrderSide = "sell"
)

// AlpacaOrderType is the type of order.
type AlpacaOrderType string

const (
	AlpacaOrderTypeMarket       AlpacaOrderType = "market"
	AlpacaOrderTypeLimit        AlpacaOrderType = "limit"
	AlpacaOrderTypeStop         AlpacaOrderType = "stop"
	AlpacaOrderTypeStopLimit    AlpacaOrderType = "stop_limit"
	AlpacaOrderTypeTrailingStop AlpacaOrderType = "trailing_stop"
)

// AlpacaTimeInForce is how long an order stays active.
type AlpacaTimeInForce string

const (
	AlpacaTimeInForceDay AlpacaTimeInForce = "day"
	AlpacaTimeInForceGTC AlpacaTimeInForce = "gtc"
	AlpacaTimeInForceOPG AlpacaTimeInForce = "opg"
	AlpacaTimeInForceCLS AlpacaTimeInForce = "cls"
	AlpacaTimeInForceIOC AlpacaTimeInForce = "ioc"
	AlpacaTimeInForceFOK AlpacaTimeInForce = "fok"
)

// AlpacaOrderStatus is the status of an order.
type AlpacaOrderStatus string

const (
	AlpacaOrderStatusNew                AlpacaOrderStatus = "new"
	AlpacaOrderStatusPartiallyFilled    AlpacaOrderStatus = "partially_filled"
	AlpacaOrderStatusFilled             AlpacaOrderStatus = "filled"
	AlpacaOrderStatusDoneForDay         AlpacaOrderStatus = "done_for_day"
	AlpacaOrderStatusCanceled           AlpacaOrderStatus = "canceled"
	AlpacaOrderStatusExpired            AlpacaOrderStatus = "expired"
	AlpacaOrderStatusReplaced           AlpacaOrderStatus = "replaced"
	AlpacaOrderStatusPendingCancel      AlpacaOrderStatus = "pending_cancel"
	AlpacaOrderStatusPendingReplace     AlpacaOrderStatus = "pending_replace"
	AlpacaOrderStatusAccepted           AlpacaOrderStatus = "accepted"
	AlpacaOrderStatusPendingNew         AlpacaOrderStatus = "pending_new"
	AlpacaOrderStatusAcceptedForBidding AlpacaOrderStatus = "accepted_for_bidding"
	AlpacaOrderStatusStopped            AlpacaOrderStatus = "stopped"
	AlpacaOrderStatusRejected           AlpacaOrderStatus = "rejected"
	AlpacaOrderStatusSuspended          AlpacaOrderStatus = "suspended"
	AlpacaOrderStatusCalculated         AlpacaOrderStatus = "calculated"
)

// AlpacaCreateOrderRequest is a deprecated order request shape (stub).
type AlpacaCreateOrderRequest struct {
	Symbol         string            `json:"symbol"`
	Qty            *decimal.Decimal  `json:"qty,omitempty"`
	Notional       *decimal.Decimal  `json:"notional,omitempty"`
	Side           AlpacaOrderSide   `json:"side"`
	Type           AlpacaOrderType   `json:"type"`
	TimeInForce    AlpacaTimeInForce `json:"time_in_force"`
	LimitPrice     *decimal.Decimal  `json:"limit_price,omitempty"`
	StopPrice      *decimal.Decimal  `json:"stop_price,omitempty"`
	TrailPrice     *decimal.Decimal  `json:"trail_price,omitempty"`
	TrailPercent   *decimal.Decimal  `json:"trail_percent,omitempty"`
	ExtendedHours  bool              `json:"extended_hours,omitempty"`
	ClientOrderID  string            `json:"client_order_id,omitempty"`
	OrderClass     string            `json:"order_class,omitempty"`
	Commission     *decimal.Decimal  `json:"commission,omitempty"`
	CommissionType string            `json:"commission_type,omitempty"`
}

// AlpacaOrderResponse is the order response shape (stub).
type AlpacaOrderResponse struct {
	ID             string                `json:"id"`
	ClientOrderID  string                `json:"client_order_id"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
	SubmittedAt    time.Time             `json:"submitted_at"`
	FilledAt       *time.Time            `json:"filled_at"`
	ExpiredAt      *time.Time            `json:"expired_at"`
	CanceledAt     *time.Time            `json:"canceled_at"`
	FailedAt       *time.Time            `json:"failed_at"`
	ReplacedAt     *time.Time            `json:"replaced_at"`
	AssetID        string                `json:"asset_id"`
	Symbol         string                `json:"symbol"`
	AssetClass     string                `json:"asset_class"`
	Qty            decimal.Decimal       `json:"qty"`
	Notional       *decimal.Decimal      `json:"notional"`
	FilledQty      decimal.Decimal       `json:"filled_qty"`
	FilledAvgPrice *decimal.Decimal      `json:"filled_avg_price"`
	OrderClass     string                `json:"order_class"`
	OrderType      AlpacaOrderType       `json:"order_type"`
	Type           AlpacaOrderType       `json:"type"`
	Side           AlpacaOrderSide       `json:"side"`
	TimeInForce    AlpacaTimeInForce     `json:"time_in_force"`
	LimitPrice     *decimal.Decimal      `json:"limit_price"`
	StopPrice      *decimal.Decimal      `json:"stop_price"`
	Status         AlpacaOrderStatus     `json:"status"`
	ExtendedHours  bool                  `json:"extended_hours"`
	Legs           []AlpacaOrderResponse `json:"legs,omitempty"`
	TrailPrice     *decimal.Decimal      `json:"trail_price,omitempty"`
	TrailPercent   *decimal.Decimal      `json:"trail_percent,omitempty"`
	Commission     decimal.Decimal       `json:"commission"`
	CommissionType string                `json:"commission_type,omitempty"`
}

// AlpacaAssetClass is the class of an asset.
type AlpacaAssetClass string

const (
	AlpacaAssetClassUSEquity AlpacaAssetClass = "us_equity"
	AlpacaAssetClassCrypto   AlpacaAssetClass = "crypto"
)

// AlpacaAssetStatus is the status of an asset.
type AlpacaAssetStatus string

const (
	AlpacaAssetStatusActive   AlpacaAssetStatus = "active"
	AlpacaAssetStatusInactive AlpacaAssetStatus = "inactive"
)

// AlpacaAssetResponse is the asset response shape (stub).
type AlpacaAssetResponse struct {
	ID                string            `json:"id"`
	Class             AlpacaAssetClass  `json:"class"`
	Exchange          string            `json:"exchange"`
	Symbol            string            `json:"symbol"`
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	LogoURL           *string           `json:"logo_url,omitempty"`
	Status            AlpacaAssetStatus `json:"status"`
	Tradable          bool              `json:"tradable"`
	Marginable        bool              `json:"marginable"`
	Shortable         bool              `json:"shortable"`
	EasyToBorrow      bool              `json:"easy_to_borrow"`
	Fractionable      bool              `json:"fractionable"`
	MinOrderSize      *decimal.Decimal  `json:"min_order_size,omitempty"`
	MinTradeIncrement *decimal.Decimal  `json:"min_trade_increment,omitempty"`
	PriceIncrement    *decimal.Decimal  `json:"price_increment,omitempty"`
}

// AlpacaPositionResponse is the position response shape (stub).
type AlpacaPositionResponse struct {
	AssetID                string          `json:"asset_id"`
	Symbol                 string          `json:"symbol"`
	Exchange               string          `json:"exchange"`
	AssetClass             string          `json:"asset_class"`
	AvgEntryPrice          decimal.Decimal `json:"avg_entry_price"`
	Qty                    decimal.Decimal `json:"qty"`
	QtyAvailable           decimal.Decimal `json:"qty_available"`
	Side                   string          `json:"side"`
	MarketValue            decimal.Decimal `json:"market_value"`
	CostBasis              decimal.Decimal `json:"cost_basis"`
	UnrealizedPL           decimal.Decimal `json:"unrealized_pl"`
	UnrealizedPLPC         decimal.Decimal `json:"unrealized_plpc"`
	UnrealizedIntradayPL   decimal.Decimal `json:"unrealized_intraday_pl"`
	UnrealizedIntradayPLPC decimal.Decimal `json:"unrealized_intraday_plpc"`
	CurrentPrice           decimal.Decimal `json:"current_price"`
	LastdayPrice           decimal.Decimal `json:"lastday_price"`
	ChangeToday            decimal.Decimal `json:"change_today"`
}

// AlpacaAccount is a deprecated account lookup model (stub).
type AlpacaAccount struct {
	AlpacaAccountID     string              `json:"alpaca_account_id"`
	BrokerAccountID     string              `json:"broker_account_id"`
	AlpacaAccountStatus AlpacaAccountStatus `json:"alpaca_account_status"`
	Status              AlpacaAccountStatus `json:"status"`
	AccountID           string              `json:"account_id"`
	UserID              uuid.UUID           `json:"user_id"`
	Cash                decimal.Decimal     `json:"cash"`
	PortfolioValue      decimal.Decimal     `json:"portfolio_value"`
	CreatedAt           time.Time           `json:"created_at"`
}

// AlpacaJournalRequest is a deprecated journal request shape (stub).
type AlpacaJournalRequest struct {
	FromAccount                     string          `json:"from_account"`
	ToAccount                       string          `json:"to_account"`
	EntryType                       string          `json:"entry_type"`
	Amount                          decimal.Decimal `json:"amount"`
	Description                     string          `json:"description,omitempty"`
	ClientTransferID                string          `json:"client_transfer_id,omitempty"`
	TransmitterName                 string          `json:"transmitter_name,omitempty"`
	TransmitterAccountNumber        string          `json:"transmitter_account_number,omitempty"`
	TransmitterAddress              string          `json:"transmitter_address,omitempty"`
	TransmitterFinancialInstitution string          `json:"transmitter_financial_institution,omitempty"`
}

// AlpacaJournalResponse is a deprecated journal response shape (stub).
type AlpacaJournalResponse struct {
	ID          string          `json:"id"`
	FromAccount string          `json:"from_account"`
	ToAccount   string          `json:"to_account"`
	EntryType   string          `json:"entry_type"`
	Amount      decimal.Decimal `json:"amount"`
	Status      string          `json:"status"`
	SettleDate  string          `json:"settle_date,omitempty"`
	SystemDate  string          `json:"system_date,omitempty"`
	NetAmount   decimal.Decimal `json:"net_amount"`
	Description string          `json:"description,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AlpacaErrorResponse is a deprecated Alpaca error shape (stub).
type AlpacaErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *AlpacaErrorResponse) Error() string { return e.Message }

// AlpacaInstantFundingRequest is a deprecated instant-funding request shape (stub).
type AlpacaInstantFundingRequest struct {
	AccountNo       string          `json:"account_no"`
	SourceAccountNo string          `json:"source_account_no"`
	Amount          decimal.Decimal `json:"amount"`
}

// AlpacaInstantFundingResponse is a deprecated instant-funding response shape (stub).
type AlpacaInstantFundingResponse struct {
	ID               string           `json:"id"`
	AccountNo        string           `json:"account_no"`
	SourceAccountNo  string           `json:"source_account_no"`
	Amount           decimal.Decimal  `json:"amount"`
	RemainingPayable decimal.Decimal  `json:"remaining_payable"`
	TotalInterest    decimal.Decimal  `json:"total_interest"`
	Status           string           `json:"status"`
	SystemDate       string           `json:"system_date"`
	Deadline         string           `json:"deadline"`
	CreatedAt        time.Time        `json:"created_at"`
	Fees             []AlpacaFee      `json:"fees,omitempty"`
	Interests        []AlpacaInterest `json:"interests,omitempty"`
}

// AlpacaFee represents a fee associated with instant funding (stub).
type AlpacaFee struct {
	Amount      decimal.Decimal `json:"amount"`
	Description string          `json:"description"`
}

// AlpacaInterest represents interest charges for late settlement (stub).
type AlpacaInterest struct {
	Amount      decimal.Decimal `json:"amount"`
	Description string          `json:"description"`
}

// AlpacaInstantFundingLimitsResponse is a deprecated funding-limits shape (stub).
type AlpacaInstantFundingLimitsResponse struct {
	AmountAvailable decimal.Decimal `json:"amount_available"`
	AmountInUse     decimal.Decimal `json:"amount_in_use"`
	AmountLimit     decimal.Decimal `json:"amount_limit"`
}

// AlpacaActivityResponse is a deprecated account-activity shape (stub).
type AlpacaActivityResponse struct {
	ID           string          `json:"id"`
	AccountID    string          `json:"account_id"`
	ActivityType string          `json:"activity_type"`
	Date         string          `json:"date"`
	NetAmount    decimal.Decimal `json:"net_amount"`
	Symbol       string          `json:"symbol,omitempty"`
	Qty          decimal.Decimal `json:"qty,omitempty"`
	Price        decimal.Decimal `json:"price,omitempty"`
	Side         string          `json:"side,omitempty"`
	Description  string          `json:"description,omitempty"`
}

// AlpacaPortfolioHistoryResponse is a deprecated portfolio-history shape (stub).
type AlpacaPortfolioHistoryResponse struct {
	Timestamp    []int64           `json:"timestamp"`
	Equity       []decimal.Decimal `json:"equity"`
	ProfitLoss   []decimal.Decimal `json:"profit_loss"`
	ProfitLossPC []decimal.Decimal `json:"profit_loss_pct"`
	BaseValue    decimal.Decimal   `json:"base_value"`
	Timeframe    string            `json:"timeframe"`
}

// AlpacaNewsImage is a deprecated market-data shape (stub).
type AlpacaNewsImage struct {
	Size string `json:"size"`
	URL  string `json:"url"`
}

// AlpacaNewsArticle is a deprecated market-data shape (stub).
type AlpacaNewsArticle struct {
	ID        int               `json:"id"`
	Author    string            `json:"author"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Headline  string            `json:"headline"`
	Summary   string            `json:"summary"`
	Content   string            `json:"content"`
	Images    []AlpacaNewsImage `json:"images,omitempty"`
	Symbols   []string          `json:"symbols"`
	Source    string            `json:"source"`
	URL       string            `json:"url"`
}

// AlpacaNewsRequest is a deprecated market-data shape (stub).
type AlpacaNewsRequest struct {
	Symbols            []string   `json:"symbols,omitempty"`
	Start              *time.Time `json:"start,omitempty"`
	End                *time.Time `json:"end,omitempty"`
	Limit              int        `json:"limit,omitempty"`
	Sort               string     `json:"sort,omitempty"`
	IncludeContent     bool       `json:"include_content,omitempty"`
	ExcludeContentless bool       `json:"exclude_contentless,omitempty"`
	PageToken          string     `json:"page_token,omitempty"`
}

// AlpacaNewsResponse is a deprecated market-data shape (stub).
type AlpacaNewsResponse struct {
	News          []AlpacaNewsArticle `json:"news"`
	NextPageToken string              `json:"next_page_token,omitempty"`
}

// InvestmentPosition represents a single holding in a portfolio.
// Used by portfolio analytics, rebalancing, and investment-stash handlers.
type InvestmentPosition struct {
	ID                   uuid.UUID       `json:"id"`
	UserID               uuid.UUID       `json:"user_id"`
	Symbol               string          `json:"symbol"`
	Name                 string          `json:"name"`
	QTY                  decimal.Decimal `json:"qty"`
	AvgEntryPrice        decimal.Decimal `json:"avg_entry_price"`
	CurrentPrice         decimal.Decimal `json:"current_price"`
	MarketValue          decimal.Decimal `json:"market_value"`
	CostBasis            decimal.Decimal `json:"cost_basis"`
	UnrealizedPL         decimal.Decimal `json:"unrealized_pl"`
	UnrealizedPLPC       decimal.Decimal `json:"unrealized_plpc"`
	UnrealizedIntradayPL decimal.Decimal `json:"unrealized_intraday_pl"`
	LastdayPrice         decimal.Decimal `json:"lastday_price"`
	ChangeToday          decimal.Decimal `json:"change_today"`
}

// InvestmentOrder represents an executed or pending investment order.
type InvestmentOrder struct {
	ID             uuid.UUID         `json:"id"`
	UserID         uuid.UUID         `json:"user_id"`
	BasketID       *uuid.UUID        `json:"basket_id,omitempty"`
	Symbol         string            `json:"symbol"`
	Side           AlpacaOrderSide   `json:"side"`
	Type           AlpacaOrderType   `json:"type"`
	ClientOrderID  string            `json:"client_order_id"`
	Status         AlpacaOrderStatus `json:"status"`
	Qty            *decimal.Decimal  `json:"qty,omitempty"`
	Notional       *decimal.Decimal  `json:"notional,omitempty"`
	LimitPrice     *decimal.Decimal  `json:"limit_price,omitempty"`
	FilledQty      decimal.Decimal   `json:"filled_qty"`
	FilledAvgPrice *decimal.Decimal  `json:"filled_avg_price,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	SubmittedAt    *time.Time        `json:"submitted_at,omitempty"`
	FilledAt       *time.Time        `json:"filled_at,omitempty"`
	AlpacaOrderID  *string           `json:"alpaca_order_id,omitempty"`
}
