package entities

import "testing"

func TestTierLevelToKYCTier(t *testing.T) {
	tests := []struct {
		level    int
		expected KYCTier
	}{
		{KYCTierLevelNonKYC, KYCTierNonKYC},
		{KYCTierLevelBasic, KYCTierBasic},
		{KYCTierLevelAdvanced, KYCTierAdvanced},
		{0, KYCTierUnverified},
		{99, KYCTierUnverified},
	}
	for _, tt := range tests {
		if got := TierLevelToKYCTier(tt.level); got != tt.expected {
			t.Errorf("TierLevelToKYCTier(%d) = %v, want %v", tt.level, got, tt.expected)
		}
	}
}

func TestKYCTierToLevel(t *testing.T) {
	tests := []struct {
		tier     KYCTier
		expected int
	}{
		{KYCTierAdvanced, KYCTierLevelAdvanced},
		{KYCTierBasic, KYCTierLevelBasic},
		{KYCTierNonKYC, KYCTierLevelNonKYC},
		{KYCTierUnverified, KYCTierLevelUnverified},
	}
	for _, tt := range tests {
		if got := KYCTierToLevel(tt.tier); got != tt.expected {
			t.Errorf("KYCTierToLevel(%v) = %d, want %d", tt.tier, got, tt.expected)
		}
	}
}

func TestEffectiveKYCTier(t *testing.T) {
	tests := []struct {
		name      string
		tierLevel int
		kycStatus string
		expected  KYCTier
	}{
		{"persisted advanced wins", KYCTierLevelAdvanced, "pending", KYCTierAdvanced},
		{"persisted basic wins", KYCTierLevelBasic, "", KYCTierBasic},
		{"falls back to status when non-basic level", KYCTierLevelNonKYC, "approved", KYCTierBasic},
		{"legacy advanced status capped at basic without persisted tier", 0, "advanced_approved", KYCTierBasic},
		{"account block clamps to unverified", KYCTierLevelAdvanced, "suspended", KYCTierUnverified},
		{"unverified default", KYCTierLevelNonKYC, "rejected", KYCTierUnverified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveKYCTier(tt.tierLevel, tt.kycStatus); got != tt.expected {
				t.Errorf("EffectiveKYCTier(%d, %q) = %v, want %v", tt.tierLevel, tt.kycStatus, got, tt.expected)
			}
		})
	}
}

func TestCapabilitiesForTier(t *testing.T) {
	tier1 := CapabilitiesForTier(KYCTierLevelNonKYC)
	if !tier1.CanDepositCrypto {
		t.Error("tier 1 should allow crypto deposit")
	}
	if tier1.CanReceiveNGN || tier1.CanDepositFiatUSD || tier1.CanUseCard || tier1.CanInvest {
		t.Error("tier 1 should not unlock NGN/USD/card/invest")
	}

	tier2 := CapabilitiesForTier(KYCTierLevelBasic)
	if !tier2.CanReceiveNGN {
		t.Error("tier 2 should unlock NGN")
	}
	if tier2.CanDepositFiatUSD || tier2.CanUseCard || tier2.CanInvest || tier2.CanInvestTokenized {
		t.Error("tier 2 should not unlock USD/card/invest/tokenized")
	}

	tier3 := CapabilitiesForTier(KYCTierLevelAdvanced)
	if !(tier3.CanReceiveNGN && tier3.CanDepositFiatUSD && tier3.CanUseCard && tier3.CanInvest && tier3.CanInvestTokenized) {
		t.Error("tier 3 should unlock NGN, USD, card, invest, tokenized")
	}
}
