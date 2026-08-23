package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// displayContext holds the resolved display parameters for a user's currency.
type displayContext struct {
	Symbol string
	Rate   decimal.Decimal
	Locale string
	Country string
}

// userDisplayContext resolves a user's country from profile, then returns
// a displayContext with the correct symbol, live FX rate, and locale.
func userDisplayContext(ctx context.Context, o *AgentAdapter, userID uuid.UUID) displayContext {
	country := ""
	if o.financialProfile != nil {
		fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if profile, err := o.financialProfile.GetByUserID(fetchCtx, userID); err == nil && profile != nil {
			country = profile.ResidenceCountry
			if country == "" {
				country = profile.TaxCountry
			}
		}
	}
	if country == "" && o.userProfile != nil {
		if c, err := o.userProfile.GetCountry(ctx, userID); err == nil {
			country = strings.ToUpper(strings.TrimSpace(c))
		}
	}

	symbol := entities.CurrencySymbol(country)
	rate := decimal.NewFromInt(1) // default 1:1
	locale := "en-US"

	if o.currencyRates != nil && country != "" {
		currencyCode := currencyCodeForCountry(country)
		if currencyCode != "" && !strings.EqualFold(currencyCode, "USD") {
			if r, err := o.currencyRates.GetLatestRate(ctx, "USD", currencyCode); err == nil && r.IsPositive() {
				rate = r
			}
		}
	}

	locale = localeForCountry(country)

	return displayContext{
		Symbol:  symbol,
		Rate:    rate,
		Locale:  locale,
		Country: country,
	}
}

// displayAmount converts a USD amount to the user's local currency and formats it.
func (d displayContext) displayAmount(usdAmount decimal.Decimal) string {
	local := usdAmount.Mul(d.Rate)
	return fmt.Sprintf("%s%s", d.Symbol, local.StringFixed(2))
}

// displayMap returns the currency_display object for tool output.
func (d displayContext) displayMap() map[string]interface{} {
	return map[string]interface{}{
		"currency_symbol": d.Symbol,
		"fx_rate":         d.Rate.StringFixed(4),
		"locale":          d.Locale,
		"country":         d.Country,
	}
}

// currencyCodeForCountry returns the ISO currency code for a country.
func currencyCodeForCountry(country string) string {
	codes := map[string]string{
		"NG": "NGN", "KE": "KES", "GH": "GHS", "ZA": "ZAR",
		"EG": "EGP", "TZ": "TZS", "UG": "UGX",
		"US": "USD", "GB": "GBP", "CA": "CAD", "AU": "AUD",
		"EU": "EUR", "DE": "EUR", "FR": "EUR",
		"JP": "JPY", "IN": "INR", "BR": "BRL", "MX": "MXN",
		"KR": "KRW", "CN": "CNY",
	}
	if c, ok := codes[country]; ok {
		return c
	}
	return ""
}

// countryForLocale maps locale strings to country codes.
func countryForLocale(locale string) string {
	localeMap := map[string]string{
		"en-NG": "NG", "en-KE": "KE", "en-GH": "GH", "en-ZA": "ZA",
		"en-US": "US", "en-GB": "GB", "en-CA": "CA", "en-AU": "AU",
		"nigeria": "NG", "west_africa": "NG", "east_africa": "KE",
		"southern_africa": "ZA",
		"diaspora_us": "US", "diaspora_uk": "GB", "europe": "DE",
		"global": "", "formal_global": "",
	}
	if c, ok := localeMap[locale]; ok {
		return c
	}
	return ""
}

// localeForCountry maps country codes to locale strings.
func localeForCountry(country string) string {
	localeMap := map[string]string{
		"NG": "en-NG", "KE": "en-KE", "GH": "en-GH", "ZA": "en-ZA",
		"US": "en-US", "GB": "en-GB", "CA": "en-CA", "AU": "en-AU",
		"DE": "de-DE", "FR": "fr-FR", "JP": "ja-JP", "IN": "en-IN",
		"BR": "pt-BR", "MX": "es-MX", "KR": "ko-KR", "CN": "zh-CN",
	}
	if l, ok := localeMap[country]; ok {
		return l
	}
	return "en-US"
}
