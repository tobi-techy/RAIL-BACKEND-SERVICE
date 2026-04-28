package market

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

const (
	assetUniverseTTL = 5 * time.Minute
	snapshotTTL      = 15 * time.Second
	logoCacheTTL     = 24 * time.Hour
)

type explorerAlpacaClient interface {
	ListAssets(ctx context.Context, query map[string]string) ([]entities.AlpacaAssetResponse, error)
	GetAsset(ctx context.Context, symbolOrID string) (*entities.AlpacaAssetResponse, error)
	GetStockSnapshots(ctx context.Context, symbols []string) (map[string]*entities.MarketQuote, error)
	GetStockSnapshot(ctx context.Context, symbol string) (*entities.MarketQuote, error)
	GetBars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]*entities.MarketBar, error)
}

// ExplorerService handles browsing and filtering market instruments.
type ExplorerService struct {
	alpacaClient explorerAlpacaClient
	logger       *zap.Logger
	logoResolver *logoResolver

	mu sync.RWMutex

	assetCache     []entities.AlpacaAssetResponse
	assetFetchedAt time.Time

	snapshotCache map[string]*cachedExplorerQuote
	taxonomy      marketTaxonomy
}

type cachedExplorerQuote struct {
	quote     *entities.MarketQuote
	fetchedAt time.Time
}

type logoResolver struct {
	provider string
	baseURL  string
	token    string
	size     int
	format   string
	theme    string

	mu    sync.RWMutex
	cache map[string]cachedLogoURL
}

type cachedLogoURL struct {
	url       *string
	fetchedAt time.Time
}

type marketTaxonomy struct {
	SymbolOverrides map[string]taxonomySymbolOverride `yaml:"symbol_overrides"`
	KeywordRules    []taxonomyKeywordRule             `yaml:"keyword_rules"`
}

type taxonomySymbolOverride struct {
	InstrumentType string   `yaml:"instrument_type"`
	Categories     []string `yaml:"categories"`
	Tags           []string `yaml:"tags"`
}

type taxonomyKeywordRule struct {
	Name           string   `yaml:"name"`
	Keywords       []string `yaml:"keywords"`
	InstrumentType string   `yaml:"instrument_type"`
	Categories     []string `yaml:"categories"`
	Tags           []string `yaml:"tags"`
}

type classifiedAsset struct {
	asset          entities.AlpacaAssetResponse
	instrumentType entities.MarketInstrumentType
	categories     []string
	tags           []string
}

func NewExplorerService(alpacaClient explorerAlpacaClient, logger *zap.Logger, taxonomyPath string) *ExplorerService {
	taxonomy, err := loadMarketTaxonomy(taxonomyPath)
	if err != nil && logger != nil {
		logger.Warn("Failed to load market taxonomy", zap.Error(err), zap.String("path", taxonomyPath))
	}

	return &ExplorerService{
		alpacaClient:  alpacaClient,
		logger:        logger,
		logoResolver:  newLogoResolverFromEnv(logger),
		snapshotCache: make(map[string]*cachedExplorerQuote),
		taxonomy:      taxonomy,
	}
}

func (s *ExplorerService) Explore(ctx context.Context, filters entities.MarketExploreFilters) (*entities.MarketExploreResponse, error) {
	if s == nil || s.alpacaClient == nil {
		return nil, fmt.Errorf("market explorer unavailable")
	}

	normalized := normalizeFilters(filters)

	assets, err := s.getAssetUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("get asset universe: %w", err)
	}

	classified := make([]classifiedAsset, 0, len(assets))
	for _, asset := range assets {
		ca := s.classifyAsset(asset)
		if !matchesMetadataFilters(ca, normalized) {
			continue
		}
		classified = append(classified, ca)
	}

	symbols := make([]string, 0, len(classified))
	for _, ca := range classified {
		symbols = append(symbols, strings.ToUpper(ca.asset.Symbol))
	}

	quotes, err := s.getSnapshots(ctx, symbols)
	if err != nil {
		return nil, fmt.Errorf("get snapshots: %w", err)
	}

	cards := make([]entities.MarketInstrumentCard, 0, len(classified))
	for _, ca := range classified {
		quote := quotes[strings.ToUpper(ca.asset.Symbol)]
		if !matchesNumericFilters(quote, normalized) {
			continue
		}
		var logoURL *string
		if s.logoResolver != nil {
			logoURL = s.logoResolver.Resolve(ca.asset.Symbol)
		}
		cards = append(cards, buildInstrumentCard(ca, quote, logoURL))
	}

	sortCards(cards, normalized.SortBy, normalized.SortOrder)
	facets := buildFacets(cards)

	totalItems := int64(len(cards))
	paginated := paginateCards(cards, normalized.Page, normalized.PageSize)
	paginated = s.enrichPaginatedQuotes(ctx, paginated)
	asOf := latestQuoteTime(paginated)

	return &entities.MarketExploreResponse{
		Items:          paginated,
		Pagination:     buildPagination(normalized.Page, normalized.PageSize, totalItems),
		AppliedFilters: normalized,
		Facets:         facets,
		AsOf:           asOf,
	}, nil
}

func (s *ExplorerService) GetInstrument(ctx context.Context, symbol string, includeBars bool, timeframe string, barsLimit int) (*entities.MarketInstrumentDetailsResponse, error) {
	if s == nil || s.alpacaClient == nil {
		return nil, fmt.Errorf("market explorer unavailable")
	}

	normalizedSymbol := strings.ToUpper(strings.TrimSpace(symbol))
	if normalizedSymbol == "" {
		return nil, fmt.Errorf("symbol required")
	}

	asset, err := s.alpacaClient.GetAsset(ctx, normalizedSymbol)
	if err != nil {
		return nil, fmt.Errorf("get asset: %w", err)
	}

	ca := s.classifyAsset(*asset)

	quotes, err := s.getSnapshots(ctx, []string{normalizedSymbol})
	if err != nil {
		return nil, fmt.Errorf("get snapshot: %w", err)
	}

	quote := quotes[normalizedSymbol]
	if quote == nil {
		fallback, snapErr := s.alpacaClient.GetStockSnapshot(ctx, normalizedSymbol)
		if snapErr == nil {
			quote = fallback
		}
	}

	resp := &entities.MarketInstrumentDetailsResponse{
		Instrument: buildInstrumentCard(ca, quote, s.logoResolver.Resolve(ca.asset.Symbol)),
	}

	if includeBars {
		if timeframe == "" {
			timeframe = "1Day"
		}
		if barsLimit <= 0 {
			barsLimit = 30
		}
		if barsLimit > 365 {
			barsLimit = 365
		}

		end := time.Now().UTC()
		start := end.AddDate(0, 0, -barsLimit*3)
		bars, barsErr := s.alpacaClient.GetBars(ctx, normalizedSymbol, timeframe, start, end)
		if barsErr != nil {
			return nil, fmt.Errorf("get bars: %w", barsErr)
		}
		if len(bars) > barsLimit {
			bars = bars[len(bars)-barsLimit:]
		}
		resp.Bars = bars
	}

	return resp, nil
}

func (s *ExplorerService) GetFilterMetadata(ctx context.Context) (*entities.MarketFilterMetadataResponse, error) {
	if s == nil || s.alpacaClient == nil {
		return nil, fmt.Errorf("market explorer unavailable")
	}

	assets, err := s.getAssetUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("get asset universe: %w", err)
	}

	exchangeSet := make(map[string]struct{})
	categorySet := make(map[string]struct{})
	for _, asset := range assets {
		ca := s.classifyAsset(asset)
		if ca.asset.Exchange != "" {
			exchangeSet[ca.asset.Exchange] = struct{}{}
		}
		for _, category := range ca.categories {
			categorySet[category] = struct{}{}
		}
	}

	exchanges := mapKeys(exchangeSet)
	categories := mapKeys(categorySet)

	defaults := defaultExploreFilters()
	return &entities.MarketFilterMetadataResponse{
		SupportedTypes: []string{
			string(entities.MarketInstrumentTypeStock),
			string(entities.MarketInstrumentTypeETF),
			string(entities.MarketInstrumentTypeBond),
			string(entities.MarketInstrumentTypeCrypto),
			string(entities.MarketInstrumentTypeOption),
		},
		SupportedExchanges:  exchanges,
		SupportedCategories: categories,
		SupportedSortBy:     []string{"symbol", "name", "price", "change_pct", "volume"},
		SupportedSortOrder:  []string{"asc", "desc"},
		Defaults:            defaults,
	}, nil
}

func (s *ExplorerService) getAssetUniverse(ctx context.Context) ([]entities.AlpacaAssetResponse, error) {
	s.mu.RLock()
	if len(s.assetCache) > 0 && time.Since(s.assetFetchedAt) < assetUniverseTTL {
		assets := make([]entities.AlpacaAssetResponse, len(s.assetCache))
		copy(assets, s.assetCache)
		s.mu.RUnlock()
		return assets, nil
	}
	s.mu.RUnlock()

	query := map[string]string{
		"status":      "active",
		"asset_class": "us_equity",
	}
	assets, err := s.alpacaClient.ListAssets(ctx, query)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.assetCache = assets
	s.assetFetchedAt = time.Now().UTC()
	s.mu.Unlock()

	result := make([]entities.AlpacaAssetResponse, len(assets))
	copy(result, assets)
	return result, nil
}

func (s *ExplorerService) getSnapshots(ctx context.Context, symbols []string) (map[string]*entities.MarketQuote, error) {
	result := make(map[string]*entities.MarketQuote)
	missing := make([]string, 0, len(symbols))
	now := time.Now().UTC()

	for _, symbol := range dedupeAndNormalize(symbols) {
		s.mu.RLock()
		cached, ok := s.snapshotCache[symbol]
		s.mu.RUnlock()
		if ok && now.Sub(cached.fetchedAt) < snapshotTTL {
			result[symbol] = cached.quote
			continue
		}
		missing = append(missing, symbol)
	}

	if len(missing) > 0 {
		for _, chunk := range chunkSymbols(missing, 200) {
			snapshots, err := s.alpacaClient.GetStockSnapshots(ctx, chunk)
			if err != nil {
				return nil, err
			}

			s.mu.Lock()
			for symbol, quote := range snapshots {
				normalizedSymbol := strings.ToUpper(symbol)
				if len(s.snapshotCache) > 10000 {
					s.snapshotCache = make(map[string]*cachedExplorerQuote)
				}
				s.snapshotCache[normalizedSymbol] = &cachedExplorerQuote{quote: quote, fetchedAt: now}
				result[normalizedSymbol] = quote
			}
			s.mu.Unlock()
		}
	}

	for _, symbol := range dedupeAndNormalize(symbols) {
		if _, ok := result[symbol]; ok {
			continue
		}
		s.mu.RLock()
		if cached, exists := s.snapshotCache[symbol]; exists {
			result[symbol] = cached.quote
		}
		s.mu.RUnlock()
	}

	return result, nil
}

func (s *ExplorerService) classifyAsset(asset entities.AlpacaAssetResponse) classifiedAsset {
	normalizedSymbol := strings.ToUpper(strings.TrimSpace(asset.Symbol))
	nameLower := strings.ToLower(asset.Name)

	instrumentType := detectInstrumentType(asset)
	categories := make([]string, 0)
	tags := make([]string, 0)

	if override, ok := s.taxonomy.SymbolOverrides[normalizedSymbol]; ok {
		if override.InstrumentType != "" {
			instrumentType = entities.MarketInstrumentType(strings.ToLower(override.InstrumentType))
		}
		categories = append(categories, override.Categories...)
		tags = append(tags, override.Tags...)
	}

	for _, rule := range s.taxonomy.KeywordRules {
		for _, keyword := range rule.Keywords {
			kw := strings.ToLower(strings.TrimSpace(keyword))
			if kw == "" {
				continue
			}
			if strings.Contains(nameLower, kw) || strings.Contains(strings.ToLower(normalizedSymbol), kw) {
				if rule.InstrumentType != "" {
					instrumentType = entities.MarketInstrumentType(strings.ToLower(rule.InstrumentType))
				}
				categories = append(categories, rule.Categories...)
				tags = append(tags, rule.Tags...)
				break
			}
		}
	}

	if len(categories) == 0 {
		switch instrumentType {
		case entities.MarketInstrumentTypeETF:
			categories = append(categories, "etf")
		default:
			categories = append(categories, "equities")
		}
	}

	return classifiedAsset{
		asset:          asset,
		instrumentType: instrumentType,
		categories:     dedupeStrings(categories),
		tags:           dedupeStrings(tags),
	}
}

func detectInstrumentType(asset entities.AlpacaAssetResponse) entities.MarketInstrumentType {
	name := strings.ToLower(asset.Name)
	if strings.Contains(name, " etf") || strings.Contains(name, "exchange traded fund") || strings.Contains(name, " fund") || strings.HasSuffix(name, " etf") {
		return entities.MarketInstrumentTypeETF
	}
	return entities.MarketInstrumentTypeStock
}

func matchesMetadataFilters(asset classifiedAsset, filters entities.MarketExploreFilters) bool {
	if filters.Query != "" {
		q := strings.ToLower(filters.Query)
		if !strings.Contains(strings.ToLower(asset.asset.Symbol), q) && !strings.Contains(strings.ToLower(asset.asset.Name), q) {
			return false
		}
	}

	if len(filters.Types) > 0 {
		match := false
		for _, allowed := range filters.Types {
			if asset.instrumentType == allowed {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	if len(filters.Exchanges) > 0 {
		match := false
		for _, ex := range filters.Exchanges {
			if strings.EqualFold(asset.asset.Exchange, ex) {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	if len(filters.Categories) > 0 {
		matched := false
		for _, candidate := range filters.Categories {
			for _, category := range asset.categories {
				if strings.EqualFold(candidate, category) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}

	if filters.Tradable != nil && asset.asset.Tradable != *filters.Tradable {
		return false
	}
	if filters.Fractionable != nil && asset.asset.Fractionable != *filters.Fractionable {
		return false
	}
	if filters.Marginable != nil && asset.asset.Marginable != *filters.Marginable {
		return false
	}
	if filters.Shortable != nil && asset.asset.Shortable != *filters.Shortable {
		return false
	}

	return true
}

func matchesNumericFilters(quote *entities.MarketQuote, filters entities.MarketExploreFilters) bool {
	if filters.MinPrice == nil && filters.MaxPrice == nil && filters.MinChangePct == nil && filters.MaxChangePct == nil {
		return true
	}
	if quote == nil {
		return false
	}

	if filters.MinPrice != nil && quote.Price.LessThan(*filters.MinPrice) {
		return false
	}
	if filters.MaxPrice != nil && quote.Price.GreaterThan(*filters.MaxPrice) {
		return false
	}
	if filters.MinChangePct != nil && quote.ChangePct.LessThan(*filters.MinChangePct) {
		return false
	}
	if filters.MaxChangePct != nil && quote.ChangePct.GreaterThan(*filters.MaxChangePct) {
		return false
	}
	return true
}

func buildInstrumentCard(asset classifiedAsset, quote *entities.MarketQuote, logoURL *string) entities.MarketInstrumentCard {
	uiQuote := entities.MarketInstrumentQuote{}
	if quote != nil {
		uiQuote = entities.MarketInstrumentQuote{
			Price:         quote.Price,
			Bid:           quote.Bid,
			Ask:           quote.Ask,
			Change:        quote.Change,
			ChangePct:     quote.ChangePct,
			Open:          quote.Open,
			High:          quote.High,
			Low:           quote.Low,
			PreviousClose: quote.PreviousClose,
			Volume:        quote.Volume,
			Timestamp:     quote.Timestamp,
		}
	}

	return entities.MarketInstrumentCard{
		Symbol:         asset.asset.Symbol,
		Name:           asset.asset.Name,
		Description:    buildInstrumentDescription(asset),
		InstrumentType: asset.instrumentType,
		AssetClass:     string(asset.asset.Class),
		Exchange:       asset.asset.Exchange,
		Categories:     asset.categories,
		Tags:           asset.tags,
		Tradability: entities.MarketTradability{
			Tradable:     asset.asset.Tradable,
			Marginable:   asset.asset.Marginable,
			Fractionable: asset.asset.Fractionable,
			Shortable:    asset.asset.Shortable,
			EasyToBorrow: asset.asset.EasyToBorrow,
		},
		Quote:         uiQuote,
		MarketSession: determineMarketSession(uiQuote.Timestamp),
		LogoURL:       logoURL,
	}
}

func buildInstrumentDescription(asset classifiedAsset) string {
	typeLabel := "stock"
	if asset.instrumentType == entities.MarketInstrumentTypeETF {
		typeLabel = "ETF"
	}

	description := fmt.Sprintf("%s is a %s listed on %s.", asset.asset.Name, typeLabel, asset.asset.Exchange)
	if len(asset.categories) > 0 {
		description += fmt.Sprintf(" Category: %s.", strings.Join(asset.categories, ", "))
	}
	if asset.asset.Fractionable {
		description += " Supports fractional investing."
	}
	if asset.asset.Marginable {
		description += " Margin eligible."
	}
	return strings.TrimSpace(description)
}

func newLogoResolverFromEnv(logger *zap.Logger) *logoResolver {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("MARKET_LOGO_PROVIDER")))
	if provider == "" {
		provider = "logo_dev"
	}

	baseURL := strings.TrimSpace(os.Getenv("MARKET_LOGO_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://img.logo.dev"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	token := strings.TrimSpace(os.Getenv("LOGO_DEV_PUBLISHABLE_KEY"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("LOGO_DEV_TOKEN"))
	}

	size := 128
	if rawSize := strings.TrimSpace(os.Getenv("LOGO_DEV_SIZE")); rawSize != "" {
		if parsed, err := strconv.Atoi(rawSize); err == nil && parsed > 0 {
			size = parsed
		}
	}

	format := strings.ToLower(strings.TrimSpace(os.Getenv("LOGO_DEV_FORMAT")))
	if format == "" {
		format = "png"
	}

	theme := strings.ToLower(strings.TrimSpace(os.Getenv("LOGO_DEV_THEME")))
	if theme == "" {
		theme = "auto"
	}

	resolver := &logoResolver{
		provider: provider,
		baseURL:  baseURL,
		token:    token,
		size:     size,
		format:   format,
		theme:    theme,
		cache:    make(map[string]cachedLogoURL),
	}

	if token == "" && logger != nil {
		logger.Warn("Market logo provider token not configured; logo_url will be omitted",
			zap.String("provider", provider))
	}
	return resolver
}

func (r *logoResolver) Resolve(symbol string) *string {
	if r == nil {
		return nil
	}
	normalized := strings.ToUpper(strings.TrimSpace(symbol))
	if normalized == "" {
		return nil
	}

	now := time.Now().UTC()
	r.mu.RLock()
	if cached, ok := r.cache[normalized]; ok && now.Sub(cached.fetchedAt) < logoCacheTTL {
		r.mu.RUnlock()
		return cloneStringPtr(cached.url)
	}
	r.mu.RUnlock()

	var resolved *string
	switch r.provider {
	case "logo_dev":
		resolved = r.resolveLogoDevURL(normalized)
	default:
		resolved = nil
	}

	r.mu.Lock()
	if len(r.cache) > 10000 {
		r.cache = make(map[string]cachedLogoURL)
	}
	r.cache[normalized] = cachedLogoURL{url: resolved, fetchedAt: now}
	r.mu.Unlock()

	return cloneStringPtr(resolved)
}

func (r *logoResolver) resolveLogoDevURL(symbol string) *string {
	if strings.TrimSpace(r.token) == "" {
		return nil
	}

	// R7-5: logo.dev publishable keys (pk_*) are designed for client-side embedding.
	// Reject non-publishable tokens to avoid leaking secret keys to clients.
	if !strings.HasPrefix(r.token, "pk_") {
		return nil
	}

	values := url.Values{}
	values.Set("token", r.token)
	values.Set("size", strconv.Itoa(r.size))
	values.Set("format", r.format)
	values.Set("theme", r.theme)

	u := fmt.Sprintf("%s/ticker/%s?%s", r.baseURL, url.PathEscape(symbol), values.Encode())
	return &u
}

func cloneStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

func determineMarketSession(ts time.Time) entities.MarketSession {
	if ts.IsZero() {
		return entities.MarketSessionClosed
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.FixedZone("EST", -5*60*60)
	}
	et := ts.In(loc)
	if et.Weekday() == time.Saturday || et.Weekday() == time.Sunday {
		return entities.MarketSessionClosed
	}

	minutes := et.Hour()*60 + et.Minute()
	switch {
	case minutes >= 4*60 && minutes < 9*60+30:
		return entities.MarketSessionPre
	case minutes >= 9*60+30 && minutes < 16*60:
		return entities.MarketSessionRegular
	case minutes >= 16*60 && minutes < 20*60:
		return entities.MarketSessionPost
	default:
		return entities.MarketSessionClosed
	}
}

func normalizeFilters(filters entities.MarketExploreFilters) entities.MarketExploreFilters {
	normalized := defaultExploreFilters()

	normalized.Query = strings.TrimSpace(filters.Query)
	if len(filters.Types) > 0 {
		normalized.Types = filters.Types
	}
	if len(filters.Exchanges) > 0 {
		normalized.Exchanges = dedupeStrings(filters.Exchanges)
	}
	if len(filters.Categories) > 0 {
		normalized.Categories = dedupeStrings(filters.Categories)
	}
	normalized.Tradable = filters.Tradable
	normalized.Fractionable = filters.Fractionable
	normalized.Marginable = filters.Marginable
	normalized.Shortable = filters.Shortable
	normalized.MinPrice = filters.MinPrice
	normalized.MaxPrice = filters.MaxPrice
	normalized.MinChangePct = filters.MinChangePct
	normalized.MaxChangePct = filters.MaxChangePct

	sortBy := strings.ToLower(strings.TrimSpace(filters.SortBy))
	switch sortBy {
	case "symbol", "name", "price", "change_pct", "volume":
		normalized.SortBy = sortBy
	}

	sortOrder := strings.ToLower(strings.TrimSpace(filters.SortOrder))
	if sortOrder == "asc" || sortOrder == "desc" {
		normalized.SortOrder = sortOrder
	}

	if filters.Page > 0 {
		normalized.Page = filters.Page
	}
	if filters.PageSize > 0 {
		normalized.PageSize = filters.PageSize
	}
	if normalized.PageSize > 50 {
		normalized.PageSize = 50
	}

	return normalized
}

func defaultExploreFilters() entities.MarketExploreFilters {
	return entities.MarketExploreFilters{
		SortBy:    "symbol",
		SortOrder: "asc",
		Page:      1,
		PageSize:  25,
	}
}

func sortCards(cards []entities.MarketInstrumentCard, sortBy, sortOrder string) {
	desc := strings.EqualFold(sortOrder, "desc")
	less := func(i, j int) bool {
		a := cards[i]
		b := cards[j]
		switch sortBy {
		case "name":
			if desc {
				return strings.ToLower(a.Name) > strings.ToLower(b.Name)
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case "price":
			if desc {
				return a.Quote.Price.GreaterThan(b.Quote.Price)
			}
			return a.Quote.Price.LessThan(b.Quote.Price)
		case "change_pct":
			if desc {
				return a.Quote.ChangePct.GreaterThan(b.Quote.ChangePct)
			}
			return a.Quote.ChangePct.LessThan(b.Quote.ChangePct)
		case "volume":
			if desc {
				return a.Quote.Volume > b.Quote.Volume
			}
			return a.Quote.Volume < b.Quote.Volume
		default:
			if desc {
				return strings.ToLower(a.Symbol) > strings.ToLower(b.Symbol)
			}
			return strings.ToLower(a.Symbol) < strings.ToLower(b.Symbol)
		}
	}
	sort.SliceStable(cards, less)
}

func paginateCards(cards []entities.MarketInstrumentCard, page, pageSize int) []entities.MarketInstrumentCard {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 25
	}
	start := (page - 1) * pageSize
	if start >= len(cards) {
		return []entities.MarketInstrumentCard{}
	}
	end := start + pageSize
	if end > len(cards) {
		end = len(cards)
	}
	return cards[start:end]
}

func buildPagination(page, pageSize int, totalItems int64) entities.MarketExplorePagination {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 25
	}
	totalPages := 0
	if totalItems > 0 {
		totalPages = int((totalItems + int64(pageSize) - 1) / int64(pageSize))
	}

	return entities.MarketExplorePagination{
		Page:       page,
		Limit:      pageSize,
		TotalItems: totalItems,
		TotalPages: totalPages,
		HasNext:    totalPages > 0 && page < totalPages,
		HasPrev:    page > 1,
	}
}

func buildFacets(cards []entities.MarketInstrumentCard) entities.MarketExploreFacets {
	typeCounts := make(map[string]int)
	exchangeCounts := make(map[string]int)
	categoryCounts := make(map[string]int)

	for _, card := range cards {
		typeCounts[string(card.InstrumentType)]++
		exchangeCounts[card.Exchange]++
		for _, category := range card.Categories {
			categoryCounts[category]++
		}
	}

	return entities.MarketExploreFacets{
		Types:      mapToFacetValues(typeCounts),
		Exchanges:  mapToFacetValues(exchangeCounts),
		Categories: mapToFacetValues(categoryCounts),
	}
}

func mapToFacetValues(counts map[string]int) []entities.MarketFacetValue {
	values := make([]entities.MarketFacetValue, 0, len(counts))
	for value, count := range counts {
		if strings.TrimSpace(value) == "" {
			continue
		}
		values = append(values, entities.MarketFacetValue{Value: value, Count: count})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Count == values[j].Count {
			return values[i].Value < values[j].Value
		}
		return values[i].Count > values[j].Count
	})
	return values
}

func (s *ExplorerService) enrichPaginatedQuotes(ctx context.Context, cards []entities.MarketInstrumentCard) []entities.MarketInstrumentCard {
	if len(cards) == 0 {
		return cards
	}

	updated := make([]entities.MarketInstrumentCard, len(cards))
	copy(updated, cards)

	type enrichmentResult struct {
		index int
		quote *entities.MarketQuote
	}

	results := make(chan enrichmentResult, len(updated))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup

	for idx := range updated {
		if hasUsefulQuote(updated[idx].Quote) {
			continue
		}

		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			symbol := strings.ToUpper(strings.TrimSpace(updated[i].Symbol))
			if symbol == "" {
				return
			}

			quote, err := s.alpacaClient.GetStockSnapshot(ctx, symbol)
			if err != nil || quote == nil {
				return
			}
			results <- enrichmentResult{index: i, quote: quote}
		}(idx)
	}

	wg.Wait()
	close(results)

	for result := range results {
		updated[result.index].Quote = entities.MarketInstrumentQuote{
			Price:         result.quote.Price,
			Bid:           result.quote.Bid,
			Ask:           result.quote.Ask,
			Change:        result.quote.Change,
			ChangePct:     result.quote.ChangePct,
			Open:          result.quote.Open,
			High:          result.quote.High,
			Low:           result.quote.Low,
			PreviousClose: result.quote.PreviousClose,
			Volume:        result.quote.Volume,
			Timestamp:     result.quote.Timestamp,
		}
		updated[result.index].MarketSession = determineMarketSession(result.quote.Timestamp)
	}

	return updated
}

func hasUsefulQuote(quote entities.MarketInstrumentQuote) bool {
	return quote.Price.GreaterThan(decimal.Zero) ||
		quote.PreviousClose.GreaterThan(decimal.Zero) ||
		quote.Open.GreaterThan(decimal.Zero) ||
		quote.High.GreaterThan(decimal.Zero) ||
		quote.Low.GreaterThan(decimal.Zero) ||
		quote.Bid.GreaterThan(decimal.Zero) ||
		quote.Ask.GreaterThan(decimal.Zero)
}

func latestQuoteTime(cards []entities.MarketInstrumentCard) time.Time {
	latest := time.Time{}
	for _, card := range cards {
		if card.Quote.Timestamp.After(latest) {
			latest = card.Quote.Timestamp
		}
	}
	if latest.IsZero() {
		return time.Now().UTC()
	}
	return latest
}

func loadMarketTaxonomy(path string) (marketTaxonomy, error) {
	if strings.TrimSpace(path) == "" {
		return marketTaxonomy{}, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return marketTaxonomy{}, err
	}

	var taxonomy marketTaxonomy
	if err := yaml.Unmarshal(content, &taxonomy); err != nil {
		return marketTaxonomy{}, err
	}

	if taxonomy.SymbolOverrides == nil {
		taxonomy.SymbolOverrides = make(map[string]taxonomySymbolOverride)
	}

	normalized := make(map[string]taxonomySymbolOverride, len(taxonomy.SymbolOverrides))
	for symbol, override := range taxonomy.SymbolOverrides {
		normalized[strings.ToUpper(strings.TrimSpace(symbol))] = override
	}
	taxonomy.SymbolOverrides = normalized

	return taxonomy, nil
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		key := strings.ToLower(normalized)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result
}

func dedupeAndNormalize(symbols []string) []string {
	seen := make(map[string]struct{}, len(symbols))
	result := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		normalized := strings.ToUpper(strings.TrimSpace(symbol))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func chunkSymbols(symbols []string, chunkSize int) [][]string {
	if chunkSize <= 0 {
		chunkSize = 200
	}
	chunks := make([][]string, 0, (len(symbols)+chunkSize-1)/chunkSize)
	for start := 0; start < len(symbols); start += chunkSize {
		end := start + chunkSize
		if end > len(symbols) {
			end = len(symbols)
		}
		chunks = append(chunks, symbols[start:end])
	}
	return chunks
}
