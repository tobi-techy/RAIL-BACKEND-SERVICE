package billpay

import (
	"testing"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/airbills"
)

func TestCategoryToProductCode(t *testing.T) {
	cases := map[string]string{
		CategoryAirtime:     airbills.ProductAirtime,
		CategoryData:        airbills.ProductData,
		CategoryElectricity: airbills.ProductElectricity,
		CategoryCable:       airbills.ProductCableTV,
		CategoryBetting:     airbills.ProductBetting,
		CategoryTransport:   airbills.ProductTransport,
		"unknown":           "",
		"":                  "",
	}
	for category, want := range cases {
		if got := categoryToProductCode(category); got != want {
			t.Errorf("categoryToProductCode(%q) = %q, want %q", category, got, want)
		}
	}
}

func TestIsCategorySupported(t *testing.T) {
	supported := []string{"airtime", "DATA", " Electricity ", "cable", "betting", "transport"}
	for _, c := range supported {
		if !IsCategorySupported(c) {
			t.Errorf("IsCategorySupported(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"", "loans", "insurance"} {
		if IsCategorySupported(c) {
			t.Errorf("IsCategorySupported(%q) = true, want false", c)
		}
	}
}

func TestCallbackStatusClassification(t *testing.T) {
	succeed := []string{airbills.StatusSuccess, airbills.StatusAlreadyProcessed, "success", "Successful", "COMPLETED", "delivered", "paid"}
	for _, s := range succeed {
		if !callbackSucceeded(s) {
			t.Errorf("callbackSucceeded(%q) = false, want true", s)
		}
		if callbackFailed(s) {
			t.Errorf("callbackFailed(%q) = true, want false", s)
		}
	}

	fail := []string{"failed", "Failure", "ERROR", "declined", "reversed", "cancelled"}
	for _, s := range fail {
		if !callbackFailed(s) {
			t.Errorf("callbackFailed(%q) = false, want true", s)
		}
		if callbackSucceeded(s) {
			t.Errorf("callbackSucceeded(%q) = true, want false", s)
		}
	}

	// Unknown / pending states are neither success nor terminal failure.
	for _, s := range []string{"", "pending", "processing", "queued"} {
		if callbackSucceeded(s) {
			t.Errorf("callbackSucceeded(%q) = true, want false", s)
		}
		if callbackFailed(s) {
			t.Errorf("callbackFailed(%q) = true, want false", s)
		}
	}
}

func TestIsFailedState(t *testing.T) {
	for _, s := range []string{"DENIED", "failed", "Cancelled"} {
		if !isFailedState(s) {
			t.Errorf("isFailedState(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"COMPLETE", "CONFIRMED", "PENDING", ""} {
		if isFailedState(s) {
			t.Errorf("isFailedState(%q) = true, want false", s)
		}
	}
}

func TestRecipientFieldMapping(t *testing.T) {
	const r = "08012345678"
	if phoneFor(CategoryAirtime, r) != r || phoneFor(CategoryData, r) != r {
		t.Error("airtime/data should map recipient to phone")
	}
	if phoneFor(CategoryElectricity, r) != "" {
		t.Error("electricity should not map recipient to phone")
	}
	if meterFor(CategoryElectricity, r) != r {
		t.Error("electricity should map recipient to meter")
	}
	if smartcardFor(CategoryCable, r) != r {
		t.Error("cable should map recipient to smartcard")
	}
	if customerFor(CategoryBetting, r) != r || customerFor(CategoryTransport, r) != r {
		t.Error("betting/transport should map recipient to customer id")
	}
	if meterFor(CategoryAirtime, r) != "" || smartcardFor(CategoryAirtime, r) != "" || customerFor(CategoryAirtime, r) != "" {
		t.Error("airtime should only populate the phone field")
	}
}
