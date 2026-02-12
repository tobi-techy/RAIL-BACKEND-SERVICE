package util

import (
    "crypto/sha256"
    "encoding/hex"
)

// Redact returns a deterministic SHA-256 hash of the input string.
// It is used to avoid logging raw PII while still allowing correlation of logs.
func Redact(input string) string {
    if input == "" {
        return ""
    }
    h := sha256.Sum256([]byte(input))
    return hex.EncodeToString(h[:])
}
