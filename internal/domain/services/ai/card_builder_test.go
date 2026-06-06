package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildMiriamBriefCardKeepsDefaultsWhenInsightKeysMissing(t *testing.T) {
	cards := buildMiriamBriefCards(map[string]interface{}{
		"insights": []map[string]interface{}{
			{"body": "missing title and severity"},
		},
	})

	require.NotEmpty(t, cards)
	card := cards[0]
	require.Equal(t, "Miriam Brief", card.Title)
	require.Equal(t, "neutral", card.Sentiment)
}
