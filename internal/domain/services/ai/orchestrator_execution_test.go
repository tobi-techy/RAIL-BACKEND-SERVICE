package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExecutionActionDescription_BookFlight_WithNGN(t *testing.T) {
	got := executionActionDescription(ToolBookFlight, map[string]interface{}{
		"passenger": map[string]interface{}{"given_name": "Ada", "family_name": "Lovelace"},
		"total_usd": "70.98",
		"total_ngn": "120000",
	})
	assert.Equal(t, "Book flight for Ada Lovelace — $70.98 total (fare + Rail fee) charged from Spend (≈₦120000)", got)
}

func TestExecutionActionDescription_BookFlight_NoNGN(t *testing.T) {
	// When no live rate resolved, quoteFlightBooking omits total_ngn; the card
	// must show the exact USD amount and skip the naira equivalent.
	got := executionActionDescription(ToolBookFlight, map[string]interface{}{
		"passenger": map[string]interface{}{"given_name": "Ada", "family_name": "Lovelace"},
		"total_usd": "70.98",
	})
	assert.Equal(t, "Book flight for Ada Lovelace — $70.98 total (fare + Rail fee) charged from Spend", got)
}

func TestExecutionActionDescription_BookFlight_FallbackWording(t *testing.T) {
	// Without a resolved total the card falls back to generic wording.
	got := executionActionDescription(ToolBookFlight, map[string]interface{}{
		"passenger": map[string]interface{}{"given_name": "Ada", "family_name": "Lovelace"},
	})
	assert.Equal(t, "Book flight for Ada Lovelace — escrow and the Rail fee are charged from Spend", got)
}

func TestExecutionActionDescription_BookFlight_MissingPassengerName(t *testing.T) {
	got := executionActionDescription(ToolBookFlight, map[string]interface{}{
		"total_usd": "70.98",
	})
	assert.Equal(t, "Book flight for the selected traveler — $70.98 total (fare + Rail fee) charged from Spend", got)
}
