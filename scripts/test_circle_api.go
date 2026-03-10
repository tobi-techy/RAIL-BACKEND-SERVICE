// Deprecated: Circle API tests are legacy. Use Bridge integration tests instead.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/entity_secret"
	"go.uber.org/zap"
)

func main() {
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

	fmt.Println("=== Circle API Wallet Set Creation Test ===")
	fmt.Printf("API Key: %s...\n", apiKey[:20])
	fmt.Printf("Entity Secret: %s...\n", entitySecret[:20])

	// Generate entity secret ciphertext
	fmt.Println("\n1. Generating entity secret ciphertext...")
	entitySecretService, err := entitysecret.NewService(logger, entitySecret, publicKeyPEM)
	if err != nil {
		fmt.Printf("   ❌ Failed to create entity secret service: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	ciphertext, err := entitySecretService.GenerateEntitySecretCiphertext(ctx)
	if err != nil {
		fmt.Printf("   ❌ Failed to generate ciphertext: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   ✅ Generated ciphertext: %d chars\n", len(ciphertext))

	// Test 1: Try to list wallet sets
	fmt.Println("\n2. Listing wallet sets...")
	listResp, listErr := listWalletSets(apiKey)
	if listErr != nil {
		fmt.Printf("   ❌ List wallet sets failed: %v\n", listErr)
	} else {
		fmt.Printf("   ✅ Found %d wallet sets\n", len(listResp.Data.WalletSets))
		for _, ws := range listResp.Data.WalletSets {
			fmt.Printf("      - %s (ID: %s)\n", ws.Name, ws.ID)
		}
	}

	// Test 2: Try to create a wallet set
	fmt.Println("\n3. Creating wallet set...")
	createResp, createErr := createWalletSet(apiKey, ciphertext, "RAIL-Test-Set-"+time.Now().Format("20060102"))
	if createErr != nil {
		fmt.Printf("   ❌ Create wallet set failed: %v\n", createErr)
		fmt.Println("\n   This error likely means:")
		fmt.Println("   - The entity secret is NOT registered with Circle")
		fmt.Println("   - OR the entity secret ciphertext is invalid")
		fmt.Println("\n   To fix:")
		fmt.Println("   1. Go to Circle Web3 Console → Wallet Settings → Entity Secret")
		fmt.Println("   2. Register your entity secret: " + entitySecret)
	} else {
		fmt.Printf("   ✅ Wallet set created: %s\n", createResp.Data.WalletSet.ID)
		fmt.Printf("   Name: %s\n", createResp.Data.WalletSet.Name)

		// Test 3: Create a wallet in this set
		fmt.Println("\n4. Creating wallet in the set...")
		walletResp, walletErr := createWallet(apiKey, ciphertext, createResp.Data.WalletSet.ID, "SOL-DEVNET")
		if walletErr != nil {
			fmt.Printf("   ❌ Create wallet failed: %v\n", walletErr)
		} else {
			fmt.Printf("   ✅ Wallet created: %s\n", walletResp.Data.Wallets[0].ID)
			fmt.Printf("   Address: %s\n", walletResp.Data.Wallets[0].Address)
			fmt.Printf("   Blockchain: %s\n", walletResp.Data.Wallets[0].Blockchain)
		}
	}
}

// Helper functions to call Circle API directly

func listWalletSets(apiKey string) (*ListWalletSetsResponse, error) {
	req, _ := http.NewRequest("GET", "https://api.circle.com/v1/w3s/developer/walletSets", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result ListWalletSetsResponse
	json.Unmarshal(body, &result)
	return &result, nil
}

func createWalletSet(apiKey, ciphertext, name string) (*CreateWalletSetResponse, error) {
	payload := map[string]interface{}{
		"entitySecretCiphertext": ciphertext,
		"idempotencyKey":         uuid.New().String(),
		"name":                   name,
	}

	bodyBytes, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://api.circle.com/v1/w3s/developer/walletSets", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result CreateWalletSetResponse
	json.Unmarshal(body, &result)
	return &result, nil
}

func createWallet(apiKey, ciphertext, walletSetID, blockchain string) (*CreateWalletResponse, error) {
	payload := map[string]interface{}{
		"entitySecretCiphertext": ciphertext,
		"idempotencyKey":         uuid.New().String(),
		"walletSetId":            walletSetID,
		"blockchains":            []string{blockchain},
		"accountType":            "EOA",
	}

	bodyBytes, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://api.circle.com/v1/w3s/developer/wallets", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result CreateWalletResponse
	json.Unmarshal(body, &result)
	return &result, nil
}

// Types

type ListWalletSetsResponse struct {
	Data struct {
		WalletSets []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			CustodyType string `json:"custodyType"`
		} `json:"walletSets"`
	} `json:"data"`
}

type CreateWalletSetResponse struct {
	Data struct {
		WalletSet struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			CustodyType string `json:"custodyType"`
		} `json:"walletSet"`
	} `json:"data"`
}

type CreateWalletResponse struct {
	Data struct {
		Wallets []struct {
			ID         string `json:"id"`
			Address    string `json:"address"`
			Blockchain string `json:"blockchain"`
		} `json:"wallets"`
	} `json:"data"`
}
