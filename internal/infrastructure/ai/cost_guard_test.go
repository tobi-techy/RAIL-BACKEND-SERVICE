package ai

import (
	"testing"

	"go.uber.org/zap"
)

func TestGuard_DisabledWhenCapsZero(t *testing.T) {
	g := NewGuard(nil, zap.NewNop(), 0, 0)
	if g.IsEnabled() {
		t.Fatal("guard with 0/0 caps should report disabled")
	}
}

func TestGuard_NilRedisMeansDisabled(t *testing.T) {
	g := NewGuard(nil, zap.NewNop(), 0.10, 2.00)
	if g.IsEnabled() {
		t.Fatal("guard with nil redis should report disabled")
	}
}

func TestGuard_ExceededErrorShape(t *testing.T) {
	e := &ExceededError{Scope: "daily", LimitUSD: 0.10, SpentUSD: 0.11}
	if e.Error() == "" {
		t.Fatal("Error() should be non-empty")
	}
	ex, ok := IsExceeded(e)
	if !ok || ex.Scope != "daily" {
		t.Fatalf("IsExceeded didn't round-trip the error: %+v", ex)
	}
	if _, ok := IsExceeded(nil); ok {
		t.Fatal("IsExceeded(nil) should return false")
	}
	if _, ok := IsExceeded(errPlain("boom")); ok {
		t.Fatal("IsExceeded should return false for non-ExceededError")
	}
}

func TestGuard_USDCentsRoundTrip(t *testing.T) {
	if usdToCents(0.10) != 10 {
		t.Fatalf("expected 10 cents for $0.10, got %d", usdToCents(0.10))
	}
	if centsToUSD(10) != 0.10 {
		t.Fatalf("expected $0.10 for 10 cents, got %.2f", centsToUSD(10))
	}
	// Rounding: $0.123 → 12 cents.
	if usdToCents(0.123) != 12 {
		t.Fatalf("expected 12 cents for $0.123, got %d", usdToCents(0.123))
	}
	// $0.001 → 0 cents (below the rounding threshold).
	if usdToCents(0.001) != 0 {
		t.Fatalf("expected 0 cents for $0.001, got %d", usdToCents(0.001))
	}
}

func TestGuard_EstimateCostUSD_FastModelCheaper(t *testing.T) {
	fast := estimateCostUSD("gpt-4o-mini", 1000)
	smart := estimateCostUSD("gpt-4o", 1000)
	if fast >= smart {
		t.Fatalf("gpt-4o-mini should be cheaper than gpt-4o: fast=%.6f smart=%.6f", fast, smart)
	}
	// Unknown model falls back to $10/M tokens.
	unknown := estimateCostUSD("never-heard-of-this-model", 1000)
	if unknown != 0.00001*1000 {
		t.Fatalf("expected fallback pricing, got %.6f", unknown)
	}
}

// errPlain is a tiny error type for IsExceeded negative test.
type errPlain string

func (e errPlain) Error() string { return string(e) }
