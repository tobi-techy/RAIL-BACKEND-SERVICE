package confirmation

import (
	"fmt"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

func strPayload(p map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := p[k]; ok && v != nil {
			if s := fmt.Sprintf("%v", v); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

// investBuyRenderer: "Buy GOOGL" / "Fractional · market".
func investBuyRenderer(_ entities.ConfirmationAction, p map[string]any) (title, subtitle, amount, asset, dest, fee, risk string) {
	sym := strPayload(p, "symbol", "asset", "ticker")
	if sym == "" {
		sym = "asset"
	}
	title = "Buy " + sym
	qty := strPayload(p, "quantity", "qty", "shares")
	notional := strPayload(p, "notional", "amount", "amount_usd")
	venue := strPayload(p, "venue", "market")
	if venue == "" {
		venue = "market"
	}
	frac := strPayload(p, "fractional")
	if frac == "" {
		frac = "Fractional"
	}
	if qty != "" && notional != "" {
		amount = qty + " sh · " + notional
	} else if notional != "" {
		amount = notional
	} else if qty != "" {
		amount = qty + " sh"
	}
	subtitle = frac + " · " + venue
	return title, subtitle, amount, sym, "", strPayload(p, "fee"), "Orders adjust target allocation — no price guarantee."
}

// transferSendRenderer: "Send ₦50,000" / "To Funsho · OPay".
func transferSendRenderer(_ entities.ConfirmationAction, p map[string]any) (title, subtitle, amount, asset, dest, fee, risk string) {
	amount = strPayload(p, "amount", "amount_ngn", "amount_usd")
	to := strPayload(p, "to_name", "recipient", "to")
	rail := strPayload(p, "rail", "channel", "bank")
	if amount != "" {
		title = "Send " + amount
	} else {
		title = "Send money"
	}
	if to != "" && rail != "" {
		subtitle = "To " + to + " · " + rail
	} else if to != "" {
		subtitle = "To " + to
	} else {
		subtitle = "P2P transfer"
	}
	return title, subtitle, amount, strPayload(p, "currency", "asset"), to, strPayload(p, "fee"), "Irreversible once sent — confirm the recipient."
}

// saveSweepRenderer: "Park 15% of inflow" / "Every inflow · Stash".
func saveSweepRenderer(_ entities.ConfirmationAction, p map[string]any) (title, subtitle, amount, asset, dest, fee, risk string) {
	pct := strPayload(p, "percentage", "percent", "pct")
	amount = strPayload(p, "amount")
	if pct != "" {
		title = "Park " + pct + " of inflow"
		amount = pct
	} else if amount != "" {
		title = "Park " + amount + " per inflow"
	} else {
		title = "Park inflow to savings"
	}
	trigger := strPayload(p, "trigger", "when")
	if trigger == "" {
		trigger = "Every inflow"
	}
	dest = strPayload(p, "destination", "to")
	if dest == "" {
		dest = "Stash"
	}
	subtitle = trigger + " · " + dest
	return title, subtitle, amount, "", dest, "", "Only future inflows are affected."
}
func limitChangeRenderer(_ entities.ConfirmationAction, p map[string]any) (title, subtitle, amount, asset, dest, fee, risk string) {
	scope := strPayload(p, "scope", "period")
	if scope == "" {
		scope = "weekly"
	}
	from := strPayload(p, "from", "old_limit", "current")
	to := strPayload(p, "to", "new_limit", "limit")
	dir := "Change"
	if f, t := from != "", to != ""; f && t {
		subtitle = from + " → " + to
	} else if t {
		subtitle = "New cap " + to
	} else {
		subtitle = scope + " cap"
	}
	_ = dir
	title = "Raise " + scope + " cap"
	if d := strPayload(p, "direction"); d == "lower" || d == "decrease" {
		title = "Lower " + scope + " cap"
	}
	return title, subtitle, to, "", "", "", "Applies to new activity after approval."
}
