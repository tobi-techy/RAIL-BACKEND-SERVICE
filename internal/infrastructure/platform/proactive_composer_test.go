package platform

import "testing"

func TestValidProactiveRewrite(t *testing.T) {
	draft := "Moved ₦12,500 from Stash to Spending for upcoming bills."
	out := "quick heads up, ₦12,500 is in Spending for the bills coming up."
	if !validProactiveRewrite(draft, out) {
		t.Fatal("expected a concise number-preserving rewrite to pass")
	}
}

func TestValidProactiveRewriteRejectsUnsafeOutput(t *testing.T) {
	draft := "Your food spending is 25% above the usual ₦12,500."
	cases := map[string]string{
		"adds a number":  "your food spending is 25% above usual, by ₦13,000.",
		"drops a number": "your food spending is above the usual amount.",
		"uses bullet":    "• your food spending is 25% above the usual ₦12,500.",
		"names Miriam":   "Miriam noticed your food spending is 25% above ₦12,500.",
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			if validProactiveRewrite(draft, out) {
				t.Fatalf("expected %q to be rejected", out)
			}
		})
	}
}

func TestSameNumberTokensNormalizesCurrencyWhitespace(t *testing.T) {
	if !sameNumberTokens("Moved ₦ 12,500.", "₦12,500 moved.") {
		t.Fatal("expected currency whitespace normalization to preserve tokens")
	}
}
