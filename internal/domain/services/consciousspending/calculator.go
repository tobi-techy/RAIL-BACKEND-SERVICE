package consciousspending

import (
	"strings"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

const (
	BucketFixedCosts        = "fixed_costs"
	BucketInvestments       = "investments"
	BucketSavings           = "savings"
	BucketGuiltFreeSpending = "guilt_free_spending"
)

type ObservedAmount struct {
	Amount     decimal.Decimal `json:"amount"`
	Known      bool            `json:"known"`
	Source     string          `json:"source"`
	Confidence string          `json:"confidence"`
}

func NewObservedAmount(amount decimal.Decimal, source, confidence string) ObservedAmount {
	return ObservedAmount{
		Amount: amount, Known: true, Source: source, Confidence: confidence,
	}
}

type SnapshotInput struct {
	TakeHomeIncome    ObservedAmount
	FixedCosts        ObservedAmount
	Investments       ObservedAmount
	Savings           ObservedAmount
	GuiltFreeSpending ObservedAmount
	Currency          string
}

type BucketSnapshot struct {
	Name       string          `json:"name"`
	Amount     decimal.Decimal `json:"amount"`
	Percentage decimal.Decimal `json:"percentage"`
	Known      bool            `json:"known"`
	Source     string          `json:"source"`
	Confidence string          `json:"confidence"`
	RangeMin   decimal.Decimal `json:"range_min"`
	RangeMax   decimal.Decimal `json:"range_max"`
	Status     string          `json:"status"`
}

type Snapshot struct {
	Currency       string           `json:"currency"`
	TakeHomeIncome ObservedAmount   `json:"take_home_income"`
	Buckets        []BucketSnapshot `json:"buckets"`
	Complete       bool             `json:"complete"`
}

func CalculateSnapshot(in SnapshotInput) Snapshot {
	if !in.GuiltFreeSpending.Known && in.TakeHomeIncome.Known &&
		in.FixedCosts.Known && in.Investments.Known && in.Savings.Known {
		remainder := in.TakeHomeIncome.Amount.
			Sub(in.FixedCosts.Amount).
			Sub(in.Investments.Amount).
			Sub(in.Savings.Amount)
		if !remainder.IsNegative() {
			in.GuiltFreeSpending = ObservedAmount{
				Amount: remainder, Known: true, Source: "derived_remainder", Confidence: "medium",
			}
		}
	}

	specs := []struct {
		name        string
		observation ObservedAmount
		min, max    int64
	}{
		{BucketFixedCosts, in.FixedCosts, 50, 60},
		{BucketInvestments, in.Investments, 10, 10},
		{BucketSavings, in.Savings, 5, 10},
		{BucketGuiltFreeSpending, in.GuiltFreeSpending, 20, 35},
	}
	out := Snapshot{
		Currency:       strings.ToUpper(strings.TrimSpace(in.Currency)),
		TakeHomeIncome: in.TakeHomeIncome,
		Complete:       in.TakeHomeIncome.Known && in.TakeHomeIncome.Amount.IsPositive(),
	}
	for _, spec := range specs {
		bucket := BucketSnapshot{
			Name: spec.name, Amount: spec.observation.Amount, Known: spec.observation.Known,
			Source: spec.observation.Source, Confidence: spec.observation.Confidence,
			RangeMin: decimal.NewFromInt(spec.min), RangeMax: decimal.NewFromInt(spec.max),
			Status: "unknown",
		}
		if bucket.Known && in.TakeHomeIncome.Known && in.TakeHomeIncome.Amount.IsPositive() {
			bucket.Percentage = percentage(bucket.Amount, in.TakeHomeIncome.Amount)
			switch {
			case bucket.Percentage.LessThan(bucket.RangeMin):
				bucket.Status = "below_reference"
			case bucket.Percentage.GreaterThan(bucket.RangeMax):
				bucket.Status = "above_reference"
			default:
				bucket.Status = "within_reference"
			}
		} else {
			out.Complete = false
		}
		out.Buckets = append(out.Buckets, bucket)
	}
	return out
}

func Compare(plan *entities.ConsciousSpendingPlan, actual Snapshot, materialVariance decimal.Decimal) []Variance {
	if plan == nil || !actual.Complete {
		return nil
	}
	if !materialVariance.IsPositive() {
		materialVariance = decimal.NewFromInt(5)
	}
	targets := map[string]decimal.Decimal{
		BucketFixedCosts: plan.FixedCostsPct, BucketInvestments: plan.InvestmentsPct,
		BucketSavings: plan.SavingsPct, BucketGuiltFreeSpending: plan.GuiltFreeSpendingPct,
	}
	var variances []Variance
	for _, bucket := range actual.Buckets {
		target, ok := targets[bucket.Name]
		if !ok || !bucket.Known {
			continue
		}
		delta := bucket.Percentage.Sub(target)
		if delta.Abs().GreaterThanOrEqual(materialVariance) {
			variances = append(variances, Variance{
				Bucket: bucket.Name, ActualPct: bucket.Percentage, TargetPct: target, DeltaPct: delta,
			})
		}
	}
	return variances
}

type Variance struct {
	Bucket    string          `json:"bucket"`
	ActualPct decimal.Decimal `json:"actual_pct"`
	TargetPct decimal.Decimal `json:"target_pct"`
	DeltaPct  decimal.Decimal `json:"delta_pct"`
}
