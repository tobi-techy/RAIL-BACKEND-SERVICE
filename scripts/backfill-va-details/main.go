package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"go.uber.org/zap"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	bridgeKey := os.Getenv("BRIDGE_API_KEY")
	if dbURL == "" || bridgeKey == "" {
		log.Fatal("DATABASE_URL and BRIDGE_API_KEY required")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	logger, _ := zap.NewProduction()
	client := bridge.NewClient(bridge.Config{APIKey: bridgeKey, Environment: "production"}, logger)
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, `
		SELECT id, bridge_customer_id, bridge_account_id
		FROM virtual_accounts
		WHERE bridge_account_id IS NOT NULL
		  AND (bank_address = '' OR bank_address IS NULL)
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	var updated int
	for rows.Next() {
		var id, customerID, bridgeAccountID string
		if err := rows.Scan(&id, &customerID, &bridgeAccountID); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}

		va, err := client.GetVirtualAccount(ctx, customerID, bridgeAccountID)
		if err != nil {
			log.Printf("bridge fetch %s: %v", id, err)
			continue
		}

		sdi := va.SourceDepositInstructions
		_, err = db.ExecContext(ctx, `
			UPDATE virtual_accounts
			SET bank_address = $1, beneficiary_address = $2, payment_rails = $3,
			    bank_name = COALESCE(NULLIF(bank_name, ''), $4),
			    beneficiary_name = COALESCE(NULLIF(beneficiary_name, ''), $5),
			    account_number = COALESCE(NULLIF(account_number, ''), $6),
			    routing_number = COALESCE(NULLIF(routing_number, ''), $7)
			WHERE id = $8
		`, sdi.BankAddress, sdi.BankBeneficiaryAddress, pq.Array(sdi.PaymentRails),
			sdi.BankName, sdi.BankBeneficiaryName, sdi.BankAccountNumber, sdi.BankRoutingNumber, id)
		if err != nil {
			log.Printf("update %s: %v", id, err)
			continue
		}

		updated++
		fmt.Printf("✓ %s (%s) — %s, rails=%v\n", id, customerID, sdi.BankName, sdi.PaymentRails)
	}

	fmt.Printf("\nDone. Updated %d virtual accounts.\n", updated)
}
