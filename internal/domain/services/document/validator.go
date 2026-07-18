package document

import "github.com/shopspring/decimal"

// RuleValidator applies deterministic consistency checks. This is the most
// important stage: it catches OCR errors like "$1880" vs "$18.80" before any
// data is trusted or sent to the LLM.
type RuleValidator struct {
	// tolerance is the absolute rounding slack allowed on balance/total math.
	tolerance decimal.Decimal
}

// NewRuleValidator builds a validator with a sensible rounding tolerance.
func NewRuleValidator() *RuleValidator {
	return &RuleValidator{tolerance: decimal.NewFromFloat(1.0)}
}

// Validate implements Validator.
func (v *RuleValidator) Validate(doc DocType, f *Fields) *Validation {
	switch doc {
	case DocReceipt, DocInvoice:
		return v.validateReceipt(f)
	case DocBankStatement:
		return v.validateStatement(f)
	default:
		return &Validation{Passed: false, Confidence: 0, Errors: []string{"unknown document type"}}
	}
}

func (v *RuleValidator) validateReceipt(f *Fields) *Validation {
	res := &Validation{Passed: true, Confidence: 1.0}

	if f.Total == nil || !f.Total.IsPositive() {
		res.Passed = false
		res.Confidence = 0
		res.Errors = append(res.Errors, "missing or non-positive total")
		return res
	}

	// If subtotal + tax are present, they must reconcile with total.
	if f.Subtotal != nil && f.Tax != nil {
		expected := f.Subtotal.Add(*f.Tax)
		if expected.Sub(*f.Total).Abs().GreaterThan(v.tolerance) {
			res.Passed = false
			res.Confidence = 0.3
			res.Errors = append(res.Errors, "subtotal + tax does not equal total")
		}
	} else if f.Subtotal != nil {
		// Subtotal alone should never exceed total.
		if f.Subtotal.Sub(*f.Total).GreaterThan(v.tolerance) {
			res.Passed = false
			res.Confidence = 0.3
			res.Errors = append(res.Errors, "subtotal exceeds total")
		}
	}

	// Line-item sum sanity check (only when we have prices).
	if sum, ok := sumItems(f.Items); ok {
		// Items usually sum to subtotal (pre-tax). Allow either subtotal or total.
		target := f.Total
		if f.Subtotal != nil {
			target = f.Subtotal
		}
		if sum.Sub(*target).Abs().GreaterThan(target.Mul(decimal.NewFromFloat(0.1))) {
			// Off by >10% — likely an OCR misread on an item price.
			res.Confidence = minFloat(res.Confidence, 0.6)
			res.Errors = append(res.Errors, "line items do not sum close to total")
		}
	}

	return res
}

func (v *RuleValidator) validateStatement(f *Fields) *Validation {
	res := &Validation{Passed: true, Confidence: 1.0}

	// Core invariant: opening + credits - debits = closing.
	if f.OpeningBalance != nil && f.ClosingBalance != nil && len(f.Transactions) > 0 {
		credits, debits := decimal.Zero, decimal.Zero
		for _, t := range f.Transactions {
			if t.Type == "credit" {
				credits = credits.Add(t.Amount)
			} else {
				debits = debits.Add(t.Amount)
			}
		}
		expected := f.OpeningBalance.Add(credits).Sub(debits)
		if expected.Sub(*f.ClosingBalance).Abs().GreaterThan(v.tolerance) {
			res.Passed = false
			res.Confidence = 0.4
			res.Errors = append(res.Errors, "opening + credits - debits does not equal closing balance")
		}
	}

	if len(f.Transactions) == 0 {
		res.Passed = false
		res.Confidence = 0
		res.Errors = append(res.Errors, "no transactions extracted")
	}

	return res
}

func sumItems(items []LineItem) (decimal.Decimal, bool) {
	if len(items) == 0 {
		return decimal.Zero, false
	}
	sum := decimal.Zero
	any := false
	for _, it := range items {
		if it.Price == "" {
			continue
		}
		p, ok := parseAmount(it.Price)
		if !ok {
			continue
		}
		q := int64(1)
		if it.Quantity > 0 {
			q = int64(it.Quantity)
		}
		sum = sum.Add(p.Mul(decimal.NewFromInt(q)))
		any = true
	}
	return sum, any
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
