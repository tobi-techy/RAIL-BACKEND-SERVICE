package security

import (
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

func TestGetFloat(t *testing.T) {
	m := map[string]interface{}{
		"float":   3.14,
		"int":     42,
		"missing": nil,
	}

	if v := getFloat(m, "float"); v != 3.14 {
		t.Errorf("getFloat(float) = %f, want 3.14", v)
	}
	if v := getFloat(m, "int"); v != 42.0 {
		t.Errorf("getFloat(int) = %f, want 42.0", v)
	}
	if v := getFloat(m, "nonexistent"); v != 0 {
		t.Errorf("getFloat(nonexistent) = %f, want 0", v)
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
	// Verify that triggered rules carry their weight as score
	rule := entities.FraudRule{
		ScoreWeight: 1.5,
		Action:      entities.RuleActionBlock,
	}

	result := entities.RuleEvalResult{
		RuleID:    rule.ID,
		RuleName:  "test_rule",
		Triggered: true,
		Score:     rule.ScoreWeight,
		Action:    rule.Action,
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
	}

	if alert.Status != entities.AlertStatusOpen {
		t.Errorf("Expected status open, got %s", alert.Status)
	}
	if !alert.Amount.Equal(decimal.NewFromFloat(5000)) {
		t.Errorf("Expected amount 5000, got %s", alert.Amount.String())
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
	if detection.ActionTaken != "frozen" {
		t.Errorf("Expected action frozen, got %s", detection.ActionTaken)
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
