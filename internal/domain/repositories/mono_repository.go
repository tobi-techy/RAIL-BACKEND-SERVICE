package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// MonoRepository is the domain-facing persistence contract for Mono open
// banking (linked accounts, imported transactions, DirectPay payments).
//
// The implementation lives in
// `internal/infrastructure/repositories/mono_repository.go` and is wired in
// through `internal/infrastructure/di/mono_adapters.go`, mirroring the
// user-goal repository split so the Mono service stays unit-testable with
// fakes.
type MonoRepository interface {
	// --- Linked Accounts ---

	CreateLinkedAccount(ctx context.Context, acct *entities.MonoLinkedAccount) error
	GetLinkedAccountByID(ctx context.Context, userID, accountID uuid.UUID) (*entities.MonoLinkedAccount, error)
	GetLinkedAccountByMonoID(ctx context.Context, monoAccountID string) (*entities.MonoLinkedAccount, error)
	ListLinkedAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.MonoLinkedAccount, error)
	UpdateLinkedAccountStatus(ctx context.Context, accountID uuid.UUID, status string) error
	UpdateLinkedAccountBalance(ctx context.Context, accountID uuid.UUID, balance int64) error

	// --- Imported Transactions ---

	ImportTransactions(ctx context.Context, txns []*entities.MonoImportedTransaction) (int, error)
	GetTransactions(ctx context.Context, userID, accountID uuid.UUID, limit, offset int) ([]*entities.MonoImportedTransaction, error)
	GetSpendingSummary(ctx context.Context, userID uuid.UUID, start, end time.Time) (totalCredits, totalDebits int64, txnCount int, err error)
	GetCategoryBreakdown(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.MonoCategoryBreakdown, error)

	// --- Payments ---

	CreatePayment(ctx context.Context, pmt *entities.MonoPayment) error
	// GetPaymentByUserAndReference looks up a payment scoped to its owner so
	// one authenticated user can never read or refresh another user's payment.
	GetPaymentByUserAndReference(ctx context.Context, userID uuid.UUID, reference string) (*entities.MonoPayment, error)
	// GetPaymentByReference looks up a payment by reference alone. Only for
	// trusted system callers (secret-verified Mono webhooks) that have no user
	// context — never expose it behind user authentication.
	GetPaymentByReference(ctx context.Context, reference string) (*entities.MonoPayment, error)
	UpdatePaymentStatus(ctx context.Context, paymentID uuid.UUID, status, monoRef string) error
}
