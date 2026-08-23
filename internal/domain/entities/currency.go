package entities

import "strings"

// currencySymbols maps ISO country codes to their currency symbols.
var currencySymbols = map[string]string{
	"NG": "₦", "KE": "KSh", "GH": "GH₵", "ZA": "R",
	"EG": "E£", "TZ": "TSh", "UG": "USh",
	"GB": "£", "US": "$", "CA": "CA$", "AU": "A$",
	"IN": "₹", "PH": "₱", "BR": "R$", "MX": "MX$",
	"DE": "€", "FR": "€", "ES": "€", "NL": "€", "IE": "€",
	"EU": "€", "JP": "¥", "KR": "₩", "CN": "¥",
}

// CurrencySymbol returns the display symbol for a given ISO country code.
// Used by voice, nudges, and daily pulse to avoid hardcoded "$".
// Falls back to "$" for unknown countries.
func CurrencySymbol(country string) string {
	country = strings.ToUpper(strings.TrimSpace(country))
	if s, ok := currencySymbols[country]; ok {
		return s
	}
	return "$"
}
