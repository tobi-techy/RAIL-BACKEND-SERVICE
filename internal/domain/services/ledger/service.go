package ledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/retry"
	"github.com/shopspring/decimal"
)

// ErrAccountNotFound is returned when a requested user ledger account does not exist.
var ErrAccountNotFound = errors.New("ledger account not found")

// Service handles ledger operations using double-entry bookkeeping
type Service struct {
	ledgerRepo     *repositories.LedgerRepository
	db             *sqlx.DB
	logger         *logger.Logger
	stashLock      StashLockChecker
	stashRaids     StashRaidObserver
	outbox         *OutboxWriter
	velocityConfig *entities.VelocityConfig
}

// StashLockChecker enforces the 90-day lock / 7-day window rule.
type StashLockChecker interface {
	CanWithdraw(ctx context.Context, userID uuid.UUID) (bool, time.Time, error)
}

// StashRaidObserver receives successful stash-to-spend transfers for behavioral
// guardrails. It must never be required for ledger correctness.
type StashRaidObserver interface {
	EvaluateStashRaid(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error
}

// SetStashLockChecker wires stash lock enforcement into the ledger.
func (s *Service) SetStashLockChecker(c StashLockChecker) {
	s.stashLock = c
}

func (s *Service) SetStashRaidObserver(o StashRaidObserver) {
	s.stashRaids = o
}

// SetVelocityConfig overrides the default velocity limit configuration.
// Pass nil to reset to defaults.
func (s *Service) SetVelocityConfig(cfg *entities.VelocityConfig) {
	if cfg == nil {
		cfg := entities.DefaultVelocityConfig
		s.velocityConfig = &cfg
		return
	}
	s.velocityConfig = cfg
}

// computeTransactionHash builds the SHA256 hash for a ledger transaction.
// The hash is computed from the previous transaction hash + all normalized
// fields that define the transaction. Any mutation to a historical row will
// cause a cascade of broken hashes.
func computeTransactionHash(prevHash string, tx *entities.LedgerTransaction, entries []entities.CreateEntryRequest) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte(tx.ID.String()))
	if tx.UserID != nil {
		h.Write([]byte(tx.UserID.String()))
	}
	h.Write([]byte(string(tx.TransactionType)))
	h.Write([]byte(tx.IdempotencyKey))
	if tx.Reason != nil {
		h.Write([]byte(*tx.Reason))
	}
	// Include sorted entries so hash captures economic intent
	for _, e := range orderedEntriesForLocking(entries) {
		h.Write([]byte(e.AccountID.String()))
		h.Write([]byte(string(e.EntryType)))
		h.Write([]byte(e.Amount.String()))
		h.Write([]byte(e.Currency))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// NewService creates a new ledger service
func NewService(
	ledgerRepo *repositories.LedgerRepository,
	db *sqlx.DB,
	logger *logger.Logger,
	outbox *OutboxWriter,
) *Service {
	return &Service{
		ledgerRepo: ledgerRepo,
		db:         db,
		logger:     logger,
		outbox:     outbox,
	}
}

// CreateTransaction creates a new ledger transaction with entries atomically
// This is the core operation that ensures double-entry bookkeeping integrity
func (s *Service) CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error) {
	ledgerTx, _, err := s.createTransaction(ctx, req)
	return ledgerTx, err
}

func (s *Service) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*entities.LedgerTransaction, error) {
	return s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, key)
}

// CreatePendingTransaction inserts a ledger transaction with status=pending and
// its entry rows, but does NOT lock or update any account balances. The
// balances are updated later by CommitPendingTransaction once the transfer
// succeeds. If the transfer fails, FailPendingTransaction marks the tx as
// failed with zero balance impact.
func (s *Service) CreatePendingTransaction(ctx context.Context, req *entities.CreateTransactionRequest) error {
	if req.InitiatedBy == "" {
		req.InitiatedBy = entities.InitiatedBySystem.String()
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("validate request: %w", err)
	}

	// Idempotency: if a tx with this key already exists, it's a replay.
	existing, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return fmt.Errorf("check idempotency: %w", err)
	}
	if existing != nil {
		s.logger.Info("Pending transaction already exists (idempotent)",
			"idempotency_key", req.IdempotencyKey,
			"transaction_id", existing.ID)
		return nil
	}

	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	txCtx := repositories.WithTx(ctx, tx)
	now := time.Now()

	initiatedBy := req.InitiatedBy
	if initiatedBy == "" {
		initiatedBy = entities.InitiatedBySystem.String()
	}

	ledgerTx := &entities.LedgerTransaction{
		ID:              uuid.New(),
		UserID:          req.UserID,
		TransactionType: req.TransactionType,
		ReferenceID:     req.ReferenceID,
		ReferenceType:   req.ReferenceType,
		Status:          entities.TransactionStatusPending,
		IdempotencyKey:  req.IdempotencyKey,
		Description:     req.Description,
		Metadata:        req.Metadata,
		InitiatedBy:     initiatedBy,
		Reason:          req.Reason,
		CreatedAt:       now,
	}

	// Compute hash chain link for pending transaction.
	if ledgerTx.TransactionHash == "" {
		prevHash, err := s.ledgerRepo.GetLatestTransactionHash(txCtx)
		if err != nil {
			return fmt.Errorf("get previous hash: %w", err)
		}
		ledgerTx.PreviousTransactionHash = prevHash
		ledgerTx.TransactionHash = computeTransactionHash(prevHash, ledgerTx, req.Entries)
	}

	if err := s.ledgerRepo.CreateTransaction(txCtx, ledgerTx); err != nil {
		if repositories.IsUniqueViolation(err, "") {
			_ = tx.Rollback()
			return nil // idempotent replay
		}
		return fmt.Errorf("create pending transaction: %w", err)
	}

	// Insert entry rows (for audit trail) but do NOT touch account balances.
	for _, entryReq := range req.Entries {
		entry := &entities.LedgerEntry{
			ID:            uuid.New(),
			TransactionID: ledgerTx.ID,
			AccountID:     entryReq.AccountID,
			EntryType:     entryReq.EntryType,
			Amount:        entryReq.Amount,
			Currency:      entryReq.Currency,
			Description:   entryReq.Description,
			Metadata:      entryReq.Metadata,
			CreatedAt:     now,
		}
		if err := s.ledgerRepo.CreateEntry(txCtx, entry); err != nil {
			return fmt.Errorf("create pending entry: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pending transaction: %w", err)
	}

	s.logger.Info("Pending ledger transaction created",
		"transaction_id", ledgerTx.ID,
		"idempotency_key", req.IdempotencyKey,
		"type", ledgerTx.TransactionType)
	return nil
}

// CommitPendingTransaction promotes a pending ledger transaction to completed
// by locking the affected accounts, updating their balances, and marking the
// transaction as completed. This is called after the external transfer succeeds.
func (s *Service) CommitPendingTransaction(ctx context.Context, idempotencyKey string) error {
	ledgerTx, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return fmt.Errorf("get pending transaction: %w", err)
	}
	if ledgerTx == nil {
		return fmt.Errorf("pending transaction not found for key: %s", idempotencyKey)
	}
	if ledgerTx.Status == entities.TransactionStatusCompleted {
		s.logger.Info("Transaction already committed (idempotent)",
			"idempotency_key", idempotencyKey)
		return nil
	}
	if ledgerTx.Status != entities.TransactionStatusPending {
		return fmt.Errorf("transaction %s is in status %s, cannot commit", ledgerTx.ID, ledgerTx.Status)
	}

	entries, err := s.ledgerRepo.GetEntriesByTransactionID(ctx, ledgerTx.ID)
	if err != nil {
		return fmt.Errorf("get pending entries: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("pending transaction %s has no entries", ledgerTx.ID)
	}

	// Use its own DB transaction to lock accounts and update balances atomically.
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin commit transaction: %w", err)
	}
	defer tx.Rollback()

	txCtx := repositories.WithTx(ctx, tx)

	// Convert repo entries to CreateEntryRequest for the existing helper.
	entryReqs := make([]entities.CreateEntryRequest, len(entries))
	for i, e := range entries {
		entryReqs[i] = entities.CreateEntryRequest{
			AccountID:   e.AccountID,
			EntryType:   e.EntryType,
			Amount:      e.Amount,
			Currency:    e.Currency,
			Description: e.Description,
		}
	}

	for _, entryReq := range orderedEntriesForLocking(entryReqs) {
		if err := s.updateAccountBalanceInTx(txCtx, entryReq.AccountID, entryReq.EntryType, entryReq.Amount); err != nil {
			return fmt.Errorf("commit balance update: %w", err)
		}
	}

	ledgerTx.MarkCompleted()
	if err := s.ledgerRepo.UpdateTransactionStatus(txCtx, ledgerTx.ID, entities.TransactionStatusCompleted); err != nil {
		return fmt.Errorf("mark transaction completed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	s.logger.Info("Pending ledger transaction committed",
		"transaction_id", ledgerTx.ID,
		"idempotency_key", idempotencyKey)
	return nil
}

// FailPendingTransaction marks a pending ledger transaction as failed without
// touching any account balances (they were never updated). This is called when
// the external transfer fails.
func (s *Service) FailPendingTransaction(ctx context.Context, idempotencyKey string) error {
	ledgerTx, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return fmt.Errorf("get pending transaction: %w", err)
	}
	if ledgerTx == nil {
		s.logger.Warn("FailPendingTransaction: transaction not found (may already be cleaned up)",
			"idempotency_key", idempotencyKey)
		return nil
	}
	if ledgerTx.Status != entities.TransactionStatusPending {
		s.logger.Info("FailPendingTransaction: transaction already in terminal status",
			"idempotency_key", idempotencyKey, "status", ledgerTx.Status)
		return nil
	}

	if err := s.ledgerRepo.UpdateTransactionStatus(ctx, ledgerTx.ID, entities.TransactionStatusFailed); err != nil {
		return fmt.Errorf("mark transaction failed: %w", err)
	}

	s.logger.Info("Pending ledger transaction marked failed",
		"transaction_id", ledgerTx.ID,
		"idempotency_key", idempotencyKey)
	return nil
}

// GetLedgerTransactionStatus returns the current status of a ledger transaction
// identified by its idempotency key. Used by callers that need to decide whether
// to fail a pending tx or reverse a committed one.
func (s *Service) GetLedgerTransactionStatus(ctx context.Context, idempotencyKey string) (entities.TransactionStatus, error) {
	ledgerTx, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return "", fmt.Errorf("get transaction: %w", err)
	}
	if ledgerTx == nil {
		return "", nil
	}
	return ledgerTx.Status, nil
}

// ledgerTxRetryConfig governs how many times we replay a ledger transaction
// after a Postgres serialization failure / deadlock. Ledger work is short and
// fully rolled back by the server on 40001/40P01, so replaying from scratch is
// safe and cannot double-apply balances.
var ledgerTxRetryConfig = retry.RetryConfig{
	MaxAttempts: 5,
	BaseDelay:   10 * time.Millisecond,
	MaxDelay:    250 * time.Millisecond,
	Multiplier:  2.0,
}

func (s *Service) createTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, bool, error) {
	// Default initiated_by to system if not explicitly set.
	if req.InitiatedBy == "" {
		req.InitiatedBy = entities.InitiatedBySystem.String()
	}
	// Validate request
	if err := req.Validate(); err != nil {
		return nil, false, fmt.Errorf("validate request: %w", err)
	}

	// Fast-path idempotency check. This is a best-effort optimization to avoid
	// opening a transaction for an obvious duplicate; it is NOT the correctness
	// boundary. The authoritative guarantee is the UNIQUE(idempotency_key)
	// constraint enforced inside executeTransaction, which closes the
	// check-then-insert TOCTOU window between concurrent callers.
	existing, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, false, fmt.Errorf("check idempotency: %w", err)
	}
	if existing != nil {
		s.logger.Info("Transaction already exists (idempotent)",
			"idempotency_key", req.IdempotencyKey,
			"transaction_id", existing.ID)
		return existing, false, nil
	}

	// If the caller already established a transaction (e.g. ReverseTransaction),
	// run the body inline on that transaction. We must NOT open a second
	// transaction on a different connection, and we must NOT retry — the caller
	// owns the transaction lifecycle and a server-side rollback would already
	// have poisoned their tx.
	if repositories.HasTx(ctx) {
		return s.executeTransaction(ctx, req)
	}

	// Standalone path: own the transaction lifecycle and retry on
	// serialization/deadlock failures with bounded exponential backoff.
	var (
		resultTx *entities.LedgerTransaction
		created  bool
	)
	retryErr := retry.WithExponentialBackoff(ctx, ledgerTxRetryConfig, func() error {
		var execErr error
		resultTx, created, execErr = s.executeTransaction(ctx, req)
		return execErr
	}, repositories.IsSerializationFailure)
	if retryErr != nil {
		return nil, false, retryErr
	}
	return resultTx, created, nil
}

// checkVelocityLimit verifies that a debit entry would not exceed the
// configured daily velocity limits. Returns nil if the entry is allowed.
func (s *Service) checkVelocityLimit(ctx context.Context, accountID uuid.UUID, amount decimal.Decimal, date time.Time) error {
	cfg := s.velocityConfig
	if cfg == nil {
		return nil
	}
	bucket, err := s.ledgerRepo.GetOrCreateVelocityBucket(ctx, accountID, date)
	if err != nil {
		return fmt.Errorf("get velocity bucket: %w", err)
	}
	if cfg.MaxDailyOutflow.IsPositive() && bucket.OutflowTotal.Add(amount).GreaterThan(cfg.MaxDailyOutflow) {
		return fmt.Errorf("daily outflow limit exceeded: current=%s + amount=%s > max=%s (account=%s)",
			bucket.OutflowTotal.String(), amount.String(), cfg.MaxDailyOutflow.String(), accountID)
	}
	if cfg.MaxDailyTxCount > 0 && bucket.TxCount+1 > cfg.MaxDailyTxCount {
		return fmt.Errorf("daily transaction count limit exceeded: current=%d + 1 > max=%d (account=%s)",
			bucket.TxCount, cfg.MaxDailyTxCount, accountID)
	}
	return nil
}

// executeTransaction performs the full double-entry write inside a single
// database transaction: insert the transaction header, pre-lock all affected
// accounts with SELECT FOR UPDATE, insert each entry, and update each
// account balance. It enforces deterministic lock ordering to prevent
// deadlocks and treats a unique-key collision on the idempotency key as a
// successful idempotent replay rather than an error.
func (s *Service) executeTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, bool, error) {
	// Reuse the caller's transaction if present; otherwise open our own.
	var (
		tx    *sqlx.Tx
		txCtx context.Context
		owned bool
	)
	if repositories.HasTx(ctx) {
		txCtx = ctx
	} else {
		var err error
		tx, err = s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return nil, false, fmt.Errorf("begin transaction: %w", err)
		}
		owned = true
		defer func() {
			// Safe no-op if already committed.
			_ = tx.Rollback()
		}()
		txCtx = repositories.WithTx(ctx, tx)
	}

	now := time.Now()
	ledgerTx := &entities.LedgerTransaction{
		ID:              uuid.New(),
		UserID:          req.UserID,
		TransactionType: req.TransactionType,
		ReferenceID:     req.ReferenceID,
		ReferenceType:   req.ReferenceType,
		Status:          entities.TransactionStatusPending,
		IdempotencyKey:  req.IdempotencyKey,
		Description:     req.Description,
		Metadata:        req.Metadata,
		InitiatedBy:     req.InitiatedBy,
		Reason:          req.Reason,
		CreatedAt:       now,
	}

	// Compute hash chain link.
	if ledgerTx.TransactionHash == "" {
		prevHash, err := s.ledgerRepo.GetLatestTransactionHash(txCtx)
		if err != nil {
			return nil, false, fmt.Errorf("get previous hash: %w", err)
		}
		ledgerTx.PreviousTransactionHash = prevHash
		ledgerTx.TransactionHash = computeTransactionHash(prevHash, ledgerTx, req.Entries)
	}

	if err := s.ledgerRepo.CreateTransaction(txCtx, ledgerTx); err != nil {
		// Concurrent caller with the same idempotency key already inserted the
		// transaction. The UNIQUE(idempotency_key) constraint is the real
		// guard against balance duplication. Resolve to the winning row and
		// return it idempotently instead of surfacing a hard error.
		if repositories.IsUniqueViolation(err, "") {
			if owned {
				_ = tx.Rollback()
			}
			existing, getErr := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
			if getErr != nil {
				return nil, false, fmt.Errorf("resolve idempotent transaction: %w", getErr)
			}
			if existing != nil {
				s.logger.Info("Transaction already exists (idempotent, race resolved)",
					"idempotency_key", req.IdempotencyKey,
					"transaction_id", existing.ID)
				return existing, false, nil
			}
		}
		return nil, false, fmt.Errorf("create transaction: %w", err)
	}

	// Pre-lock all affected accounts in sorted order BEFORE inserting any
	// entries. FK checks on ledger_entries.account_id acquire FOR KEY SHARE
	// locks; if we insert entries first, concurrent transactions holding
	// FOR KEY SHARE block each other's FOR UPDATE, causing deadlocks.
	sortedEntries := orderedEntriesForLocking(req.Entries)
	locked := make(map[uuid.UUID]struct{}, len(sortedEntries))
	for _, entryReq := range sortedEntries {
		if _, seen := locked[entryReq.AccountID]; !seen {
			if _, err := s.ledgerRepo.GetAccountBalanceForUpdate(txCtx, entryReq.AccountID); err != nil {
				return nil, false, fmt.Errorf("lock account: %w", err)
			}
			locked[entryReq.AccountID] = struct{}{}
		}
	}

	// Insert entries and update balances. Each account already holds FOR
	// UPDATE, so the repeated lock inside updateAccountBalanceInTx is a
	// no-op within the same transaction.
	today := now.Truncate(24 * time.Hour)
	for _, entryReq := range sortedEntries {
		// Circuit-breaker: check velocity limits before debiting an account.
		if entryReq.EntryType == entities.EntryTypeCredit {
			if err := s.checkVelocityLimit(txCtx, entryReq.AccountID, entryReq.Amount, today); err != nil {
				return nil, false, fmt.Errorf("velocity limit: %w", err)
			}
		}

		entry := &entities.LedgerEntry{
			ID:            uuid.New(),
			TransactionID: ledgerTx.ID,
			AccountID:     entryReq.AccountID,
			EntryType:     entryReq.EntryType,
			Amount:        entryReq.Amount,
			Currency:      entryReq.Currency,
			Description:   entryReq.Description,
			Metadata:      entryReq.Metadata,
			CreatedAt:     now,
		}

		if err := s.ledgerRepo.CreateEntry(txCtx, entry); err != nil {
			return nil, false, fmt.Errorf("create entry: %w", err)
		}

		// Update account balance (acquires SELECT ... FOR UPDATE row lock).
		if err := s.updateAccountBalanceInTx(txCtx, entryReq.AccountID, entryReq.EntryType, entryReq.Amount); err != nil {
			return nil, false, fmt.Errorf("update account balance: %w", err)
		}

		// Increment velocity bucket for debit entries.
		if entryReq.EntryType == entities.EntryTypeCredit {
			if err := s.ledgerRepo.IncrementVelocityBucket(txCtx, entryReq.AccountID, today, entryReq.Amount); err != nil {
				s.logger.Error("Failed to increment velocity bucket (tx will still commit)",
					"account_id", entryReq.AccountID,
					"error", err)
			}
		}
	}

	// Mark transaction as completed
	ledgerTx.MarkCompleted()
	if err := s.ledgerRepo.UpdateTransactionStatus(txCtx, ledgerTx.ID, entities.TransactionStatusCompleted); err != nil {
		return nil, false, fmt.Errorf("update transaction status: %w", err)
	}

	// Write outbox event inside the same DB transaction.
	// This ensures at-least-once delivery: if the commit succeeds, the event is
	// durable. The outbox publisher picks it up asynchronously.
	if s.outbox != nil {
		if err := s.outbox.WriteTransactionEvents(txCtx, ledgerTx); err != nil {
			s.logger.Error("Failed to write outbox event (tx will still commit)",
				"transaction_id", ledgerTx.ID,
				"error", err)
			// Non-fatal: the transaction is valid, the outbox publisher can
			// reconstruct events from the ledger tables if needed.
		}
	}

	// Commit only if we own the transaction; otherwise the caller commits.
	if owned {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit transaction: %w", err)
		}
	}

	s.logger.Info("Ledger transaction created successfully",
		"transaction_id", ledgerTx.ID,
		"type", ledgerTx.TransactionType,
		"user_id", ledgerTx.UserID)

	return ledgerTx, true, nil
}

// orderedEntriesForLocking returns the entries sorted by account ID so that
// row locks are always acquired in a consistent global order across all
// callers. This is the standard defense against lock-ordering deadlocks when
// multiple transactions touch overlapping account sets. Entry rows are written
// in the same order; ledger entries are order-independent, so this is safe.
func orderedEntriesForLocking(entries []entities.CreateEntryRequest) []entities.CreateEntryRequest {
	ordered := make([]entities.CreateEntryRequest, len(entries))
	copy(ordered, entries)
	sort.SliceStable(ordered, func(i, j int) bool {
		return bytes.Compare(ordered[i].AccountID[:], ordered[j].AccountID[:]) < 0
	})
	return ordered
}

// updateAccountBalanceInTx updates an account balance within a database transaction
func (s *Service) updateAccountBalanceInTx(ctx context.Context, accountID uuid.UUID, entryType entities.EntryType, amount decimal.Decimal) error {
	// Acquire row-level lock and get current balance atomically
	oldBalance, err := s.ledgerRepo.GetAccountBalanceForUpdate(ctx, accountID)
	if err != nil {
		return fmt.Errorf("get account balance: %w", err)
	}

	// Calculate new balance
	var newBalance decimal.Decimal
	switch entryType {
	case entities.EntryTypeDebit:
		newBalance = oldBalance.Add(amount)
	case entities.EntryTypeCredit:
		newBalance = oldBalance.Sub(amount)
	}

	// Ensure balance doesn't go negative (skip for system accounts — they track external reserves)
	if newBalance.IsNegative() {
		account, accountErr := s.ledgerRepo.GetAccountByID(ctx, accountID)
		if accountErr != nil || !account.AccountType.IsSystemAccountType() {
			return fmt.Errorf("insufficient balance: current=%s, adjustment=%s %s",
				oldBalance.String(), amount.String(), entryType)
		}
		// SECURITY: Solvency guard — system accounts cannot go below -$100,000.
		// This prevents unbounded liability from bugs in yield distribution or reconciliation.
		maxDeficit := decimal.NewFromInt(-100000)
		if newBalance.LessThan(maxDeficit) {
			return fmt.Errorf("system account solvency limit breached: balance=%s exceeds max deficit=%s",
				newBalance.String(), maxDeficit.String())
		}
	}

	// Update balance using an optimistic CAS on the balance we just read under
	// the row lock. This is belt-and-suspenders on top of the FOR UPDATE lock:
	// if the balance changed between the locked read and this write (which the
	// lock should make impossible), the guarded update aborts instead of
	// silently overwriting a concurrent modification.
	if err := s.ledgerRepo.UpdateAccountBalanceGuarded(ctx, accountID, oldBalance, newBalance); err != nil {
		return fmt.Errorf("update account balance: %w", err)
	}

	// Write balance change outbox event inside the same transaction.
	if s.outbox != nil {
		if err := s.outbox.WriteBalanceEvent(ctx, accountID, oldBalance, newBalance); err != nil {
			s.logger.Error("Failed to write balance outbox event (tx will still commit)",
				"account_id", accountID,
				"error", err)
		}
	}

	return nil
}

// GetAccountBalance retrieves the current balance for an account
func (s *Service) GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	if s == nil || s.ledgerRepo == nil {
		return decimal.Zero, fmt.Errorf("ledger repository not configured")
	}

	account, err := s.ledgerRepo.GetAccountByUserAndType(ctx, userID, accountType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return decimal.Zero, fmt.Errorf("%w: user_id=%s account_type=%s", ErrAccountNotFound, userID, accountType)
		}
		return decimal.Zero, fmt.Errorf("get account: %w", err)
	}

	return account.Balance, nil
}

// GetUserBalances retrieves all balances for a user
func (s *Service) GetUserBalances(ctx context.Context, userID uuid.UUID) (*entities.UserBalances, error) {
	balances, err := s.ledgerRepo.GetUserBalances(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user balances: %w", err)
	}

	return balances, nil
}

// ReconcileBalance directly sets a user's account balance (for admin reconciliation)
// Uses SELECT FOR UPDATE to prevent TOCTOU race conditions
func (s *Service) ReconcileBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, newBalance decimal.Decimal) error {
	// Validate accountType is valid (spending or savings)
	if accountType != entities.AccountTypeSpendingBalance && accountType != entities.AccountTypeStashBalance {
		return fmt.Errorf("invalid account type: %s (must be spending_balance or stash_balance)", accountType)
	}

	// Validate newBalance is not negative
	if newBalance.IsNegative() {
		return fmt.Errorf("reconciliation cannot set negative balance: %s", newBalance.String())
	}

	// Begin transaction for atomic read-modify-write
	txCtx, err := s.ledgerRepo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer s.ledgerRepo.RollbackTx(txCtx)

	// Use SELECT FOR UPDATE to lock the row and prevent concurrent modifications
	account, err := s.ledgerRepo.GetAccountByUserAndTypeForUpdate(txCtx, userID, accountType)
	if err != nil {
		return fmt.Errorf("get account for update: %w", err)
	}

	// Get old balance for audit trail
	oldBalance := account.Balance

	// Log with Warn level since this is an admin override action
	diff := newBalance.Sub(oldBalance)
	s.logger.Warn("Reconciling balance (admin override)",
		"user_id", userID.String(),
		"account_type", string(accountType),
		"old_balance", oldBalance.String(),
		"new_balance", newBalance.String(),
		"difference", diff.String())

	// Update balance within the transaction
	if err := s.ledgerRepo.UpdateAccountBalance(txCtx, account.ID, newBalance); err != nil {
		return fmt.Errorf("update account balance: %w", err)
	}

	// Commit transaction
	if err := s.ledgerRepo.CommitTx(txCtx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// GetSystemBuffers retrieves system buffer balances
func (s *Service) GetSystemBuffers(ctx context.Context) (*entities.SystemBuffers, error) {
	buffers, err := s.ledgerRepo.GetSystemBuffers(ctx)
	if err != nil {
		return nil, fmt.Errorf("get system buffers: %w", err)
	}

	return buffers, nil
}

// GetOrCreateUserAccount ensures a user account exists
func (s *Service) GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	// Determine currency based on account type
	currency := "USDC"
	if accountType == entities.AccountTypeFiatExposure {
		currency = "USD"
	}

	account, err := s.ledgerRepo.GetOrCreateUserAccount(ctx, userID, accountType, currency)
	if err != nil {
		return nil, fmt.Errorf("get or create user account: %w", err)
	}

	return account, nil
}

// GetSystemAccount retrieves a system-level account
func (s *Service) GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	account, err := s.ledgerRepo.GetSystemAccount(ctx, accountType)
	if err != nil {
		return nil, fmt.Errorf("get system account: %w", err)
	}

	return account, nil
}

// GetAccountByID retrieves an account by its ID
func (s *Service) GetAccountByID(ctx context.Context, accountID uuid.UUID) (*entities.LedgerAccount, error) {
	account, err := s.ledgerRepo.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account by id: %w", err)
	}
	return account, nil
}

// ReserveForInvestment reserves funds for an investment by moving from usdc_balance to pending_investment
func (s *Service) ReserveForInvestment(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	// Get user accounts
	usdcAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("get usdc account: %w", err)
	}

	pendingAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypePendingInvestment)
	if err != nil {
		return fmt.Errorf("get pending account: %w", err)
	}

	// Balance check removed: CreateTransaction uses SELECT FOR UPDATE internally,
	// which atomically checks and prevents overdraft. Pre-flight check was a TOCTOU race.

	// Create reservation transaction
	idempotencyKey := fmt.Sprintf("reserve-%s-%s-%d", userID.String(), amount.String(), time.Now().UnixNano())
	desc := "Reserve funds for investment"

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   usdcAccount.ID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   pendingAccount.ID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("create reservation transaction: %w", err)
	}

	s.logger.Info("Funds reserved for investment",
		"user_id", userID,
		"amount", amount.String())

	return nil
}

// ReleaseReservation releases reserved funds back to usdc_balance (e.g., on trade cancellation)
func (s *Service) ReleaseReservation(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	// Get user accounts
	usdcAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("get usdc account: %w", err)
	}

	pendingAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypePendingInvestment)
	if err != nil {
		return fmt.Errorf("get pending account: %w", err)
	}

	// Balance check removed: CreateTransaction uses SELECT FOR UPDATE internally,
	// which atomically checks and prevents overdraft. Pre-flight check was a TOCTOU race.

	// Create release transaction
	idempotencyKey := fmt.Sprintf("release-%s-%s-%d", userID.String(), amount.String(), time.Now().UnixNano())
	desc := "Release reserved funds"

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   pendingAccount.ID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   usdcAccount.ID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("create release transaction: %w", err)
	}

	s.logger.Info("Reserved funds released",
		"user_id", userID,
		"amount", amount.String())

	return nil
}

// ReverseTransaction creates compensating entries to reverse a transaction
func (s *Service) ReverseTransaction(ctx context.Context, originalTxID uuid.UUID, reason string) error {
	// Get original transaction
	originalTx, err := s.ledgerRepo.GetTransactionByID(ctx, originalTxID)
	if err != nil {
		return fmt.Errorf("get original transaction: %w", err)
	}

	if originalTx.Status == entities.TransactionStatusReversed {
		return fmt.Errorf("transaction already reversed")
	}

	// Get original entries
	entries, err := s.ledgerRepo.GetEntriesByTransactionID(ctx, originalTxID)
	if err != nil {
		return fmt.Errorf("get original entries: %w", err)
	}

	// Begin database transaction for atomicity
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	txCtx := repositories.WithTx(ctx, tx)

	// Mark original transaction as reversed first
	if err := s.ledgerRepo.UpdateTransactionStatus(txCtx, originalTxID, entities.TransactionStatusReversed); err != nil {
		return fmt.Errorf("update original transaction status: %w", err)
	}

	// Create reversal entries (flip debit/credit)
	reversalEntries := make([]entities.CreateEntryRequest, len(entries))
	for i, entry := range entries {
		var reversalType entities.EntryType
		if entry.EntryType == entities.EntryTypeDebit {
			reversalType = entities.EntryTypeCredit
		} else {
			reversalType = entities.EntryTypeDebit
		}

		desc := fmt.Sprintf("Reversal of transaction %s: %s", originalTxID.String(), reason)
		reversalEntries[i] = entities.CreateEntryRequest{
			AccountID:   entry.AccountID,
			EntryType:   reversalType,
			Amount:      entry.Amount,
			Currency:    entry.Currency,
			Description: &desc,
		}
	}

	// Create reversal transaction within same db transaction
	idempotencyKey := fmt.Sprintf("reversal-%s", originalTxID.String())
	desc := fmt.Sprintf("Reversal: %s", reason)

	req := &entities.CreateTransactionRequest{
		UserID:          originalTx.UserID,
		TransactionType: entities.TransactionTypeReversal,
		ReferenceID:     &originalTxID,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries:         reversalEntries,
	}

	// Note: CreateTransaction will use the existing tx from context
	_, err = s.CreateTransaction(txCtx, req)
	if err != nil {
		return fmt.Errorf("create reversal transaction: %w", err)
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reversal: %w", err)
	}

	s.logger.Info("Transaction reversed",
		"original_tx_id", originalTxID,
		"reason", reason)

	return nil
}

// GetTransactionHistory retrieves transaction history for a user
func (s *Service) GetTransactionHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.LedgerEntry, error) {
	// Get all user accounts
	accounts, err := s.ledgerRepo.GetUserAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user accounts: %w", err)
	}

	// Aggregate entries from all accounts
	var allEntries []*entities.LedgerEntry
	for _, account := range accounts {
		entries, err := s.ledgerRepo.GetEntriesByAccountID(ctx, account.ID, limit, offset)
		if err != nil {
			return nil, fmt.Errorf("get entries for account %s: %w", account.ID, err)
		}
		allEntries = append(allEntries, entries...)
	}

	return allEntries, nil
}

// GetSystemBufferBalance retrieves the balance for a system buffer account
func (s *Service) GetSystemBufferBalance(ctx context.Context, accountType string) (decimal.Decimal, error) {
	// Convert string to AccountType enum
	var accountTypeEnum entities.AccountType
	switch accountType {
	case "system_buffer_usdc", "liquidity_buffer":
		accountTypeEnum = entities.AccountTypeSystemBufferUSDC
	case "system_buffer_fiat", "fee_revenue":
		accountTypeEnum = entities.AccountTypeSystemBufferFiat
	case "broker_operational":
		accountTypeEnum = entities.AccountTypeBrokerOperational
	case "withdrawal_fee_revenue":
		accountTypeEnum = entities.AccountTypeWithdrawalFeeRevenue
	default:
		return decimal.Zero, fmt.Errorf("unknown account type: %s", accountType)
	}

	account, err := s.GetSystemAccount(ctx, accountTypeEnum)
	if err != nil {
		return decimal.Zero, fmt.Errorf("get system account: %w", err)
	}

	return account.Balance, nil
}

// GetTotalUserFiatExposure calculates total USD exposure across all users
func (s *Service) GetTotalUserFiatExposure(ctx context.Context) (decimal.Decimal, error) {
	// Query sum of all user fiat exposure accounts from database
	query := `
		SELECT COALESCE(SUM(balance), 0) as total
		FROM ledger_accounts
		WHERE account_type = $1 AND user_id IS NOT NULL
	`

	var total decimal.Decimal
	err := s.db.QueryRowContext(ctx, query, entities.AccountTypeFiatExposure).Scan(&total)
	if err != nil {
		return decimal.Zero, fmt.Errorf("get total fiat exposure: %w", err)
	}

	return total, nil
}

// RecordCardTransaction records a card transaction by debiting the spend balance
func (s *Service) RecordCardTransaction(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	// Get user's spend balance account (spending_balance for Smart Allocation Mode)
	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spend account: %w", err)
	}

	// Balance check removed: CreateTransaction uses SELECT FOR UPDATE internally,
	// which atomically checks and prevents overdraft. Pre-flight check was a TOCTOU race.

	// Get system card settlement account (or create one)
	settlementAccount, err := s.GetSystemAccount(ctx, entities.AccountTypeSystemBufferFiat)
	if err != nil {
		return fmt.Errorf("get settlement account: %w", err)
	}

	// Create card transaction
	idempotencyKey := fmt.Sprintf("card-tx-%s-%s-%d", userID.String(), reference, time.Now().UnixNano())
	desc := fmt.Sprintf("Card transaction: %s", reference)
	refType := "card_transaction"

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeCardPayment,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Debit from user's perspective
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
			{
				AccountID:   settlementAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Credit to settlement
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("create card transaction: %w", err)
	}

	s.logger.Info("Card transaction recorded",
		"user_id", userID,
		"amount", amount.String(),
		"reference", reference)

	return nil
}

func stringPtr(s string) *string {
	return &s
}

// TransferSpendingToStash moves funds from spending_balance to stash_balance.
// Used for roundup collection: the spare change is already in spending and should move to stash.
func (s *Service) TransferSpendingToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}
	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}

	desc := fmt.Sprintf("Transfer to stash: $%s", amount.StringFixed(2))
	refType := "spend_to_stash"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		InitiatedBy:     entities.InitiatedBySystem.String(),
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeCredit, // debit spending
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeDebit, // credit stash
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	return err
}

// TransferStashToSpending moves funds from stash to spending.
func (s *Service) TransferStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}

	// Enforce stash lock: transfers from stash only allowed during the 7-day window.
	if s.stashLock != nil {
		canWithdraw, _, err := s.stashLock.CanWithdraw(ctx, userID)
		if err != nil {
			return fmt.Errorf("stash lock check failed: %w", err)
		}
		if !canWithdraw {
			return fmt.Errorf("stash funds are locked: no active withdrawal window (funds lock for 90 days, then a 7-day window opens)")
		}
	}

	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}

	desc := fmt.Sprintf("Transfer from stash: $%s", amount.StringFixed(2))
	refType := "stash_to_spend"
	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeCredit, // debit stash
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeDebit, // credit spending
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	_, created, err := s.createTransaction(ctx, txReq)
	if err != nil {
		return err
	}
	if created {
		s.observeStashRaid(userID, amount, idempotencyKey)
	}
	return nil
}

// AdminTransferStashToSpending moves funds from stash to spending, bypassing the stash lock.
// This is an admin-only operation with a distinct reference type for audit trail.
func (s *Service) AdminTransferStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey, adminReason string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}
	// SECURITY: Enforce cap at service level — defense in depth regardless of caller.
	maxAdminTransfer := decimal.NewFromInt(500)
	if amount.GreaterThan(maxAdminTransfer) {
		return fmt.Errorf("admin transfer amount %s exceeds maximum of $500", amount.String())
	}

	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}

	reason := adminReason
	if len(reason) > 255 {
		reason = reason[:255]
	}
	desc := fmt.Sprintf("Admin stash-to-spend transfer: %s (reason: %s)", amount.String(), reason)
	refType := "admin_stash_transfer"
	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByAdmin.String(),
		Reason:          &reason,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	s.logger.Warn("Admin stash-to-spend transfer (bypasses stash lock)",
		"user_id", userID.String(),
		"amount", amount.String(),
		"reason", adminReason,
	)

	_, err = s.CreateTransaction(ctx, txReq)
	return err
}

// EmergencyTransferStashToSpending moves funds from stash to spending with a fee
// credited to the emergency withdrawal revenue account. Bypasses stash lock.
func (s *Service) EmergencyTransferStashToSpending(ctx context.Context, userID uuid.UUID, amount, fee decimal.Decimal, idempotencyKey string) error {
	if amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}
	if fee.IsNegative() {
		return fmt.Errorf("fee cannot be negative")
	}
	if amount.IsZero() && !fee.IsPositive() {
		return fmt.Errorf("transfer amount or fee must be positive")
	}

	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}
	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	var revenueAccount *entities.LedgerAccount
	if fee.IsPositive() {
		revenueAccount, err = s.GetSystemAccount(ctx, entities.AccountTypeEmergencyWithdrawalRevenue)
		if err != nil {
			return fmt.Errorf("get emergency revenue account: %w", err)
		}
	}

	total := amount.Add(fee)
	desc := fmt.Sprintf("Emergency stash withdrawal: %s (fee: %s)", amount.String(), fee.String())
	refType := "emergency_stash_transfer"
	entries := []entities.CreateEntryRequest{
		{AccountID: stashAccount.ID, EntryType: entities.EntryTypeCredit, Amount: total, Currency: "USD"},
	}
	if amount.IsPositive() {
		entries = append(entries, entities.CreateEntryRequest{
			AccountID: spendAccount.ID,
			EntryType: entities.EntryTypeDebit,
			Amount:    amount,
			Currency:  "USD",
		})
	}
	if fee.IsPositive() {
		entries = append(entries, entities.CreateEntryRequest{
			AccountID: revenueAccount.ID,
			EntryType: entities.EntryTypeDebit,
			Amount:    fee,
			Currency:  "USD",
		})
	}
	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries:         entries,
	}

	_, created, err := s.createTransaction(ctx, txReq)
	if err != nil {
		return err
	}
	if created && amount.IsPositive() {
		s.observeStashRaid(userID, amount, idempotencyKey)
	}
	return nil
}

// CreatePendingEmergencyTransfer is the pending-twin of EmergencyTransferStashToSpending.
// It inserts the same double-entry rows but leaves account balances untouched until
// CommitPendingTransaction is called after the Blend redemption completes.
func (s *Service) CreatePendingEmergencyTransfer(ctx context.Context, userID uuid.UUID, amount, fee decimal.Decimal, idempotencyKey string) error {
	if amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}
	if fee.IsNegative() {
		return fmt.Errorf("fee cannot be negative")
	}
	if amount.IsZero() && !fee.IsPositive() {
		return fmt.Errorf("transfer amount or fee must be positive")
	}

	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}
	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	var revenueAccount *entities.LedgerAccount
	if fee.IsPositive() {
		revenueAccount, err = s.GetSystemAccount(ctx, entities.AccountTypeEmergencyWithdrawalRevenue)
		if err != nil {
			return fmt.Errorf("get emergency revenue account: %w", err)
		}
	}

	total := amount.Add(fee)
	desc := fmt.Sprintf("Emergency stash withdrawal (pending): %s (fee: %s)", amount.String(), fee.String())
	refType := "emergency_stash_transfer"
	entries := []entities.CreateEntryRequest{
		{AccountID: stashAccount.ID, EntryType: entities.EntryTypeCredit, Amount: total, Currency: "USD"},
	}
	if amount.IsPositive() {
		entries = append(entries, entities.CreateEntryRequest{
			AccountID: spendAccount.ID,
			EntryType: entities.EntryTypeDebit,
			Amount:    amount,
			Currency:  "USD",
		})
	}
	if fee.IsPositive() {
		entries = append(entries, entities.CreateEntryRequest{
			AccountID: revenueAccount.ID,
			EntryType: entities.EntryTypeDebit,
			Amount:    fee,
			Currency:  "USD",
		})
	}

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries:         entries,
	}

	return s.CreatePendingTransaction(ctx, req)
}

// ChargeLimitIncreaseFee debits a flat fee from the user's spending_balance and
// credits it to the limit-increase revenue account. Used when a user raises
// their self-imposed daily spending commitment. Idempotent by key.
func (s *Service) ChargeLimitIncreaseFee(ctx context.Context, userID uuid.UUID, fee decimal.Decimal, idempotencyKey string) error {
	if !fee.IsPositive() {
		return fmt.Errorf("fee must be positive")
	}
	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	revenueAccount, err := s.GetSystemAccount(ctx, entities.AccountTypeLimitIncreaseRevenue)
	if err != nil {
		return fmt.Errorf("get limit increase revenue account: %w", err)
	}
	desc := fmt.Sprintf("Daily limit increase fee: %s", fee.String())
	refType := "limit_increase_fee"
	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAccount.ID, EntryType: entities.EntryTypeCredit, Amount: fee, Currency: "USD", Description: &desc},
			{AccountID: revenueAccount.ID, EntryType: entities.EntryTypeDebit, Amount: fee, Currency: "USD", Description: &desc},
		},
	}
	_, _, err = s.createTransaction(ctx, txReq)
	return err
}

// CreditStash credits a user's stash_balance from the system USDC buffer.
// Used for yield distribution payouts.
func (s *Service) CreditStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid credit amount: %s (must be positive)", amount.String())
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}
	systemAccount, err := s.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("get system buffer account: %w", err)
	}

	refType := "yield_distribution"
	// Include userID + description (which contains distributionID) for per-distribution uniqueness.
	idempotencyKey := fmt.Sprintf("yield-credit-%s-%s", userID, description)
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeDeposit,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &description,
		Entries: []entities.CreateEntryRequest{
			{AccountID: stashAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USDC", Description: &description},
			{AccountID: systemAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USDC", Description: &description},
		},
	}
	_, err = s.CreateTransaction(ctx, req)
	return err
}

func (s *Service) observeStashRaid(userID uuid.UUID, amount decimal.Decimal, reference string) {
	if s.stashRaids == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("stash raid observer panicked", "user_id", userID.String(), "panic", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.stashRaids.EvaluateStashRaid(ctx, userID, amount, reference); err != nil {
			s.logger.Warn("stash raid observer failed", "user_id", userID.String(), "error", err)
		}
	}()
}

// DistributeYieldToGoals delegates to CreditYieldToSavings for interface compatibility.
func (s *Service) DistributeYieldToGoals(ctx context.Context, userID uuid.UUID, yieldAmount decimal.Decimal, distributionID string) error {
	return s.CreditYieldToSavings(ctx, userID, yieldAmount, distributionID)
}

// CreditYieldToSavings credits yield directly to stash AND each goal account in a single transaction.
// Each account earns yield independently based on its share of total savings.
func (s *Service) CreditYieldToSavings(ctx context.Context, userID uuid.UUID, totalYield decimal.Decimal, distributionID string) error {
	if totalYield.IsZero() || totalYield.IsNegative() {
		return nil
	}

	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}
	systemAccount, err := s.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("get system buffer account: %w", err)
	}
	goalAccounts, err := s.ledgerRepo.GetGoalAccounts(ctx, userID)
	if err != nil {
		return fmt.Errorf("get goal accounts: %w", err)
	}

	// Calculate total savings position
	totalSavings := stashAccount.Balance
	for _, ga := range goalAccounts {
		totalSavings = totalSavings.Add(ga.Balance)
	}

	// Build batch entries: one debit per account, one credit to system
	var entries []entities.CreateEntryRequest
	desc := fmt.Sprintf("Yield distribution %s", distributionID)
	systemCreditTotal := decimal.Zero

	if totalSavings.IsZero() {
		// No savings anywhere — credit all to stash as fallback
		entries = append(entries, entities.CreateEntryRequest{
			AccountID: stashAccount.ID, EntryType: entities.EntryTypeDebit, Amount: totalYield, Currency: "USDC", Description: &desc,
		})
		systemCreditTotal = totalYield
	} else {
		distributed := decimal.Zero

		// Stash share
		stashShare := stashAccount.Balance.Div(totalSavings).Mul(totalYield).Truncate(6)
		if stashShare.IsPositive() {
			entries = append(entries, entities.CreateEntryRequest{
				AccountID: stashAccount.ID, EntryType: entities.EntryTypeDebit, Amount: stashShare, Currency: "USDC", Description: &desc,
			})
			distributed = distributed.Add(stashShare)
		}

		// Each goal's share
		for _, ga := range goalAccounts {
			share := ga.Balance.Div(totalSavings).Mul(totalYield).Truncate(6)
			if share.IsZero() {
				continue
			}
			goalDesc := fmt.Sprintf("Yield on goal %s", ga.GoalID)
			entries = append(entries, entities.CreateEntryRequest{
				AccountID: ga.ID, EntryType: entities.EntryTypeDebit, Amount: share, Currency: "USDC", Description: &goalDesc,
			})
			distributed = distributed.Add(share)
		}

		// Rounding dust to stash
		dust := totalYield.Sub(distributed)
		if dust.IsPositive() {
			entries = append(entries, entities.CreateEntryRequest{
				AccountID: stashAccount.ID, EntryType: entities.EntryTypeDebit, Amount: dust, Currency: "USDC", Description: &desc,
			})
			distributed = distributed.Add(dust)
		}
		systemCreditTotal = distributed
	}

	// Single system credit to balance the debits
	entries = append(entries, entities.CreateEntryRequest{
		AccountID: systemAccount.ID, EntryType: entities.EntryTypeCredit, Amount: systemCreditTotal, Currency: "USDC", Description: &desc,
	})

	idempKey := fmt.Sprintf("yield-%s-%s", distributionID, userID)
	refType := "yield_distribution"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeDeposit,
		ReferenceType:   &refType,
		IdempotencyKey:  idempKey,
		Description:     &desc,
		Metadata:        map[string]any{"distribution_id": distributionID},
		Entries:         entries,
	}

	_, err = s.CreateTransaction(ctx, req)
	return err
}

// --- Automation Transfer Operations ---

// AutomationTransferSpendToStash moves funds from spend to stash, labeled as an automation transfer
// so it appears correctly in transaction history.
func (s *Service) AutomationTransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey, automationName string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}
	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}

	desc := fmt.Sprintf("Automation: %s — $%s to stash", automationName, amount.StringFixed(2))
	refType := "automation_transfer"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByAutomation.String(),
		Metadata:        map[string]any{"source": "automation", "automation_name": automationName},
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USDC", Description: &desc},
			{AccountID: stashAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USDC", Description: &desc},
		},
	}
	_, err = s.CreateTransaction(ctx, req)
	return err
}

// --- Goal Sub-Account Operations ---

// GetOrCreateGoalAccount ensures a goal_balance ledger account exists for a user+goal pair.
func (s *Service) GetOrCreateGoalAccount(ctx context.Context, userID, goalID uuid.UUID) (*entities.LedgerAccount, error) {
	return s.ledgerRepo.GetOrCreateGoalAccount(ctx, userID, goalID)
}

// TransferSpendToGoal moves funds from the user's spending balance to a goal sub-account.
func (s *Service) TransferSpendToGoal(ctx context.Context, userID, goalID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}

	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}

	goalAccount, err := s.ledgerRepo.GetOrCreateGoalAccount(ctx, userID, goalID)
	if err != nil {
		return fmt.Errorf("get goal account: %w", err)
	}

	desc := fmt.Sprintf("Goal contribution: spend → goal %s", goalID)
	refType := "goal_contribution"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Metadata:        map[string]any{"goal_id": goalID.String()},
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USDC", Description: &desc},
			{AccountID: goalAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USDC", Description: &desc},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	return err
}

// TransferGoalToSpend moves funds from a goal sub-account back to spending.
func (s *Service) TransferGoalToSpend(ctx context.Context, userID, goalID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}

	goalAccount, err := s.ledgerRepo.GetOrCreateGoalAccount(ctx, userID, goalID)
	if err != nil {
		return fmt.Errorf("get goal account: %w", err)
	}

	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}

	desc := fmt.Sprintf("Goal withdrawal: goal %s → spend", goalID)
	refType := "goal_withdrawal"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Metadata:        map[string]any{"goal_id": goalID.String()},
		Entries: []entities.CreateEntryRequest{
			{AccountID: goalAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USDC", Description: &desc},
			{AccountID: spendAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USDC", Description: &desc},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	return err
}

// GetGoalBalance returns the balance of a specific goal sub-account.
func (s *Service) GetGoalBalance(ctx context.Context, userID, goalID uuid.UUID) (decimal.Decimal, error) {
	account, err := s.ledgerRepo.GetOrCreateGoalAccount(ctx, userID, goalID)
	if err != nil {
		return decimal.Zero, err
	}
	return account.Balance, nil
}

// GetTotalGoalAllocated returns the sum of all goal sub-account balances for a user.
func (s *Service) GetTotalGoalAllocated(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return s.ledgerRepo.GetTotalGoalAllocated(ctx, userID)
}

// GetGoalAccounts returns all goal sub-accounts with balances for a user.
func (s *Service) GetGoalAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.LedgerAccount, error) {
	return s.ledgerRepo.GetGoalAccounts(ctx, userID)
}

// GetTotalSavingsBalance returns stash_balance + all goal_balance accounts for a user.
// This represents the user's total savings position for yield calculation.
func (s *Service) GetTotalSavingsBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return decimal.Zero, err
	}
	goalTotal, err := s.ledgerRepo.GetTotalGoalAllocated(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	return stashAccount.Balance.Add(goalTotal), nil
}

// GetUnallocatedStashBalance returns the stash balance minus all goal-allocated funds.
// This is the amount available for withdrawal without impacting any goals.
func (s *Service) GetUnallocatedStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return decimal.Zero, err
	}
	goalTotal, err := s.ledgerRepo.GetTotalGoalAllocated(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	unallocated := stashAccount.Balance.Sub(goalTotal)
	if unallocated.IsNegative() {
		return decimal.Zero, nil
	}
	return unallocated, nil
}

// GetWithdrawableStashBalance returns the stash balance minus ONLY protected goal funds.
// Unprotected goals produce a warning but don't block withdrawals.
func (s *Service) GetWithdrawableStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return decimal.Zero, err
	}
	protectedTotal, err := s.ledgerRepo.GetProtectedGoalAllocated(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	withdrawable := stashAccount.Balance.Sub(protectedTotal)
	if withdrawable.IsNegative() {
		return decimal.Zero, nil
	}
	return withdrawable, nil
}

// RecordDailySnapshots records the balance of every account for today's date
// by iterating in batches of 1000. Idempotent: existing (account, date) rows
// are skipped via ON CONFLICT DO NOTHING.
//
// Snapshot consistency: each batch is read within its own transaction (one
// snapshot per batch) rather than a single giant transaction, which keeps lock
// contention low while still providing point-in-time consistency per batch.
func (s *Service) RecordDailySnapshots(ctx context.Context, snapshotDate time.Time) (int, error) {
	const batchSize = 1000
	date := snapshotDate.Truncate(24 * time.Hour)
	var total int
	var afterID uuid.UUID

	for {
		batch, err := s.ledgerRepo.GetAccountIDsBatch(ctx, afterID, batchSize)
		if err != nil {
			return total, fmt.Errorf("get account batch after %s: %w", afterID, err)
		}
		if len(batch) == 0 {
			break
		}

		for _, accountID := range batch {
			balance, err := s.ledgerRepo.GetAccountBalance(ctx, accountID)
			if err != nil {
				return total, fmt.Errorf("get balance for account %s: %w", accountID, err)
			}
			inserted, err := s.ledgerRepo.InsertBalanceSnapshot(ctx, accountID, balance, date)
			if err != nil {
				return total, fmt.Errorf("insert snapshot for account %s: %w", accountID, err)
			}
			if inserted {
				total++
			}
		}

		afterID = batch[len(batch)-1]
	}

	return total, nil
}

// ReconcileDay compares balance snapshots against transaction deltas for a
// given date. For each account with a closing snapshot, it verifies:
//
//	opening_snapshot(date-1) + net_transaction_delta(date) == closing_snapshot(date)
//
// Accounts with activity but no opening snapshot are flagged (first-day or
// missing-snapshot edge case). Returns the number of accounts checked, number
// of failures, a list of error descriptions, and any hard error.
func (s *Service) ReconcileDay(ctx context.Context, date time.Time) (checked, failures int, errors []string, _ error) {
	start := date.Truncate(24 * time.Hour)
	end := start.Add(24 * time.Hour)

	deltas, err := s.ledgerRepo.GetTransactionDeltaByAccount(ctx, start, end)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("get transaction deltas: %w", err)
	}

	for accountID, netChange := range deltas {
		closeSnap, err := s.ledgerRepo.GetBalanceSnapshot(ctx, accountID, date)
		if err != nil {
			errors = append(errors, fmt.Sprintf("get close snapshot for %s: %s", accountID, err))
			failures++
			continue
		}
		if closeSnap == nil {
			errors = append(errors, fmt.Sprintf("account %s: no closing snapshot for %s", accountID, date.Format("2006-01-02")))
			failures++
			continue
		}

		openSnap, err := s.ledgerRepo.GetBalanceSnapshot(ctx, accountID, date.AddDate(0, 0, -1))
		if err != nil {
			errors = append(errors, fmt.Sprintf("get open snapshot for %s: %s", accountID, err))
			failures++
			continue
		}

		if netChange.IsZero() {
			checked++
			continue
		}

		checked++
		if openSnap != nil {
			expectedClose := openSnap.Add(netChange)
			if !expectedClose.Equal(*closeSnap) {
				failures++
				diff := closeSnap.Sub(expectedClose)
				errors = append(errors,
					fmt.Sprintf("account %s: reconciliation mismatch (open=%s + net=%s = expected=%s, actual=%s, diff=%s)",
						accountID, openSnap, netChange, expectedClose, closeSnap, diff))
			}
		} else {
			errors = append(errors,
				fmt.Sprintf("account %s: no opening snapshot for %s (close=%s, net=%s)",
					accountID, date.AddDate(0, 0, -1).Format("2006-01-02"), closeSnap, netChange))
		}
	}

	return checked, failures, errors, nil
}

// CheckIntegrity runs all ledger invariant checks and returns a diagnostic report.
// The deficitThreshold is the minimum balance a system account can have before it
// is flagged (e.g. -100000 means accounts with balance < -100000 are flagged).
// Pass nil to use the default threshold of -100000.
func (s *Service) CheckIntegrity(ctx context.Context, deficitThreshold ...decimal.Decimal) *IntegrityReport {
	report := &IntegrityReport{}

	debits, credits, err := s.ledgerRepo.GetTotalDebitsAndCredits(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("get total debits/credits: %s", err))
	} else {
		report.TotalDebits = debits
		report.TotalCredits = credits
		report.Balanced = debits.Equal(credits)
		if !report.Balanced {
			report.Errors = append(report.Errors,
				fmt.Sprintf("LEDGER OUT OF BALANCE: debits=%s credits=%s diff=%s",
					debits.String(), credits.String(), debits.Sub(credits).String()))
		}
	}

	negCount, err := s.ledgerRepo.CountNegativeBalanceAccounts(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count negative balances: %s", err))
	} else {
		report.NegativeBalanceAccounts = negCount
		if negCount > 0 {
			report.Errors = append(report.Errors,
				fmt.Sprintf("%d account(s) have negative balance", negCount))
		}
	}

	threshold := decimal.NewFromInt(-100000)
	if len(deficitThreshold) > 0 {
		threshold = deficitThreshold[0]
	}
	defCount, err := s.ledgerRepo.CountSystemAccountDeficits(ctx, threshold)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count system deficits: %s", err))
	} else {
		report.SystemAccountDeficits = defCount
		if defCount > 0 {
			report.Errors = append(report.Errors,
				fmt.Sprintf("%d system account(s) exceed solvency limit of %s", defCount, threshold))
		}
	}

	orphanCount, err := s.ledgerRepo.CountOrphanedEntries(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count orphaned entries: %s", err))
	} else {
		report.OrphanedEntries = orphanCount
		if orphanCount > 0 {
			report.Errors = append(report.Errors,
				fmt.Sprintf("%d orphaned entries (no matching transaction)", orphanCount))
		}
	}

	txCount, err := s.ledgerRepo.CountTransactionsWithoutEntries(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count transactions without entries: %s", err))
	} else {
		report.TransactionsWithoutEntries = txCount
		if txCount > 0 {
			report.Errors = append(report.Errors,
				fmt.Sprintf("%d transaction(s) with 0 entries", txCount))
		}
	}

	latestSnap, err := s.ledgerRepo.GetLatestSnapshotDate(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("get latest snapshot date: %s", err))
	} else if latestSnap != nil {
		report.LatestSnapshotDate = latestSnap
		since := time.Since(*latestSnap)
		if since > 30*24*time.Hour {
			report.Errors = append(report.Errors,
				fmt.Sprintf("balance snapshots stale: last snapshot was %s ago on %s",
					formatDuration(since), latestSnap.Format("2006-01-02")))
		}
	}

	outboxCount, err := s.ledgerRepo.CountUnpublishedOutbox(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("count unpublished outbox: %s", err))
	} else {
		report.UnpublishedOutboxCount = outboxCount
		if outboxCount > 1000 {
			report.Errors = append(report.Errors,
				fmt.Sprintf("%d unpublished outbox events (possible publisher stall)", outboxCount))
		}
	}

	oldestOutbox, err := s.ledgerRepo.GetOldestUnpublishedOutbox(ctx)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("get oldest unpublished outbox: %s", err))
	} else if oldestOutbox != nil {
		report.OldestUnpublishedOutbox = oldestOutbox
	}

	// Hash chain integrity verification (sample first 1000 txs for performance).
	broken, err := s.VerifyHashChain(ctx, 1000)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("hash chain verification: %s", err))
	} else {
		report.HashChainBroken = len(broken)
		if len(broken) > 0 {
			report.Errors = append(report.Errors,
				fmt.Sprintf("%d broken hash(es) in chain (first: %s)", len(broken), broken[0]))
		}
	}

	// 9. Daily snapshot reconciliation (only if we have recent snapshots).
	if latestSnap != nil {
		report.ReconciliationDate = latestSnap
		reconciled, failures, reconcilErrs, err := s.ReconcileDay(ctx, *latestSnap)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("snapshot reconciliation: %s", err))
		} else {
			report.ReconciledAccounts = reconciled
			report.ReconciliationFailures = failures
			report.ReconciliationErrors = reconcilErrs
			if failures > 0 {
				report.Errors = append(report.Errors,
					fmt.Sprintf("%d of %d reconciliation check(s) failed for %s",
						failures, reconciled+1, latestSnap.Format("2006-01-02")))
				for _, rErr := range reconcilErrs {
					report.Errors = append(report.Errors, "reconciliation: "+rErr)
				}
			}
		}
	}

	return report
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(d.Hours())
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// VerifyHashChain checks that the hash chain is unbroken.
// Verifies previous_transaction_hash links (full content hash would require
// loading entries; the entry-content check is done separately by the
// double-entry balance check in CheckIntegrity).
func (s *Service) VerifyHashChain(ctx context.Context, maxCheck int) ([]uuid.UUID, error) {
	if maxCheck <= 0 || maxCheck > 10000 {
		maxCheck = 1000
	}
	total, err := s.ledgerRepo.CountTransactions(ctx)
	if err != nil {
		return nil, fmt.Errorf("count transactions: %w", err)
	}
	if total == 0 {
		return nil, nil
	}
	var broken []uuid.UUID
	prevHash := ""
	for offset := 0; offset < total; offset += maxCheck {
		txs, err := s.ledgerRepo.GetTransactionsForHashVerification(ctx, maxCheck, offset)
		if err != nil {
			return nil, fmt.Errorf("get txs at offset %d: %w", offset, err)
		}
		for _, ltx := range txs {
			if ltx.PreviousTransactionHash != prevHash {
				broken = append(broken, ltx.ID)
				// Update prevHash to this tx's hash so subsequent checks
				// still validate against the actual stored chain.
				prevHash = ltx.TransactionHash
				continue
			}
			prevHash = ltx.TransactionHash
		}
	}
	return broken, nil
}

// IntegrityReport contains the results of a ledger integrity verification.
type IntegrityReport struct {
	TotalDebits                decimal.Decimal `json:"total_debits"`
	TotalCredits               decimal.Decimal `json:"total_credits"`
	Balanced                   bool            `json:"balanced"`
	NegativeBalanceAccounts    int             `json:"negative_balance_accounts"`
	SystemAccountDeficits      int             `json:"system_account_deficits"`
	OrphanedEntries            int             `json:"orphaned_entries"`
	TransactionsWithoutEntries int             `json:"transactions_without_entries"`
	LatestSnapshotDate         *time.Time      `json:"latest_snapshot_date,omitempty"`
	UnpublishedOutboxCount     int             `json:"unpublished_outbox_count"`
	OldestUnpublishedOutbox    *time.Time      `json:"oldest_unpublished_outbox,omitempty"`
	HashChainBroken            int             `json:"hash_chain_broken"`
	ReconciliationDate         *time.Time      `json:"reconciliation_date,omitempty"`
	ReconciledAccounts         int             `json:"reconciled_accounts"`
	ReconciliationFailures     int             `json:"reconciliation_failures"`
	ReconciliationErrors       []string        `json:"reconciliation_errors,omitempty"`
	Errors                     []string        `json:"errors,omitempty"`
}
