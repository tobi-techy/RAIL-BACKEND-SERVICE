package entities

// CurrencySymbol returns the display symbol for a given ISO country code.
// Used by voice, nudges, and daily pulse to avoid hardcoded "$".
func CurrencySymbol(country string) string {
	symbols := map[string]string{
		"NG": "₦", "KE": "KSh", "GH": "GH₵", "ZA": "R",
		"EG": "E£", "TZ": "TSh", "UG": "USh", "US": "$",
		"GB": "£", "CA": "C$", "AU": "A$", "EU": "€",
		"DE": "€", "FR": "€", "JP": "¥", "IN": "₹",
		"BR": "R$", "MX": "MX$", "KR": "₩", "CN": "¥",
	}
	if s, ok := symbols[country]; ok {
		return s
	}
	return "$"
}
