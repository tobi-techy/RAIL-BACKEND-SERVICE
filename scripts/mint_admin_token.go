// +build ignore

package main

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/auth"
)

// Usage: go run scripts/mint_admin_token.go <user_id> <email>
// Requires JWT_SECRET env var
func main() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "JWT_SECRET env var required")
		os.Exit(1)
	}
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/mint_admin_token.go <user_id> <email>")
		os.Exit(1)
	}
	userID, err := uuid.Parse(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid user_id: %v\n", err)
		os.Exit(1)
	}
	email := os.Args[2]

	tokens, err := auth.GenerateTokenPair(userID, email, "super_admin", secret, 1440, 10080)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate token: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(tokens.AccessToken)
}
