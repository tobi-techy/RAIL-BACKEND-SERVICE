package travel

import (
	"testing"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/brij"
)

func TestBrijGender(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"m", "m"},
		{"male", "m"},
		{"Male", "m"},
		{"f", "f"},
		{"female", "f"},
		{"FEMALE", "f"},
		{"", ""},
		{"other", ""},
	}
	for _, tc := range cases {
		if got := brijGender(tc.in); got != tc.want {
			t.Errorf("brijGender(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestISOBornOn(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"04/12/1990", "1990-04-12"},
		{"04/12/1990 ", "1990-04-12"},
		{"1990-04-12", "1990-04-12"},
		{"1990-4-2", "1990-4-2"},
		{"", ""},
		{"not-a-date", "not-a-date"},
	}
	for _, tc := range cases {
		if got := isoBornOn(tc.in); got != tc.want {
			t.Errorf("isoBornOn(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestToFlightPassenger(t *testing.T) {
	p := TravelPassenger{
		FirstName: " Ada ",
		LastName:  "Lovelace",
		Title:     "Ms",
		Sex:       "Female",
		DOB:       "12/10/1815",
		Email:     "ada@example.com",
		Phone:     "+44 20 0000 0000",
	}
	got := p.ToFlightPassenger()
	want := brij.PassengerInput{
		GivenName:   "Ada",
		FamilyName:  "Lovelace",
		BornOn:      "1815-12-10",
		Title:       "ms",
		Gender:      "f",
		Email:       "ada@example.com",
		PhoneNumber: "+44 20 0000 0000",
	}
	if got != want {
		t.Errorf("ToFlightPassenger() = %+v, want %+v", got, want)
	}
}
