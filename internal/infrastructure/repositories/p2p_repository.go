package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// P2PRepository implements the P2P transfer repository
type P2PRepository struct {
	db     *sqlx.DB
	logger *zap.Logger
}

// NewP2PRepository creates a new P2P repository
func NewP2PRepository(db *sqlx.DB, logger *zap.Logger) *P2PRepository {
	return &P2PRepository{db: db, logger: logger}
}

// Create creates a new P2P transfer
func (r *P2PRepository) Create(ctx context.Context, transfer *entities.P2PTransfer) error {
	query := `
		INSERT INTO p2p_transfers (
			id, sender_id, recipient_id, recipient_identifier, identifier_type,
			amount, currency, note, status, claim_token, provider_transfer_id, provider_status,
			expires_at, created_at, updated_at, idempotency_key
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`

	_, err := r.db.ExecContext(ctx, query,
		transfer.ID, transfer.SenderID, transfer.RecipientID, transfer.RecipientIdentifier,
		transfer.IdentifierType, transfer.Amount, transfer.Currency, transfer.Note,
		transfer.Status, transfer.ClaimToken, transfer.ProviderTransferID, transfer.ProviderStatus,
		transfer.ExpiresAt, transfer.CreatedAt, transfer.UpdatedAt, transfer.IdempotencyKey)
	if err != nil {
		r.logger.Error("Failed to create P2P transfer", zap.Error(err))
		return fmt.Errorf("failed to create transfer: %w", err)
	}
	return nil
}

// GetByID retrieves a transfer by ID
func (r *P2PRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.P2PTransfer, error) {
	var t entities.P2PTransfer
	query := `SELECT id, sender_id, recipient_id, recipient_identifier, identifier_type,
		amount, currency, note, status, claim_token, claim_link_sent_at, reminder_sent_at,
		provider_transfer_id, provider_status,
		completed_at, cancelled_at, expires_at, created_at, updated_at
		FROM p2p_transfers WHERE id = $1`

	err := r.db.GetContext(ctx, &t, query, id)
	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transfer: %w", err)
	}
	return &t, nil
}

// GetByClaimToken retrieves a transfer by claim token
func (r *P2PRepository) GetByClaimToken(ctx context.Context, token string) (*entities.P2PTransfer, error) {
	var t entities.P2PTransfer
	query := `SELECT id, sender_id, recipient_id, recipient_identifier, identifier_type,
		amount, currency, note, status, claim_token, claim_link_sent_at, reminder_sent_at,
		provider_transfer_id, provider_status,
		completed_at, cancelled_at, expires_at, created_at, updated_at
		FROM p2p_transfers WHERE claim_token = $1`

	err := r.db.GetContext(ctx, &t, query, token)
	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transfer by token: %w", err)
	}
	return &t, nil
}

// GetByIdempotencyKey retrieves a transfer by idempotency key
func (r *P2PRepository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*entities.P2PTransfer, error) {
	var t entities.P2PTransfer
	query := `SELECT id, sender_id, recipient_id, recipient_identifier, identifier_type,
		amount, currency, note, status, claim_token, claim_link_sent_at, reminder_sent_at,
		provider_transfer_id, provider_status,
		completed_at, cancelled_at, expires_at, created_at, updated_at, idempotency_key
		FROM p2p_transfers WHERE idempotency_key = $1`

	err := r.db.GetContext(ctx, &t, query, idempotencyKey)
	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transfer by idempotency key: %w", err)
	}
	return &t, nil
}

// GetBySender retrieves transfers by sender ID
func (r *P2PRepository) GetBySender(ctx context.Context, senderID uuid.UUID, limit, offset int) ([]*entities.P2PTransfer, error) {
	var transfers []*entities.P2PTransfer
	query := `SELECT id, sender_id, recipient_id, recipient_identifier, identifier_type,
		amount, currency, note, status, claim_token, claim_link_sent_at, reminder_sent_at,
		provider_transfer_id, provider_status,
		completed_at, cancelled_at, expires_at, created_at, updated_at
		FROM p2p_transfers WHERE sender_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`

	err := r.db.SelectContext(ctx, &transfers, query, senderID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get transfers: %w", err)
	}
	return transfers, nil
}

// GetPendingByIdentifier retrieves pending transfers by email or phone
func (r *P2PRepository) GetPendingByIdentifier(ctx context.Context, email, phone string) ([]*entities.P2PTransfer, error) {
	var transfers []*entities.P2PTransfer
	query := `SELECT id, sender_id, recipient_id, recipient_identifier, identifier_type,
		amount, currency, note, status, claim_token, claim_link_sent_at, reminder_sent_at,
		provider_transfer_id, provider_status,
		completed_at, cancelled_at, expires_at, created_at, updated_at
		FROM p2p_transfers 
		WHERE status = 'pending' AND expires_at > NOW()
		AND ((identifier_type = 'email' AND recipient_identifier = $1)
		  OR (identifier_type = 'phone' AND recipient_identifier = $2))`

	err := r.db.SelectContext(ctx, &transfers, query, email, phone)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending transfers: %w", err)
	}
	return transfers, nil
}

// GetExpired retrieves expired pending transfers
func (r *P2PRepository) GetExpired(ctx context.Context) ([]*entities.P2PTransfer, error) {
	var transfers []*entities.P2PTransfer
	query := `SELECT id, sender_id, recipient_id, recipient_identifier, identifier_type,
		amount, currency, note, status, claim_token, claim_link_sent_at, reminder_sent_at,
		provider_transfer_id, provider_status,
		completed_at, cancelled_at, expires_at, created_at, updated_at
		FROM p2p_transfers WHERE status = 'pending' AND expires_at <= NOW()`

	err := r.db.SelectContext(ctx, &transfers, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired transfers: %w", err)
	}
	return transfers, nil
}

func (r *P2PRepository) AcquirePendingByID(ctx context.Context, id uuid.UUID) (*entities.P2PTransfer, error) {
	return r.acquirePending(ctx, "id = $1", id)
}

func (r *P2PRepository) AcquirePendingByClaimToken(ctx context.Context, token string) (*entities.P2PTransfer, error) {
	return r.acquirePending(ctx, "claim_token = $1", token)
}

func (r *P2PRepository) acquirePending(ctx context.Context, predicate string, arg interface{}) (*entities.P2PTransfer, error) {
	var transfer entities.P2PTransfer
	query := fmt.Sprintf(`UPDATE p2p_transfers
		SET status = 'processing', updated_at = NOW()
		WHERE %s AND status = 'pending'
		RETURNING id, sender_id, recipient_id, recipient_identifier, identifier_type,
			amount, currency, note, status, claim_token, claim_link_sent_at, reminder_sent_at,
			provider_transfer_id, provider_status,
			completed_at, cancelled_at, expires_at, created_at, updated_at`, predicate)

	if err := r.db.GetContext(ctx, &transfer, query, arg); err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to acquire pending transfer: %w", err)
	}

	return &transfer, nil
}

func (r *P2PRepository) ReleaseProcessing(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE p2p_transfers
		SET status = 'pending', updated_at = NOW()
		WHERE id = $1 AND status = 'processing'`

	if _, err := r.db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("failed to release processing transfer: %w", err)
	}
	return nil
}

// Update updates a transfer
func (r *P2PRepository) Update(ctx context.Context, transfer *entities.P2PTransfer) error {
	query := `UPDATE p2p_transfers SET
		recipient_id = $2, status = $3, claim_link_sent_at = $4, reminder_sent_at = $5,
		provider_transfer_id = $6, provider_status = $7, completed_at = $8, cancelled_at = $9, updated_at = $10
		WHERE id = $1 AND status = 'processing'`

	transfer.UpdatedAt = time.Now()
	result, err := r.db.ExecContext(ctx, query,
		transfer.ID, transfer.RecipientID, transfer.Status, transfer.ClaimLinkSentAt,
		transfer.ReminderSentAt, transfer.ProviderTransferID, transfer.ProviderStatus,
		transfer.CompletedAt, transfer.CancelledAt, transfer.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update transfer: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read transfer update result: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("transfer is no longer in processing state")
	}
	return nil
}

// UpsertRecentRecipient upserts a recent recipient
func (r *P2PRepository) UpsertRecentRecipient(ctx context.Context, userID, recipientID uuid.UUID) error {
	query := `INSERT INTO p2p_recent_recipients (user_id, recipient_id, last_sent_at, send_count)
		VALUES ($1, $2, NOW(), 1)
		ON CONFLICT (user_id, recipient_id) DO UPDATE SET
		last_sent_at = NOW(), send_count = p2p_recent_recipients.send_count + 1`

	_, err := r.db.ExecContext(ctx, query, userID, recipientID)
	if err != nil {
		return fmt.Errorf("failed to upsert recent recipient: %w", err)
	}
	return nil
}

// GetRecentRecipients retrieves recent recipients with user details
func (r *P2PRepository) GetRecentRecipients(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.P2PRecentRecipientWithUser, error) {
	query := `SELECT r.recipient_id, u.rail_tag, u.first_name, u.last_name, r.last_sent_at, r.send_count
		FROM p2p_recent_recipients r
		JOIN users u ON u.id = r.recipient_id
		WHERE r.user_id = $1
		ORDER BY r.last_sent_at DESC LIMIT $2`

	rows, err := r.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent recipients: %w", err)
	}
	defer rows.Close()

	var recipients []*entities.P2PRecentRecipientWithUser
	for rows.Next() {
		var rec entities.P2PRecentRecipientWithUser
		var railTag, firstName, lastName sql.NullString
		var lastSentAt time.Time
		var sendCount int

		if err := rows.Scan(&rec.RecipientID, &railTag, &firstName, &lastName, &lastSentAt, &sendCount); err != nil {
			return nil, fmt.Errorf("failed to scan recipient: %w", err)
		}
		if railTag.Valid {
			rec.RailTag = &railTag.String
		}
		if firstName.Valid {
			rec.FirstName = &firstName.String
		}
		if lastName.Valid {
			rec.LastName = &lastName.String
		}
		rec.LastSentAt = lastSentAt
		rec.SendCount = sendCount
		recipients = append(recipients, &rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return recipients, nil
}

// P2PBalanceProvider adapts ledger service for P2P
type P2PBalanceProvider struct {
	ledgerService interface {
		GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	}
}

func NewP2PBalanceProvider(ledgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}) *P2PBalanceProvider {
	return &P2PBalanceProvider{ledgerService: ledgerService}
}

func (p *P2PBalanceProvider) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return p.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
}

// P2PTransferExecutor adapts ledger service for P2P transfers
type P2PTransferExecutor struct {
	ledgerService interface {
		CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error)
		GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error)
		GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error)
	}
}

func NewP2PTransferExecutor(ledgerService interface {
	CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error)
	GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error)
	GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error)
}) *P2PTransferExecutor {
	return &P2PTransferExecutor{ledgerService: ledgerService}
}

func (p *P2PTransferExecutor) TransferBetweenUsers(ctx context.Context, fromUserID, toUserID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error {
	fromAccount, err := p.ledgerService.GetOrCreateUserAccount(ctx, fromUserID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get sender account: %w", err)
	}

	toAccount, err := p.ledgerService.GetOrCreateUserAccount(ctx, toUserID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get recipient account: %w", err)
	}

	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = fmt.Sprintf("p2p-%s-%s", fromUserID, toUserID)
	}
	desc := &description

	req := &entities.CreateTransactionRequest{
		TransactionType: entities.TransactionTypeP2PTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: fromAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD", Description: desc},
			{AccountID: toAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD", Description: desc},
		},
	}

	_, err = p.ledgerService.CreateTransaction(ctx, req)
	return err
}

func (p *P2PTransferExecutor) ReserveFunds(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error {
	userAccount, err := p.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get user account: %w", err)
	}

	systemAccount, err := p.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("get system account: %w", err)
	}

	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = fmt.Sprintf("p2p-debit-%s", userID)
	}
	desc := &description

	req := &entities.CreateTransactionRequest{
		TransactionType: entities.TransactionTypeP2PTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: userAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD", Description: desc},
			{AccountID: systemAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD", Description: desc},
		},
	}

	_, err = p.ledgerService.CreateTransaction(ctx, req)
	return err
}

func (p *P2PTransferExecutor) CreditUserFromSystem(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error {
	userAccount, err := p.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get user account: %w", err)
	}

	systemAccount, err := p.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("get system account: %w", err)
	}

	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = fmt.Sprintf("p2p-credit-%s", userID)
	}
	desc := &description

	req := &entities.CreateTransactionRequest{
		TransactionType: entities.TransactionTypeP2PTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: systemAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD", Description: desc},
			{AccountID: userAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD", Description: desc},
		},
	}

	_, err = p.ledgerService.CreateTransaction(ctx, req)
	return err
}
