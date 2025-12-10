package funding

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// TransactionHistoryService provides unified transaction history across all types
type TransactionHistoryService struct {
	depositRepo    DepositRepository
	withdrawalRepo WithdrawalRepository
	ledgerRepo     LedgerRepository
	logger         *logger.Logger
}

// WithdrawalRepository interface for withdrawal queries
type WithdrawalRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
}

// LedgerRepository interface for ledger transaction queries
type LedgerRepository interface {
	GetTransactionsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.LedgerTransaction, error)
	GetEntriesByTransactionID(ctx context.Context, txID uuid.UUID) ([]*entities.LedgerEntry, error)
}

// UnifiedTransaction represents a transaction in the unified history
type UnifiedTransaction struct {
	ID              uuid.UUID       `json:"id"`
	UserID          uuid.UUID       `json:"user_id"`
	Type            string          `json:"type"` // deposit, withdrawal, investment, conversion, transfer
	Status          string          `json:"status"`
	Amount          decimal.Decimal `json:"amount"`
	Currency        string          `json:"currency"`
	Description     string          `json:"description,omitempty"`
	ReferenceID     *uuid.UUID      `json:"reference_id,omitempty"`
	ReferenceType   string          `json:"reference_type,omitempty"`
	Chain           string          `json:"chain,omitempty"`
	TxHash          string          `json:"tx_hash,omitempty"`
	Address         string          `json:"address,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	Metadata        map[string]any  `json:"metadata,omitempty"`
}

// TransactionHistoryResponse contains paginated transaction history
type TransactionHistoryResponse struct {
	Transactions []*UnifiedTransaction `json:"transactions"`
	Total        int                   `json:"total"`
	Limit        int                   `json:"limit"`
	Offset       int                   `json:"offset"`
	HasMore      bool                  `json:"has_more"`
}

// TransactionFilter defines filters for transaction history
type TransactionFilter struct {
	Types     []string   // Filter by transaction types
	Status    *string    // Filter by status
	StartDate *time.Time // Filter by date range
	EndDate   *time.Time
	MinAmount *decimal.Decimal
	MaxAmount *decimal.Decimal
}

// NewTransactionHistoryService creates a new transaction history service
func NewTransactionHistoryService(
	depositRepo DepositRepository,
	withdrawalRepo WithdrawalRepository,
	ledgerRepo LedgerRepository,
	logger *logger.Logger,
) *TransactionHistoryService {
	return &TransactionHistoryService{
		depositRepo:    depositRepo,
		withdrawalRepo: withdrawalRepo,
		ledgerRepo:     ledgerRepo,
		logger:         logger,
	}
}

// GetTransactionHistory returns unified transaction history for a user
func (s *TransactionHistoryService) GetTransactionHistory(
	ctx context.Context,
	userID uuid.UUID,
	limit, offset int,
	filter *TransactionFilter,
) (*TransactionHistoryResponse, error) {
	s.logger.Info("Fetching unified transaction history",
		"user_id", userID.String(),
		"limit", limit,
		"offset", offset)

	var allTransactions []*UnifiedTransaction

	// Fetch deposits
	if s.shouldIncludeType(filter, "deposit") {
		deposits, err := s.depositRepo.GetByUserID(ctx, userID, limit*2, 0)
		if err != nil {
			s.logger.Warn("Failed to fetch deposits", "error", err)
		} else {
			for _, d := range deposits {
				allTransactions = append(allTransactions, s.depositToUnified(d))
			}
		}
	}

	// Fetch withdrawals
	if s.shouldIncludeType(filter, "withdrawal") {
		withdrawals, err := s.withdrawalRepo.GetByUserID(ctx, userID, limit*2, 0)
		if err != nil {
			s.logger.Warn("Failed to fetch withdrawals", "error", err)
		} else {
			for _, w := range withdrawals {
				allTransactions = append(allTransactions, s.withdrawalToUnified(w))
			}
		}
	}

	// Fetch ledger transactions (investments, conversions, transfers)
	if s.shouldIncludeType(filter, "investment") || s.shouldIncludeType(filter, "conversion") || s.shouldIncludeType(filter, "transfer") {
		ledgerTxs, err := s.ledgerRepo.GetTransactionsByUserID(ctx, userID, limit*2, 0)
		if err != nil {
			s.logger.Warn("Failed to fetch ledger transactions", "error", err)
		} else {
			for _, tx := range ledgerTxs {
				unified := s.ledgerTxToUnified(tx)
				if s.shouldIncludeType(filter, unified.Type) {
					allTransactions = append(allTransactions, unified)
				}
			}
		}
	}

	// Sort by created_at descending
	s.sortByDate(allTransactions)

	// Apply filters
	filtered := s.applyFilters(allTransactions, filter)

	// Apply pagination
	total := len(filtered)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	paginated := filtered[start:end]

	return &TransactionHistoryResponse{
		Transactions: paginated,
		Total:        total,
		Limit:        limit,
		Offset:       offset,
		HasMore:      end < total,
	}, nil
}

// GetTransactionByID retrieves a specific transaction
func (s *TransactionHistoryService) GetTransactionByID(
	ctx context.Context,
	userID uuid.UUID,
	txID uuid.UUID,
) (*UnifiedTransaction, error) {
	// Try deposits first
	deposits, err := s.depositRepo.GetByUserID(ctx, userID, 100, 0)
	if err == nil {
		for _, d := range deposits {
			if d.ID == txID {
				return s.depositToUnified(d), nil
			}
		}
	}

	// Try withdrawals
	withdrawals, err := s.withdrawalRepo.GetByUserID(ctx, userID, 100, 0)
	if err == nil {
		for _, w := range withdrawals {
			if w.ID == txID {
				return s.withdrawalToUnified(w), nil
			}
		}
	}

	// Try ledger transactions
	ledgerTxs, err := s.ledgerRepo.GetTransactionsByUserID(ctx, userID, 100, 0)
	if err == nil {
		for _, tx := range ledgerTxs {
			if tx.ID == txID {
				return s.ledgerTxToUnified(tx), nil
			}
		}
	}

	return nil, fmt.Errorf("transaction not found: %s", txID)
}

// Helper methods

func (s *TransactionHistoryService) depositToUnified(d *entities.Deposit) *UnifiedTransaction {
	return &UnifiedTransaction{
		ID:          d.ID,
		UserID:      d.UserID,
		Type:        "deposit",
		Status:      d.Status,
		Amount:      d.Amount,
		Currency:    "USDC",
		Description: "USDC deposit",
		Chain:       string(d.Chain),
		TxHash:      d.TxHash,
		CreatedAt:   d.CreatedAt,
		CompletedAt: d.ConfirmedAt,
		Metadata: map[string]any{
			"token": d.Token,
		},
	}
}

func (s *TransactionHistoryService) withdrawalToUnified(w *entities.Withdrawal) *UnifiedTransaction {
	var completedAt *time.Time
	if w.Status == entities.WithdrawalStatusCompleted {
		completedAt = &w.UpdatedAt
	}

	return &UnifiedTransaction{
		ID:          w.ID,
		UserID:      w.UserID,
		Type:        "withdrawal",
		Status:      string(w.Status),
		Amount:      w.Amount,
		Currency:    "USD",
		Description: "USD to USDC withdrawal",
		Chain:       w.DestinationChain,
		Address:     w.DestinationAddress,
		CreatedAt:   w.CreatedAt,
		CompletedAt: completedAt,
		Metadata: map[string]any{
			"alpaca_account_id": w.AlpacaAccountID,
		},
	}
}

func (s *TransactionHistoryService) ledgerTxToUnified(tx *entities.LedgerTransaction) *UnifiedTransaction {
	txType := s.mapLedgerType(tx.TransactionType)
	
	var completedAt *time.Time
	if tx.Status == entities.TransactionStatusCompleted {
		t := tx.CreatedAt
		completedAt = &t
	}

	// Get amount from first entry (simplified)
	amount := decimal.Zero
	currency := "USDC"

	description := ""
	if tx.Description != nil {
		description = *tx.Description
	}

	return &UnifiedTransaction{
		ID:            tx.ID,
		UserID:        *tx.UserID,
		Type:          txType,
		Status:        string(tx.Status),
		Amount:        amount,
		Currency:      currency,
		Description:   description,
		ReferenceID:   tx.ReferenceID,
		ReferenceType: derefString(tx.ReferenceType),
		CreatedAt:     tx.CreatedAt,
		CompletedAt:   completedAt,
		Metadata:      tx.Metadata,
	}
}

func (s *TransactionHistoryService) mapLedgerType(t entities.TransactionType) string {
	switch t {
	case entities.TransactionTypeDeposit:
		return "deposit"
	case entities.TransactionTypeWithdrawal:
		return "withdrawal"
	case entities.TransactionTypeInvestment:
		return "investment"
	case entities.TransactionTypeConversion:
		return "conversion"
	case entities.TransactionTypeInternalTransfer:
		return "transfer"
	default:
		return string(t)
	}
}

func (s *TransactionHistoryService) shouldIncludeType(filter *TransactionFilter, txType string) bool {
	if filter == nil || len(filter.Types) == 0 {
		return true
	}
	for _, t := range filter.Types {
		if t == txType {
			return true
		}
	}
	return false
}

func (s *TransactionHistoryService) sortByDate(txs []*UnifiedTransaction) {
	// Simple bubble sort for now - could use sort.Slice for larger datasets
	for i := 0; i < len(txs)-1; i++ {
		for j := 0; j < len(txs)-i-1; j++ {
			if txs[j].CreatedAt.Before(txs[j+1].CreatedAt) {
				txs[j], txs[j+1] = txs[j+1], txs[j]
			}
		}
	}
}

func (s *TransactionHistoryService) applyFilters(txs []*UnifiedTransaction, filter *TransactionFilter) []*UnifiedTransaction {
	if filter == nil {
		return txs
	}

	var result []*UnifiedTransaction
	for _, tx := range txs {
		if filter.Status != nil && tx.Status != *filter.Status {
			continue
		}
		if filter.StartDate != nil && tx.CreatedAt.Before(*filter.StartDate) {
			continue
		}
		if filter.EndDate != nil && tx.CreatedAt.After(*filter.EndDate) {
			continue
		}
		if filter.MinAmount != nil && tx.Amount.LessThan(*filter.MinAmount) {
			continue
		}
		if filter.MaxAmount != nil && tx.Amount.GreaterThan(*filter.MaxAmount) {
			continue
		}
		result = append(result, tx)
	}
	return result
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
