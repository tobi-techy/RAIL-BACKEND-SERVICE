package travel

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/brij"
	"github.com/shopspring/decimal"
)

func TestRefundReason(t *testing.T) {
	cases := []struct {
		name   string
		intent *brij.BookingIntent
		want   string
	}{
		{"airline reason", &brij.BookingIntent{RefundReason: "schedule change"}, "schedule change"},
		{"blank reason", &brij.BookingIntent{RefundReason: "  "}, "the airline refunded this booking"},
		{"nil intent", nil, "the airline refunded this booking"},
	}
	for _, tc := range cases {
		if got := refundReason(tc.intent); got != tc.want {
			t.Errorf("%s: refundReason() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRefundResolvedMessage(t *testing.T) {
	order := &orderRow{
		Route:      "LOS to MAN",
		HoldAmount: decimal.NewFromFloat(71.40),
	}
	msg := refundResolvedMessage(order, "the airline refunded this booking")
	for _, want := range []string{"LOS to MAN", "$71.40", "Spend"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refundResolvedMessage() = %q, missing %q", msg, want)
		}
	}
	if !strings.Contains(msg, "the airline refunded this booking") {
		t.Errorf("refundResolvedMessage() = %q, missing airline reason", msg)
	}

	noRoute := refundResolvedMessage(&orderRow{HoldAmount: decimal.NewFromFloat(10)}, "")
	if !strings.Contains(noRoute, "your flight") {
		t.Errorf("refundResolvedMessage() with no route = %q, missing fallback", noRoute)
	}
}

func TestHoldReleasedMessage(t *testing.T) {
	order := &orderRow{
		ID:         uuid.New(),
		Route:      "ABV to LOS",
		HoldAmount: decimal.NewFromFloat(50),
	}
	msg := holdReleasedMessage(order, "the booking never went through")
	for _, want := range []string{"ABV to LOS", "$50.00", "the booking never went through"} {
		if !strings.Contains(msg, want) {
			t.Errorf("holdReleasedMessage() = %q, missing %q", msg, want)
		}
	}
}

func TestRailFee(t *testing.T) {
	s := &Service{cfg: Config{DeveloperFeePercent: 5}}
	escrow := decimal.NewFromFloat(67.60)
	fee := s.railFee(escrow)
	if !fee.Equal(decimal.NewFromFloat(3.38)) {
		t.Errorf("railFee(67.60) = %s, want 3.38", fee.String())
	}
	total := escrow.Add(fee)
	if !total.Equal(decimal.NewFromFloat(70.98)) {
		t.Errorf("escrow + fee = %s, want 70.98", total.String())
	}

	zero := s.railFee(decimal.Zero)
	if !zero.IsZero() {
		t.Errorf("railFee(0) = %s, want 0", zero.String())
	}
}
