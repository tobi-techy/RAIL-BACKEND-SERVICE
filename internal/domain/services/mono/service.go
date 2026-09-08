package mono

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"go.uber.org/zap"
)

// Service orchestrates Mono Financial Data and DirectPay operations.
// It sits between the API handlers and the Mono HTTP client + repository,
// handling account linking, transaction syncing, spending analysis, and
// one-time payment initiation.
type Service struct {
	client Client
	repo   repositories.MonoRepository
	logger *zap.Logger
}

func NewService(client Client, repo repositories.MonoRepository, logger *zap.Logger) *Service {
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
	resp, err := s.client.InitiateLinking(ctx, &LinkingRequest{
		CustomerName:  customerName,
		CustomerEmail: customerEmail,
		MetaRef:       userID.String(),
		RedirectURL:   redirectURL,
	})
	if err != nil {
		return "", fmt.Errorf("initiate mono linking: %w", err)
	}
	return resp, nil
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
		acct = &AccountInfo{
			ID:            exchangeResp.ID,
			Name:          exchangeResp.Name,
			AccountNumber: exchangeResp.AccountNumber,
			Type:          exchangeResp.Type,
		}
	}

	// Build the last-4 for display.
	accountNumberLast4 := acct.AccountNumber
	if len(accountNumberLast4) > 4 {
		accountNumberLast4 = accountNumberLast4[len(accountNumberLast4)-4:]
	}

	entity := &entities.MonoLinkedAccount{
		UserID:        &userID,
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

// InitiateGuestLinking starts a Mono Connect flow for a guest (pre-signup)
// chat session. The guest token is passed as MetaRef so the linking session is
// tied to the guest conversation, not to a user that does not exist yet.
func (s *Service) InitiateGuestLinking(ctx context.Context, guestToken, customerName, customerEmail, redirectURL string) (string, error) {
	if guestToken == "" {
		return "", fmt.Errorf("guest token is required")
	}
	resp, err := s.client.InitiateLinking(ctx, &LinkingRequest{
		CustomerName:  customerName,
		CustomerEmail: customerEmail,
		MetaRef:       guestToken,
		RedirectURL:   redirectURL,
	})
	if err != nil {
		return "", fmt.Errorf("initiate guest mono linking: %w", err)
	}
	return resp, nil
}

// CompleteGuestLinking exchanges the Mono Connect widget code for a persistent
// account and stores it as a guest-linked account (user_id NULL, guest_token
// set). The account is claimed by a real user at signup via
// AttachLinkedAccountToUser.
func (s *Service) CompleteGuestLinking(ctx context.Context, guestToken, code string) (*entities.MonoLinkedAccount, error) {
	if guestToken == "" {
		return nil, fmt.Errorf("guest token is required")
	}
	exchangeResp, err := s.client.ExchangeCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange mono code: %w", err)
	}

	acct, err := s.client.GetAccount(ctx, exchangeResp.ID)
	if err != nil {
		s.logger.Warn("Failed to fetch Mono account details after guest linking",
			zap.String("mono_account_id", exchangeResp.ID),
			zap.Error(err))
		acct = &AccountInfo{
			ID:            exchangeResp.ID,
			Name:          exchangeResp.Name,
			AccountNumber: exchangeResp.AccountNumber,
			Type:          exchangeResp.Type,
		}
	}

	accountNumberLast4 := acct.AccountNumber
	if len(accountNumberLast4) > 4 {
		accountNumberLast4 = accountNumberLast4[len(accountNumberLast4)-4:]
	}

	entity := &entities.MonoLinkedAccount{
		GuestToken:    &guestToken,
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
		return nil, fmt.Errorf("persist guest mono linked account: %w", err)
	}

	return entity, nil
}

// AttachGuestAccountToUser claims a guest-linked Mono account for a real user
// at signup. Returns the claimed account.
func (s *Service) AttachGuestAccountToUser(ctx context.Context, guestToken string, userID uuid.UUID) (*entities.MonoLinkedAccount, error) {
	acct, err := s.repo.GetLinkedAccountByGuestToken(ctx, guestToken)
	if err != nil {
		return nil, fmt.Errorf("get guest mono account: %w", err)
	}
	if err := s.repo.AttachLinkedAccountToUser(ctx, acct.MonoAccountID, userID); err != nil {
		return nil, fmt.Errorf("attach guest mono account: %w", err)
	}
	acct.UserID = &userID
	acct.GuestToken = nil
	return acct, nil
}

// GetGuestSpendingAnalysis computes a spending breakdown for a guest-linked
// Mono account directly from Mono (the guest has no user row yet, so the
// imported-transactions tables cannot hold their data). This is the data behind
// Miriam's conversational "aha" moment before signup.
func (s *Service) GetGuestSpendingAnalysis(ctx context.Context, guestToken string, days int) (*entities.MonoSpendingAnalysis, error) {
	if guestToken == "" {
		return nil, fmt.Errorf("guest token is required")
	}
	acct, err := s.repo.GetLinkedAccountByGuestToken(ctx, guestToken)
	if err != nil {
		return nil, fmt.Errorf("get guest mono account: %w", err)
	}
	if acct.Status == entities.MonoAccountStatusUnlinked {
		return nil, fmt.Errorf("account is unlinked")
	}
	if days <= 0 {
		days = 30
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -days)

	txns, err := s.client.GetTransactions(ctx, acct.MonoAccountID, &TransactionQuery{Start: start, End: end})
	if err != nil {
		return nil, fmt.Errorf("fetch guest mono transactions: %w", err)
	}

	analysis := &entities.MonoSpendingAnalysis{
		Period: entities.MonoAnalysisPeriod{Start: start, End: end, Days: days},
	}
	byCat := map[string]*entities.MonoCategoryBreakdown{}
	for _, t := range txns {
		if t.Type == "credit" {
			analysis.TotalCredits += t.Amount
			continue
		}
		analysis.TotalDebits += t.Amount
		cat := t.Category
		if cat == "" {
			cat = "Other"
		}
		b := byCat[cat]
		if b == nil {
			b = &entities.MonoCategoryBreakdown{Category: cat}
			byCat[cat] = b
		}
		b.Amount += t.Amount
		b.Count++
	}
	analysis.TransactionCount = len(txns)
	analysis.NetCashFlow = analysis.TotalCredits - analysis.TotalDebits
	if analysis.TotalCredits > 0 {
		analysis.SavingsRate = float64(analysis.NetCashFlow) / float64(analysis.TotalCredits)
	}
	for _, b := range byCat {
		if analysis.TotalDebits > 0 {
			b.Percent = float64(b.Amount) / float64(analysis.TotalDebits)
		}
		analysis.ByCategory = append(analysis.ByCategory, *b)
	}
	enrichAnalysis(analysis, txns)
	return analysis, nil
}

// enrichAnalysis fills the deeper financial picture (income stability, income
// sources, recurring subscriptions, cash-flow forecast) from a transaction list.
// It is shared by the guest (live Mono fetch) and authenticated (imported rows)
// paths so both produce the same enriched shape.
func enrichAnalysis(analysis *entities.MonoSpendingAnalysis, txns []Transaction) {
	if analysis == nil {
		return
	}
	credits := make([]int64, 0, len(txns))
	creditByDesc := map[string][]int64{}
	recurring := map[string]*recurringCharges{}
	for _, t := range txns {
		if t.Type == "credit" {
			credits = append(credits, t.Amount)
			key := normalizeMerchant(t.Description)
			if key != "" {
				creditByDesc[key] = append(creditByDesc[key], t.Amount)
			}
			continue
		}
		key := normalizeMerchant(t.Description)
		if key == "" {
			continue
		}
		rc := recurring[key]
		if rc == nil {
			rc = &recurringCharges{merchant: t.Description, category: t.Category}
			recurring[key] = rc
		}
		rc.amounts = append(rc.amounts, t.Amount)
		rc.count++
	}

	// Income stability: how consistent the credit amounts are. Low spread of
	// credit amounts (a steady salary) scores high.
	analysis.IncomeStability = incomeStability(credits)
	analysis.IncomeSources = len(creditByDesc)

	// Recurring subscriptions: a merchant charged >=2 times with a stable amount.
	var subs []entities.MonoRecurringSubscription
	var recurringDebits int64
	for _, rc := range recurring {
		if rc.count < 2 {
			continue
		}
		modal := modalAmount(rc.amounts)
		if modal <= 0 {
			continue
		}
		// Require amounts to be reasonably stable around the modal.
		if !amountsStable(rc.amounts, modal) {
			continue
		}
		subs = append(subs, entities.MonoRecurringSubscription{
			Merchant: rc.merchant,
			Category: rc.category,
			Amount:   modal,
			Count:    rc.count,
		})
		recurringDebits += modal
	}
	analysis.RecurringSubscriptions = subs

	// Cash-flow forecast: typical recurring credit per source minus recurring
	// debits. Using the modal per source (not the period total) gives a monthly
	// estimate rather than summing every paycheck.
	var recurringCredits int64
	for _, amounts := range creditByDesc {
		recurringCredits += modalAmount(amounts)
	}
	analysis.CashFlowForecast = recurringCredits - recurringDebits
}

type recurringCharges struct {
	merchant string
	category string
	amounts  []int64
	count    int
}

// normalizeMerchant collapses a transaction description to a stable key for
// grouping recurring charges. Lowercased, stripped of digits and whitespace.
func normalizeMerchant(desc string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(desc) {
		if (r >= 'a' && r <= 'z') || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// incomeStability scores credit regularity from 0-1: 1 when there is a clear
// dominant steady credit, 0 when credits are scattered or absent.
func incomeStability(credits []int64) float64 {
	if len(credits) == 0 {
		return 0
	}
	if len(credits) == 1 {
		return 0.5
	}
	modal := modalAmount(credits)
	if modal <= 0 {
		return 0
	}
	// Fraction of credits close to the modal amount.
	close := 0
	for _, c := range credits {
		if modal > 0 && c > 0 {
			ratio := float64(c) / float64(modal)
			if ratio >= 0.8 && ratio <= 1.25 {
				close++
			}
		}
	}
	return float64(close) / float64(len(credits))
}

// modalAmount returns the most frequent amount in a list (a simple majority),
// falling back to the median when no clear mode exists.
func modalAmount(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	counts := map[int64]int{}
	var best int64
	bestN := 0
	for _, v := range vals {
		counts[v]++
		if counts[v] > bestN {
			bestN = counts[v]
			best = v
		}
	}
	if bestN >= 2 {
		return best
	}
	// No clear mode: return the median.
	sorted := make([]int64, len(vals))
	copy(sorted, vals)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

// amountsStable reports whether the charges for one merchant hover near the
// modal amount (within ~30%), so a subscription is recurring rather than one-off.
func amountsStable(vals []int64, modal int64) bool {
	if modal <= 0 {
		return false
	}
	stable := 0
	for _, v := range vals {
		ratio := float64(v) / float64(modal)
		if ratio >= 0.7 && ratio <= 1.3 {
			stable++
		}
	}
	return float64(stable)/float64(len(vals)) >= 0.6
}

// --- Account Sync ---

// TriggerIncomeAnalysis starts the async income analysis for a linked account.
// Results arrive via the mono.events.account_income webhook and include income
// streams, monthly averages, stability scores, employer, and regular vs
// irregular income split — valuable data for Miriam's coaching.
func (s *Service) TriggerIncomeAnalysis(ctx context.Context, userID, accountID uuid.UUID) error {
	acct, err := s.repo.GetLinkedAccountByID(ctx, userID, accountID)
	if err != nil {
		return fmt.Errorf("get linked account: %w", err)
	}
	if acct.Status == entities.MonoAccountStatusUnlinked {
		return fmt.Errorf("account is unlinked")
	}
	return s.client.InitiateIncomeAnalysis(ctx, acct.MonoAccountID, 12)
}

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
	txns, err := s.client.GetTransactions(ctx, acct.MonoAccountID, &TransactionQuery{
		Start: start,
		End:   end,
	})
	if err != nil {
		return 0, fmt.Errorf("fetch mono transactions: %w", err)
	}

	// Convert to entities. Category/SubCategory arrive already resolved from
	// Mono's enriched metadata with the raw-category fallback applied.
	imported := make([]*entities.MonoImportedTransaction, 0, len(txns))
	for _, t := range txns {
		imported = append(imported, &entities.MonoImportedTransaction{
			UserID:          userID,
			AccountID:       accountID,
			MonoTxnID:       t.ID,
			Amount:          t.Amount,
			Type:            t.Type,
			Description:     t.Description,
			Category:        t.Category,
			SubCategory:     t.SubCategory,
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
	}

	analysis := &entities.MonoSpendingAnalysis{
		TotalCredits:     totalCredits,
		TotalDebits:      totalDebits,
		NetCashFlow:      netCashFlow,
		SavingsRate:      savingsRate,
		ByCategory:       categories,
		Period:           entities.MonoAnalysisPeriod{Start: start, End: end, Days: days},
		TransactionCount: txnCount,
	}

	// Enrich with income stability, recurring subscriptions, and cash-flow
	// forecast from the imported transactions, so the authenticated analysis
	// matches the guest shape (and the subscription follow-up nudge can fire).
	if recent, err := s.repo.GetRecentTransactions(ctx, userID, start, end); err == nil {
		enrichAnalysis(analysis, importedTxnsToDomain(recent))
	}

	return analysis, nil
}

// importedTxnsToDomain converts imported transactions to the domain Transaction
// shape used by enrichAnalysis.
func importedTxnsToDomain(txns []*entities.MonoImportedTransaction) []Transaction {
	if len(txns) == 0 {
		return nil
	}
	out := make([]Transaction, 0, len(txns))
	for _, t := range txns {
		if t == nil {
			continue
		}
		out = append(out, Transaction{
			ID:          t.MonoTxnID,
			Amount:      t.Amount,
			Type:        t.Type,
			Description: t.Description,
			Category:    t.Category,
			SubCategory: t.SubCategory,
			Date:        t.TransactionDate,
			Reference:   t.Reference,
		})
	}
	return out
}

// DetectedSubscriptions returns the recurring subscriptions detected in a
// user's linked bank data (last 30 days). Satisfies the proactive nudge
// engine's SubscriptionProvider so Miriam can reopen a charge worth cutting.
func (s *Service) DetectedSubscriptions(ctx context.Context, userID uuid.UUID) ([]entities.MonoRecurringSubscription, error) {
	analysis, err := s.GetSpendingAnalysis(ctx, userID, 30)
	if err != nil {
		return nil, err
	}
	return analysis.RecurringSubscriptions, nil
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

	resp, err := s.client.InitiatePayment(ctx, &PaymentRequest{
		AmountKobo:    amountKobo,
		AccountID:     acct.MonoAccountID,
		Description:   description,
		Reference:     reference,
		RedirectURL:   redirectURL,
		CustomerEmail: customerEmail,
		CustomerName:  customerName,
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

// VerifyDeposit checks the payment status with Mono and updates the local
// record. The lookup is scoped to the authenticated user so one user can
// never read or refresh another user's payment by reference.
func (s *Service) VerifyDeposit(ctx context.Context, userID uuid.UUID, reference string) (*entities.MonoPayment, error) {
	pmt, err := s.repo.GetPaymentByUserAndReference(ctx, userID, reference)
	if err != nil {
		return nil, fmt.Errorf("get payment: %w", err)
	}
	return s.refreshPaymentStatus(ctx, pmt, reference)
}

// VerifyDepositByReference refreshes a payment by reference alone. Only for
// trusted system callers without user context (the secret-verified Mono
// webhook) — never expose it behind user authentication; use VerifyDeposit
// there.
func (s *Service) VerifyDepositByReference(ctx context.Context, reference string) (*entities.MonoPayment, error) {
	pmt, err := s.repo.GetPaymentByReference(ctx, reference)
	if err != nil {
		return nil, fmt.Errorf("get payment: %w", err)
	}
	return s.refreshPaymentStatus(ctx, pmt, reference)
}

// refreshPaymentStatus verifies with Mono and persists the new status. The
// in-memory entity is only mutated after persistence succeeds so callers never
// see a status the database rejected.
func (s *Service) refreshPaymentStatus(ctx context.Context, pmt *entities.MonoPayment, reference string) (*entities.MonoPayment, error) {
	resp, err := s.client.VerifyPayment(ctx, reference)
	if err != nil {
		return nil, fmt.Errorf("verify mono payment: %w", err)
	}

	newStatus := resp.Status
	if newStatus == "" {
		newStatus = entities.MonoPaymentStatusPending
	}
	if err := s.repo.UpdatePaymentStatus(ctx, pmt.ID, newStatus, resp.MonoRef); err != nil {
		return nil, fmt.Errorf("update mono payment status: %w", err)
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
