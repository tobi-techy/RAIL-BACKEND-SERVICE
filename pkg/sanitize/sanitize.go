package sanitize

import (
	"html"
	"regexp"
	"strings"
)

var newlinePattern = regexp.MustCompile(`[\r\n]`)

func String(s string) string {
	return html.EscapeString(strings.TrimSpace(s))
}

func LogString(s string) string {
	return newlinePattern.ReplaceAllString(s, " ")
}

func Email(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func AlphaNumeric(s string) string {
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		}
	}
	return result.String()
}
