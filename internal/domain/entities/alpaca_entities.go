package entities

import (
	"time"

	"github.com/shopspring/decimal"
)

// Alpaca Account Management Entities

// AlpacaAccountType represents the type of brokerage account
type AlpacaAccountType string

const (
	AlpacaAccountTypeTradingCash   AlpacaAccountType = "trading_cash"
	AlpacaAccountTypeTradingMargin AlpacaAccountType = "trading_margin"
)

// AlpacaAccountStatus represents the status of an account
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

// AlpacaCreateAccountRequest represents a request to create a new brokerage account
type AlpacaCreateAccountRequest struct {
	Contact        AlpacaContact         `json:"contact"`
	Identity       AlpacaIdentity        `json:"identity"`
	Disclosures    AlpacaDisclosures     `json:"disclosures"`
	Agreements     []AlpacaAgreement     `json:"agreements"`
	Documents      []AlpacaDocument      `json:"documents,omitempty"`
	TrustedContact *AlpacaTrustedContact `json:"trusted_contact,omitempty"`
}

// AlpacaContact contains contact information
type AlpacaContact struct {
	EmailAddress  string   `json:"email_address"`
	PhoneNumber   string   `json:"phone_number"`
	StreetAddress []string `json:"street_address"`
	City          string   `json:"city"`
	State         string   `json:"state,omitempty"` // For US addresses
	PostalCode    string   `json:"postal_code"`
	Country       string   `json:"country,omitempty"`
}

// AlpacaIdentity contains identity information
type AlpacaIdentity struct {
	GivenName             string   `json:"given_name"`
	MiddleName            string   `json:"middle_name,omitempty"`
	FamilyName            string   `json:"family_name"`
	DateOfBirth           string   `json:"date_of_birth"` // YYYY-MM-DD format
	TaxID                 string   `json:"tax_id,omitempty"`
	TaxIDType             string   `json:"tax_id_type,omitempty"` // USA_SSN, etc.
	CountryOfCitizenship  string   `json:"country_of_citizenship,omitempty"`
	CountryOfBirth        string   `json:"country_of_birth,omitempty"`
	CountryOfTaxResidence string   `json:"country_of_tax_residence,omitempty"`
	FundingSource         []string `json:"funding_source,omitempty"`
}

// AlpacaDisclosures contains regulatory disclosures
type AlpacaDisclosures struct {
	IsControlPerson             bool   `json:"is_control_person"`
	IsAffiliatedExchangeOrFINRA bool   `json:"is_affiliated_exchange_or_finra"`
	IsPoliticallyExposed        bool   `json:"is_politically_exposed"`
	ImmediateFamilyExposed      bool   `json:"immediate_family_exposed"`
	EmploymentStatus            string `json:"employment_status,omitempty"` // employed, unemployed, student, retired
	EmployerName                string `json:"employer_name,omitempty"`
	EmployerAddress             string `json:"employer_address,omitempty"`
	EmploymentPosition          string `json:"employment_position,omitempty"`
}

// AlpacaAgreement represents a signed agreement
type AlpacaAgreement struct {
	Agreement string `json:"agreement"` // account, customer, margin, etc.
	SignedAt  string `json:"signed_at"` // RFC3339 format
	IPAddress string `json:"ip_address"`
	Revision  string `json:"revision,omitempty"`
}

// AlpacaDocument represents an uploaded document
type AlpacaDocument struct {
	DocumentType    string `json:"document_type"` // identity_verification, etc.
	DocumentSubType string `json:"document_sub_type,omitempty"`
	Content         string `json:"content"` // Base64 encoded
	MIMEType        string `json:"mime_type"`
}

// AlpacaTrustedContact contains trusted contact information
type AlpacaTrustedContact struct {
	GivenName    string `json:"given_name"`
	FamilyName   string `json:"family_name"`
	EmailAddress string `json:"email_address,omitempty"`
}

// AlpacaAccountResponse represents the response when creating/getting an account
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

// Alpaca Trading Entities

// AlpacaOrderSide represents the side of an order
type AlpacaOrderSide string

const (
	AlpacaOrderSideBuy  AlpacaOrderSide = "buy"
	AlpacaOrderSideSell AlpacaOrderSide = "sell"
)

// AlpacaOrderType represents the type of order
type AlpacaOrderType string

const (
	AlpacaOrderTypeMarket       AlpacaOrderType = "market"
	AlpacaOrderTypeLimit        AlpacaOrderType = "limit"
	AlpacaOrderTypeStop         AlpacaOrderType = "stop"
	AlpacaOrderTypeStopLimit    AlpacaOrderType = "stop_limit"
	AlpacaOrderTypeTrailingStop AlpacaOrderType = "trailing_stop"
)

// AlpacaTimeInForce represents how long an order stays active
type AlpacaTimeInForce string

const (
	AlpacaTimeInForceDay AlpacaTimeInForce = "day"
	AlpacaTimeInForceGTC AlpacaTimeInForce = "gtc" // Good til canceled
	AlpacaTimeInForceOPG AlpacaTimeInForce = "opg" // Market on open
	AlpacaTimeInForceCLS AlpacaTimeInForce = "cls" // Market on close
	AlpacaTimeInForceIOC AlpacaTimeInForce = "ioc" // Immediate or cancel
	AlpacaTimeInForceFOK AlpacaTimeInForce = "fok" // Fill or kill
)

// AlpacaOrderStatus represents the status of an order
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

// AlpacaCreateOrderRequest represents a request to create an order
type AlpacaCreateOrderRequest struct {
	Symbol         string            `json:"symbol"`
	Qty            *decimal.Decimal  `json:"qty,omitempty"`      // Quantity (fractional shares supported)
	Notional       *decimal.Decimal  `json:"notional,omitempty"` // Dollar amount (for market orders)
	Side           AlpacaOrderSide   `json:"side"`
	Type           AlpacaOrderType   `json:"type"`
	TimeInForce    AlpacaTimeInForce `json:"time_in_force"`
	LimitPrice     *decimal.Decimal  `json:"limit_price,omitempty"`
	StopPrice      *decimal.Decimal  `json:"stop_price,omitempty"`
	TrailPrice     *decimal.Decimal  `json:"trail_price,omitempty"`
	TrailPercent   *decimal.Decimal  `json:"trail_percent,omitempty"`
	ExtendedHours  bool              `json:"extended_hours,omitempty"`
	ClientOrderID  string            `json:"client_order_id,omitempty"`
	OrderClass     string            `json:"order_class,omitempty"` // simple, bracket, oco, oto
	Commission     *decimal.Decimal  `json:"commission,omitempty"`
	CommissionType string            `json:"commission_type,omitempty"` // notional, qty, bps
}

// AlpacaOrderResponse represents the response when creating/getting an order
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

// Alpaca Asset Entities

// AlpacaAssetClass represents the class of an asset
type AlpacaAssetClass string

const (
	AlpacaAssetClassUSEquity AlpacaAssetClass = "us_equity"
	AlpacaAssetClassCrypto   AlpacaAssetClass = "crypto"
)

// AlpacaAssetStatus represents the status of an asset
type AlpacaAssetStatus string

const (
	AlpacaAssetStatusActive   AlpacaAssetStatus = "active"
	AlpacaAssetStatusInactive AlpacaAssetStatus = "inactive"
)

// AlpacaAssetResponse represents an asset (stock, ETF, crypto)
type AlpacaAssetResponse struct {
	ID                string            `json:"id"`
	Class             AlpacaAssetClass  `json:"class"`
	Exchange          string            `json:"exchange"`
	Symbol            string            `json:"symbol"`
	Name              string            `json:"name"`
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

// Alpaca Position Entities

// AlpacaPositionResponse represents a position in a portfolio
type AlpacaPositionResponse struct {
	AssetID                string          `json:"asset_id"`
	Symbol                 string          `json:"symbol"`
	Exchange               string          `json:"exchange"`
	AssetClass             string          `json:"asset_class"`
	AvgEntryPrice          decimal.Decimal `json:"avg_entry_price"`
	Qty                    decimal.Decimal `json:"qty"`
	QtyAvailable           decimal.Decimal `json:"qty_available"`
	Side                   string          `json:"side"` // long or short
	MarketValue            decimal.Decimal `json:"market_value"`
	CostBasis              decimal.Decimal `json:"cost_basis"`
	UnrealizedPL           decimal.Decimal `json:"unrealized_pl"`
	UnrealizedPLPC         decimal.Decimal `json:"unrealized_plpc"` // percentage
	UnrealizedIntradayPL   decimal.Decimal `json:"unrealized_intraday_pl"`
	UnrealizedIntradayPLPC decimal.Decimal `json:"unrealized_intraday_plpc"`
	CurrentPrice           decimal.Decimal `json:"current_price"`
	LastdayPrice           decimal.Decimal `json:"lastday_price"`
	ChangeToday            decimal.Decimal `json:"change_today"`
}

// Alpaca Market Data Entities

// AlpacaNewsArticle represents a news article from market data API
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

// AlpacaNewsImage represents an image in a news article
type AlpacaNewsImage struct {
	Size string `json:"size"` // thumb, small, large
	URL  string `json:"url"`
}

// AlpacaNewsRequest represents a request for news articles
type AlpacaNewsRequest struct {
	Symbols            []string   `json:"symbols,omitempty"`             // Filter by symbols
	Start              *time.Time `json:"start,omitempty"`               // Start time (RFC3339)
	End                *time.Time `json:"end,omitempty"`                 // End time (RFC3339)
	Limit              int        `json:"limit,omitempty"`               // Max results (default 10, max 50)
	Sort               string     `json:"sort,omitempty"`                // ASC or DESC
	IncludeContent     bool       `json:"include_content,omitempty"`     // Include full content
	ExcludeContentless bool       `json:"exclude_contentless,omitempty"` // Exclude articles without content
	PageToken          string     `json:"page_token,omitempty"`          // Pagination token
}

// AlpacaNewsResponse represents the response for news articles
type AlpacaNewsResponse struct {
	News          []AlpacaNewsArticle `json:"news"`
	NextPageToken string              `json:"next_page_token,omitempty"`
}

// Alpaca Error Response

// Alpaca Funding Entities

// AlpacaInstantFundingRequest represents a request to create an instant funding transfer
type AlpacaInstantFundingRequest struct {
	AccountNo       string          `json:"account_no"`
	SourceAccountNo string          `json:"source_account_no"`
	Amount          decimal.Decimal `json:"amount"`
}

// AlpacaInstantFundingResponse represents the response for an instant funding transfer
type AlpacaInstantFundingResponse struct {
	ID               string           `json:"id"`
	AccountNo        string           `json:"account_no"`
	SourceAccountNo  string           `json:"source_account_no"`
	Amount           decimal.Decimal  `json:"amount"`
	RemainingPayable decimal.Decimal  `json:"remaining_payable"`
	TotalInterest    decimal.Decimal  `json:"total_interest"`
	Status           string           `json:"status"` // PENDING, EXECUTED, COMPLETED, CANCELED, FAILED
	SystemDate       string           `json:"system_date"`
	Deadline         string           `json:"deadline"`
	CreatedAt        time.Time        `json:"created_at"`
	Fees             []AlpacaFee      `json:"fees,omitempty"`
	Interests        []AlpacaInterest `json:"interests,omitempty"`
}

// AlpacaFee represents a fee associated with instant funding
type AlpacaFee struct {
	Amount      decimal.Decimal `json:"amount"`
	Description string          `json:"description"`
}

// AlpacaInterest represents interest charges for late settlement
type AlpacaInterest struct {
	Amount      decimal.Decimal `json:"amount"`
	Description string          `json:"description"`
}

// AlpacaInstantFundingLimitsResponse represents instant funding limits
type AlpacaInstantFundingLimitsResponse struct {
	AmountAvailable decimal.Decimal `json:"amount_available"`
	AmountInUse     decimal.Decimal `json:"amount_in_use"`
	AmountLimit     decimal.Decimal `json:"amount_limit"`
}

// AlpacaJournalRequest represents a request to create a journal entry
type AlpacaJournalRequest struct {
	FromAccount                     string          `json:"from_account"`
	ToAccount                       string          `json:"to_account"`
	EntryType                       string          `json:"entry_type"` // JNLC (cash), JNLS (securities)
	Amount                          decimal.Decimal `json:"amount"`
	Description                     string          `json:"description,omitempty"`
	TransmitterName                 string          `json:"transmitter_name,omitempty"`
	TransmitterAccountNumber        string          `json:"transmitter_account_number,omitempty"`
	TransmitterAddress              string          `json:"transmitter_address,omitempty"`
	TransmitterFinancialInstitution string          `json:"transmitter_financial_institution,omitempty"`
}

// AlpacaJournalResponse represents the response for a journal entry
type AlpacaJournalResponse struct {
	ID          string          `json:"id"`
	FromAccount string          `json:"from_account"`
	ToAccount   string          `json:"to_account"`
	EntryType   string          `json:"entry_type"`
	Amount      decimal.Decimal `json:"amount"`
	Status      string          `json:"status"` // pending, executed, canceled
	SettleDate  string          `json:"settle_date,omitempty"`
	SystemDate  string          `json:"system_date,omitempty"`
	NetAmount   decimal.Decimal `json:"net_amount"`
	Description string          `json:"description,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AlpacaErrorResponse represents an error response from Alpaca API
type AlpacaErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *AlpacaErrorResponse) Error() string {
	return e.Message
}

// Additional Alpaca Entities

// AlpacaActivityResponse represents account activity (trades, dividends, etc.)
type AlpacaActivityResponse struct {
	ID            string          `json:"id"`
	AccountID     string          `json:"account_id"`
	ActivityType  string          `json:"activity_type"` // FILL, DIV, etc.
	Date          string          `json:"date"`
	NetAmount     decimal.Decimal `json:"net_amount"`
	Symbol        string          `json:"symbol,omitempty"`
	Qty           decimal.Decimal `json:"qty,omitempty"`
	Price         decimal.Decimal `json:"price,omitempty"`
	Side          string          `json:"side,omitempty"`
	Description   string          `json:"description,omitempty"`
}

// AlpacaPortfolioHistoryResponse represents portfolio performance over time
type AlpacaPortfolioHistoryResponse struct {
	Timestamp    []int64           `json:"timestamp"`
	Equity       []decimal.Decimal `json:"equity"`
	ProfitLoss   []decimal.Decimal `json:"profit_loss"`
	ProfitLossPC []decimal.Decimal `json:"profit_loss_pct"`
	BaseValue    decimal.Decimal   `json:"base_value"`
	Timeframe    string            `json:"timeframe"`
}
