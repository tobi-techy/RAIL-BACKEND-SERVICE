package integration

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"
)

// getEnvOrSkip returns environment variable value or skips the test if not found
func getEnvOrSkip(t *testing.T, key string) string {
	value := os.Getenv(key)
	if value == "" {
		t.Skipf("Environment variable %s is required for integration tests", key)
	}
	return value
}

// generateTestEmail generates a unique test email
func generateTestEmail() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomNum := rng.Intn(10000)
	return fmt.Sprintf("test-%d-%d@example.com", time.Now().UnixNano(), randomNum)
}