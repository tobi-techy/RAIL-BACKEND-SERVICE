package ramphub

import (
	"encoding/json"
	"testing"
)

// These fixtures are the ACTUAL create-order responses captured from the
// RampHub sandbox (POST /api/developer/orders, side=buy). They lock in the
// contract our onramp flow depends on:
//
//   - The fiat pay-in account is only ever returned inline in the create-order
//     response under providerDetails.virtualAccount. It is NOT available from
//     monitor-status, there is no GET /orders/:id, and order-intent returns the
//     crypto deposit address only. So if a provider omits virtualAccount here,
//     there is no way to recover it — the order is unusable for a bank-transfer
//     UX and CreateOnramp must treat it as a failure.
//   - Paycrest returns virtualAccount inline (usable). UseBread does not, which
//     is why UseBread-routed onramp orders produced a blank pay-in screen in
//     production.
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

// UseBread-style buy response: routed and accepted, but providerDetails carries
// no virtualAccount — mirrors the documented UseBread sample and the production
// order that dead-ended on a blank "transfer to" screen.
const usebreadBuyResponse = `{
  "transactionId": "95abed94-8d42-479b-96ba-e545e35c3ade",
  "requestReference": "RH-TX-AB12CD34",
  "side": "buy",
  "asset": "USDC",
  "chain": "solana",
  "selectedProvider": "UseBread",
  "bestRateUsed": 1388.39,
  "providerDetails": {
    "status": "AWAITING_DEPOSIT"
  },
  "ourCryptoAddress": null,
  "environment": "test",
  "sandbox": true,
  "trackable": true
}`

func TestParseBuyResponse_PaycrestHasInlineVirtualAccount(t *testing.T) {
	var resp OrderResponse
	if err := json.Unmarshal([]byte(paycrestBuyResponse), &resp); err != nil {
		t.Fatalf("unmarshal paycrest buy response: %v", err)
	}
	va := resp.ProviderDetails.VirtualAccount
	if va == nil {
		t.Fatal("expected inline virtualAccount, got nil")
	}
	if va.AccountNumber != "BXB3E864D9" || va.BankName != "RampHub Sandbox Bank" || va.AccountName != "RampHub Sandbox Checkout" {
		t.Fatalf("virtual account mis-parsed: %+v", va)
	}
	if resp.Side != "buy" {
		t.Fatalf("side = %q, want buy", resp.Side)
	}
}

func TestParseBuyResponse_UseBreadHasNoVirtualAccount(t *testing.T) {
	var resp OrderResponse
	if err := json.Unmarshal([]byte(usebreadBuyResponse), &resp); err != nil {
		t.Fatalf("unmarshal usebread buy response: %v", err)
	}
	if resp.ProviderDetails.VirtualAccount != nil {
		t.Fatalf("expected nil virtualAccount for UseBread, got %+v", resp.ProviderDetails.VirtualAccount)
	}
	// This is the exact condition CreateOnramp uses to reject an unusable order,
	// and the case extractProviderDetailsRaw is logged against for diagnosis.
	if raw := extractProviderDetailsRaw([]byte(usebreadBuyResponse)); raw == "" {
		t.Fatal("expected providerDetails to be extractable for diagnostics")
	}
}
