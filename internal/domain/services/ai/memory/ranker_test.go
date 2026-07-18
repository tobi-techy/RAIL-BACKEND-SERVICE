package memory

import (
	"math"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRankFacts_EmptyInput(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	result := r.RankFacts(nil, nil, now, 15)
	assert.Nil(t, result)
}

func TestRankFacts_LimitsOutput(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	facts := make([]*entities.MiriamUserFact, 20)
	for i := range facts {
		facts[i] = &entities.MiriamUserFact{
			Importance:     i + 1,
			Confidence:     decimal.NewFromFloat(0.8),
			LastConfirmedAt: now,
		}
	}
	result := r.RankFacts(facts, nil, now, 15)
	require.Len(t, result, 15)
}

func TestRankFacts_SortsByCompositeScore(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()

	high := &entities.MiriamUserFact{
		Importance:      9,
		Confidence:      decimal.NewFromFloat(0.95),
		LastConfirmedAt: now,
	}
	low := &entities.MiriamUserFact{
		Importance:      3,
		Confidence:      decimal.NewFromFloat(0.4),
		LastConfirmedAt: now.Add(-300 * 24 * time.Hour),
	}
	medium := &entities.MiriamUserFact{
		Importance:      6,
		Confidence:      decimal.NewFromFloat(0.7),
		LastConfirmedAt: now.Add(-50 * 24 * time.Hour),
	}

	result := r.RankFacts([]*entities.MiriamUserFact{low, medium, high}, nil, now, 15)
	require.Len(t, result, 3)
	assert.Equal(t, high, result[0], "highest importance+confidence+recency should be first")
	assert.Equal(t, medium, result[1])
	assert.Equal(t, low, result[2], "lowest should be last")
}

func TestRankFacts_WithQueryEmbedding(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()

	queryEmb := []float32{1, 0, 0}

	// Fact with similar embedding (same direction as query)
	similarFact := &entities.MiriamUserFact{
		Importance:      5,
		Confidence:      decimal.NewFromFloat(0.7),
		LastConfirmedAt: now,
		Embedding:       float32ToBytes([]float32{1, 0, 0}),
	}

	// Fact with dissimilar embedding (orthogonal to query)
	dissimilarFact := &entities.MiriamUserFact{
		Importance:      5,
		Confidence:      decimal.NewFromFloat(0.7),
		LastConfirmedAt: now,
		Embedding:       float32ToBytes([]float32{0, 1, 0}),
	}

	result := r.RankFacts([]*entities.MiriamUserFact{dissimilarFact, similarFact}, queryEmb, now, 15)
	require.Len(t, result, 2)
	assert.Equal(t, similarFact, result[0], "similar embedding should rank higher")
	assert.Equal(t, dissimilarFact, result[1])
}

func TestScoreFact_NoEmbedding_DefaultsSimilarity(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()

	f := &entities.MiriamUserFact{
		Importance:      10,
		Confidence:      decimal.NewFromFloat(1.0),
		LastConfirmedAt: now,
	}

	score := r.scoreFact(f, nil, now)
	// similarity=0.5, importance=1.0, recency=1.0, confidence=1.0
	// score = 0.5*0.30 + 1.0*0.25 + 1.0*0.25 + 1.0*0.20 = 0.15+0.25+0.25+0.20 = 0.85
	expected := 0.5*0.30 + 1.0*0.25 + 1.0*0.25 + 1.0*0.20
	assert.InDelta(t, expected, score, 0.001)
}

func TestRecencyScore_Today(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	score := r.recencyScore(now, now)
	assert.Equal(t, 1.0, score)
}

func TestRecencyScore_WithinWeek(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	score := r.recencyScore(now.Add(-5*24*time.Hour), now)
	assert.Equal(t, 1.0, score)
}

func TestRecencyScore_OneMonth(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	score := r.recencyScore(now.Add(-30*24*time.Hour), now)
	// ageDays=30, > 7 and < 365
	expected := 1.0 - (30.0-7.0)/358.0
	assert.InDelta(t, expected, score, 0.001)
}

func TestRecencyScore_OldFact(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	score := r.recencyScore(now.Add(-400*24*time.Hour), now)
	assert.Equal(t, 0.0, score)
}

func TestRecencyScore_FutureFact(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	// Future timestamp — ageDays should clamp to 0
	score := r.recencyScore(now.Add(24*time.Hour), now)
	assert.Equal(t, 1.0, score)
}

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	assert.InDelta(t, 1.0, sim, 0.001)
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	sim := cosineSimilarity(a, b)
	assert.InDelta(t, 0.0, sim, 0.001)
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{-1, 0}
	sim := cosineSimilarity(a, b)
	assert.InDelta(t, -1.0, sim, 0.001)
}

func TestCosineSimilarity_Empty(t *testing.T) {
	assert.Equal(t, 0.0, cosineSimilarity(nil, []float32{1, 0}))
	assert.Equal(t, 0.0, cosineSimilarity([]float32{1, 0}, nil))
	assert.Equal(t, 0.0, cosineSimilarity(nil, nil))
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0}
	sim := cosineSimilarity(a, b)
	assert.Equal(t, 0.0, sim)
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float32{0, 0, 0}
	b := []float32{1, 0, 0}
	sim := cosineSimilarity(a, b)
	assert.Equal(t, 0.0, sim)
}

func TestBytesToFloat32_RoundTrip(t *testing.T) {
	original := []float32{1.5, -2.5, 3.0, 0}
	encoded := float32ToBytes(original)
	decoded := bytesToFloat32(encoded)
	require.Len(t, decoded, len(original))
	for i := range original {
		assert.InDelta(t, float64(original[i]), float64(decoded[i]), 0.001)
	}
}

func TestBytesToFloat32_InvalidLength(t *testing.T) {
	// Not a multiple of 4 bytes
	result := bytesToFloat32([]byte{1, 2, 3})
	assert.Nil(t, result)
}

func TestBytesToFloat32_Empty(t *testing.T) {
	result := bytesToFloat32(nil)
	assert.Empty(t, result)
	result = bytesToFloat32([]byte{})
	assert.Empty(t, result)
}

func TestRankFacts_AllSameScore(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	facts := make([]*entities.MiriamUserFact, 5)
	for i := range facts {
		facts[i] = &entities.MiriamUserFact{
			Importance:      5,
			Confidence:      decimal.NewFromFloat(0.7),
			LastConfirmedAt: now,
		}
	}
	result := r.RankFacts(facts, nil, now, 15)
	require.Len(t, result, 5)
	// All should be returned, order stable
}

func TestRankFacts_LimitZero(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()
	facts := []*entities.MiriamUserFact{
		{Importance: 5, Confidence: decimal.NewFromFloat(0.7), LastConfirmedAt: now},
	}
	result := r.RankFacts(facts, nil, now, 0)
	// limit=0 means no cap
	require.Len(t, result, 1)
}

func TestScoreFact_ImportanceWeighting(t *testing.T) {
	r := &Ranker{}
	now := time.Now().UTC()

	high := r.scoreFact(&entities.MiriamUserFact{
		Importance: 10, Confidence: decimal.NewFromFloat(0.5), LastConfirmedAt: now,
	}, nil, now)
	low := r.scoreFact(&entities.MiriamUserFact{
		Importance: 1, Confidence: decimal.NewFromFloat(0.5), LastConfirmedAt: now,
	}, nil, now)

	// Importance differs by 9/10 = 0.9, weight 0.25 → delta ~0.225
	delta := high - low
	assert.InDelta(t, 0.9*0.25, delta, 0.001)
}

// Helper: convert float32 slice to bytes for test data
func float32ToBytes(f []float32) []byte {
	b := make([]byte, len(f)*4)
	for i, v := range f {
		bits := math.Float32bits(v)
		b[i*4+0] = byte(bits)
		b[i*4+1] = byte(bits >> 8)
		b[i*4+2] = byte(bits >> 16)
		b[i*4+3] = byte(bits >> 24)
	}
	return b
}
