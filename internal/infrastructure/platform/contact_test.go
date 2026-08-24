package platform

import "testing"

func TestParseVCard_NamePhoneEmailCountry(t *testing.T) {
	raw := "BEGIN:VCARD\r\n" +
		"VERSION:3.0\r\n" +
		"FN:Ada Okafor\r\n" +
		"N:Okafor;Ada;;;\r\n" +
		"TEL;TYPE=CELL:+2348012345678\r\n" +
		"EMAIL;TYPE=INTERNET:ada@example.com\r\n" +
		"ADR;TYPE=HOME:;;12 Broad St;Lagos;LA;;Nigeria\r\n" +
		"END:VCARD\r\n"

	got := ParseVCard(raw)
	if got.FirstName != "Ada" {
		t.Errorf("FirstName = %q, want Ada", got.FirstName)
	}
	if got.LastName != "Okafor" {
		t.Errorf("LastName = %q, want Okafor", got.LastName)
	}
	if got.FormattedName != "Ada Okafor" {
		t.Errorf("FormattedName = %q", got.FormattedName)
	}
	if len(got.Phones) != 1 || got.Phones[0] != "+2348012345678" {
		t.Errorf("Phones = %#v", got.Phones)
	}
	if got.PrimaryEmail() != "ada@example.com" {
		t.Errorf("email = %q", got.PrimaryEmail())
	}
	if got.Country != "Nigeria" {
		t.Errorf("Country = %q, want Nigeria", got.Country)
	}
}

func TestParseVCard_FoldedLine(t *testing.T) {
	raw := "BEGIN:VCARD\nFN:Ada\nTEL:+234\n 8012345678\nEND:VCARD\n"
	got := ParseVCard(raw)
	if got.PrimaryPhone() != "+2348012345678" {
		t.Errorf("folded TEL = %q", got.PrimaryPhone())
	}
}

func TestParseVCard_FNOnly(t *testing.T) {
	got := ParseVCard("BEGIN:VCARD\nFN:Bola Ahmed\nEND:VCARD")
	if got.FirstNameResolved() != "Bola" {
		t.Errorf("FirstNameResolved = %q, want Bola", got.FirstNameResolved())
	}
}

func TestInferCountryFromPhone(t *testing.T) {
	cases := map[string]string{
		"+2348012345678": "NG",
		"+233201234567":  "GH",
		"+447700900123":  "GB",
		"+15551234567":   "US",
		"08012345678":    "",
	}
	for in, want := range cases {
		if got := inferCountryFromPhone(in); got != want {
			t.Errorf("inferCountryFromPhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	got := maskPhone("+2348012345678")
	if got != "+234801234…" {
		t.Errorf("maskPhone = %q", got)
	}
}

func TestIsContactReject(t *testing.T) {
	yes := []string{"no", "that's not me", "someone else", "friend's card"}
	no := []string{"yes", "that's me", "ok", "Ada"}
	for _, s := range yes {
		if !isContactReject(s) {
			t.Errorf("isContactReject(%q) = false", s)
		}
	}
	for _, s := range no {
		if isContactReject(s) {
			t.Errorf("isContactReject(%q) = true", s)
		}
	}
}
