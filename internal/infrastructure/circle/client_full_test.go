package circle

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

func TestCircleWalletCreationFull(t *testing.T) {
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

	fmt.Println("=== Circle Wallet Creation Full Test ===")

	// Create Circle client
	config := Config{
		APIKey:                 apiKey,
		BaseURL:                "https://api.circle.com",
		EntitySecretCiphertext: entitySecret,
		PublicKeyPEM:           publicKeyPEM,
	}

	client := NewClient(config, logger)
	ctx := context.Background()

	// First, try to get existing wallet set (or create new one)
	fmt.Println("\n1. Getting/creating wallet set...")

	// Use a specific wallet set ID for testing, or create new
	walletSetID := os.Getenv("CIRCLE_WALLET_SET_ID")

	if walletSetID == "" {
		// Create new wallet set
		fmt.Println("   No wallet set ID, creating new one...")
		setName := fmt.Sprintf("RAIL-Test-%s", uuid.New().String()[:8])
		wsResp, err := client.CreateWalletSet(ctx, setName, "")
		if err != nil {
			t.Fatalf("Failed to create wallet set: %v", err)
		}
		walletSetID = wsResp.WalletSet.ID
		fmt.Printf("   ✅ Created wallet set: %s\n", walletSetID)
	} else {
		fmt.Printf("   Using existing wallet set: %s\n", walletSetID)
	}

	// Now create a wallet
	fmt.Println("\n2. Creating wallet...")

	// IMPORTANT: Each request must generate a NEW ciphertext
	// The Circle client should do this automatically via getEntitySecretCiphertext()
	req := entities.CircleWalletCreateRequest{
		WalletSetID: walletSetID,
		Blockchains: []string{"SOL-DEVNET"},
		AccountType: "EOA",
		Count:       1,
	}

	resp, err := client.CreateWallet(ctx, req)
	if err != nil {
		t.Fatalf("Failed to create wallet: %v", err)
	}

	fmt.Printf("   ✅ Wallet created: %s\n", resp.Wallet.ID)
	fmt.Printf("   Address: %s\n", resp.Wallet.Address)
	fmt.Printf("   Blockchain: %s\n", resp.Wallet.Blockchain)

	fmt.Println("\n=== Test Complete ===")
}
