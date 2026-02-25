package entities

import "time"

import "github.com/shopspring/decimal"

// MarketInstrumentType represents supported instrument families for explorer filtering.
type MarketInstrumentType string

const (
	MarketInstrumentTypeStock  MarketInstrumentType = "stock"
	MarketInstrumentTypeETF    MarketInstrumentType = "etf"
	MarketInstrumentTypeBond   MarketInstrumentType = "bond"
	MarketInstrumentTypeCrypto MarketInstrumentType = "crypto"
	MarketInstrumentTypeOption MarketInstrumentType = "option"
)

// MarketSession represents rough US market session buckets.
type MarketSession string

const (
	MarketSessionPre     MarketSession = "pre"
	MarketSessionRegular MarketSession = "regular"
	MarketSessionPost    MarketSession = "post"
	MarketSessionClosed  MarketSession = "closed"
)

// MarketExploreFilters holds all supported query filters for explorer endpoints.
type MarketExploreFilters struct {
	Query        string                 `json:"q"`
	Types        []MarketInstrumentType `json:"types"`
	Exchanges    []string               `json:"exchanges"`
	Categories   []string               `json:"categories"`
	Tradable     *bool                  `json:"tradable,omitempty"`
	Fractionable *bool                  `json:"fractionable,omitempty"`
	Marginable   *bool                  `json:"marginable,omitempty"`
	Shortable    *bool                  `json:"shortable,omitempty"`
	MinPrice     *decimal.Decimal       `json:"min_price,omitempty"`
	MaxPrice     *decimal.Decimal       `json:"max_price,omitempty"`
	MinChangePct *decimal.Decimal       `json:"min_change_pct,omitempty"`
	MaxChangePct *decimal.Decimal       `json:"max_change_pct,omitempty"`
	SortBy       string                 `json:"sort_by"`
	SortOrder    string                 `json:"sort_order"`
	Page         int                    `json:"page"`
	PageSize     int                    `json:"page_size"`
}

// MarketTradability exposes tradability flags used by UI chips.
type MarketTradability struct {
	Tradable     bool `json:"tradable"`
	Marginable   bool `json:"marginable"`
	Fractionable bool `json:"fractionable"`
	Shortable    bool `json:"shortable"`
	EasyToBorrow bool `json:"easy_to_borrow"`
}

// MarketInstrumentQuote is a UI-ready market quote snapshot.
type MarketInstrumentQuote struct {
	Price         decimal.Decimal `json:"price"`
	Bid           decimal.Decimal `json:"bid"`
	Ask           decimal.Decimal `json:"ask"`
	Change        decimal.Decimal `json:"change"`
	ChangePct     decimal.Decimal `json:"change_pct"`
	Open          decimal.Decimal `json:"open"`
	High          decimal.Decimal `json:"high"`
	Low           decimal.Decimal `json:"low"`
	PreviousClose decimal.Decimal `json:"previous_close"`
	Volume        int64           `json:"volume"`
	Timestamp     time.Time       `json:"timestamp"`
}

// MarketInstrumentCard is the core item in market explorer list responses.
type MarketInstrumentCard struct {
	Symbol         string                `json:"symbol"`
	Name           string                `json:"name"`
	Description    string                `json:"description"`
	InstrumentType MarketInstrumentType  `json:"instrument_type"`
	AssetClass     string                `json:"asset_class"`
	Exchange       string                `json:"exchange"`
	Categories     []string              `json:"categories"`
	Tags           []string              `json:"tags"`
	Tradability    MarketTradability     `json:"tradability"`
	Quote          MarketInstrumentQuote `json:"quote"`
	MarketSession  MarketSession         `json:"market_session"`
	LogoURL        *string               `json:"logo_url,omitempty"`
}

// MarketExplorePagination describes list pagination metadata.
type MarketExplorePagination struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
	HasNext    bool  `json:"has_next"`
	HasPrev    bool  `json:"has_prev"`
}

// MarketFacetValue is a faceted value/count pair for filter UIs.
type MarketFacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// MarketExploreFacets contains aggregated current-result filter options.
type MarketExploreFacets struct {
	Types      []MarketFacetValue `json:"types"`
	Exchanges  []MarketFacetValue `json:"exchanges"`
	Categories []MarketFacetValue `json:"categories"`
}

// MarketExploreResponse is the UI-focused explorer list response.
type MarketExploreResponse struct {
	Items          []MarketInstrumentCard  `json:"items"`
	Pagination     MarketExplorePagination `json:"pagination"`
	AppliedFilters MarketExploreFilters    `json:"applied_filters"`
	Facets         MarketExploreFacets     `json:"facets"`
	AsOf           time.Time               `json:"as_of"`
}

// MarketInstrumentDetailsResponse is used by /market/instruments/:symbol.
type MarketInstrumentDetailsResponse struct {
	Instrument MarketInstrumentCard `json:"instrument"`
	Bars       []*MarketBar         `json:"bars,omitempty"`
}

// MarketFilterMetadataResponse powers UI filter controls.
type MarketFilterMetadataResponse struct {
	SupportedTypes      []string             `json:"supported_types"`
	SupportedExchanges  []string             `json:"supported_exchanges"`
	SupportedCategories []string             `json:"supported_categories"`
	SupportedSortBy     []string             `json:"supported_sort_by"`
	SupportedSortOrder  []string             `json:"supported_sort_order"`
	Defaults            MarketExploreFilters `json:"defaults"`
}
