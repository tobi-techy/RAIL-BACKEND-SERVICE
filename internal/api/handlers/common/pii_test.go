package common

import (
    "testing"
)

func TestRedactPII(t *testing.T) {
    input := "test@example.com"
    hashed := RedactPII(input)
    if len(hashed) != 64 { // SHA-256 hex length
        t.Fatalf("expected length 64, got %d", len(hashed))
    }
    // Ensure deterministic
    expected := RedactPII(input)
    if hashed != expected {
        t.Fatalf("hash is not deterministic")
    }
}
