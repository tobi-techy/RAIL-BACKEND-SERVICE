package funding

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/graph"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/shopspring/decimal"
)

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505), falling back to a string match for wrapped or
// non-pq drivers. Used to treat a duplicate idempotency-key insert as a no-op.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "23505")
}

// GraphClient is the subset of the Graph adapter used for NGN virtual accounts.
type GraphClient interface {
	CreatePerson(ctx context.Context, req *graph.CreatePersonRequest) (*graph.Person, error)
	FetchPerson(ctx context.Context, personID string) (*graph.Person, error)
	UpgradePersonKYC(ctx context.Context, personID string, req *graph.UpgradePersonKYCRequest) (*graph.Person, error)
	CreateDocument(ctx context.Context, req *graph.CreateDocumentRequest) (*graph.EntityDocument, error)
	CreateBankAccount(ctx context.Context, req *graph.CreateBankAccountRequest) (*graph.BankAccount, error)
	FetchBankAccount(ctx context.Context, accountID string) (*graph.BankAccount, error)
	ListBankAccounts(ctx context.Context, personID string) ([]graph.BankAccount, error)
	FetchRate(ctx context.Context, source, target string) (*graph.Rate, error)
	CreateConversion(ctx context.Context, req *graph.CreateConversionRequest) (*graph.Conversion, error)
}

// GraphVirtualAccountRepository extends VirtualAccountRepository with the Graph
// account lookup used to route inbound NGN deposit webhooks to a user.
type GraphVirtualAccountRepository interface {
	VirtualAccountRepository
	GetByGraphAccountID(ctx context.Context, graphAccountID string) (*entities.VirtualAccount, error)
}

// GraphUserProvider reads and mutates the user identity fields Graph needs.
type GraphUserProvider interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
	UpdateGraphPersonID(ctx context.Context, userID uuid.UUID, personID string) error
	UpdateKYCTier(ctx context.Context, userID uuid.UUID, tier int) error
	MarkBVNVerified(ctx context.Context, userID uuid.UUID, last4 string) error
	MarkNINVerified(ctx context.Context, userID uuid.UUID, last4 string) error
}

// Tier2LivenessInitiator kicks off an async, non-blocking identity liveness
// check (e.g. Didit selfie) after NGN provisioning. Optional; when unset the
// service skips the liveness step. Provisioning never waits on this.
type Tier2LivenessInitiator interface {
	InitiateLiveness(ctx context.Context, userID uuid.UUID) error
}

// CurrencyRateProvider returns the latest FX rate for a pair. GetLatestRate
// with ("USD","NGN") returns NGN-per-USD (e.g. ~1600).
type CurrencyRateProvider interface {
	GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error)
}

// GraphVirtualAccountService provisions NGN named virtual accounts via Graph and
// processes inbound Naira deposits (NGN → USDC → 70/30 split).
type GraphVirtualAccountService struct {
	graphClient         GraphClient
	virtualAccountRepo  GraphVirtualAccountRepository
	depositRepo         DepositRepository
	userProvider        GraphUserProvider
	allocationService   AllocationService
	ledgerIntegration   LedgerIntegration
	complianceScreener  ComplianceScreener
	notificationService FundingNotificationService
	gameplayHooks       FundingGameplayHooks
	currencyRates       CurrencyRateProvider
	livenessInitiator   Tier2LivenessInitiator
	developerFeePercent decimal.Decimal
	logger              *logger.Logger
}

// NewGraphVirtualAccountService constructs the Graph NGN virtual account service.
func NewGraphVirtualAccountService(
	graphClient GraphClient,
	virtualAccountRepo GraphVirtualAccountRepository,
	depositRepo DepositRepository,
	userProvider GraphUserProvider,
	allocationService AllocationService,
	ledgerIntegration LedgerIntegration,
	developerFeePercent float64,
	logger *logger.Logger,
) *GraphVirtualAccountService {
	fee := decimal.NewFromFloat(developerFeePercent)
	if fee.LessThan(decimal.Zero) {
		fee = decimal.Zero
	}
	return &GraphVirtualAccountService{
		graphClient:         graphClient,
		virtualAccountRepo:  virtualAccountRepo,
		depositRepo:         depositRepo,
		userProvider:        userProvider,
		allocationService:   allocationService,
		ledgerIntegration:   ledgerIntegration,
		developerFeePercent: fee,
		logger:              logger,
	}
}

// SetComplianceScreener sets the AML/sanctions screening service (optional).
func (s *GraphVirtualAccountService) SetComplianceScreener(cs ComplianceScreener) {
	s.complianceScreener = cs
}

// SetNotificationService sets the notification service (optional).
func (s *GraphVirtualAccountService) SetNotificationService(n FundingNotificationService) {
	s.notificationService = n
}

// SetGameplayHooks sets the gameplay hooks (optional).
func (s *GraphVirtualAccountService) SetGameplayHooks(gh FundingGameplayHooks) {
	s.gameplayHooks = gh
}

// SetCurrencyRates sets the FX rate provider used for NGN→USDC fallback pricing.
func (s *GraphVirtualAccountService) SetCurrencyRates(r CurrencyRateProvider) {
	s.currencyRates = r
}

// SetLivenessInitiator sets the optional async Tier 2 liveness initiator.
func (s *GraphVirtualAccountService) SetLivenessInitiator(l Tier2LivenessInitiator) {
	s.livenessInitiator = l
}

// ── Errors ────────────────────────────────────────────────────────

// ErrNGNRequiresBVNNIN is returned when a user tries to provision an NGN account
// without supplying the Tier 2 prerequisites (BVN + a means of ID, NIN preferred).
var ErrNGNRequiresBVNNIN = fmt.Errorf("graph: BVN and a means of ID (NIN preferred) are required to open an NGN account")

// ProvisionNGNAccountRequest carries the transient PII needed to create a Graph
// person and NGN account. BVN and ID number are never persisted.
type ProvisionNGNAccountRequest struct {
	UserID        uuid.UUID
	BVN           string // transient — never stored, only verified flag + last4 persisted
	IDType        string // nin, voters_card, drivers_license, passport
	IDNumber      string // transient
	IDDocumentURL string // URL of the uploaded ID document (e.g. R2)
	// Optional KYC background information.
	EmploymentStatus string
	Occupation       string
	SourceOfFunds    string
	PrimaryPurpose   string
}

// ProvisionNGNAccount creates (or returns the existing) Graph NGN named virtual
// account for a user. Tier 2 gate: requires BVN + a means of ID (NIN preferred).
// Graph verifies BVN/NIN against government databases on CreatePerson; on success
// we promote the user to Tier 2 and provision the account immediately. Any Didit
// liveness check runs async and never blocks provisioning.
func (s *GraphVirtualAccountService) ProvisionNGNAccount(ctx context.Context, req *ProvisionNGNAccountRequest) (*entities.VirtualAccount, error) {
	user, err := s.userProvider.GetByID(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// Tier 2 prerequisite: BVN + a means of ID must be supplied. Graph performs
	// the actual BVN/NIN verification when the person is created.
	if strings.TrimSpace(req.BVN) == "" || strings.TrimSpace(req.IDType) == "" || strings.TrimSpace(req.IDNumber) == "" {
		return nil, ErrNGNRequiresBVNNIN
	}

	// Idempotent: return existing NGN account if present.
	if existing, _ := s.virtualAccountRepo.GetActiveByUserIDAndCurrency(ctx, req.UserID, "NGN"); existing != nil {
		return existing, nil
	}

	// Ensure a Graph person exists for this user.
	personID, err := s.ensurePerson(ctx, user, req)
	if err != nil {
		return nil, err
	}

	// Create the NGN bank account (autosweep to Rail's master wallet).
	label := "Rail NGN"
	if user.FirstName != nil && *user.FirstName != "" {
		label = *user.FirstName + " NGN"
	}
	bankAcct, err := s.graphClient.CreateBankAccount(ctx, &graph.CreateBankAccountRequest{
		PersonID:         personID,
		Label:            label,
		Currency:         "NGN",
		AutosweepEnabled: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create graph bank account: %w", err)
	}

	now := time.Now()
	graphAccountID := bankAcct.ID
	va := &entities.VirtualAccount{
		ID:              uuid.New(),
		UserID:          req.UserID,
		Provider:        entities.VirtualAccountProviderGraph,
		GraphPersonID:   &personID,
		GraphAccountID:  &graphAccountID,
		AccountNumber:   bankAcct.AccountNumber,
		RoutingNumber:   bankAcct.RoutingNumber,
		BankCode:        bankAcct.BankCode,
		BankName:        bankAcct.BankName,
		BeneficiaryName: bankAcct.AccountName,
		Status:          mapGraphAccountStatus(bankAcct.Status),
		Currency:        "NGN",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if va.BeneficiaryName == "" && user.FirstName != nil && user.LastName != nil {
		va.BeneficiaryName = *user.FirstName + " " + *user.LastName
	}

	if err := s.virtualAccountRepo.Create(ctx, va); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			if existing, fetchErr := s.virtualAccountRepo.GetActiveByUserIDAndCurrency(ctx, req.UserID, "NGN"); fetchErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("store graph virtual account: %w", err)
	}

	s.logger.Info("Graph NGN virtual account provisioned",
		"user_id", req.UserID,
		"virtual_account_id", va.ID,
		"graph_account_id", graphAccountID,
		"status", string(va.Status))

	// Persist BVN/NIN-verified flags + Tier 2 promotion only AFTER the NGN account
	// exists, so a mid-flight failure can never leave a user promoted-but-
	// unprovisioned (higher limits with no account). Raw BVN/NIN are never stored.
	if req.BVN != "" {
		if err := s.userProvider.MarkBVNVerified(ctx, req.UserID, last4(req.BVN)); err != nil {
			s.logger.Warn("Failed to mark BVN verified", "user_id", req.UserID, "error", err)
		}
	}
	if strings.EqualFold(strings.TrimSpace(req.IDType), "nin") && req.IDNumber != "" {
		if err := s.userProvider.MarkNINVerified(ctx, req.UserID, last4(req.IDNumber)); err != nil {
			s.logger.Warn("Failed to mark NIN verified", "user_id", req.UserID, "error", err)
		}
	}
	if user.KYCTier < entities.KYCTierLevelBasic {
		if err := s.userProvider.UpdateKYCTier(ctx, req.UserID, entities.KYCTierLevelBasic); err != nil {
			s.logger.Warn("Failed to promote KYC tier to basic", "user_id", req.UserID, "error", err)
		}
	}

	// Kick off the async Tier 2 liveness/fraud check (non-blocking, best-effort).
	if s.livenessInitiator != nil {
		userID := req.UserID
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.livenessInitiator.InitiateLiveness(bgCtx, userID); err != nil {
				s.logger.Warn("Failed to initiate Tier 2 liveness check", "user_id", userID, "error", err)
			}
		}()
	}

	return va, nil
}

// ensurePerson returns the user's Graph person ID, creating the person if needed.
func (s *GraphVirtualAccountService) ensurePerson(ctx context.Context, user *entities.UserProfile, req *ProvisionNGNAccountRequest) (string, error) {
	if user.GraphPersonID != nil && *user.GraphPersonID != "" {
		return *user.GraphPersonID, nil
	}

	if err := validateProfileForGraph(user, req); err != nil {
		return "", err
	}

	idLevel := "secondary"
	if strings.EqualFold(req.IDType, "passport") {
		idLevel = "primary"
	}

	personReq := &graph.CreatePersonRequest{
		NameFirst:    strings.TrimSpace(deref(user.FirstName)),
		NameLast:     strings.TrimSpace(deref(user.LastName)),
		NameOther:    strings.TrimSpace(deref(user.FirstName)), // Graph NGN requires name_other; reuse when no middle name on file
		Phone:        normalizePhone(deref(user.Phone)),
		Email:        user.Email,
		DOB:          user.DateOfBirth.Format("2006-01-02"),
		IDType:       normalizeIDType(req.IDType),
		IDNumber:     req.IDNumber,
		IDCountry:    "NG",
		IDLevel:      idLevel,
		BankIDNumber: req.BVN,
		KYCLevel:     "basic",
		Address: graph.Address{
			Line1:      deref(user.AddressStreet),
			City:       deref(user.AddressCity),
			State:      deref(user.AddressState),
			Country:    countryAlpha2("NG", user.AddressCountry),
			PostalCode: deref(user.AddressPostalCode),
		},
	}
	if req.EmploymentStatus != "" || req.Occupation != "" || req.SourceOfFunds != "" || req.PrimaryPurpose != "" {
		personReq.BackgroundInformation = &graph.BackgroundInformation{
			EmploymentStatus: req.EmploymentStatus,
			Occupation:       req.Occupation,
			SourceOfFunds:    req.SourceOfFunds,
			PrimaryPurpose:   req.PrimaryPurpose,
		}
	}
	if req.IDDocumentURL != "" {
		personReq.Documents = []graph.Document{{Type: graphDocType(req.IDType), URL: req.IDDocumentURL}}
	}

	person, err := s.graphClient.CreatePerson(ctx, personReq)
	if err != nil {
		return "", fmt.Errorf("create graph person: %w", err)
	}

	if err := s.userProvider.UpdateGraphPersonID(ctx, user.ID, person.ID); err != nil {
		s.logger.Warn("Failed to persist graph person id", "user_id", user.ID, "error", err)
	}
	return person.ID, nil
}

// GraphNGNDepositEvent is a normalized inbound NGN deposit from Graph webhooks.
type GraphNGNDepositEvent struct {
	GraphAccountID string
	TransactionID  string
	AmountNGN      string
	Reference      string
	Direction      string // credit | debit
}

// ProcessNGNDeposit credits an inbound Naira deposit: converts NGN → USDC and
// runs the automatic 70/30 spend/stash split. Idempotent by transaction id.
func (s *GraphVirtualAccountService) ProcessNGNDeposit(ctx context.Context, event *GraphNGNDepositEvent) error {
	if event.Direction != "" && !strings.EqualFold(event.Direction, "credit") {
		return nil // ignore debits/sweeps
	}
	graphAccountID := strings.TrimSpace(event.GraphAccountID)
	if graphAccountID == "" {
		return fmt.Errorf("graph_account_id is required")
	}
	txRef := strings.TrimSpace(event.TransactionID)
	if txRef == "" {
		return fmt.Errorf("transaction_id is required for idempotent NGN deposit processing")
	}

	ngnAmount, err := decimal.NewFromString(strings.TrimSpace(event.AmountNGN))
	if err != nil {
		return fmt.Errorf("invalid NGN amount %q: %w", event.AmountNGN, err)
	}
	if !ngnAmount.GreaterThan(decimal.Zero) {
		return fmt.Errorf("amount must be greater than zero")
	}
	if ngnAmount.LessThan(entities.MinDepositAmountNGN) {
		return fmt.Errorf("deposit %s NGN is below minimum %s", ngnAmount.String(), entities.MinDepositAmountNGN.String())
	}

	va, err := s.virtualAccountRepo.GetByGraphAccountID(ctx, graphAccountID)
	if err != nil {
		return fmt.Errorf("get virtual account: %w", err)
	}
	if va == nil {
		return fmt.Errorf("virtual account not found: %s", graphAccountID)
	}
	userID := va.UserID

	idempotencyKey := generateGraphIdempotencyKey(txRef, graphAccountID)

	// Idempotency pre-check. A confirmed deposit means this webhook was already
	// fully processed (Graph retries/replays are normal) — return without doing
	// any irreversible work. A pending row means a prior attempt claimed the lock
	// but failed before confirming; we resume it with the same deposit ID (both
	// the Graph conversion, keyed by reference, and the ledger credit, keyed by
	// deposit ID, are idempotent, so resuming cannot double-spend).
	depositID := uuid.New()
	resuming := false
	if s.depositRepo != nil {
		if existing, _ := s.depositRepo.GetByIdempotencyKey(ctx, idempotencyKey); existing != nil {
			switch existing.Status {
			case "confirmed", "broker_funded", "off_ramp_initiated", "off_ramp_completed":
				s.logger.Info("NGN deposit already processed (idempotent)", "idempotency_key", idempotencyKey)
				return nil
			default:
				depositID = existing.ID
				resuming = true
				s.logger.Info("Resuming stuck NGN deposit", "deposit_id", depositID, "idempotency_key", idempotencyKey)
			}
		}
	}

	// Compliance screening (NGN inbound). Only screen on the first attempt; a
	// resume already passed screening when the pending row was created.
	if !resuming && s.complianceScreener != nil {
		status, screenErr := s.complianceScreener.ScreenTransaction(ctx, userID, txRef, "inbound", ngnAmount, "NGN", "")
		if screenErr != nil {
			s.logger.Error("Compliance screening unavailable, blocking NGN deposit",
				"user_id", userID.String(), "ref", txRef, "error", screenErr)
			return fmt.Errorf("deposit held: compliance screening unavailable")
		}
		if status != "APPROVED" {
			s.logger.Warn("NGN deposit not approved by compliance",
				"user_id", userID.String(), "ref", txRef, "status", status)
			return fmt.Errorf("deposit held: compliance status %s", status)
		}
	}

	// Claim the idempotency lock BEFORE the irreversible NGN→USDC conversion, so
	// a replay/retry/race can never trigger a second real conversion. The row is
	// created with a zero placeholder amount + the raw NGN source amount (which
	// lets the recovery worker re-drive a conversion that failed mid-flight); the
	// resolved USDC amount is written once the conversion succeeds.
	if !resuming && s.depositRepo != nil {
		vaID := va.ID
		ngnCurrency := "NGN"
		sourceAmount := ngnAmount
		pending := &entities.Deposit{
			ID:               depositID,
			IdempotencyKey:   idempotencyKey,
			UserID:           userID,
			VirtualAccountID: &vaID,
			Chain:            entities.ChainFiat,
			TxHash:           txRef,
			Token:            entities.StablecoinUSDC,
			Amount:           decimal.Zero,
			SourceAmount:     &sourceAmount,
			SourceCurrency:   &ngnCurrency,
			Status:           "pending",
			CreatedAt:        time.Now(),
		}
		if err := s.depositRepo.Create(ctx, pending); err != nil {
			if isUniqueViolation(err) {
				// Lost a race to a concurrent delivery — the winner owns processing.
				s.logger.Info("NGN deposit already claimed (idempotent race)", "idempotency_key", idempotencyKey)
				return nil
			}
			return fmt.Errorf("create pending deposit: %w", err)
		}
	}

	// Convert NGN → USDC (net of developer fee). Graph dedupes by reference, so a
	// resume re-issues the same conversion safely. On failure the pending row is
	// intentionally left in place for the recovery worker / next webhook retry.
	usdcAmount, err := s.convertNGNToUSDC(ctx, ngnAmount, txRef)
	if err != nil {
		return fmt.Errorf("convert NGN to USDC: %w", err)
	}
	if !usdcAmount.GreaterThan(decimal.Zero) {
		return fmt.Errorf("converted USDC amount must be greater than zero")
	}

	// Persist the resolved settlement amount now that conversion succeeded.
	if s.depositRepo != nil {
		if err := s.depositRepo.UpdateDepositAmount(ctx, depositID, usdcAmount); err != nil {
			s.logger.Warn("Failed to persist resolved NGN deposit amount", "deposit_id", depositID, "error", err)
		}
	}

	// Atomic ledger credit + 70/30 allocation (idempotent on deposit ID).
	spendingAmount := usdcAmount.Mul(decimal.NewFromFloat(0.70)).Round(2)
	stashAmount := usdcAmount.Sub(spendingAmount)
	if err := s.ledgerIntegration.RecordDepositWithAllocation(
		ctx, userID, usdcAmount, depositID, "ngn_fiat", txRef, spendingAmount, stashAmount,
	); err != nil {
		// Leave the pending row for the recovery worker / webhook retry to resume;
		// deleting it here would lose the idempotency claim and the NGN source
		// amount needed to re-drive.
		return fmt.Errorf("record deposit with allocation: %w", err)
	}

	if s.depositRepo != nil {
		confirmedAt := time.Now()
		if err := s.depositRepo.UpdateStatus(ctx, depositID, "confirmed", &confirmedAt); err != nil {
			s.logger.Warn("Failed to update NGN deposit status", "deposit_id", depositID, "error", err)
		}
	}

	if metrics.Business != nil {
		metrics.Business.DepositsCompleted.WithLabelValues("ngn_fiat").Inc()
		metrics.Business.DepositAmount.WithLabelValues("ngn_fiat").Observe(usdcAmount.InexactFloat64())
	}

	// Allocation side-effects (yield routing, auto-invest, events). Ledger split
	// already committed atomically above; failures here are retried by recovery.
	sourceTxID := txRef
	if err := s.allocationService.ProcessIncomingFunds(ctx, &entities.IncomingFundsRequest{
		UserID:     userID,
		Amount:     usdcAmount,
		EventType:  entities.AllocationEventTypeFiatDeposit,
		DepositID:  &depositID,
		SourceTxID: &sourceTxID,
		Metadata: map[string]any{
			"source":            "graph_ngn",
			"graph_account_id":  graphAccountID,
			"original_currency": "NGN",
			"ngn_amount":        ngnAmount.String(),
			"transaction_ref":   txRef,
			"atomic_split":      true,
		},
	}); err != nil {
		s.logger.Warn("Allocation side-effects failed (ledger split already committed)",
			"user_id", userID, "deposit_id", depositID, "error", err)
	}

	if s.notificationService != nil {
		if err := s.notificationService.NotifyDepositConfirmed(ctx, userID, usdcAmount.StringFixed(2), "NGN", txRef); err != nil {
			s.logger.Warn("Failed to send NGN deposit notification", "user_id", userID, "error", err)
		}
	}
	if s.gameplayHooks != nil {
		s.gameplayHooks.OnDeposit(ctx, userID, usdcAmount, depositID)
	}

	s.logger.Info("Graph NGN deposit processed",
		"user_id", userID, "ngn_amount", ngnAmount.String(), "usdc_amount", usdcAmount.String(), "deposit_id", depositID)
	return nil
}

// convertNGNToUSDC converts a NGN amount to USDC net of the developer fee. It
// prefers a real Graph conversion (actual proceeds) and falls back to the live
// FX rate when the conversion API is unavailable.
func (s *GraphVirtualAccountService) convertNGNToUSDC(ctx context.Context, ngnAmount decimal.Decimal, reference string) (decimal.Decimal, error) {
	feeMultiplier := decimal.NewFromInt(1).Sub(s.developerFeePercent.Div(decimal.NewFromInt(100)))
	if feeMultiplier.LessThan(decimal.Zero) {
		feeMultiplier = decimal.Zero
	}

	// Primary path: Graph conversion API (NGN → USD). Amount must be in kobo
	// (minor units) — consistent with how Graph sends amounts in webhooks.
	amountKobo := int(ngnAmount.Mul(decimal.NewFromInt(100)).IntPart())
	conv, convErr := s.graphClient.CreateConversion(ctx, &graph.CreateConversionRequest{
		CurrencySource:      "NGN",
		CurrencyDestination: "USD",
		AmountSource:        amountKobo,
		Reference:           reference,
	})
	if convErr == nil && conv != nil {
		if target, perr := decimal.NewFromString(conv.TargetAmount); perr == nil && target.GreaterThan(decimal.Zero) {
			result := target.Mul(feeMultiplier).Round(2)
			return result, nil
		}
	}
	// Log primary path failure so production conversion issues are debuggable.
	if convErr != nil {
		s.logger.Warnw("Graph conversion API failed, falling back to rate lookup",
			"error", convErr, "ngn_amount", ngnAmount.String(), "reference", reference)
	}

	// Fallback 1: live rate from CurrencyRateProvider (e.g. external FX feed).
	if s.currencyRates != nil {
		if rate, err := s.currencyRates.GetLatestRate(ctx, "USD", "NGN"); err == nil && rate.GreaterThan(decimal.Zero) {
			result := ngnAmount.Div(rate).Mul(feeMultiplier).Round(2)
			s.logger.Infow("NGN→USD conversion used CurrencyRateProvider fallback",
				"rate", rate.String(), "result_usd", result.String(), "reference", reference)
			return result, nil
		}
	}

	// Fallback 2: Graph FetchRate endpoint.
	if rate, err := s.graphClient.FetchRate(ctx, "NGN", "USDC"); err == nil && rate != nil && rate.Rate > 0 {
		result := ngnAmount.Mul(decimal.NewFromFloat(rate.Rate)).Mul(feeMultiplier).Round(2)
		s.logger.Infow("NGN→USD conversion used Graph FetchRate fallback",
			"rate", rate.Rate, "result_usd", result.String(), "reference", reference)
		return result, nil
	}

	return decimal.Zero, fmt.Errorf("no NGN→USD rate available (all conversion paths exhausted)")
}

// HandleAccountActivated flips a pending NGN virtual account to active and fills
// HandleAccountActivatedWithData updates the virtual account with bank details
// from the webhook payload directly — no extra API call needed.
func (s *GraphVirtualAccountService) HandleAccountActivatedWithData(ctx context.Context, graphAccountID string, acct *graph.BankAccount) error {
	va, err := s.virtualAccountRepo.GetByGraphAccountID(ctx, graphAccountID)
	if err != nil {
		return fmt.Errorf("get virtual account: %w", err)
	}
	if va == nil {
		return fmt.Errorf("virtual account not found: %s", graphAccountID)
	}

	va.AccountNumber = acct.AccountNumber
	va.RoutingNumber = acct.RoutingNumber
	va.BankCode = acct.BankCode
	va.BankName = acct.BankName
	if acct.AccountName != "" {
		va.BeneficiaryName = acct.AccountName
	}
	va.Status = mapGraphAccountStatus(acct.Status)
	va.UpdatedAt = time.Now()

	if err := s.virtualAccountRepo.Update(ctx, va); err != nil {
		return fmt.Errorf("update virtual account: %w", err)
	}
	s.logger.Info("Graph NGN account activated", "graph_account_id", graphAccountID, "status", string(va.Status))
	return nil
}

// HandleAccountActivated fetches bank details from Graph and updates the virtual
// account in the bank details Graph delivers asynchronously via issuance webhook.
// Prefer HandleAccountActivatedWithData when webhook data is already available.
func (s *GraphVirtualAccountService) HandleAccountActivated(ctx context.Context, graphAccountID string) error {
	va, err := s.virtualAccountRepo.GetByGraphAccountID(ctx, graphAccountID)
	if err != nil {
		return fmt.Errorf("get virtual account: %w", err)
	}
	if va == nil {
		return fmt.Errorf("virtual account not found: %s", graphAccountID)
	}

	acct, err := s.graphClient.FetchBankAccount(ctx, graphAccountID)
	if err != nil {
		return fmt.Errorf("fetch graph bank account: %w", err)
	}

	va.AccountNumber = acct.AccountNumber
	va.RoutingNumber = acct.RoutingNumber
	va.BankCode = acct.BankCode
	va.BankName = acct.BankName
	if acct.AccountName != "" {
		va.BeneficiaryName = acct.AccountName
	}
	va.Status = mapGraphAccountStatus(acct.Status)
	va.UpdatedAt = time.Now()

	if err := s.virtualAccountRepo.Update(ctx, va); err != nil {
		return fmt.Errorf("update virtual account: %w", err)
	}
	s.logger.Info("Graph NGN account activated", "graph_account_id", graphAccountID, "status", string(va.Status))
	return nil
}

// GetNGNAccount returns the user's NGN virtual account, if any.
func (s *GraphVirtualAccountService) GetNGNAccount(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error) {
	return s.virtualAccountRepo.GetActiveByUserIDAndCurrency(ctx, userID, "NGN")
}

// ── helpers ───────────────────────────────────────────────────────

func validateProfileForGraph(user *entities.UserProfile, req *ProvisionNGNAccountRequest) error {
	var missing []string
	if user.FirstName == nil || strings.TrimSpace(*user.FirstName) == "" {
		missing = append(missing, "first_name")
	}
	if user.LastName == nil || strings.TrimSpace(*user.LastName) == "" {
		missing = append(missing, "last_name")
	}
	if user.DateOfBirth == nil {
		missing = append(missing, "date_of_birth")
	}
	if user.Phone == nil || strings.TrimSpace(*user.Phone) == "" {
		missing = append(missing, "phone")
	}
	if user.AddressStreet == nil || strings.TrimSpace(*user.AddressStreet) == "" {
		missing = append(missing, "address")
	}
	if strings.TrimSpace(req.BVN) == "" {
		missing = append(missing, "bvn")
	}
	if strings.TrimSpace(req.IDType) == "" || strings.TrimSpace(req.IDNumber) == "" {
		missing = append(missing, "id")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields for NGN account: %s", strings.Join(missing, ", "))
	}
	return nil
}

func mapGraphAccountStatus(status string) entities.VirtualAccountStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "activated":
		return entities.VirtualAccountStatusActive
	case "closed", "deactivated", "deleted":
		return entities.VirtualAccountStatusClosed
	case "failed", "rejected":
		return entities.VirtualAccountStatusFailed
	default:
		return entities.VirtualAccountStatusPending
	}
}

func generateGraphIdempotencyKey(txRef, graphAccountID string) string {
	input := fmt.Sprintf("graph-ngn-deposit:%s:%s", strings.ToLower(txRef), strings.ToLower(graphAccountID))
	hash := sha256.Sum256([]byte(input))
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%x", hash[:]))).String()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func last4(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

func normalizePhone(phone string) string {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return phone
	}
	if strings.HasPrefix(phone, "+") {
		return phone
	}
	if strings.HasPrefix(phone, "0") {
		return "+234" + strings.TrimPrefix(phone, "0")
	}
	return "+" + phone
}

func normalizeIDType(idType string) string {
	switch strings.ToLower(strings.TrimSpace(idType)) {
	case "nin", "national_id", "national id":
		return "nin"
	case "voters_card", "voter_card", "voters":
		return "voters_card"
	case "drivers_license", "drivers_licence", "driver_license":
		return "drivers_license"
	case "passport", "international_passport":
		return "passport"
	default:
		return strings.ToLower(strings.TrimSpace(idType))
	}
}

func graphDocType(idType string) string {
	switch normalizeIDType(idType) {
	case "passport":
		return "passport"
	case "voters_card":
		return "voters_card"
	case "drivers_license":
		return "drivers_licence"
	default:
		return "national_id"
	}
}

func countryAlpha2(def string, override *string) string {
	if override != nil {
		v := strings.ToUpper(strings.TrimSpace(*override))
		if len(v) == 2 {
			return v
		}
	}
	return def
}
