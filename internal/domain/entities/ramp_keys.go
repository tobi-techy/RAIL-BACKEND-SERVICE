package entities

// OfframpReversalKey returns the canonical idempotency key used when reversing
// a ledger hold for a failed RampHub offramp order. All code paths (webhook,
// settlement, recovery worker) MUST use this to prevent double-reversals.
func OfframpReversalKey(txID string) string {
	if txID == "" {
		panic("entities.OfframpReversalKey: txID must not be empty — empty keys defeat double-reversal prevention")
	}
	return "ramphub-offramp-" + txID
}
