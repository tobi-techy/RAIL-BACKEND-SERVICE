package consciousspending

import (
	"strings"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

const (
	BucketFixedCosts         = "fixed_cost"
	BucketInvestments        = "investment"
	BucketSavings            = "savings"
	BucketGuiltFreeSpending  = "guilt_free_spending"
	BucketPreTaxInvestments  = "pre_tax_investments"
	BufferBucketMiscellaneous = "miscellaneous_buffer"
)

type EvidenceCoverage struct {
	Known      bool      `json:"known"`
	Confidence string    `json:"confidence"`
	Missing    []string  `json:"missing,omitempty"`
	Since      time.Time `json:"since,omitempty"`
}

type ObservedAmount struct {
	Amount     decimal.Decimal `json:"amount"`
	Known      bool            `json:"known"`
	Source     string          `json:"source"`
	Confidence string          `json:"confidence"`
	Missing    []string        `json:"missing,omitempty"`
}

func NewObservedAmount(amount decimal.Decimal, source, confidence string, missing ...string) ObservedAmount {
	return ObservedAmount{Amount: amount, Known: true, Source: source, Confidence: confidence, Missing: missing}
}

type SnapshotInput struct {
	TakeHomeIncome         ObservedAmount
	PreTaxInvestments      ObservedAmount
	FixedCosts             ObservedAmount
	Investments            ObservedAmount
	Savings                ObservedAmount
	GuiltFreeSpending      ObservedAmount
	Currency               string
	Coverage               EvidenceCoverage
}

type BucketSnapshot struct {
	Name       string          `json:"name"`
	Amount     decimal.Decimal `json:"amount"`
	Percentage decimal.Decimal `json:"percentage"`
	Known      bool            `json:"known"`
	Source     string          `json:"source"`
	Confidence string          `json:"confidence"`
	ReferenceMin decimal.Decimal `json:"reference_min"`
	ReferenceMax decimal.Decimal `json:"reference_max"`
	Status     string          `json:"status"`
	Missing    []string        `json:"missing,omitempty"`
}

type Snapshot struct {
	Currency       string           `json:"currency"`
	TakeHomeIncome ObservedAmount   `json:"take_home_income"`
	Buckets        []BucketSnapshot `json:"buckets"`
	Complete       bool             `json:"complete"`
	Coverage       EvidenceCoverage `json:"coverage"`
}

type Variance struct {
	Bucket         string          `json:"bucket"`
	ActualAmount   decimal.Decimal `json:"actual_amount"`
	TargetAmount   decimal.Decimal `json:"target_amount"`
	DeltaAmount    decimal.Decimal `json:"delta_amount"`
	DeltaPct       decimal.Decimal `json:"delta_pct"`
	TargetPct      decimal.Decimal `json:"target_pct"`
	ActualPct      decimal.Decimal `json:"actual_pct"`
	EvidenceKnown  bool            `json:"evidence_known"`
	MissingSources []string        `json:"missing_sources,omitempty"`
}

func CalculateSnapshot(in SnapshotInput) Snapshot {
	out := Snapshot{
		Currency:       strings.ToUpper(strings.TrimSpace(in.Currency)),
		TakeHomeIncome: in.TakeHomeIncome,
		Complete:       in.TakeHomeIncome.Known && in.TakeHomeIncome.Amount.IsPositive(),
		Coverage:       in.Coverage,
	}
	if !out.Coverage.Known && !out.Coverage.Since.IsZero() {
		out.Coverage.Missing = append(out.Coverage.Missing, "monthly_activity")
	}
	if len(out.Coverage.Missing) == 0 {
		out.Coverage.Missing = nil
	}

	deriveGuiltFree := !in.GuiltFreeSpending.Known &&
		in.TakeHomeIncome.Known &&
		in.FixedCosts.Known &&
		in.Investments.Known &&
		in.Savings.Known &&
		!in.TakeHomeIncome.Amount.IsZero()

	if deriveGuiltFree {
		remainder := in.TakeHomeIncome.Amount.Sub(in.FixedCosts.Amount).Sub(in.Investments.Amount).Sub(in.Savings.Amount)
		if remainder.IsNegative() {
			out.Coverage.Missing = append(out.Coverage.Missing, "negative_derived_guilt_free")
		}
		in.GuiltFreeSpending = ObservedAmount{
			Amount: remainder, Known: true, Source: "derived_remainder", Confidence: "medium",
		}
	}

	bucketSpecs := []struct {
		name       string
		observed   ObservedAmount
		refMinPct  decimal.Decimal
		refMaxPct  decimal.Decimal
		missingKey string
	}{
		{BucketFixedCosts, in.FixedCosts, decimal.NewFromFloat(0.50), decimal.NewFromFloat(0.60), "recurring_fixed_costs"},
		{BucketPreTaxInvestments, in.PreTaxInvestments, decimal.Zero, decimal.Zero, "payroll_pre_tax_investments"},
		{BucketInvestments, in.Investments, decimal.NewFromFloat(0.05), decimal.NewFromFloat(0.10), "investment_contributions"},
		{BucketSavings, in.Savings, decimal.NewFromFloat(0.05), decimal.NewFromFloat(0.10), "savings_goals"},
		{BucketGuiltFreeSpending, in.GuiltFreeSpending, decimal.NewFromFloat(0.20), decimal.NewFromFloat(0.35), "guilt_free_classification"},
	}
	income := in.TakeHomeIncome.Amount
	for _, spec := range bucketSpecs {
		bucket := BucketSnapshot{
			Name: spec.name, Amount: spec.observed.Amount, Known: spec.observed.Known,
			Source: spec.observed.Source, Confidence: spec.observed.Confidence,
			Missing: append([]string(nil), spec.observed.Missing...),
		}
		if spec.refMinPct.IsPositive() || spec.refMaxPct.IsPositive() {
			bucket.ReferenceMin = spec.refMinPct
			bucket.ReferenceMax = spec.refMaxPct
		}
		if !income.IsZero() {
			bucket.Percentage = percentage(bucket.Amount, income)
		}
		if !bucket.Known || !income.IsPositive() {
			out.Complete = false
			out.Coverage.Missing = append(out.Coverage.Missing, spec.missingKey)
			bucket.Status = "unknown"
		} else if spec.refMaxPct.IsZero() {
			bucket.Status = "reference_note"
		} else if bucket.Percentage.LessThan(spec.refMinPct) {
			bucket.Status = "reference_below"
		} else if bucket.Percentage.GreaterThan(spec.refMaxPct) {
			bucket.Status = "reference_above"
		} else {
			bucket.Status = "reference_within"
		}
		out.Buckets = append(out.Buckets, bucket)
	}
	if len(out.Coverage.Missing) == 0 {
		out.Coverage.Missing = nil
	}
	return out
}

func Compare(plan *entities.ConsciousSpendingPlan, actual Snapshot, materialVariance decimal.Decimal) ([]Variance, bool) {
	if plan == nil || !actual.Complete {
		return nil, actual.Coverage.Known
	}
	if !materialVariance.IsPositive() {
		materialVariance = decimal.NewFromFloat(0.05)
	}
	targets := map[string]decimal.Decimal{
		BucketFixedCosts: plan.FixedCosts, BucketInvestments: plan.PostTaxInvestments,
		BucketSavings: plan.Savings, BucketGuiltFreeSpending: plan.GuiltFreeSpending,
	}
	var variances []Variance
	for _, bucket := range actual.Buckets {
		target, ok := targets[bucket.Name]
		if !ok || !bucket.Known {
			continue
		}
		delta := bucket.Amount.Sub(target)
		deltaPct := decimal.Zero
		if !actual.TakeHomeIncome.Amount.IsZero() {
			deltaPct = delta.Div(actual.TakeHomeIncome.Amount).Round(6)
		}
		if delta.Abs().GreaterThanOrEqual(materialVariance) {
			variances = append(variances, Variance{
				Bucket: bucket.Name, ActualAmount: bucket.Amount, TargetAmount: target,
				DeltaAmount: delta, DeltaPct: deltaPct,
				ActualPct: bucket.Percentage, TargetPct: target.Div(actual.TakeHomeIncome.Amount).Mul(decimal.NewFromInt(100)).Round(4),
				EvidenceKnown: actual.Coverage.Known,
				MissingSources: append([]string(nil), bucket.Missing...),
			})
		}
	}
	return variances, true
}

type AdherenceResult struct {
	Plan           *entities.ConsciousSpendingPlan
	Snapshot       Snapshot
	Variances      []Variance
	Decision       string
	CoverageKnown  bool
	RecoveryPrompt string
}

func DecideAdherence(result AdherenceResult) string {
	if !result.CoverageKnown {
		return "ask_missing_evidence"
	}
	if len(result.Variances) == 0 {
		return "on_track"
	}
	return "review_variance"
}

func percentage(amount, income decimal.Decimal) decimal.Decimal {
	if !income.IsPositive() {
		return decimal.Zero
	}
	return amount.Div(income).Mul(decimal.NewFromInt(100)).Round(4)
}

