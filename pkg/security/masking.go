package security

import (
	"regexp"
	"strings"
)

var (
	// Patterns for sensitive data
	emailPattern    = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	phonePattern    = regexp.MustCompile(`\+?[0-9]{10,15}`)
	cardPattern     = regexp.MustCompile(`[0-9]{13,19}`)
	ssnPattern      = regexp.MustCompile(`[0-9]{3}-?[0-9]{2}-?[0-9]{4}`)
	jwtPattern      = regexp.MustCompile(`eyJ[a-zA-Z0-9_-]*\.eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*`)
	apiKeyPattern   = regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|token|password|auth)["\s:=]+["']?([a-zA-Z0-9_-]{16,})["']?`)
	walletPattern   = regexp.MustCompile(`0x[a-fA-F0-9]{40}`)
	
	// Sensitive field names
	sensitiveFields = []string{
		"password", "passcode", "secret", "token", "key", "auth",
		"ssn", "social_security", "tax_id", "card_number", "cvv", "cvc",
		"pin", "private_key", "seed", "mnemonic", "api_key", "apikey",
		"access_token", "refresh_token", "bearer", "credential",
	}
)

// MaskString masks sensitive patterns in a string
func MaskString(s string) string {
	// Mask emails
	s = emailPattern.ReplaceAllStringFunc(s, maskEmail)
	
	// Mask phone numbers
	s = phonePattern.ReplaceAllString(s, "***-***-****")
	
	// Mask card numbers
	s = cardPattern.ReplaceAllStringFunc(s, maskCardNumber)
	
	// Mask SSN
	s = ssnPattern.ReplaceAllString(s, "***-**-****")
	
	// Mask JWTs
	s = jwtPattern.ReplaceAllString(s, "eyJ***REDACTED***")
	
	// Mask API keys
	s = apiKeyPattern.ReplaceAllString(s, "$1: ***REDACTED***")
	
	// Mask wallet addresses (partial)
	s = walletPattern.ReplaceAllStringFunc(s, maskWalletAddress)
	
	return s
}

// MaskMap masks sensitive fields in a map
func MaskMap(data map[string]interface{}) map[string]interface{} {
	masked := make(map[string]interface{})
	for k, v := range data {
		if isSensitiveField(k) {
			masked[k] = "***REDACTED***"
			continue
		}
		
		switch val := v.(type) {
		case string:
			masked[k] = MaskString(val)
		case map[string]interface{}:
			masked[k] = MaskMap(val)
		case []interface{}:
			masked[k] = maskSlice(val)
		default:
			masked[k] = v
		}
	}
	return masked
}

// MaskEmail masks an email address
func maskEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "***@***.***"
	}
	
	local := parts[0]
	domain := parts[1]
	
	maskedLocal := maskPartial(local, 2)
	domainParts := strings.Split(domain, ".")
	if len(domainParts) > 1 {
		maskedDomain := maskPartial(domainParts[0], 1) + "." + domainParts[len(domainParts)-1]
		return maskedLocal + "@" + maskedDomain
	}
	
	return maskedLocal + "@" + maskPartial(domain, 2)
}

// MaskPhoneNumber masks a phone number
func MaskPhoneNumber(phone string) string {
	if len(phone) < 4 {
		return "****"
	}
	return strings.Repeat("*", len(phone)-4) + phone[len(phone)-4:]
}

// MaskCardNumber masks a card number showing only last 4 digits
func maskCardNumber(card string) string {
	if len(card) < 4 {
		return "****"
	}
	return strings.Repeat("*", len(card)-4) + card[len(card)-4:]
}

// MaskWalletAddress masks a wallet address showing first 6 and last 4 chars
func maskWalletAddress(addr string) string {
	if len(addr) < 10 {
		return "0x****"
	}
	return addr[:6] + "..." + addr[len(addr)-4:]
}

// MaskAPIKey masks an API key showing only first 4 chars
func MaskAPIKey(key string) string {
	if len(key) < 4 {
		return "****"
	}
	return key[:4] + strings.Repeat("*", len(key)-4)
}

func maskPartial(s string, showChars int) string {
	if len(s) <= showChars {
		return strings.Repeat("*", len(s))
	}
	return s[:showChars] + strings.Repeat("*", len(s)-showChars)
}

func isSensitiveField(field string) bool {
	lower := strings.ToLower(field)
	for _, sensitive := range sensitiveFields {
		if strings.Contains(lower, sensitive) {
			return true
		}
	}
	return false
}

func maskSlice(slice []interface{}) []interface{} {
	masked := make([]interface{}, len(slice))
	for i, v := range slice {
		switch val := v.(type) {
		case string:
			masked[i] = MaskString(val)
		case map[string]interface{}:
			masked[i] = MaskMap(val)
		default:
			masked[i] = v
		}
	}
	return masked
}

// SanitizeForLog prepares data for safe logging
func SanitizeForLog(data interface{}) interface{} {
	switch v := data.(type) {
	case string:
		return MaskString(v)
	case map[string]interface{}:
		return MaskMap(v)
	case []interface{}:
		return maskSlice(v)
	default:
		return data
	}
}

// RedactHeaders removes sensitive headers
func RedactHeaders(headers map[string][]string) map[string]string {
	redacted := make(map[string]string)
	sensitiveHeaders := []string{"authorization", "x-api-key", "cookie", "set-cookie"}
	
	for k, v := range headers {
		lower := strings.ToLower(k)
		for _, sensitive := range sensitiveHeaders {
			if strings.Contains(lower, sensitive) {
				redacted[k] = "***REDACTED***"
				continue
			}
		}
		if len(v) > 0 {
			redacted[k] = v[0]
		}
	}
	return redacted
}
