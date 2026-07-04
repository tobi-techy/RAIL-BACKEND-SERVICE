package ramphub

import (
	"encoding/json"
	"testing"
)

// These fixtures are ACTUAL create-order responses captured from the RampHub
// sandbox and live APIs (POST /api/developer/orders, side=buy). They lock in
// the contract our onramp flow depends on:
//
//   - The fiat pay-in account is only ever returned in the create-order
//     response. It is NOT available from monitor-status, there is no
//     GET /orders/:id, and order-intent returns the crypto deposit address only.
//   - RampHub does NOT normalize providerDetails across providers. Paycrest puts
//     the account at providerDetails.virtualAccount (camelCase); UseBread nests
//     it under providerDetails.data.deposit (snake_case). OrderResponse.
//     PayInAccount reads both — regressing either shape reintroduces the blank
//     pay-in screen that UseBread-routed live orders hit in production.
const paycrestBuyResponse = `{
  "transactionId": "bc35219e-5f19-446c-996a-9d5c6dbdbcbb",
  "requestReference": "RH-SBX-B3E864D9",
  "side": "buy",
  "asset": "USDC",
  "chain": "solana",
  "selectedProvider": "Paycrest Sandbox",
  "bestRateUsed": 1416.65,
  "providerDetails": {
    "provider": "Paycrest Sandbox",
    "status": "awaiting_deposit",
    "reference": "RH-SBX-B3E864D9",
    "sandbox": true,
    "virtualAccount": {
      "accountName": "RampHub Sandbox Checkout",
      "accountNumber": "BXB3E864D9",
      "bankName": "RampHub Sandbox Bank"
    },
    "amountToPay": 10000,
    "expectedSettlementWindow": "Under 2 minutes",
    "note": "Sandbox order only. Do not send a real bank transfer."
  },
  "ourCryptoAddress": null,
  "environment": "test",
  "sandbox": true,
  "trackable": true
}`

// UseBread-style buy response captured from the LIVE API: the pay-in account is
// present, but nested under providerDetails.data.deposit with snake_case keys —
// NOT providerDetails.virtualAccount. Reading only virtualAccount (as the old
// code did) yields nil here, which is what produced the blank pay-in screen.
const usebreadBuyResponse = `{
  "transactionId": "851c478f-0cb2-4a8d-b2fc-eb9b85a87064",
  "requestReference": "RH-TX-053039CC",
  "side": "buy",
  "asset": "USDC",
  "chain": "solana",
  "selectedProvider": "UseBread",
  "bestRateUsed": 1388.387,
  "providerDetails": {
    "success": true,
    "message": "Onramp initiated successfully",
    "data": {
      "status": "AWAITING_DEPOSIT",
      "type": "ONRAMP",
      "deposit": {
        "amount": 2000,
        "expires_at": "2026-07-04T15:24:57.911Z",
        "account_number": "6029511823",
        "account_name": "Switchlabsltd Checkout",
        "bank_code": "090286",
        "bank_name": "Safehaven Microfinance Bank"
      }
    }
  },
  "ourCryptoAddress": null,
  "environment": "live",
  "sandbox": false,
  "trackable": true
}`

// A provider response with no pay-in account at all — the guard-and-fallback
// case (PayInAccount returns empty; CreateOnramp rejects the order).
const noAccountBuyResponse = `{
  "transactionId": "00000000-0000-0000-0000-000000000000",
  "side": "buy",
  "selectedProvider": "SomeProvider",
  "providerDetails": { "status": "AWAITING_DEPOSIT" }
}`

func TestPayInAccount_Paycrest(t *testing.T) {
	var resp OrderResponse
	if err := json.Unmarshal([]byte(paycrestBuyResponse), &resp); err != nil {
		t.Fatalf("unmarshal paycrest buy response: %v", err)
	}
	num, name, bank := resp.PayInAccount()
	if num != "BXB3E864D9" || bank != "RampHub Sandbox Bank" || name != "RampHub Sandbox Checkout" {
		t.Fatalf("paycrest pay-in mis-parsed: num=%q name=%q bank=%q", num, name, bank)
	}
}

func TestPayInAccount_UseBread(t *testing.T) {
	var resp OrderResponse
	if err := json.Unmarshal([]byte(usebreadBuyResponse), &resp); err != nil {
		t.Fatalf("unmarshal usebread buy response: %v", err)
	}
	// The old code read only providerDetails.virtualAccount (nil here) and broke.
	if resp.ProviderDetails.VirtualAccount != nil {
		t.Fatal("UseBread should not populate virtualAccount")
	}
	num, name, bank := resp.PayInAccount()
	if num != "6029511823" || bank != "Safehaven Microfinance Bank" || name != "Switchlabsltd Checkout" {
		t.Fatalf("usebread pay-in mis-parsed from data.deposit: num=%q name=%q bank=%q", num, name, bank)
	}
}

func TestPayInAccount_NoneProvided(t *testing.T) {
	var resp OrderResponse
	if err := json.Unmarshal([]byte(noAccountBuyResponse), &resp); err != nil {
		t.Fatalf("unmarshal no-account buy response: %v", err)
	}
	if num, _, bank := resp.PayInAccount(); num != "" || bank != "" {
		t.Fatalf("expected empty pay-in account, got num=%q bank=%q", num, bank)
	}
	if raw := extractProviderDetailsRaw([]byte(noAccountBuyResponse)); raw == "" {
		t.Fatal("expected providerDetails to be extractable for diagnostics")
	}
}
