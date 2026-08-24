package mono

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestInitiateLinkingResponseJSON(t *testing.T) {
	// Actual Mono API response from POST /v2/accounts/initiate
	raw := `{
		"status": "successful",
		"message": "Request was successfully completed",
		"timestamp": "2024-03-18T11:51:41.624Z",
		"data": {
			"mono_url": "https://link.mono.co/ALGSTO222222WE",
			"customer": "65f82acd00000003aa9028d",
			"meta": {"ref": "99008877TEST"},
			"scope": "auth",
			"redirect_url": "https://mono.co",
			"created_at": "2024-03-18T11:51:41.605Z"
		}
	}`

	var resp monoResponse[InitiateLinkingResponse]
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if resp.Data.MonoURL != "https://link.mono.co/ALGSTO222222WE" {
		t.Errorf("expected mono_url to be parsed, got %q", resp.Data.MonoURL)
	}
}

func TestAccountDetailsResponseJSON(t *testing.T) {
	// Actual Mono API response from GET /v2/accounts/{id}
	raw := `{
		"status": "successful",
		"message": "Request was succesfully completed",
		"timestamp": "2026-01-27T12:30:22.399Z",
		"data": {
			"account": {
				"_id": "6972325w62ta67u33f88840a",
				"name": "Samuel Olamide",
				"account_number": "1234567890",
				"currency": "NGN",
				"balance": 73573,
				"type": "SAVINGS",
				"bvn": "6115",
				"institution": {
					"name": "GTBank",
					"bank_code": "058",
					"type": "PERSONAL_BANKING"
				}
			},
			"customer": {"id": "682dd53a74682beb490a0ed4"},
			"meta": {
				"data_status": "AVAILABLE",
				"auth_method": "internet_banking",
				"retrieved_data": ["identity", "balance", "transactions"]
			}
		}
	}`

	var resp monoResponse[AccountDetailsResponse]
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	acct := resp.Data.Account
	if acct.ID != "6972325w62ta67u33f88840a" {
		t.Errorf("expected _id, got %q", acct.ID)
	}
	if acct.Name != "Samuel Olamide" {
		t.Errorf("expected name, got %q", acct.Name)
	}
	if acct.AccountNumber != "1234567890" {
		t.Errorf("expected account_number, got %q", acct.AccountNumber)
	}
	if acct.Balance != 73573 {
		t.Errorf("expected balance 73573, got %d", acct.Balance)
	}
	if acct.Currency != "NGN" {
		t.Errorf("expected currency NGN, got %q", acct.Currency)
	}
	if acct.Institution == nil {
		t.Fatal("expected institution to be parsed")
	}
	if acct.Institution.Name != "GTBank" {
		t.Errorf("expected institution.name GTBank, got %q", acct.Institution.Name)
	}
	if acct.Institution.BankCode != "058" {
		t.Errorf("expected institution.bank_code 058, got %q", acct.Institution.BankCode)
	}

	// Verify meta with data_status
	if resp.Data.Meta == nil {
		t.Fatal("expected meta to be parsed")
	}
	if resp.Data.Meta.DataStatus != "AVAILABLE" {
		t.Errorf("expected data_status AVAILABLE, got %q", resp.Data.Meta.DataStatus)
	}
	if len(resp.Data.Meta.RetrievedData) != 3 {
		t.Errorf("expected 3 retrieved_data items, got %d", len(resp.Data.Meta.RetrievedData))
	}
}

func TestInitiatePaymentResponseJSON(t *testing.T) {
	// Actual Mono API response from POST /v2/payments/initiate
	raw := `{
		"status": "successful",
		"message": "Payment Initiated Successfully",
		"timestamp": "2025-05-21T21:58:21.087Z",
		"data": {
			"id": "ODW2QV0WLIDG",
			"mono_url": "https://checkout.mono.co/ODW2QV0WLIDG",
			"type": "onetime-debit",
			"method": "transfer",
			"amount": 21000,
			"description": "Ticket",
			"reference": "ref03098",
			"customer": "67aa0961271cb661d8cbae3b",
			"institution": "5f2d08bf60b92e2888287704",
			"auth_method": "internet_banking",
			"redirect_url": "https://mono.co",
			"created_at": "2025-05-21T21:58:21.078Z",
			"updated_at": "2025-05-21T21:58:21.078Z",
			"meta": {},
			"liveMode": true
		}
	}`

	var resp monoResponse[InitiatePaymentResponse]
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	data := resp.Data
	if data.ID != "ODW2QV0WLIDG" {
		t.Errorf("expected id ODW2QV0WLIDG, got %q", data.ID)
	}
	if data.MonoURL != "https://checkout.mono.co/ODW2QV0WLIDG" {
		t.Errorf("expected mono_url, got %q", data.MonoURL)
	}
	if data.Amount != 21000 {
		t.Errorf("expected amount 21000, got %d", data.Amount)
	}
	if data.Reference != "ref03098" {
		t.Errorf("expected reference ref03098, got %q", data.Reference)
	}
	if data.Method != "transfer" {
		t.Errorf("expected method transfer, got %q", data.Method)
	}
}

func TestTransactionsResponseJSON(t *testing.T) {
	// The Mono API returns data as a bare JSON array, not wrapped in an object.
	raw := `{
		"status": "successful",
		"message": "Transaction retrieved successfully",
		"timestamp": "2024-04-12T06:18:17.117Z",
		"data": [
			{
				"id": "66141bbff58d2687e7d91234",
				"narration": "PG00001",
				"amount": 500,
				"type": "debit",
				"balance": 1500,
				"date": "2023-12-14T00:02:00.500Z",
				"category": "unknown"
			},
			{
				"id": "66141bbff58d2687e7d91235",
				"narration": "NIP TRANSFER",
				"amount": 1000,
				"type": "debit",
				"balance": 2000,
				"date": "2023-12-09T13:23:00.100Z",
				"category": "bank_charges"
			}
		],
		"meta": {
			"total": 307,
			"page": 1
		}
	}`

	// The client unmarshals into monoResponse[[]Transaction] since data is a bare array.
	var resp monoResponse[[]Transaction]
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(resp.Data))
	}

	tx := resp.Data[0]
	if tx.ID != "66141bbff58d2687e7d91234" {
		t.Errorf("expected id, got %q", tx.ID)
	}
	if tx.Amount != 500 {
		t.Errorf("expected amount 500, got %d", tx.Amount)
	}
	if tx.Type != "debit" {
		t.Errorf("expected type debit, got %q", tx.Type)
	}
	if tx.Description != "PG00001" {
		t.Errorf("expected narration as description, got %q", tx.Description)
	}
	if tx.Category != "unknown" {
		t.Errorf("expected category unknown, got %q", tx.Category)
	}
}

func TestWebhookEventJSON(t *testing.T) {
	// mono.events.account_connected
	raw := `{
		"event": "mono.events.account_connected",
		"event_id": "jU4ixom09P6eX2arBA3AmeiPyYalMk4WPdCaQ",
		"timestamp": "2026-01-27T21:00:39.588Z",
		"data": {
			"id": "6979274350b9c321c14524b1",
			"customer": "6961439d7716b67eba2d068a",
			"meta": {
				"data_status": "PROCESSING",
				"auth_method": "internet_banking",
				"ref": "4055877-T"
			}
		}
	}`

	var event WebhookEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if event.Event != "mono.events.account_connected" {
		t.Errorf("expected event name, got %q", event.Event)
	}
	// account_connected puts the account ID in data.id
	if event.Data.ID != "6979274350b9c321c14524b1" {
		t.Errorf("expected id from data.id, got %q", event.Data.ID)
	}
}

func TestWebhookAccountUpdatedJSON(t *testing.T) {
	raw := `{
		"event": "mono.events.account_updated",
		"event_id": "tDZCfxYASzx75YtXe3zbrgNzQtohDd6AE2BwWRvKf4",
		"timestamp": "2026-01-27T21:00:38.856Z",
		"data": {
			"account": {
				"_id": "697926f050B9c321c1451dda",
				"name": "Samuel Olamide",
				"accountNumber": "0131883461",
				"currency": "NGN",
				"balance": 22967,
				"type": "Tier 3 Savings Account",
				"bvn": "9422",
				"authMethod": "internet_banking",
				"institution": {
					"name": "ALAT by WEMA",
					"bankCode": "035",
					"type": "PERSONAL_BANKING"
				}
			},
			"meta": {
				"data_status": "AVAILABLE",
				"auth_method": "internet_banking",
				"retrieved_data": ["identity", "balance", "transactions"]
			}
		}
	}`

	var event WebhookEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if event.Event != "mono.events.account_updated" {
		t.Errorf("expected event name, got %q", event.Event)
	}
	acct := event.Data.AccountObject()
	if acct == nil {
		t.Fatal("expected account to be parsed")
	}
	if acct.ID != "697926f050B9c321c1451dda" {
		t.Errorf("expected account _id, got %q", acct.ID)
	}
	// Webhook uses camelCase for accountNumber (unlike REST API which uses snake_case)
	if acct.AccountNumber != "0131883461" {
		t.Errorf("expected accountNumber (camelCase), got %q", acct.AccountNumber)
	}
	if acct.Institution == nil {
		t.Fatal("expected institution to be parsed")
	}
	if acct.Institution.Name != "ALAT by WEMA" {
		t.Errorf("expected institution.name, got %q", acct.Institution.Name)
	}
	if acct.Institution.BankCode != "035" {
		t.Errorf("expected institution bankCode 035, got %q", acct.Institution.BankCode)
	}
	if event.Data.Meta == nil {
		t.Fatal("expected meta to be parsed")
	}
	if event.Data.Meta.DataStatus != "AVAILABLE" {
		t.Errorf("expected data_status AVAILABLE, got %q", event.Data.Meta.DataStatus)
	}
}

func TestWebhookIncomeJSON(t *testing.T) {
	raw := `{
		"event": "mono.events.account_income",
		"data": {
			"account": "66605869b806c997c5d21234",
			"account_name": "SAMUEL OLAMIDE",
			"account_number": "0129141234",
			"income_summary": {
				"total_income": 0,
				"employer": ""
			},
			"income_streams": [
				{
					"income_type": "WAGES",
					"frequency": "VARIABLE",
					"monthly_average": 6532000,
					"average_income_amount": 4450666,
					"stability": 0.16,
					"first_income_date": "2023-07-25",
					"last_income_date": "2024-05-13",
					"last_income_amount": 3000000,
					"last_income_description": "transfer from piggyvest",
					"periods_with_income": 5,
					"number_of_incomes": 8,
					"number_of_months": 5
				}
			],
			"annual_income": 600704393,
			"monthly_income": 35335553
		}
	}`

	var event WebhookEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if event.Event != "mono.events.account_income" {
		t.Errorf("expected event name, got %q", event.Event)
	}
	// Income webhook has data.account as a string ID
	if event.Data.AccountIDStr() != "66605869b806c997c5d21234" {
		t.Errorf("expected account ID string, got %q", event.Data.AccountIDStr())
	}
	if event.Data.AccountName != "SAMUEL OLAMIDE" {
		t.Errorf("expected account_name, got %q", event.Data.AccountName)
	}
	if event.Data.AnnualIncome != 600704393 {
		t.Errorf("expected annual_income, got %d", event.Data.AnnualIncome)
	}
	if len(event.Data.IncomeStreams) != 1 {
		t.Fatalf("expected 1 income stream, got %d", len(event.Data.IncomeStreams))
	}
	stream := event.Data.IncomeStreams[0]
	if stream.IncomeType != "WAGES" {
		t.Errorf("expected income_type WAGES, got %q", stream.IncomeType)
	}
	if stream.Stability != 0.16 {
		t.Errorf("expected stability 0.16, got %f", stream.Stability)
	}
	if stream.MonthlyAverage != 6532000 {
		t.Errorf("expected monthly_average, got %d", stream.MonthlyAverage)
	}
}

func TestWebhookIncomeJSON_FractionalAverageIncome(t *testing.T) {
	raw := `{
		"event": "mono.events.account_income",
		"data": {
			"account": "66605869b806c997c5d21234",
			"income_streams": [
				{
					"income_type": "WAGES",
					"average_income_amount": 4450666.67
				}
			]
		}
	}`

	var event WebhookEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(event.Data.IncomeStreams) != 1 {
		t.Fatalf("expected 1 income stream, got %d", len(event.Data.IncomeStreams))
	}
	want := decimal.RequireFromString("4450666.67")
	if !event.Data.IncomeStreams[0].AverageIncomeAmount.Equal(want) {
		t.Errorf("average_income_amount = %s, want %s", event.Data.IncomeStreams[0].AverageIncomeAmount, want)
	}
}

func TestWebhookDirectDebitPayment(t *testing.T) {
	raw := `{
		"event": "direct_debit.payment_successful",
		"data": {
			"reference": "ref03098",
			"status": "successful"
		}
	}`

	var event WebhookEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if event.Event != "direct_debit.payment_successful" {
		t.Errorf("expected event name, got %q", event.Event)
	}
	if event.Data.Reference != "ref03098" {
		t.Errorf("expected reference, got %q", event.Data.Reference)
	}
}

func TestErrorResponseMethods(t *testing.T) {
	err := &ErrorResponse{StatusCode: 404, Message: "not found"}
	if !err.IsNotFound() {
		t.Error("404 should be NotFound")
	}
	if err.IsRetryable() {
		t.Error("404 should not be Retryable")
	}

	err5xx := &ErrorResponse{StatusCode: 500, Message: "server error"}
	if !err5xx.IsRetryable() {
		t.Error("500 should be Retryable")
	}

	err429 := &ErrorResponse{StatusCode: 429, Message: "rate limited"}
	if !err429.IsRateLimited() {
		t.Error("429 should be RateLimited")
	}
	if !err429.IsRetryable() {
		t.Error("429 should be Retryable")
	}
}

// Verify that time fields parse correctly from the Mono API's ISO 8601 format.
func TestTimeParsing(t *testing.T) {
	raw := `{"date": "2023-12-14T00:02:00.500Z"}`
	var s struct {
		Date time.Time `json:"date"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("time parse failed: %v", err)
	}
	expected := "2023-12-14 00:02:00.5 +0000 UTC"
	if s.Date.String() != expected {
		t.Errorf("expected %q, got %q", expected, s.Date.String())
	}
}
