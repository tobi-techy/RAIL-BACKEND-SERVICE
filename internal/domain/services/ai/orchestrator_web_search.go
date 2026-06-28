package ai

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const ToolWebSearch = "web_search"

// descriptionMaxLen caps the snippet length carried into card items.
const descriptionMaxLen = 160

// priceRegex matches simple money strings like "$240" or "$1,200".
var priceRegex = regexp.MustCompile(`\$\d[\d,]*(?:\.\d{2})?`)

// ratingRegex matches ratings like "4.6 stars", "4.6/5", or "4.6★".
var ratingRegex = regexp.MustCompile(`(\d(?:\.\d)?)\s*(?:stars?|/\s*5|★)`)

// searchResultCache is a TTL cache for search results (1hr, global across users).
type searchResultCache struct {
	mu      sync.RWMutex
	entries map[string]searchCacheEntry
}

type searchCacheEntry struct {
	result    map[string]interface{}
	expiresAt time.Time
}

var searchCache = &searchResultCache{entries: make(map[string]searchCacheEntry)}

func (c *searchResultCache) Get(key string) (map[string]interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.result, true
}

func (c *searchResultCache) Set(key string, result map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = searchCacheEntry{result: result, expiresAt: time.Now().Add(1 * time.Hour)}
	// Lazy eviction: purge expired entries if cache grows large
	if len(c.entries) > 200 {
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
}

// normalizeSearchQuery lowercases and trims the query for cache key dedup.
func normalizeSearchQuery(q string) string {
	return strings.ToLower(strings.TrimSpace(q))
}

// WebSearcher is the interface for web search (satisfied by TavilyClient).
type WebSearcher interface {
	Search(ctx context.Context, req infraai.TavilySearchRequest) (*infraai.TavilySearchResponse, error)
}

// SetWebSearcher wires the web search provider.
func (o *Orchestrator) SetWebSearcher(s WebSearcher) {
	o.webSearcher = s
}

// WebSearchTool returns the tool definition.
func WebSearchTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolWebSearch,
		Description: "Search the internet for anything the user asks about: restaurants, products, flights, events, services, experiences, news. Use when the user asks for recommendations, comparisons, or information you don't have. Returns results with titles, links, descriptions, and images.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query. Be specific: include location, budget, or preferences the user mentioned.",
				},
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"places", "flights", "products", "general"},
					"description": "Set this when the user is looking for: 'places' (restaurants, cafes, bars, hotels, spots near them), 'flights' (airfare, flying somewhere), or 'products' (buying something, prices, deals). Use 'general' otherwise. This controls how results are rendered for the user.",
				},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}

// executeWebSearch runs a Tavily search and returns results with budget context
// plus a card-ready "display" directive for the Miriam Canvas frontend.
func (o *Orchestrator) executeWebSearch(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.webSearcher == nil {
		return map[string]interface{}{"error": "web search is not available right now"}, nil
	}
	query, _ := args["query"].(string)
	if query == "" {
		return map[string]interface{}{"error": "query is required"}, nil
	}

	category, _ := args["category"].(string)
	category = classifyCategory(query, category)

	// Resolve per-user budget context up front (used for both result + display).
	var budget string
	if o.aggregateStats != nil {
		spend, err := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		if err == nil {
			budget = "$" + spend.StringFixed(2)
		}
	}
	const budgetNote = "Tell the user which options fit their budget. Flag anything that might be expensive relative to their balance."

	// Check cache first. Cached value omits per-user budget fields, so we
	// re-attach them (result map + display.data) on a hit.
	cacheKey := normalizeSearchQuery(query)
	if cached, ok := searchCache.Get(cacheKey); ok {
		return withBudget(cached, budget, budgetNote), nil
	}

	resp, err := o.webSearcher.Search(ctx, infraai.TavilySearchRequest{
		Query:         query,
		MaxResults:    5,
		IncludeImages: true,
	})
	if err != nil {
		return map[string]interface{}{"error": "search failed, try again"}, nil
	}

	// Build results for the LLM to reference (existing behavior, unchanged).
	results := make([]map[string]interface{}, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, map[string]interface{}{
			"title":   r.Title,
			"url":     r.URL,
			"content": r.Content,
		})
	}

	// Attach images if available (existing behavior, unchanged).
	images := make([]map[string]interface{}, 0, len(resp.Images))
	for _, img := range resp.Images {
		images = append(images, map[string]interface{}{
			"url":         img.URL,
			"description": img.Description,
		})
	}

	// Build the card-ready display directive.
	display := UIDirective{
		Card:     cardForCategory(category),
		Intent:   "choose",
		Title:    titleForCategory(category),
		Subtitle: query,
		Data: DisplayData{
			Query: query,
			Items: buildDisplayItems(resp, category),
		},
	}

	// Cache the result WITHOUT per-user budget fields.
	cacheResult := map[string]interface{}{
		"results": results,
		"images":  images,
		"count":   len(results),
		"display": display,
	}
	searchCache.Set(cacheKey, cacheResult)

	// Return a fresh copy with budget re-attached.
	return withBudget(cacheResult, budget, budgetNote), nil
}

// withBudget returns a shallow copy of the cached result with per-user budget
// fields attached to both the result map and the display directive's data.
func withBudget(cached map[string]interface{}, budget, budgetNote string) map[string]interface{} {
	out := make(map[string]interface{}, len(cached)+2)
	for k, v := range cached {
		out[k] = v
	}
	if budget == "" {
		return out
	}
	out["user_spend_balance"] = budget
	out["budget_note"] = budgetNote
	if d, ok := out["display"].(UIDirective); ok {
		d.Data.UserSpendBalance = budget
		d.Data.BudgetNote = budgetNote
		out["display"] = d
	}
	return out
}

// classifyCategory normalizes an explicit category or infers one from the query.
// Returns one of: "places", "flights", "products", "recommendations".
func classifyCategory(query, explicit string) string {
	switch strings.ToLower(strings.TrimSpace(explicit)) {
	case "places":
		return "places"
	case "flights":
		return "flights"
	case "products":
		return "products"
	case "general":
		return "recommendations"
	}

	q := strings.ToLower(query)
	switch {
	case containsAny(q, "flight", "fly ", "flying", "airfare", "airline", "plane ticket"):
		return "flights"
	case containsAny(q, "restaurant", "cafe", "coffee", "bar ", "hotel", "near me", "place to", "spot", "brunch", "dinner", "lunch", "eat"):
		return "places"
	case containsAny(q, "buy ", "price", "product", "cheapest", "deal", "discount", "shop"):
		return "products"
	default:
		return "recommendations"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// cardForCategory maps a normalized category to a frontend card type.
func cardForCategory(category string) string {
	switch category {
	case "places":
		return "places"
	case "flights":
		return "flights"
	default: // products, recommendations, general
		return "recommendations"
	}
}

// titleForCategory returns a short headline for the card.
func titleForCategory(category string) string {
	switch category {
	case "places":
		return "Places near you"
	case "flights":
		return "Flight options"
	case "products":
		return "Top picks"
	default:
		return "Top picks"
	}
}

// buildDisplayItems converts Tavily results into card items, matching images by
// index order and doing best-effort extraction of price/rating/location.
func buildDisplayItems(resp *infraai.TavilySearchResponse, category string) []DisplayItem {
	if resp == nil {
		return []DisplayItem{}
	}
	items := make([]DisplayItem, 0, len(resp.Results))
	for i, r := range resp.Results {
		item := DisplayItem{
			Title:       r.Title,
			URL:         r.URL,
			Description: trimDescription(r.Content),
			Price:       extractPrice(r.Content),
			Rating:      extractRating(r.Content),
		}
		if i < len(resp.Images) {
			item.ImageURL = resp.Images[i].URL
		}
		if category == "places" {
			item.Location = extractLocation(r.Content)
		}
		items = append(items, item)
	}
	return items
}

// trimDescription collapses whitespace and trims a snippet to descriptionMaxLen runes.
func trimDescription(content string) string {
	s := strings.Join(strings.Fields(content), " ")
	runes := []rune(s)
	if len(runes) <= descriptionMaxLen {
		return s
	}
	return strings.TrimSpace(string(runes[:descriptionMaxLen])) + "…"
}

// extractPrice does a best-effort scan for a money string; "" if none found.
func extractPrice(content string) string {
	return priceRegex.FindString(content)
}

// extractRating does a best-effort scan for a numeric rating; "" if none found.
func extractRating(content string) string {
	m := ratingRegex.FindStringSubmatch(content)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// extractLocation does a light best-effort scan for an address/area mention.
// Currently conservative: returns "" unless a clearly address-like pattern is
// present. Kept simple by design (no external geocoding).
func extractLocation(content string) string {
	// Match patterns like "123 Main St" / "45 Broadway Ave".
	m := locationRegex.FindString(content)
	return strings.TrimSpace(m)
}

// locationRegex matches a simple street-address-like fragment.
var locationRegex = regexp.MustCompile(`\d{1,5}\s+[A-Z][A-Za-z]+(?:\s+[A-Z][A-Za-z]+)*\s+(?:St|Street|Ave|Avenue|Rd|Road|Blvd|Boulevard|Ln|Lane|Dr|Drive)\b`)
