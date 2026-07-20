package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// VirtualAccountRepository implements the virtual account repository interface
type VirtualAccountRepository struct {
	db *sqlx.DB
}

// NewVirtualAccountRepository creates a new virtual account repository
func NewVirtualAccountRepository(db *sqlx.DB) *VirtualAccountRepository {
	return &VirtualAccountRepository{db: db}
}

// vaColumns is the shared SELECT column list for virtual_accounts, with
// COALESCE on nullable/text columns so scans into non-pointer fields are safe.
const vaColumns = `
	id, user_id, COALESCE(provider, 'bridge') as provider,
	bridge_customer_id, alpaca_account_id, bridge_account_id,
	graph_person_id, graph_account_id,
	account_number, routing_number, COALESCE(bank_code, '') as bank_code,
	COALESCE(bank_name, '') as bank_name,
	COALESCE(beneficiary_name, '') as beneficiary_name,
	COALESCE(bank_address, '') as bank_address,
	COALESCE(beneficiary_address, '') as beneficiary_address,
	COALESCE(payment_rails, '{}') as payment_rails,
	status, currency, created_at, updated_at`

// Create creates a new virtual account
func (r *VirtualAccountRepository) Create(ctx context.Context, account *entities.VirtualAccount) error {
	if account.Provider == "" {
		account.Provider = entities.VirtualAccountProviderBridge
	}
	query := `
		INSERT INTO virtual_accounts (
			id, user_id, provider, bridge_customer_id, alpaca_account_id, bridge_account_id,
			graph_person_id, graph_account_id,
			account_number, routing_number, bank_code, bank_name, beneficiary_name,
			bank_address, beneficiary_address, payment_rails,
			status, currency, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
		)
	`

	_, err := r.db.ExecContext(ctx, query,
		account.ID,
		account.UserID,
		account.Provider,
		account.BridgeCustomerID,
		account.AlpacaAccountID,
		account.BridgeAccountID,
		account.GraphPersonID,
		account.GraphAccountID,
		account.AccountNumber,
		account.RoutingNumber,
		account.BankCode,
		account.BankName,
		account.BeneficiaryName,
		account.BankAddress,
		account.BeneficiaryAddr,
		pq.Array(account.PaymentRails),
		account.Status,
		account.Currency,
		account.CreatedAt,
		account.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create virtual account: %w", err)
	}

	return nil
}

// GetByID retrieves a virtual account by ID
func (r *VirtualAccountRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error) {
	query := `SELECT ` + vaColumns + ` FROM virtual_accounts WHERE id = $1`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("virtual account not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get virtual account: %w", err)
	}

	return &account, nil
}

// GetByBridgeCustomerID retrieves a virtual account by Bridge customer ID
func (r *VirtualAccountRepository) GetByBridgeCustomerID(ctx context.Context, dueAccountID string) (*entities.VirtualAccount, error) {
	query := `SELECT ` + vaColumns + ` FROM virtual_accounts WHERE bridge_customer_id = $1`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, dueAccountID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("virtual account not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get virtual account: %w", err)
	}

	return &account, nil
}

// GetByUserID retrieves all virtual accounts for a user
func (r *VirtualAccountRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error) {
	query := `SELECT ` + vaColumns + ` FROM virtual_accounts WHERE user_id = $1 ORDER BY created_at DESC`

	var accounts []*entities.VirtualAccount
	err := r.db.SelectContext(ctx, &accounts, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list virtual accounts: %w", err)
	}

	return accounts, nil
}

// Update updates a virtual account
func (r *VirtualAccountRepository) Update(ctx context.Context, account *entities.VirtualAccount) error {
	query := `
		UPDATE virtual_accounts
		SET bridge_customer_id = $2,
			alpaca_account_id = $3,
			bridge_account_id = $4,
			graph_person_id = $5,
			graph_account_id = $6,
			account_number = $7,
			routing_number = $8,
			bank_code = $9,
			bank_name = $10,
			beneficiary_name = $11,
			status = $12,
			currency = $13,
			updated_at = $14
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query,
		account.ID,
		account.BridgeCustomerID,
		account.AlpacaAccountID,
		account.BridgeAccountID,
		account.GraphPersonID,
		account.GraphAccountID,
		account.AccountNumber,
		account.RoutingNumber,
		account.BankCode,
		account.BankName,
		account.BeneficiaryName,
		account.Status,
		account.Currency,
		account.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update virtual account: %w", err)
	}

	return nil
}

// UpdateWithVersion updates a virtual account only if the updated_at timestamp
// matches oldUpdatedAt, preventing lost updates from concurrent modifications.
func (r *VirtualAccountRepository) UpdateWithVersion(ctx context.Context, account *entities.VirtualAccount, oldUpdatedAt time.Time) error {
	query := `
		UPDATE virtual_accounts
		SET bridge_customer_id = $2,
			alpaca_account_id = $3,
			bridge_account_id = $4,
			graph_person_id = $5,
			graph_account_id = $6,
			account_number = $7,
			routing_number = $8,
			bank_code = $9,
			bank_name = $10,
			beneficiary_name = $11,
			status = $12,
			currency = $13,
			updated_at = $14
		WHERE id = $1 AND updated_at = $15
	`

	result, err := r.db.ExecContext(ctx, query,
		account.ID,
		account.BridgeCustomerID,
		account.AlpacaAccountID,
		account.BridgeAccountID,
		account.GraphPersonID,
		account.GraphAccountID,
		account.AccountNumber,
		account.RoutingNumber,
		account.BankCode,
		account.BankName,
		account.BeneficiaryName,
		account.Status,
		account.Currency,
		account.UpdatedAt,
		oldUpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update virtual account: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check update result: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("optimistic lock conflict: virtual account modified by another process")
	}
	return nil
}

// GetByAlpacaAccountID retrieves a virtual account by Alpaca account ID
func (r *VirtualAccountRepository) GetByAlpacaAccountID(ctx context.Context, alpacaAccountID string) (*entities.VirtualAccount, error) {
	query := `SELECT ` + vaColumns + ` FROM virtual_accounts WHERE alpaca_account_id = $1`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, alpacaAccountID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("virtual account not found")
		}
		return nil, fmt.Errorf("failed to get virtual account: %w", err)
	}

	return &account, nil
}

// UpdateStatus updates the status of a virtual account
func (r *VirtualAccountRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.VirtualAccountStatus) error {
	query := `
		UPDATE virtual_accounts
		SET status = $2
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query, id, status)
	if err != nil {
		return fmt.Errorf("failed to update virtual account status: %w", err)
	}

	return nil
}

// ExistsByUserAndAlpacaAccount checks if a virtual account exists for a user and Alpaca account
func (r *VirtualAccountRepository) ExistsByUserAndAlpacaAccount(ctx context.Context, userID uuid.UUID, alpacaAccountID string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM virtual_accounts
			WHERE user_id = $1 AND alpaca_account_id = $2
		)
	`

	var exists bool
	err := r.db.GetContext(ctx, &exists, query, userID, alpacaAccountID)
	if err != nil {
		return false, fmt.Errorf("failed to check virtual account existence: %w", err)
	}

	return exists, nil
}

// GetByBridgeAccountID retrieves a virtual account by Bridge account ID
func (r *VirtualAccountRepository) GetByBridgeAccountID(ctx context.Context, bridgeAccountID string) (*entities.VirtualAccount, error) {
	query := `SELECT ` + vaColumns + ` FROM virtual_accounts WHERE bridge_account_id = $1`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, bridgeAccountID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("virtual account not found")
		}
		return nil, fmt.Errorf("failed to get virtual account by bridge id: %w", err)
	}

	return &account, nil
}

// GetByGraphAccountID retrieves a virtual account by Graph bank account ID
func (r *VirtualAccountRepository) GetByGraphAccountID(ctx context.Context, graphAccountID string) (*entities.VirtualAccount, error) {
	query := `SELECT ` + vaColumns + ` FROM virtual_accounts WHERE graph_account_id = $1`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, graphAccountID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("virtual account not found")
		}
		return nil, fmt.Errorf("failed to get virtual account by graph id: %w", err)
	}

	return &account, nil
}

// GetAccountsForMigration retrieves Due virtual accounts that need Bridge migration
func (r *VirtualAccountRepository) GetAccountsForMigration(ctx context.Context, limit int) ([]*entities.VirtualAccount, error) {
	query := `SELECT ` + vaColumns + `
		FROM virtual_accounts
		WHERE bridge_customer_id IS NOT NULL 
		  AND bridge_customer_id != ''
		  AND (bridge_account_id IS NULL OR bridge_account_id = '')
		  AND status = 'active'
		ORDER BY created_at ASC
		LIMIT $1
	`

	var accounts []*entities.VirtualAccount
	err := r.db.SelectContext(ctx, &accounts, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get due accounts for migration: %w", err)
	}

	return accounts, nil
}

// UpdateBridgeAccountID updates the Bridge account ID for a virtual account
func (r *VirtualAccountRepository) UpdateBridgeAccountID(ctx context.Context, id uuid.UUID, bridgeAccountID string) error {
	query := `
		UPDATE virtual_accounts
		SET bridge_account_id = $2, updated_at = NOW()
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query, id, bridgeAccountID)
	if err != nil {
		return fmt.Errorf("failed to update bridge account id: %w", err)
	}

	return nil
}

// GetActiveByUserIDAndCurrency retrieves active virtual accounts for a user by currency
func (r *VirtualAccountRepository) GetActiveByUserIDAndCurrency(ctx context.Context, userID uuid.UUID, currency string) (*entities.VirtualAccount, error) {
	query := `SELECT ` + vaColumns + `
		FROM virtual_accounts
		WHERE user_id = $1 AND currency = $2 AND status = 'active'
		ORDER BY 
			CASE WHEN bridge_account_id IS NOT NULL THEN 0 ELSE 1 END,
			created_at DESC
		LIMIT 1
	`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, userID, currency)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get virtual account: %w", err)
	}

	return &account, nil
}
