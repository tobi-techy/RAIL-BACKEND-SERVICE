package platform

import (
	"strings"
)

// SharedContact is a contact card the user shared over iMessage (Spectrum
// `content.type === "contact"` or a raw vCard attachment).
type SharedContact struct {
	FirstName     string   `json:"first_name,omitempty"`
	LastName      string   `json:"last_name,omitempty"`
	FormattedName string   `json:"formatted_name,omitempty"`
	Phones        []string `json:"phones,omitempty"`
	Emails        []string `json:"emails,omitempty"`
	Country       string   `json:"country,omitempty"`
}

// FirstNameResolved returns the best first name from the card.
func (c *SharedContact) FirstNameResolved() string {
	if c == nil {
		return ""
	}
	if n := parseFirstName(c.FirstName); n != "" {
		return n
	}
	if n := parseFirstName(c.FormattedName); n != "" {
		return n
	}
	return ""
}

// PrimaryPhone returns the first usable phone on the card.
func (c *SharedContact) PrimaryPhone() string {
	if c == nil {
		return ""
	}
	for _, p := range c.Phones {
		if strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// PrimaryEmail returns the first valid email on the card.
func (c *SharedContact) PrimaryEmail() string {
	if c == nil {
		return ""
	}
	for _, e := range c.Emails {
		if got := normalizeEmail(e); got != "" {
			return got
		}
	}
	return ""
}

// ParseVCard extracts name, phones, emails, and country from a vCard 2.1/3.0/4.0
// payload. Unknown properties are ignored. Empty input returns a zero contact.
func ParseVCard(raw string) SharedContact {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SharedContact{}
	}
	// Strip UTF-8 BOM and unfold folded lines (CRLF + space/tab).
	raw = strings.TrimPrefix(raw, "\ufeff")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = unfoldVCard(raw)

	var out SharedContact
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "BEGIN:VCARD") || strings.EqualFold(line, "END:VCARD") {
			continue
		}
		name, value := splitVCardLine(line)
		if name == "" || value == "" {
			continue
		}
		base, params := splitVCardName(name)
		switch strings.ToUpper(base) {
		case "FN":
			out.FormattedName = unescapeVCard(value)
		case "N":
			// Family;Given;Additional;Prefix;Suffix
			parts := strings.Split(value, ";")
			if len(parts) > 1 {
				if g := unescapeVCard(parts[1]); g != "" && out.FirstName == "" {
					out.FirstName = g
				}
			}
			if len(parts) > 0 {
				if f := unescapeVCard(parts[0]); f != "" && out.LastName == "" {
					out.LastName = f
				}
			}
		case "TEL":
			phone := strings.TrimSpace(unescapeVCard(value))
			if phone != "" {
				out.Phones = append(out.Phones, phone)
			}
			_ = params
		case "EMAIL":
			email := unescapeVCard(value)
			if email != "" {
				out.Emails = append(out.Emails, email)
			}
		case "ADR":
			// PO Box;Extended;Street;Locality;Region;Postal;Country
			parts := strings.Split(value, ";")
			if len(parts) >= 7 {
				if cc := unescapeVCard(parts[6]); cc != "" && out.Country == "" {
					out.Country = cc
				}
			}
		}
	}
	if out.FirstName == "" {
		out.FirstName = parseFirstName(out.FormattedName)
	}
	return out
}

func unfoldVCard(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' && i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
			i++ // skip newline; next iteration writes the rest without the fold space
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func splitVCardLine(line string) (name, value string) {
	// First unquoted colon separates name/params from value.
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", ""
	}
	return line[:idx], line[idx+1:]
}

func splitVCardName(name string) (base string, params string) {
	if i := strings.IndexByte(name, ';'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return name, ""
}

func unescapeVCard(s string) string {
	s = strings.ReplaceAll(s, "\\n", " ")
	s = strings.ReplaceAll(s, "\\,", ",")
	s = strings.ReplaceAll(s, "\\;", ";")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return strings.TrimSpace(s)
}

// dialCodePref is longest-prefix-first so +1 maps to US rather than racing CA.
var dialCodePref = []struct{ iso, dial string }{
	{"NG", "234"},
	{"GH", "233"},
	{"KE", "254"},
	{"ZA", "27"},
	{"GB", "44"},
	{"US", "1"},
	{"CA", "1"},
}

// inferCountryFromPhone returns an ISO alpha-2 from a leading E.164 dial code.
func inferCountryFromPhone(phone string) string {
	digits := digitsOnly(phone)
	if strings.HasPrefix(digits, "00") {
		digits = digits[2:]
	}
	bestCode := ""
	bestLen := 0
	for _, d := range dialCodePref {
		if strings.HasPrefix(digits, d.dial) && len(d.dial) > bestLen {
			bestLen = len(d.dial)
			bestCode = d.iso
		}
	}
	return bestCode
}

// maskPhone shows enough of a number to recognise it without dumping the whole thing.
func maskPhone(phone string) string {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ""
	}
	runes := []rune(phone)
	if len(runes) <= 8 {
		return phone
	}
	return string(runes[:len(runes)-4]) + "…"
}

func isContactReject(text string) bool {
	s := strings.ToLower(strings.TrimSpace(text))
	s = strings.Trim(s, ".!")
	switch s {
	case "no", "nope", "nah", "wrong", "not me", "that's not me", "thats not me",
		"friend", "someone else", "not mine", "different":
		return true
	}
	if strings.Contains(s, "not me") || strings.Contains(s, "someone else") ||
		strings.Contains(s, "friend") && strings.Contains(s, "card") {
		return true
	}
	return false
}
