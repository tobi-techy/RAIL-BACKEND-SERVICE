package common

import (
    "crypto/sha256"
    "encoding/hex"
)

// RedactPII returns a SHA-256 hash of the input string.
// It is used to avoid logging raw personally identifiable information.
func RedactPII(s string) string {
    if s == "" {
        return ""
    }
    h := sha256.Sum256([]byte(s))
    return hex.EncodeToString(h[:])
}
