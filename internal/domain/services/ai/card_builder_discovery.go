package ai

import "github.com/rail-service/rail_service/internal/domain/entities"

// DiscoveryItem is a single search result rendered as a tappable card.
type DiscoveryItem struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// buildDiscoveryCards creates rich cards from web search results.
func buildDiscoveryCards(result map[string]interface{}) []entities.InsightCard {
	rawResults, ok := result["results"].([]map[string]interface{})
	if !ok {
		return nil
	}

	items := make([]DiscoveryItem, 0, len(rawResults))
	for _, r := range rawResults {
		title, _ := r["title"].(string)
		url, _ := r["url"].(string)
		content, _ := r["content"].(string)
		if title == "" || url == "" {
			continue
		}
		// Truncate snippet
		snippet := content
		if len(snippet) > 120 {
			snippet = snippet[:120] + "..."
		}
		items = append(items, DiscoveryItem{
			Title:   title,
			URL:     url,
			Snippet: snippet,
		})
	}

	// Attach images to items where possible
	if rawImages, ok := result["images"].([]map[string]interface{}); ok && len(rawImages) > 0 {
		for i := range items {
			if i < len(rawImages) {
				if imgURL, ok := rawImages[i]["url"].(string); ok {
					items[i].ImageURL = imgURL
				}
			}
		}
	}

	if len(items) == 0 {
		return nil
	}

	return []entities.InsightCard{
		{
			Type:  "discovery",
			Title: "Search Results",
			Data:  items,
		},
	}
}
