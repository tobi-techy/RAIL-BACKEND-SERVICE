// Package publictrades fetches publicly disclosed trades of US public figures
// (members of Congress) from the Financial Modeling Prep congressional
// trading API. Disclosures lag the actual trade by up to 45 days under the
// STOCK Act — consumers must present that honestly.
package publictrades

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	defaultBaseURL = "https://financialmodelingprep.com/stable"

	// Disclosure feeds change at most daily; the free FMP tier allows 250
	// calls/day, so cache aggressively.
	defaultCacheTTL = 6 * time.Hour

	latestFeedPages   = 3
	latestFeedPerPage = 250
)

// Trade is one publicly disclosed transaction by a public figure.
type Trade struct {
	FigureKey       string // normalized figure identifier, e.g. "nancy pelosi"
	FigureName      string // display name as disclosed
	Chamber         string // "house" or "senate"
	Ticker          string
	AssetName       string
	Side            string // "buy" or "sell"
	AmountMid       decimal.Decimal
	AmountRange     string // original disclosed range, e.g. "$1,001 - $15,000"
	TransactionDate time.Time
	DisclosureDate  time.Time
	Ref             string // stable dedupe reference for this disclosure line
}

// Figure summarizes a public figure present in the disclosure data.
type Figure struct {
	Key        string
	Name       string
	Chamber    string
	TradeCount int    // trades in the last 12 months
	LastTraded string // most recent transaction date, YYYY-MM-DD
	TopTickers []string
}

// Config holds API access; zero values use defaults. APIKey is required for
// live data (env FMP_API_KEY).
type Config struct {
	APIKey   string
	BaseURL  string
	CacheTTL time.Duration
}

// Client fetches and caches congressional trade disclosures from FMP.
type Client struct {
	cfg    Config
	http   *http.Client
	logger *zap.Logger

	mu          sync.Mutex
	latest      []Trade
	latestAt    time.Time
	byName      map[string][]Trade
	byNameAt    map[string]time.Time
	nameFetchMu sync.Mutex
}

func NewClient(cfg Config, logger *zap.Logger) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaultCacheTTL
	}
	return &Client{
		cfg:      cfg,
		http:     &http.Client{Timeout: 60 * time.Second},
		logger:   logger,
		byName:   map[string][]Trade{},
		byNameAt: map[string]time.Time{},
	}
}

// Configured reports whether live data access is possible.
func (c *Client) Configured() bool {
	return c.cfg.APIKey != ""
}

// SearchFigures finds public figures whose name matches the query, most
// active first.
func (c *Client) SearchFigures(ctx context.Context, query string) ([]Figure, error) {
	needle := normalizeName(query)
	if needle == "" {
		return nil, fmt.Errorf("search query is required")
	}
	trades, err := c.tradesByName(ctx, needle)
	if err != nil {
		return nil, err
	}
	matched := map[string]bool{}
	for _, t := range trades {
		if strings.Contains(t.FigureKey, needle) || containsAllTokens(t.FigureKey, needle) {
			matched[t.FigureKey] = true
		}
	}
	figures := summarizeFigures(trades, matched)
	sort.Slice(figures, func(i, j int) bool { return figures[i].TradeCount > figures[j].TradeCount })
	return figures, nil
}

// ListPopularFigures returns the most actively trading figures in the recent
// disclosure feed.
func (c *Client) ListPopularFigures(ctx context.Context, limit int) ([]Figure, error) {
	trades, err := c.latestTrades(ctx)
	if err != nil {
		return nil, err
	}
	figures := summarizeFigures(trades, nil)
	sort.Slice(figures, func(i, j int) bool { return figures[i].TradeCount > figures[j].TradeCount })
	if limit > 0 && len(figures) > limit {
		figures = figures[:limit]
	}
	return figures, nil
}

// GetFigureTrades returns a figure's stock trades disclosed after since,
// newest first. figureKey must be an exact key from SearchFigures.
func (c *Client) GetFigureTrades(ctx context.Context, figureKey string, since time.Time, limit int) ([]Trade, error) {
	key := normalizeName(figureKey)
	trades, err := c.tradesByName(ctx, key)
	if err != nil {
		return nil, err
	}
	out := make([]Trade, 0, 16)
	for _, t := range trades {
		if t.FigureKey != key {
			continue
		}
		if !since.IsZero() && !t.DisclosureDate.After(since) {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DisclosureDate.After(out[j].DisclosureDate) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// tradesByName queries both chambers' by-name endpoints. FMP matches on
// partial names; multi-word queries fall back to the last token (surname).
func (c *Client) tradesByName(ctx context.Context, name string) ([]Trade, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("public trades data source not configured: set FMP_API_KEY")
	}
	c.mu.Lock()
	if cached, ok := c.byName[name]; ok && time.Since(c.byNameAt[name]) < c.cfg.CacheTTL {
		c.mu.Unlock()
		return cached, nil
	}
	c.mu.Unlock()

	// Serialize name fetches: cheap protection for the free-tier call budget.
	c.nameFetchMu.Lock()
	defer c.nameFetchMu.Unlock()

	queries := []string{name}
	if tokens := strings.Fields(name); len(tokens) > 1 {
		queries = append(queries, tokens[len(tokens)-1])
	}
	var trades []Trade
	var lastErr error
	for _, q := range queries {
		senate, err := c.fetchTrades(ctx, "senate-trades-by-name", url.Values{"name": {q}}, "senate")
		if err != nil {
			lastErr = err
		}
		house, err := c.fetchTrades(ctx, "house-trades-by-name", url.Values{"name": {q}}, "house")
		if err != nil {
			lastErr = err
		}
		trades = append(senate, house...)
		if len(trades) > 0 {
			break
		}
	}
	if len(trades) == 0 && lastErr != nil {
		return nil, lastErr
	}

	c.mu.Lock()
	c.byName[name] = trades
	c.byNameAt[name] = time.Now()
	c.mu.Unlock()
	return trades, nil
}

func (c *Client) latestTrades(ctx context.Context) ([]Trade, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("public trades data source not configured: set FMP_API_KEY")
	}
	c.mu.Lock()
	if c.latest != nil && time.Since(c.latestAt) < c.cfg.CacheTTL {
		defer c.mu.Unlock()
		return c.latest, nil
	}
	c.mu.Unlock()

	var trades []Trade
	var lastErr error
	for _, endpoint := range []struct{ path, chamber string }{
		{"senate-latest", "senate"},
		{"house-latest", "house"},
	} {
		for page := 0; page < latestFeedPages; page++ {
			batch, err := c.fetchTrades(ctx, endpoint.path, url.Values{
				"page":  {fmt.Sprintf("%d", page)},
				"limit": {fmt.Sprintf("%d", latestFeedPerPage)},
			}, endpoint.chamber)
			if err != nil {
				lastErr = err
				break
			}
			trades = append(trades, batch...)
			if len(batch) < latestFeedPerPage {
				break
			}
		}
	}
	if len(trades) == 0 {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.latest != nil {
			// Serve stale data over failing outright.
			return c.latest, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no disclosure data returned")
	}

	c.mu.Lock()
	c.latest = trades
	c.latestAt = time.Now()
	c.mu.Unlock()
	c.logger.Info("public trade disclosures refreshed", zap.Int("trades", len(trades)))
	return trades, nil
}

// fmpDisclosure is the FMP row shape shared by house and senate endpoints.
type fmpDisclosure struct {
	Symbol           string `json:"symbol"`
	DisclosureDate   string `json:"disclosureDate"`
	TransactionDate  string `json:"transactionDate"`
	FirstName        string `json:"firstName"`
	LastName         string `json:"lastName"`
	Office           string `json:"office"`
	AssetDescription string `json:"assetDescription"`
	AssetType        string `json:"assetType"`
	Type             string `json:"type"`
	Amount           string `json:"amount"`
}

func (c *Client) fetchTrades(ctx context.Context, path string, params url.Values, chamber string) ([]Trade, error) {
	params.Set("apikey", c.cfg.APIKey)
	endpoint := fmt.Sprintf("%s/%s?%s", c.cfg.BaseURL, path, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fmp %s returned status %d", path, resp.StatusCode)
	}
	var raw []fmpDisclosure
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("fmp %s: %w", path, err)
	}
	trades := make([]Trade, 0, len(raw))
	for _, row := range raw {
		if row.AssetType != "" && !strings.Contains(strings.ToLower(row.AssetType), "stock") {
			continue
		}
		name := strings.TrimSpace(row.FirstName + " " + row.LastName)
		if name == "" {
			name = strings.TrimSpace(row.Office)
		}
		t, ok := buildTrade(chamber, name, row.Symbol, row.AssetDescription, row.Type, row.Amount, row.TransactionDate, row.DisclosureDate)
		if ok {
			trades = append(trades, t)
		}
	}
	return trades, nil
}

func summarizeFigures(trades []Trade, only map[string]bool) []Figure {
	cutoff := time.Now().AddDate(-1, 0, 0)
	type agg struct {
		figure  Figure
		last    time.Time
		tickers map[string]int
	}
	byKey := map[string]*agg{}
	for _, t := range trades {
		if only != nil && !only[t.FigureKey] {
			continue
		}
		a, ok := byKey[t.FigureKey]
		if !ok {
			a = &agg{figure: Figure{Key: t.FigureKey, Name: t.FigureName, Chamber: t.Chamber}, tickers: map[string]int{}}
			byKey[t.FigureKey] = a
		}
		if t.TransactionDate.After(cutoff) {
			a.figure.TradeCount++
			a.tickers[t.Ticker]++
		}
		if t.TransactionDate.After(a.last) {
			a.last = t.TransactionDate
			a.figure.LastTraded = t.TransactionDate.Format("2006-01-02")
		}
	}
	figures := make([]Figure, 0, len(byKey))
	for _, a := range byKey {
		type tc struct {
			ticker string
			count  int
		}
		counts := make([]tc, 0, len(a.tickers))
		for ticker, count := range a.tickers {
			counts = append(counts, tc{ticker, count})
		}
		sort.Slice(counts, func(i, j int) bool { return counts[i].count > counts[j].count })
		for i := 0; i < len(counts) && i < 5; i++ {
			a.figure.TopTickers = append(a.figure.TopTickers, counts[i].ticker)
		}
		figures = append(figures, a.figure)
	}
	return figures
}

// buildTrade normalizes one disclosure row; ok=false when the row is not a
// tradeable stock transaction (no ticker, unknown side, unparsable dates).
func buildTrade(chamber, figure, ticker, assetName, txType, amount, txDate, discDate string) (Trade, bool) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" || ticker == "--" || ticker == "N/A" || len(ticker) > 8 {
		return Trade{}, false
	}
	figure = strings.TrimSpace(figure)
	if figure == "" {
		return Trade{}, false
	}
	side := parseSide(txType)
	if side == "" {
		return Trade{}, false
	}
	transacted, err := parseDate(txDate)
	if err != nil {
		return Trade{}, false
	}
	disclosed, err := parseDate(discDate)
	if err != nil {
		disclosed = transacted
	}
	mid := parseAmountMid(amount)
	if !mid.IsPositive() {
		return Trade{}, false
	}
	key := normalizeName(figure)
	refInput := fmt.Sprintf("%s|%s|%s|%s|%s|%s", key, ticker, side, amount, txDate, discDate)
	sum := sha256.Sum256([]byte(refInput))
	return Trade{
		FigureKey:       key,
		FigureName:      figure,
		Chamber:         chamber,
		Ticker:          ticker,
		AssetName:       strings.TrimSpace(assetName),
		Side:            side,
		AmountMid:       mid,
		AmountRange:     strings.TrimSpace(amount),
		TransactionDate: transacted,
		DisclosureDate:  disclosed,
		Ref:             "disclosure_" + hex.EncodeToString(sum[:16]),
	}, true
}

func parseSide(txType string) string {
	t := strings.ToLower(strings.TrimSpace(txType))
	switch {
	case strings.HasPrefix(t, "purchase"), strings.HasPrefix(t, "buy"):
		return "buy"
	case strings.HasPrefix(t, "sale"), strings.HasPrefix(t, "sell"):
		return "sell"
	default:
		return ""
	}
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "01/02/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparsable date %q", s)
}

// parseAmountMid converts a disclosed range like "$1,001 - $15,000" to its
// midpoint. Open-ended ranges ("$50,000,000 +") use the lower bound.
func parseAmountMid(amount string) decimal.Decimal {
	parts := strings.Split(amount, "-")
	low := parseMoney(parts[0])
	if len(parts) == 1 {
		return low
	}
	high := parseMoney(parts[1])
	if !high.IsPositive() {
		return low
	}
	return low.Add(high).Div(decimal.NewFromInt(2))
}

func parseMoney(s string) decimal.Decimal {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' {
			return r
		}
		return -1
	}, s)
	if cleaned == "" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(cleaned)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func normalizeName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

// containsAllTokens reports whether every token of needle appears in key —
// lets "nancy pelosi" match a dataset name like "Pelosi, Nancy".
func containsAllTokens(key, needle string) bool {
	for _, token := range strings.Fields(needle) {
		if !strings.Contains(key, token) {
			return false
		}
	}
	return true
}
