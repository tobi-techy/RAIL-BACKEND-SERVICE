package security

import (
	"encoding/json"
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

func TestActionSeverity(t *testing.T) {
	tests := []struct {
		action   entities.FraudRuleAction
		expected int
	}{
		{entities.RuleActionAllow, 0},
		{entities.RuleActionFlag, 1},
		{entities.RuleActionManualReview, 2},
		{entities.RuleActionBlock, 3},
		{entities.RuleActionFreeze, 4},
	}

	for _, tt := range tests {
		got := actionSeverity(tt.action)
		if got != tt.expected {
			t.Errorf("actionSeverity(%s) = %d, want %d", tt.action, got, tt.expected)
		}
	}
}

func TestRuleConditions_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		json string
		want entities.RuleConditions
	}{
		{
			name: "velocity rule",
			json: `{"event":"deposit","count_threshold":5,"window_seconds":3600}`,
			want: entities.RuleConditions{Event: "deposit", CountThreshold: 5, WindowSeconds: 3600},
		},
		{
			name: "amount rule with first_transaction",
			json: `{"min_amount":5000,"max_account_age_hours":24,"first_transaction":true}`,
			want: entities.RuleConditions{MinAmount: 5000, MaxAccountAgeHours: 24, FirstTransaction: true},
		},
		{
			name: "pattern fund_through",
			json: `{"pattern":"fund_through","withdrawal_ratio":0.8,"max_delay_seconds":3600}`,
			want: entities.RuleConditions{Pattern: "fund_through", WithdrawalRatio: 0.8, MaxDelaySeconds: 3600},
		},
		{
			name: "structuring",
			json: `{"pattern":"structuring","threshold":10000,"margin":500,"count":3,"window_hours":48}`,
			want: entities.RuleConditions{Pattern: "structuring", Threshold: 10000, Margin: 500, Count: 3, WindowHours: 48},
		},
		{
			name: "device rule",
			json: `{"max_accounts_per_device":3,"min_amount":1000,"max_device_age_hours":24}`,
			want: entities.RuleConditions{MaxAccountsPerDevice: 3, MinAmount: 1000, MaxDeviceAgeHours: 24},
		},
		{
			name: "custom hour rule",
			json: `{"min_amount":2000,"hour_start":1,"hour_end":5}`,
			want: entities.RuleConditions{MinAmount: 2000, HourStart: 1, HourEnd: 5},
		},
		{
			name: "invalid JSON returns zero struct",
			json: `{}`,
			want: entities.RuleConditions{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got entities.RuleConditions
			if err := json.Unmarshal([]byte(tt.json), &got); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("got %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestRuleConditions_InvalidJSON(t *testing.T) {
	var cond entities.RuleConditions
	err := json.Unmarshal([]byte(`{invalid`), &cond)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestAlertDetails_JSONRoundTrip(t *testing.T) {
	details := entities.AlertDetails{
		RuleName:       "test_rule",
		TriggeredValue: "5 deposits",
		ThresholdValue: "3",
		RiskFactors:    []string{"new_account", "high_amount"},
	}

	data, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var got entities.AlertDetails
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if got.RuleName != details.RuleName {
		t.Errorf("RuleName = %q, want %q", got.RuleName, details.RuleName)
	}
	if len(got.RiskFactors) != 2 {
		t.Errorf("RiskFactors len = %d, want 2", len(got.RiskFactors))
	}
}

func TestSanctionsMatchDetails_JSONRoundTrip(t *testing.T) {
	details := entities.SanctionsMatchDetails{
		Matches: []entities.SanctionsMatchEntry{
			{MatchedName: "John Doe", ListName: "OFAC", Score: 0.95, EntryID: "SDN-123"},
		},
	}

	data, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var got entities.SanctionsMatchDetails
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(got.Matches) != 1 || got.Matches[0].MatchedName != "John Doe" {
		t.Errorf("unexpected matches: %+v", got.Matches)
	}
}

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  John  Doe  ", "john doe"},
		{"JANE SMITH", "jane smith"},
		{"O'Brien-Smith", "obriensmith"},
		{"José García", "josé garcía"},
		{"", ""},
	}

	for _, tt := range tests {
		got := normalizeName(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestCalculateNameSimilarity(t *testing.T) {
	tests := []struct {
		a, b     string
		minScore float64
		maxScore float64
	}{
		{"john doe", "john doe", 1.0, 1.0},
		{"john doe", "jane doe", 0.3, 0.6},
		{"john doe", "completely different", 0.0, 0.1},
		{"", "john doe", 0.0, 0.0},
		{"john doe", "", 0.0, 0.0},
	}

	for _, tt := range tests {
		got := calculateNameSimilarity(tt.a, tt.b)
		if got < tt.minScore || got > tt.maxScore {
			t.Errorf("calculateNameSimilarity(%q, %q) = %f, want between %f and %f",
				tt.a, tt.b, got, tt.minScore, tt.maxScore)
		}
	}
}

func TestRuleEvalResult_Score(t *testing.T) {
	result := entities.RuleEvalResult{
		RuleName:  "test_rule",
		Triggered: true,
		Score:     1.5,
		Action:    entities.RuleActionBlock,
	}
	if result.Score != 1.5 {
		t.Errorf("Expected score 1.5, got %f", result.Score)
	}
}

func TestFraudRuleAlert_Entities(t *testing.T) {
	alert := entities.FraudRuleAlert{
		AlertType: "rule_trigger",
		Severity:  "critical",
		Status:    entities.AlertStatusOpen,
		Amount:    decimal.NewFromFloat(5000),
		Details:   entities.AlertDetails{RuleName: "test"},
	}
	if alert.Status != entities.AlertStatusOpen {
		t.Errorf("Expected status open, got %s", alert.Status)
	}
	if alert.Details.RuleName != "test" {
		t.Errorf("Expected rule name 'test', got %s", alert.Details.RuleName)
	}
}

func TestFundThroughDetection_Entities(t *testing.T) {
	detection := entities.FundThroughDetection{
		DepositAmount:      decimal.NewFromFloat(10000),
		WithdrawalAmount:   decimal.NewFromFloat(9500),
		TimeBetweenSeconds: 300,
		WithdrawalRatio:    0.95,
		RiskScore:          0.9,
		ActionTaken:        "frozen",
	}
	if detection.WithdrawalRatio < 0.9 {
		t.Errorf("Expected high withdrawal ratio, got %f", detection.WithdrawalRatio)
	}
}

func TestSanctionsStatus_Constants(t *testing.T) {
	if entities.SanctionsStatusClear != "clear" {
		t.Error("SanctionsStatusClear should be 'clear'")
	}
	if entities.SanctionsStatusConfirmedMatch != "confirmed_match" {
		t.Error("SanctionsStatusConfirmedMatch should be 'confirmed_match'")
	}
}
