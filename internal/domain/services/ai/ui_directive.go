package ai

// UIDirective is a card-ready instruction for the Miriam Canvas frontend.
// It is attached to tool results under the "display" key so the client can
// render rich cards instead of plain text.
type UIDirective struct {
	Card     string      `json:"card"`               // "places" | "flights" | "recommendations"
	Intent   string      `json:"intent"`             // e.g. "choose"
	Title    string      `json:"title"`              // short headline
	Subtitle string      `json:"subtitle,omitempty"` // optional secondary line
	Data     DisplayData `json:"data"`
}

// DisplayData holds the list payload for a card directive.
type DisplayData struct {
	Query            string        `json:"query"`
	Items            []DisplayItem `json:"items"`
	BudgetNote       string        `json:"budget_note,omitempty"`
	UserSpendBalance string        `json:"user_spend_balance,omitempty"`
}

// DisplayItem is a single card entry. Only Title is guaranteed; the rest are
// best-effort and may be empty.
type DisplayItem struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	URL         string `json:"url,omitempty"`
	Price       string `json:"price,omitempty"`
	Rating      string `json:"rating,omitempty"`
	Location    string `json:"location,omitempty"`
	Meta        string `json:"meta,omitempty"`
}
