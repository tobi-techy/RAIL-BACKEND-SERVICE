package miriam

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestBuildRawDescriptionLeadsWithMerchant(t *testing.T) {
	got := buildRawDescription(entities.SpendingTransaction{
		Category: "groceries",
		Source:   "Shoprite Lekki",
		Amount:   decimal.NewFromInt(15000),
	})
	assert.Equal(t, "Shoprite Lekki", got)
}

func TestBuildRawDescriptionKeepsGenericSource(t *testing.T) {
	got := buildRawDescription(entities.SpendingTransaction{
		Category: "airtime",
		Source:   "card",
		Amount:   decimal.NewFromInt(500),
	})
	assert.Equal(t, "airtime card", got)
}
