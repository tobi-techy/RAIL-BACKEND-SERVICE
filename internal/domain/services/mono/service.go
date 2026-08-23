package mono

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	monoadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/mono"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

// Service orchestrates Mono Financial Data and DirectPay operations.
// It sits between the API handlers and the Mono HTTP client + repository,
// handling account linking, transaction syncing, spending analysis, and
// one-time payment initiation.
type Service struct {
	client monoadapter.Client
	repo   *repositories.MonoRepository
	logger *zap.Logger
}

func NewService(client monoadapter.Client, repo *repositories.MonoRepository, logger *zap.Logger) *Service {
	return &Service{
		client: client,
		repo:   repo,
		logger: logger,
	}
}

// --- Account Linking ---

// InitiateLinking starts the Mono Connect widget flow and returns the redirect URL
// the frontend should open in a webview.
func (s *Service) InitiateLinking(ctx context.Context, userID uuid.UUID, customerName, customerEmail, redirectURL string) (string, error) {
	resp, err := s.client.InitiateLinking(ctx, &monoadapter.InitiateLinkingRequest{
		Customer: monoadapter.Customer{
			Name:  customerName,
			Email: customerEmail,
		},
		Meta: &monoadapter.MetaRef{
			Ref: userID.String(),
		},
		Scope:       "auth",
		RedirectURL: redirectURL,
	})
	if err != nil {
		return "", fmt.Errorf("initiate mono linking: %w", err)
	}
	return resp.RedirectURL, nil
}

// CompleteLinking exchanges the public code (returned by the Mono Connect widget)
// for a persistent Mono account ID, then fetches account details and persists the
// linked account record.
func (s *Service) CompleteLinking(ctx context.Context, userID uuid.UUID, code string) (*entities.MonoLinkedAccount, error) {
	exchangeResp, err := s.client.ExchangeCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange mono code: %w", err)
	}

	// Fetch full account details.
	acct, err := s.client.GetAccount(ctx, exchangeResp.ID)
	if err != nil {
		s.logger.Warn("Failed to fetch Mono account details after linking",
			zap.String("mono_account_id", exchangeResp.ID),
			zap.Error(err))
		// Fall back to brief data from the exchange response.
		acct = &monoadapter.Account{
			ID:            exchangeResp.ID,
			Name:          exchangeResp.Account.Name,
			AccountNumber: exchangeResp.Account.AccountNumber,
			Type:          exchangeResp.Account.Type,
		}
	}

	// Build the last-4 for display.
	accountNumberLast4 := acct.AccountNumber
	if len(accountNumberLast4) > 4 {
		accountNumberLast4 = accountNumberLast4[len(accountNumberLast4)-4:]
	}

	entity := &entities.MonoLinkedAccount{
		UserID:        userID,
		MonoAccountID: exchangeResp.ID,
		Institution:   acct.BankName,
		AccountName:   acct.Name,
		AccountNumber: accountNumberLast4,
		AccountType:   acct.Type,
		Currency:      acct.Currency,
		Balance:       acct.Balance,
		Status:        entities.MonoAccountStatusLinked,
	}
	if entity.Currency == "" {
		entity.Currency = "NGN"
	}

	if err := s.repo.CreateLinkedAccount(ctx, entity); err != nil {
		return nil, fmt.Errorf("persist mono linked account: %w", err)
	}

	return entity, nil
}

// --- Account Sync ---

// SyncAccount fetches the latest account details and transactions from Mono
// and updates the stored records. Returns the number of new transactions imported.
func (s *Service) SyncAccount(ctx context.Context, userID, accountID uuid.UUID) (int, error) {
	acct, err := s.repo.GetLinkedAccountByID(ctx, userID, accountID)
	if err != nil {
		return 0, fmt.Errorf("get linked account: %w", err)
	}
	if acct.Status == entities.MonoAccountStatusUnlinked {
		return 0, fmt.Errorf("account is unlinked")
	}

	// Update balance from Mono.
	monoAcct, err := s.client.GetAccount(ctx, acct.MonoAccountID)
	if err != nil {
		s.logger.Warn("Failed to fetch Mono account during sync",
			zap.String("mono_account_id", acct.MonoAccountID),
			zap.Error(err))
	} else {
		_ = s.repo.UpdateLinkedAccountBalance(ctx, accountID, monoAcct.Balance)
	}

	// Fetch transactions (last 90 days by default).
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -90)
	txns, err := s.client.GetTransactions(ctx, acct.MonoAccountID, &monoadapter.TransactionListQuery{
		Start:    start.Format("2006-01-02"),
		End:      end.Format("2006-01-02"),
		Paginate: false, // get all in one request
	})
	if err != nil {
		return 0, fmt.Errorf("fetch mono transactions: %w", err)
	}

	// Convert to entities.
	imported := make([]*entities.MonoImportedTransaction, 0, len(txns))
	for _, t := range txns {
		category := ""
		subCategory := ""
		if t.Meta != nil {
			category = t.Meta.Category
			subCategory = t.Meta.SubCategory
		}
		if category == "" {
			category = t.Category
		}

		imported = append(imported, &entities.MonoImportedTransaction{
			UserID:          userID,
			AccountID:       accountID,
			MonoTxnID:       t.ID,
			Amount:          t.Amount,
			Type:            t.Type,
			Description:     t.Description,
			Category:        category,
			SubCategory:     subCategory,
			TransactionDate: t.Date,
			Reference:       t.Reference,
		})
	}

	inserted, err := s.repo.ImportTransactions(ctx, imported)
	if err != nil {
		return 0, fmt.Errorf("import mono transactions: %w", err)
	}

	s.logger.Info("Mono account synced",
		zap.String("account_id", accountID.String()),
		zap.Int("imported", inserted),
		zap.Int("total_fetched", len(txns)))

	return inserted, nil
}

// --- Transaction Retrieval ---

func (s *Service) GetTransactions(ctx context.Context, userID, accountID uuid.UUID, limit, offset int) ([]*entities.MonoImportedTransaction, error) {
	return s.repo.GetTransactions(ctx, userID, accountID, limit, offset)
}

// --- Spending Analysis ---

// GetSpendingAnalysis computes a spending breakdown for the given period
// (default: last 30 days). This is the primary data source for Miriam's
// coaching context and the bank statement analysis tool when a Mono account
// is linked.
func (s *Service) GetSpendingAnalysis(ctx context.Context, userID uuid.UUID, days int) (*entities.MonoSpendingAnalysis, error) {
	if days <= 0 {
		days = 30
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -days)

	totalCredits, totalDebits, txnCount, err := s.repo.GetSpendingSummary(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("get spending summary: %w", err)
	}

	categories, err := s.repo.GetCategoryBreakdown(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("get category breakdown: %w", err)
	}

	// Compute percentages.
	for i := range categories {
		if totalDebits > 0 {
			categories[i].Percent = float64(categories[i].Amount) / float64(totalDebits)
		}
	}

	netCashFlow := totalCredits - totalDebits
	var savingsRate float64
	if totalCredits > 0 {
		savingsRate = float64(netCashFlow) / float64(totalCredits)
		if savingsRate < 0 {
			savingsRate = 0
		}
	}

	return &entities.MonoSpendingAnalysis{
		TotalCredits:     totalCredits,
		TotalDebits:      totalDebits,
		NetCashFlow:      netCashFlow,
		SavingsRate:      savingsRate,
		ByCategory:       categories,
		Period:           entities.MonoAnalysisPeriod{Start: start, End: end, Days: days},
		TransactionCount: txnCount,
	}, nil
}

// --- DirectPay ---

// InitiateDeposit starts a one-time DirectPay debit from the user's linked
// bank account. Returns the approval URL the user must visit to authorise
// the payment and the payment record ID for tracking.
func (s *Service) InitiateDeposit(ctx context.Context, userID, accountID uuid.UUID, amountKobo int64, description, reference, redirectURL, customerEmail, customerName string) (*entities.MonoPayment, error) {
	acct, err := s.repo.GetLinkedAccountByID(ctx, userID, accountID)
	if err != nil {
		return nil, fmt.Errorf("get linked account: %w", err)
	}
	if acct.Status != entities.MonoAccountStatusLinked {
		return nil, fmt.Errorf("account is not linked (status: %s)", acct.Status)
	}

	resp, err := s.client.InitiatePayment(ctx, &monoadapter.InitiatePaymentRequest{
		Amount:      amountKobo,
		Type:        "onetime-debit",
		Method:      "account",
		Account:     acct.MonoAccountID,
		Description: description,
		Reference:   reference,
		RedirectURL: redirectURL,
		Customer: &monoadapter.PaymentCustomer{
			Email: customerEmail,
			Name:  customerName,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initiate mono payment: %w", err)
	}

	pmt := &entities.MonoPayment{
		UserID:      userID,
		AccountID:   accountID,
		Amount:      amountKobo,
		Reference:   reference,
		Status:      resp.Status,
		MonoRef:     resp.PaymentID,
		ApprovalURL: resp.ApprovalURL,
		Description: description,
	}
	if pmt.Status == "" {
		pmt.Status = entities.MonoPaymentStatusPending
	}

	if err := s.repo.CreatePayment(ctx, pmt); err != nil {
		return nil, fmt.Errorf("persist mono payment: %w", err)
	}

	return pmt, nil
}

// VerifyDeposit checks the payment status with Mono and updates the local record.
func (s *Service) VerifyDeposit(ctx context.Context, reference string) (*entities.MonoPayment, error) {
	pmt, err := s.repo.GetPaymentByReference(ctx, reference)
	if err != nil {
		return nil, fmt.Errorf("get payment: %w", err)
	}

	resp, err := s.client.VerifyPayment(ctx, reference)
	if err != nil {
		return nil, fmt.Errorf("verify mono payment: %w", err)
	}

	// Update local record.
	newStatus := resp.Status
	if newStatus == "" {
		newStatus = entities.MonoPaymentStatusPending
	}
	if err := s.repo.UpdatePaymentStatus(ctx, pmt.ID, newStatus, resp.MonoRef); err != nil {
		s.logger.Error("Failed to update mono payment status", zap.Error(err))
	}

	pmt.Status = newStatus
	pmt.MonoRef = resp.MonoRef
	if newStatus == entities.MonoPaymentStatusSuccessful || newStatus == entities.MonoPaymentStatusFailed {
		now := time.Now().UTC()
		pmt.VerifiedAt = &now
	}

	return pmt, nil
}

// --- Account Management ---

func (s *Service) ListLinkedAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.MonoLinkedAccount, error) {
	return s.repo.ListLinkedAccounts(ctx, userID)
}

func (s *Service) UnlinkAccount(ctx context.Context, userID, accountID uuid.UUID) error {
	acct, err := s.repo.GetLinkedAccountByID(ctx, userID, accountID)
	if err != nil {
		return fmt.Errorf("get linked account: %w", err)
	}

	// Call Mono to unlink.
	if err := s.client.UnlinkAccount(ctx, acct.MonoAccountID); err != nil {
		s.logger.Warn("Mono unlink API call failed, marking as unlinked locally",
			zap.String("mono_account_id", acct.MonoAccountID),
			zap.Error(err))
	}

	return s.repo.UpdateLinkedAccountStatus(ctx, accountID, entities.MonoAccountStatusUnlinked)
}

// --- Webhook Handling ---

// HandleWebhook processes Mono webhook events. Currently supports:
//   - account_reauthorized: mark account as linked again
//   - account_unlinked: mark account as unlinked
func (s *Service) HandleWebhook(ctx context.Context, event string, monoAccountID string) error {
	switch event {
	case "account_reauthorized":
		acct, err := s.repo.GetLinkedAccountByMonoID(ctx, monoAccountID)
		if err != nil {
			return fmt.Errorf("get linked account for reauth: %w", err)
		}
		return s.repo.UpdateLinkedAccountStatus(ctx, acct.ID, entities.MonoAccountStatusLinked)

	case "account_unlinked":
		acct, err := s.repo.GetLinkedAccountByMonoID(ctx, monoAccountID)
		if err != nil {
			return fmt.Errorf("get linked account for unlink: %w", err)
		}
		return s.repo.UpdateLinkedAccountStatus(ctx, acct.ID, entities.MonoAccountStatusUnlinked)

	default:
		s.logger.Debug("Unhandled Mono webhook event",
			zap.String("event", event),
			zap.String("mono_account_id", monoAccountID))
		return nil
	}
}
