package repositories

import (
	"encoding/json"
	"time"
)

// Shared helpers for the investment (Glider) repositories. JSONB columns are
// carried as []byte so the nullable ones can round-trip a real SQL NULL.

// investmentEncodeJSON marshals a value for a JSONB column. A nil interface is
// encoded as SQL NULL (nil bytes), which the caller only uses for nullable
// columns.
func investmentEncodeJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

// investmentDecodeJSON unmarshals a JSONB column. Empty/NULL bytes and a JSON
// literal "null" are treated as "leave the zero value alone".
func investmentDecodeJSON(data []byte, dest any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, dest)
}

// investmentNonNullJSON guarantees a value for a NOT NULL JSONB column, falling
// back to the supplied literal when the caller has nothing to store.
func investmentNonNullJSON(data []byte, fallback string) []byte {
	if len(data) == 0 {
		return []byte(fallback)
	}
	return data
}

// investmentNullString maps an empty string to SQL NULL. This matters for the
// partial unique indexes on idempotency_key: an empty string is a live value
// and would collide across rows, while NULL is not indexed.
func investmentNullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// investmentTimeOrNow returns the supplied timestamp, or UTC now when unset.
func investmentTimeOrNow(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}
