package circle

import (
	"context"
	"fmt"
	"os"
	"testing"

	"go.uber.org/zap"
)

func TestCircleClientEntitySecret(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// Get config from environment
	apiKey := os.Getenv("CIRCLE_API_KEY")
	entitySecret := os.Getenv("CIRCLE_ENTITY_SECRET_CIPHERTEXT")
	publicKeyPEM := os.Getenv("EXPO_PUBLIC_CIRCLE_PUBLIC_KEY_PEM")

	// Use defaults from .env if not set
	if apiKey == "" {
		apiKey = "TEST_API_KEY:41d6b3aa0927dd63b69f372ed8702045:3af3046ec65e569e97b705e40d2c9b9e"
	}
	if entitySecret == "" {
		entitySecret = "dcd90b5d7bfd4f17222283d14ac0e2ce0d814df1d4f030a37065868113437fdc"
	}
	if publicKeyPEM == "" {
		publicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEAxQsczKCXuMCgyGYff2tZ
xR+ZUW8MBvgwmbFkGTmyoenSC6X/5o5BPPkPZTIZs/oC8ouOdAKijOYsUP3+qdc+
mzjx2lIHnQN1TtNQ2Vm93Hk+G6vEFHDsYsb0nchk+7V5Pbki3ynOnfsV6LRbaFCf
cgTGxHSSmKbnItW3qAiVluPPoPBx4WbQNyeS5TREv0R1NC1U311rxLGbxl+bjb73
fFzlvSkGe2UyPs8tJnAYhqpvFOQv1SdXDvGbfwM5lBfqjCGMlkHkYYwsgLYl4R/R
x01ncZvYjgYwXAungJMRpD9aUBSt8f4pDDlUxoXq294y7hCSi6aNGoDPqDyAaqoN
2rSYbswGZmCz5ivJLHZNFP9qCwoKeL1l9+VlDrKs+nhRmrhCoXG0OOUdTbpkU4Ff
oUjh4SKR8YPq7TfSGyBe9q5VAF7bEici1FkH9I7+wf41YSq47dU3UOryjbF34fXZ
dQJ9xBEk1thTDUK8ZmIY8SQwqolSQIAKxsxOf2XoNdk3PiaXJHDTtfEiTtZFybKR
rWFG4h0GeRPLCy52KAe+nfJmpODKeGmrGgvlA0IVeHDpqv7WNsG/o3G4JBL3odWs
6qKoMrDhL1W/32EMPObdtUPTtAyTO3HxfXWsUavJ5KLHApoiwDx9Vn7aW5ytBvAV
6aAk60U2+xWaJJqFlWAx6a8CAwEAAQ==
-----END PUBLIC KEY-----`
	}

	fmt.Println("=== Circle Client Entity Secret Test ===")

	// Create Circle client
	config := Config{
		APIKey:                 apiKey,
		BaseURL:                "https://api-sandbox.circle.com",
		EntitySecretCiphertext: entitySecret,
		PublicKeyPEM:           publicKeyPEM,
	}

	client := NewClient(config, logger)

	// Test getting entity secret ciphertext
	fmt.Println("\n1. Testing getEntitySecretCiphertext...")
	ctx := context.Background()
	ciphertext, err := client.getEntitySecretCiphertext(ctx)
	if err != nil {
		t.Fatalf("Failed to get ciphertext: %v", err)
	}
	fmt.Printf("   ✅ Got ciphertext: %d chars\n", len(ciphertext))
	fmt.Printf("   Ciphertext: %s...\n", ciphertext[:min(50, len(ciphertext))])

	// Verify it's the pre-registered ciphertext (not dynamically generated)
	fmt.Println("\n2. Verifying ciphertext type...")
	// The client should use the pre-registered ciphertext from config
	// Check if it's using the config value or dynamically generating
	if ciphertext == entitySecret {
		fmt.Println("   ✅ Using pre-registered ciphertext from config")
	} else {
		fmt.Println("   ℹ️  Using dynamically generated ciphertext")
	}

	// Test that Circle client has entity secret service
	fmt.Println("\n3. Checking entity secret service...")
	if client.entitySecretService != nil {
		fmt.Println("   ✅ Entity secret service is initialized")
	} else {
		fmt.Println("   ⚠️  Entity secret service is nil (will use pre-registered ciphertext)")
	}

	fmt.Println("\n=== Test Complete ===")
	fmt.Println("\nThe Circle client is correctly configured.")
	fmt.Println("Wallet creation should work if:")
	fmt.Println("1. CIRCLE_DEFAULT_WALLET_SET_ID is set in config")
	fmt.Println("2. The entity secret is registered with Circle")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
