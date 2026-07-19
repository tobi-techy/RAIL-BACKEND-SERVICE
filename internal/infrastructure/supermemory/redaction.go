package supermemory

import "regexp"

// PII redaction for anything leaving the process toward Supermemory.
//
// This is deliberately conservative for a fintech memory system: it must strip
// genuinely sensitive identifiers (emails, phones, card PANs, SSNs, keyword-anchored
// account numbers) WITHOUT destroying the financial signal Miriam relies on -
// transaction amounts (e.g. "25000"), compact dates (e.g. "20250115"), and unix
// timestamps. The previous implementation redacted every 8-17 digit run, which
// silently corrupted amounts and dates; the account rule below is keyword-anchored
// and the card rule targets true 13-19 digit PANs (optionally space/dash grouped).
var (
	emailRE = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	// Phone: require an explicit international prefix or separators/parentheses so a
	// bare block of digits (an amount) is not mistaken for a phone number.
	phoneRE = regexp.MustCompile(`(?:\+\d{1,3}[-.\s]?)?(?:\(\d{3}\)|\d{3})[-.\s]\d{3}[-.\s]\d{4}\b`)
	ssnRE   = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	// Card PAN: 13-19 digits, optionally grouped in 4s by spaces/dashes.
	cardRE = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
	// Account number: only when explicitly labelled, so plain amounts survive.
	accountRE = regexp.MustCompile(`(?i)\b(?:a/?c(?:count)?|acct)\.?\s*(?:no\.?|number|#|:)?\s*\d{6,17}\b`)
	// Nigerian mobile numbers lead with 0 or +234, so they never collide with
	// transaction amounts (amounts do not lead with a zero).
	ngMobileRE = regexp.MustCompile(`(?:\+234|\b0)\d{10}\b`)
)

// RedactPII replaces emails, phone numbers, card/account numbers, and SSNs with
// placeholders while preserving transaction amounts and dates.
func RedactPII(text string) string {
	if text == "" {
		return text
	}
	text = emailRE.ReplaceAllString(text, "[email redacted]")
	text = ssnRE.ReplaceAllString(text, "[ssn redacted]")
	text = accountRE.ReplaceAllString(text, "[account redacted]")
	text = cardRE.ReplaceAllString(text, "[card redacted]")
	text = ngMobileRE.ReplaceAllString(text, "[phone redacted]")
	text = phoneRE.ReplaceAllString(text, "[phone redacted]")
	return text
}
