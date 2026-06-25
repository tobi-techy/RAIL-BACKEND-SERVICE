package ai

import (
	"strings"
	"testing"

	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

func TestClassifyCategory(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		explicit string
		want     string
	}{
		{"explicit places", "anything", "places", "places"},
		{"explicit flights", "anything", "flights", "flights"},
		{"explicit products", "anything", "products", "products"},
		{"explicit general maps to recommendations", "anything", "general", "recommendations"},
		{"infer flights from fly", "cheap flights to Lagos", "", "flights"},
		{"infer flights from airfare", "airfare to London next week", "", "flights"},
		{"infer places from restaurant", "best restaurant in Lekki", "", "places"},
		{"infer places from near me", "coffee near me", "", "places"},
		{"infer products from buy", "buy a standing desk", "", "products"},
		{"infer products from cheapest", "cheapest iphone 15", "", "products"},
		{"default recommendations", "what to do this weekend", "", "recommendations"},
		{"explicit beats heuristic", "buy a flight", "places", "places"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyCategory(tc.query, tc.explicit); got != tc.want {
				t.Fatalf("classifyCategory(%q,%q) = %q, want %q", tc.query, tc.explicit, got, tc.want)
			}
		})
	}
}

func TestCardForCategory(t *testing.T) {
	tests := map[string]string{
		"places":          "places",
		"flights":         "flights",
		"products":        "recommendations",
		"recommendations": "recommendations",
		"general":         "recommendations",
	}
	for in, want := range tests {
		if got := cardForCategory(in); got != want {
			t.Errorf("cardForCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildDisplayItems(t *testing.T) {
	longContent := strings.Repeat("a", 300)

	resp := &infraai.TavilySearchResponse{
		Results: []infraai.TavilySearchResult{
			{Title: "Spot One", URL: "https://one.example", Content: "Great food $240 rated 4.6 stars at 123 Main Street downtown"},
			{Title: "Spot Two", URL: "https://two.example", Content: longContent},
			{Title: "Spot Three", URL: "https://three.example", Content: "no price or rating here"},
		},
		Images: []infraai.TavilyImage{
			{URL: "https://img1.example/a.jpg"},
			{URL: "https://img2.example/b.jpg"},
		},
	}

	items := buildDisplayItems(resp, "places")

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Item 0: full extraction + image by index.
	if items[0].Title != "Spot One" {
		t.Errorf("item0 title = %q", items[0].Title)
	}
	if items[0].ImageURL != "https://img1.example/a.jpg" {
		t.Errorf("item0 image = %q, want index-matched image", items[0].ImageURL)
	}
	if items[0].Price != "$240" {
		t.Errorf("item0 price = %q, want $240", items[0].Price)
	}
	if items[0].Rating != "4.6" {
		t.Errorf("item0 rating = %q, want 4.6", items[0].Rating)
	}
	if items[0].Location != "123 Main Street" {
		t.Errorf("item0 location = %q, want '123 Main Street'", items[0].Location)
	}

	// Item 1: long description trimmed; image by index.
	if items[1].ImageURL != "https://img2.example/b.jpg" {
		t.Errorf("item1 image = %q", items[1].ImageURL)
	}
	// 160 chars + ellipsis rune.
	if !strings.HasSuffix(items[1].Description, "…") {
		t.Errorf("item1 description not trimmed: %q", items[1].Description)
	}
	if got := len([]rune(items[1].Description)); got != descriptionMaxLen+1 {
		t.Errorf("item1 description rune len = %d, want %d", got, descriptionMaxLen+1)
	}

	// Item 2: no image (only 2 images), no price/rating.
	if items[2].ImageURL != "" {
		t.Errorf("item2 should have no image, got %q", items[2].ImageURL)
	}
	if items[2].Price != "" || items[2].Rating != "" {
		t.Errorf("item2 should have empty price/rating, got price=%q rating=%q", items[2].Price, items[2].Rating)
	}

	// Non-places category should not populate location.
	recItems := buildDisplayItems(resp, "recommendations")
	if recItems[0].Location != "" {
		t.Errorf("recommendations item should not have location, got %q", recItems[0].Location)
	}
}

func TestWebSearchToolHasCategoryParam(t *testing.T) {
	tool := WebSearchTool()
	props, ok := tool.Parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("tool has no properties map")
	}
	cat, ok := props["category"].(map[string]interface{})
	if !ok {
		t.Fatal("tool missing optional 'category' param")
	}
	enum, ok := cat["enum"].([]string)
	if !ok || len(enum) != 4 {
		t.Fatalf("category enum = %v, want 4 values", cat["enum"])
	}
	// category must NOT be required.
	req, _ := tool.Parameters["required"].([]string)
	for _, r := range req {
		if r == "category" {
			t.Error("category must not be in required list")
		}
	}
}

func TestExtractPriceAndRating(t *testing.T) {
	priceTests := map[string]string{
		"costs $240 per night": "$240",
		"only $1,200 total":    "$1,200",
		"price is $19.99":      "$19.99",
		"no money here":        "",
	}
	for in, want := range priceTests {
		if got := extractPrice(in); got != want {
			t.Errorf("extractPrice(%q) = %q, want %q", in, got, want)
		}
	}

	ratingTests := map[string]string{
		"rated 4.6 stars":   "4.6",
		"score of 4.2/5":    "4.2",
		"5★ reviews":        "5",
		"no rating present": "",
	}
	for in, want := range ratingTests {
		if got := extractRating(in); got != want {
			t.Errorf("extractRating(%q) = %q, want %q", in, got, want)
		}
	}
}
