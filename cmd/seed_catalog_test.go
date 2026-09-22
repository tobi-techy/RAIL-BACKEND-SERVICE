package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
)

// TestPrintCatalogReportIsCopyPasteable pins the operator-facing output: the
// whole point of the command is that a human can read the ids off the screen.
func TestPrintCatalogReportIsCopyPasteable(t *testing.T) {
	const usdc = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/token:EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

	report := &entities.InvestmentAssetCatalogReport{
		Collection: "curated",
		Strategies: 3,
		Written:    true,
		Upserted:   1,
		Skipped:    1,
		Assets: []entities.InvestmentCatalogAsset{
			{CAIP19: usdc, Symbol: "USDC", Chain: "solana", AssetClass: "unknown"},
			{CAIP19: "solana:x/slip44:501", Symbol: "SOL", Chain: "solana", AssetClass: "unknown", AlreadyKnown: true},
		},
	}

	var buf bytes.Buffer
	printCatalogReport(&buf, report)
	out := buf.String()

	assert.Contains(t, out, "WRITTEN")
	assert.Contains(t, out, "collection=curated")
	assert.Contains(t, out, "added=1")
	assert.Contains(t, out, "already_known=1")
	assert.Contains(t, out, usdc, "the raw id must be printed verbatim")
	assert.Contains(t, out, "SYMBOL")
	assert.Contains(t, out, "new")
	assert.Contains(t, out, "known")
	assert.Contains(t, out, "vault.strategies", "the operator is told where the ids go")
	assert.NotContains(t, out, "Re-run with -confirm")
}

func TestPrintCatalogReportMarksAPreview(t *testing.T) {
	report := &entities.InvestmentAssetCatalogReport{
		Collection: "curated",
		Strategies: 1,
		Written:    false,
		Assets: []entities.InvestmentCatalogAsset{
			{CAIP19: "solana:x/token:abc", Symbol: "ABC", Chain: "solana"},
		},
	}

	var buf bytes.Buffer
	printCatalogReport(&buf, report)
	out := buf.String()

	assert.Contains(t, out, "PREVIEW")
	assert.Contains(t, out, "Re-run with -confirm")
	assert.Contains(t, out, "added=0")
}

func TestPrintCatalogReportHandlesAnEmptyProvider(t *testing.T) {
	report := &entities.InvestmentAssetCatalogReport{
		Collection: "curated",
		Note:       "the provider returned no public strategies for this collection, so there was nothing to ingest",
	}

	var buf bytes.Buffer
	printCatalogReport(&buf, report)
	out := buf.String()

	assert.Contains(t, out, "No assets found")
	assert.Contains(t, out, "nothing to ingest")
	assert.True(t, strings.Contains(out, "ASSET CATALOG INGEST"))
}

// TestPrintClassifyAssetPinsOperatorOutput verifies the classify step is
// copy-pasteable and loud: the raw id is printed verbatim, the class change is
// explicit, and a placeholder id warns before it can be pasted into a tier file.
func TestPrintClassifyAssetPinsOperatorOutput(t *testing.T) {
	caip := "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/token:EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	asset := &entities.InvestmentAsset{
		CAIP19:      caip,
		Symbol:      "USDC",
		AssetClass:  "unknown",
		Allowlisted: true,
	}

	var buf bytes.Buffer
	printClassifyAsset(&buf, asset, "treasury", "PREVIEW (nothing written)")
	out := buf.String()

	assert.Contains(t, out, "PREVIEW")
	assert.Contains(t, out, caip, "the raw id must be printed so a human can paste it")
	assert.Contains(t, out, "unknown -> treasury")
	assert.Contains(t, out, "Re-run with -apply")

	// A placeholder id must never be silently accepted.
	var placeholder bytes.Buffer
	asset.CAIP19 = "solana:...:*REPLACE*"
	printClassifyAsset(&placeholder, asset, "treasury", "WRITTEN")
	assert.Contains(t, placeholder.String(), "REPLACE", "a placeholder must warn loudly")
}
