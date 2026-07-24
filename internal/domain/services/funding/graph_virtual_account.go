package funding

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/graph"
	"github.com/rail-service/rail_service/pkg/analytics"
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
	GetProvisionedByUserIDAndCurrency(ctx context.Context, userID uuid.UUID, currency string) (*entities.VirtualAccount, error)
	GetFailedNGNByUserID(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error)
	DeleteByID(ctx context.Context, id uuid.UUID) error
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
	// Per-user mutex for ensurePerson — prevents two concurrent requests from
	// creating duplicate Graph persons before either writes the VA record.
	personMu sync.Map
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
var ErrNGNProfileIncomplete = fmt.Errorf("graph: profile is missing required fields for NGN account")
var ErrNGNInvalidIdentity = fmt.Errorf("graph: invalid BVN or identity number")

type NGNProfileIncompleteError struct {
	Missing []string
}

func (e *NGNProfileIncompleteError) Error() string {
	return fmt.Sprintf("%v: %s", ErrNGNProfileIncomplete, strings.Join(e.Missing, ", "))
}

func (e *NGNProfileIncompleteError) Unwrap() error {
	return ErrNGNProfileIncomplete
}

// ProvisionNGNAccountRequest carries the transient PII needed to create a Graph
// person and NGN account. BVN and ID number are never persisted.
type ProvisionNGNAccountRequest struct {
	UserID        uuid.UUID
	BVN           string // transient — never stored, only verified flag + last4 persisted
	IDType        string // nin, voters_card, drivers_license, passport
	IDNumber      string // transient
	IDDocumentURL string // URL of the uploaded ID document (e.g. R2)
	// Didit verification images — presigned URLs from a completed Didit session.
	// These are passed directly to Graph to satisfy its document requirement.
	DiditFrontImage string
	DiditBackImage  string
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
	if err := validateIdentityForGraph(req); err != nil {
		return nil, err
	}

	// Idempotent: return an existing active or in-flight NGN account if present.
	if existing, fetchErr := s.virtualAccountRepo.GetProvisionedByUserIDAndCurrency(ctx, req.UserID, "NGN"); existing != nil {
		return existing, nil
	} else if fetchErr != nil {
		s.logger.Warn("Error checking existing NGN account", "user_id", req.UserID, "error", fetchErr)
	}

	// Per-user lock prevents two concurrent requests from both creating a Graph
	// person or bank account before either writes the virtual account record
	// (orphaned persons/bank accounts).
	userKey := req.UserID.String()
	val, _ := s.personMu.LoadOrStore(userKey, &sync.Mutex{})
	mu := val.(*sync.Mutex)
	mu.Lock()
	defer func() {
		mu.Unlock()
		s.personMu.Delete(userKey)
	}()

	// Re-check after acquiring lock — another request may have completed provisioning.
	if existing, _ := s.virtualAccountRepo.GetProvisionedByUserIDAndCurrency(ctx, req.UserID, "NGN"); existing != nil {
		return existing, nil
	}

	// Check for failed accounts that the active/pending query missed.
	// A failed VA WITH GraphPersonID: reuse the person and retry bank account creation,
	// updating the existing record to satisfy the unique constraint on (user_id, currency).
	// A failed VA without GraphPersonID: the stale record blocks the unique constraint;
	// the main flow handles cleanup via DeleteByID after a unique-violation on Create.
	if failed, _ := s.virtualAccountRepo.GetFailedNGNByUserID(ctx, req.UserID); failed != nil {
		if failed.GraphPersonID != nil && *failed.GraphPersonID != "" {
			s.logger.Info("Retrying NGN provisioning for previously failed account",
				"user_id", req.UserID, "virtual_account_id", failed.ID, "graph_person_id", *failed.GraphPersonID)
			personID := *failed.GraphPersonID
			retryLabel := "Rail NGN"
			if user.FirstName != nil && *user.FirstName != "" {
				retryLabel = *user.FirstName + " NGN"
			}
			bankAcct, retryErr := s.graphClient.CreateBankAccount(ctx, &graph.CreateBankAccountRequest{
				PersonID:         personID,
				Label:            retryLabel,
				Currency:         "NGN",
				AutosweepEnabled: true,
			})
			if retryErr != nil {
				return nil, fmt.Errorf("retry create graph bank account: %w", retryErr)
			}
			graphAccountID := bankAcct.ID
			failed.GraphAccountID = &graphAccountID
			failed.AccountNumber = bankAcct.AccountNumber
			failed.RoutingNumber = bankAcct.RoutingNumber
			failed.BankCode = bankAcct.BankCode
			failed.BankName = bankAcct.BankName
			if bankAcct.AccountName != "" {
				failed.BeneficiaryName = bankAcct.AccountName
			}
			failed.Status = mapGraphAccountStatus(bankAcct.Status)
			oldUpdatedAt := failed.UpdatedAt
			failed.UpdatedAt = time.Now()
			if err := s.virtualAccountRepo.UpdateWithVersion(ctx, failed, oldUpdatedAt); err != nil {
				return nil, fmt.Errorf("update retried virtual account: %w", err)
			}
			s.logger.Info("NGN virtual account retried successfully",
				"user_id", req.UserID,
				"virtual_account_id", failed.ID,
				"graph_account_id", graphAccountID,
				"status", string(failed.Status))
			return failed, nil
		}
		// Failed account has no person — the stale record is harmless and provides
		// audit trail. The main flow will create a new person + bank account, and
		// the unique constraint on (user_id, currency) will cause a conflict. We
		// handle that in the Create call below by deleting the stale failed record
		// and retrying.
		s.logger.Info("Found stale failed NGN account (no graph person), will clean up",
			"user_id", req.UserID, "virtual_account_id", failed.ID)
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
		if isUniqueViolation(err) {
			// A stale failed record (no GraphPersonID) from a prior attempt is blocking
			// the unique constraint. Delete it and retry the insert.
			if failed, fetchErr := s.virtualAccountRepo.GetFailedNGNByUserID(ctx, req.UserID); fetchErr == nil && failed != nil {
				s.logger.Info("Removing stale failed NGN record to unblock retry",
					"user_id", req.UserID, "virtual_account_id", failed.ID)
				if delErr := s.virtualAccountRepo.DeleteByID(ctx, failed.ID); delErr != nil {
					return nil, fmt.Errorf("delete stale failed NGN record: %w", delErr)
				}
				// Retry the insert — the stale record is gone.
				if retryErr := s.virtualAccountRepo.Create(ctx, va); retryErr != nil {
					return nil, fmt.Errorf("retry store graph virtual account: %w", retryErr)
				}
			} else {
				// Active or pending record exists — return it (idempotent).
				if existing, fetchErr := s.virtualAccountRepo.GetProvisionedByUserIDAndCurrency(ctx, req.UserID, "NGN"); fetchErr == nil && existing != nil {
					return existing, nil
				}
				return nil, fmt.Errorf("store graph virtual account: %w", err)
			}
		} else {
			// Log orphaned Graph resources for cleanup — person and bank account exist
			// in Graph but won't be tracked. On retry, ensurePerson will reuse the
			// person (GraphPersonID was persisted) but a new bank account will be created.
			s.logger.Error("Orphaned Graph resources after DB write failure",
				"user_id", req.UserID,
				"graph_person_id", personID,
				"graph_account_id", graphAccountID,
				"error", err)
			return nil, fmt.Errorf("store graph virtual account: %w", err)
		}
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

// isAllowedDocumentURL validates that a document URL uses HTTPS with a valid
// host, resolves the hostname, and rejects loopback, private, link-local,
// unspecified, and metadata/internal addresses for every resolved IP including
// the full 172.16.0.0/12 range and IPv6 equivalents. Returns true for empty
// URLs (no document). Denies malformed or unresolvable URLs.
func isAllowedDocumentURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	// Block obvious internal hostnames before DNS resolution.
	lower := strings.ToLower(host)
	if lower == "localhost" || lower == "metadata.google" || lower == "169.254.169.254" {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if isReservedOrInternal(ip) {
			return false
		}
	}
	return true
}

// isReservedOrInternal reports whether an IP falls into loopback, link-local,
// private (RFC 1918 / RFC 4193), unspecified, or cloud-metadata ranges.
func isReservedOrInternal(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if private := ip.IsPrivate(); private {
		return true
	}
	// ip.IsPrivate() covers 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16,
	// and fc00::/7. Also catch 169.254.0.0/16 (cloud metadata / link-local).
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
	}
	return false
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
		NameOther:    strings.TrimSpace(deref(user.MiddleName)),
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
	// Graph NGN accounts require at least one document. Prefer Didit front image
	// (presigned, short-lived), then fall back to explicit IDDocumentURL.
	docType := graphDocType(req.IDType)
	if req.DiditFrontImage != "" {
		if !isAllowedDocumentURL(req.DiditFrontImage) {
			return "", fmt.Errorf("invalid Didit front image URL: must be a valid https URL")
		}
		doc := graph.Document{Type: docType, URL: req.DiditFrontImage}
		personReq.Documents = []graph.Document{doc}
		if req.DiditBackImage != "" {
			if !isAllowedDocumentURL(req.DiditBackImage) {
				return "", fmt.Errorf("invalid Didit back image URL: must be a valid https URL")
			}
			personReq.Documents = append(personReq.Documents, graph.Document{
				Type: docType, URL: req.DiditBackImage,
			})
		}
	} else if req.IDDocumentURL != "" {
		if !isAllowedDocumentURL(req.IDDocumentURL) {
			return "", fmt.Errorf("invalid document URL: must be a valid https URL")
		}
		personReq.Documents = []graph.Document{{Type: docType, URL: req.IDDocumentURL}}
	}

	person, err := s.graphClient.CreatePerson(ctx, personReq)
	if err != nil {
		return "", fmt.Errorf("create graph person: %w", err)
	}

	if err := s.userProvider.UpdateGraphPersonID(ctx, user.ID, person.ID); err != nil {
		return "", fmt.Errorf("persist graph person id: %w", err)
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

	analytics.TrackEvent(ctx, userID.String(), analytics.EventDepositCompleted, map[string]any{
		"amount":        usdcAmount.InexactFloat64(),
		"currency":      "USDC",
		"provider":      "graph",
		"method":        "ngn_fiat",
		"ngn_amount":    ngnAmount.InexactFloat64(),
		"deposit_id":    depositID.String(),
	})

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

// HandleAccountActivatedWithData updates the virtual account with bank details
// from the webhook payload directly — no extra API call needed.
// Uses optimistic concurrency via updated_at to prevent lost updates from
// concurrent webhook deliveries or parallel processing.
func (s *GraphVirtualAccountService) HandleAccountActivatedWithData(ctx context.Context, graphAccountID string, acct *graph.BankAccount) error {
	va, err := s.virtualAccountRepo.GetByGraphAccountID(ctx, graphAccountID)
	if err != nil {
		return fmt.Errorf("get virtual account: %w", err)
	}
	if va == nil {
		return fmt.Errorf("virtual account not found: %s", graphAccountID)
	}

	oldUpdatedAt := va.UpdatedAt

	va.AccountNumber = acct.AccountNumber
	va.RoutingNumber = acct.RoutingNumber
	va.BankCode = acct.BankCode
	va.BankName = acct.BankName
	if acct.AccountName != "" {
		va.BeneficiaryName = acct.AccountName
	}
	va.Status = mapGraphAccountStatus(acct.Status)
	va.UpdatedAt = time.Now()

	if err := s.virtualAccountRepo.UpdateWithVersion(ctx, va, oldUpdatedAt); err != nil {
		return fmt.Errorf("update virtual account: %w", err)
	}
	s.logger.Info("Graph NGN account activated", "graph_account_id", graphAccountID, "status", string(va.Status))
	return nil
}

// GetNGNAccount returns the user's NGN virtual account, if any.
// Returns active, pending, or failed accounts so the frontend can show retry UI.
// If no DB record exists but a Graph bank account does, reconciles automatically.
func (s *GraphVirtualAccountService) GetNGNAccount(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error) {
	va, err := s.virtualAccountRepo.GetProvisionedByUserIDAndCurrency(ctx, userID, "NGN")
	if err != nil {
		return nil, err
	}
	if va != nil {
		return va, nil
	}

	// No DB record — try to recover from Graph.
	// This handles cases where the DB write failed after Graph created the account,
	// or the record was lost. List the user's Graph bank accounts and reconcile.
	user, err := s.userProvider.GetByID(ctx, userID)
	if err != nil || user.GraphPersonID == nil || *user.GraphPersonID == "" {
		// No Graph person — check for a failed one so the frontend can show retry UI.
		return s.virtualAccountRepo.GetFailedNGNByUserID(ctx, userID)
	}

	accounts, err := s.graphClient.ListBankAccounts(ctx, *user.GraphPersonID)
	if err != nil || len(accounts) == 0 {
		return s.virtualAccountRepo.GetFailedNGNByUserID(ctx, userID)
	}

	// Find the user's personal NGN bank account (holder_type == "person")
	for _, acct := range accounts {
		if acct.Currency != "NGN" || acct.HolderType != "person" {
			continue
		}
		vaStatus := mapGraphAccountStatus(acct.Status)
		now := time.Now()
		va = &entities.VirtualAccount{
			ID:              uuid.New(),
			UserID:          userID,
			Provider:        entities.VirtualAccountProviderGraph,
			GraphPersonID:   user.GraphPersonID,
			GraphAccountID:  &acct.ID,
			AccountNumber:   acct.AccountNumber,
			RoutingNumber:   acct.RoutingNumber,
			BankCode:        acct.BankCode,
			BankName:        acct.BankName,
			BeneficiaryName: acct.AccountName,
			Status:          vaStatus,
			Currency:        "NGN",
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if va.BeneficiaryName == "" && user.FirstName != nil && user.LastName != nil {
			va.BeneficiaryName = *user.FirstName + " " + *user.LastName
		}
		if err := s.virtualAccountRepo.Create(ctx, va); err != nil {
			if isUniqueViolation(err) {
				return s.virtualAccountRepo.GetProvisionedByUserIDAndCurrency(ctx, userID, "NGN")
			}
			s.logger.Warn("failed to store recovered NGN account from Graph",
				"user_id", userID, "graph_account_id", acct.ID, "error", err)
			// Return error instead of phantom VA — caller needs a real DB record
			return nil, fmt.Errorf("store recovered NGN account: %w", err)
		}
		return va, nil
	}

	// No NGN account in Graph either — check for a failed one in DB
	return s.virtualAccountRepo.GetFailedNGNByUserID(ctx, userID)
}

// HandleAccountIssuanceFailed marks an NGN virtual account as failed when Graph
// fires the account.issuance.failed webhook. This prevents the user from being
// stuck in an infinite "pending" state.
func (s *GraphVirtualAccountService) HandleAccountIssuanceFailed(ctx context.Context, graphAccountID string) error {
	va, err := s.virtualAccountRepo.GetByGraphAccountID(ctx, graphAccountID)
	if err != nil {
		return fmt.Errorf("get virtual account: %w", err)
	}
	if va == nil {
		s.logger.Warn("Issuance failed for unknown graph account", "graph_account_id", graphAccountID)
		return nil
	}

	oldUpdatedAt := va.UpdatedAt
	va.Status = entities.VirtualAccountStatusFailed
	va.UpdatedAt = time.Now()

	if err := s.virtualAccountRepo.UpdateWithVersion(ctx, va, oldUpdatedAt); err != nil {
		return fmt.Errorf("update virtual account to failed: %w", err)
	}
	s.logger.Warn("Graph NGN account issuance failed",
		"graph_account_id", graphAccountID,
		"user_id", va.UserID)
	return nil
}

// HandleAccountClosed marks an NGN virtual account as closed when Graph fires
// the account.closed webhook. This prevents deposits from being routed to a
// closed account.
func (s *GraphVirtualAccountService) HandleAccountClosed(ctx context.Context, graphAccountID string) error {
	va, err := s.virtualAccountRepo.GetByGraphAccountID(ctx, graphAccountID)
	if err != nil {
		return fmt.Errorf("get virtual account: %w", err)
	}
	if va == nil {
		s.logger.Warn("Account closed for unknown graph account", "graph_account_id", graphAccountID)
		return nil
	}
	if va.Status == entities.VirtualAccountStatusClosed {
		return nil // already closed
	}

	oldUpdatedAt := va.UpdatedAt
	va.Status = entities.VirtualAccountStatusClosed
	va.UpdatedAt = time.Now()

	if err := s.virtualAccountRepo.UpdateWithVersion(ctx, va, oldUpdatedAt); err != nil {
		return fmt.Errorf("update virtual account to closed: %w", err)
	}
	s.logger.Warn("Graph NGN account closed",
		"graph_account_id", graphAccountID,
		"user_id", va.UserID)
	return nil
}

// HandleConversionFailed marks a pending NGN deposit as failed when Graph fires
// a conversion.failed webhook. This prevents the deposit from being stuck in
// pending state indefinitely.
func (s *GraphVirtualAccountService) HandleConversionFailed(ctx context.Context, graphAccountID string, conversionID string) error {
	// Look up the virtual account to get the user
	va, err := s.virtualAccountRepo.GetByGraphAccountID(ctx, graphAccountID)
	if err != nil || va == nil {
		s.logger.Warn("conversion.failed for unknown graph account",
			"graph_account_id", graphAccountID, "conversion_id", conversionID)
		return nil
	}

	// The idempotency key format is "graph-{graphAccountID}-{txRef}".
	// We don't have the txRef here, so search for any pending deposit for this user.
	// Look up all deposits for this user and find pending NGN ones
	deposits, err := s.depositRepo.GetByUserID(ctx, va.UserID, 100, 0)
	if err != nil {
		return fmt.Errorf("get deposits: %w", err)
	}

	updated := 0
	for _, dep := range deposits {
		if dep.Status == "pending" && dep.SourceCurrency != nil && *dep.SourceCurrency == "NGN" {
			if err := s.depositRepo.UpdateStatus(ctx, dep.ID, "failed", nil); err != nil {
				s.logger.Warn("failed to mark deposit as failed",
					"deposit_id", dep.ID, "error", err)
				continue
			}
			updated++
			s.logger.Warn("NGN deposit marked failed due to conversion failure",
				"deposit_id", dep.ID, "user_id", va.UserID,
				"conversion_id", conversionID)
		}
	}

	if updated == 0 {
		s.logger.Warn("conversion.failed but no pending NGN deposits found",
			"graph_account_id", graphAccountID, "conversion_id", conversionID)
	}
	return nil
}

// HandleAddressMigrated updates the stored bank address when Graph fires an
// address.migrated webhook. This ensures the user always sees the current
// deposit address.
func (s *GraphVirtualAccountService) HandleAddressMigrated(ctx context.Context, graphAccountID string, newAddress string) error {
	va, err := s.virtualAccountRepo.GetByGraphAccountID(ctx, graphAccountID)
	if err != nil || va == nil {
		s.logger.Warn("address.migrated for unknown graph account",
			"graph_account_id", graphAccountID)
		return nil
	}

	oldUpdatedAt := va.UpdatedAt
	va.BankAddress = newAddress
	va.UpdatedAt = time.Now()

	if err := s.virtualAccountRepo.UpdateWithVersion(ctx, va, oldUpdatedAt); err != nil {
		return fmt.Errorf("update virtual account address: %w", err)
	}
	s.logger.Info("Graph NGN account address migrated",
		"graph_account_id", graphAccountID, "new_address", newAddress)
	return nil
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
		return &NGNProfileIncompleteError{Missing: missing}
	}
	return nil
}

func validateIdentityForGraph(req *ProvisionNGNAccountRequest) error {
	bvn := strings.TrimSpace(req.BVN)
	if len(bvn) != 11 || !allDigits(bvn) {
		return fmt.Errorf("%w: BVN must be 11 digits", ErrNGNInvalidIdentity)
	}

	idType := normalizeIDType(req.IDType)
	idNumber := strings.TrimSpace(req.IDNumber)
	switch idType {
	case "nin":
		if len(idNumber) != 11 || !allDigits(idNumber) {
			return fmt.Errorf("%w: NIN must be 11 digits", ErrNGNInvalidIdentity)
		}
	case "voters_card", "drivers_license", "passport":
		if len(idNumber) < 6 {
			return fmt.Errorf("%w: ID number is too short", ErrNGNInvalidIdentity)
		}
	default:
		return fmt.Errorf("%w: unsupported id_type %q", ErrNGNInvalidIdentity, req.IDType)
	}
	return nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
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
	// Strip any existing + prefix for clean processing.
	stripped := strings.TrimPrefix(phone, "+")
	// Handle numbers that already include the 234 country code.
	if strings.HasPrefix(stripped, "234") && len(stripped) > 3 {
		// Strip leading 0 after country code (e.g. +23408012345678 → +2348012345678).
		rest := stripped[3:]
		rest = strings.TrimLeft(rest, "0")
		if rest == "" {
			return "+234"
		}
		return "+234" + rest
	}
	if strings.HasPrefix(stripped, "0") {
		return "+234" + strings.TrimPrefix(stripped, "0")
	}
	// Bare local number without leading 0 (e.g. 8012345678).
	if len(stripped) >= 10 && len(stripped) <= 11 {
		return "+234" + stripped
	}
	return "+" + stripped
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

// AutoProvisionNGN checks Graph person verification state and, if everything
// is in order, creates the NGN bank account without requiring any user input.
// Used for retry when a prior ProvisionNGNAccount call created the Graph person
// but failed on CreateBankAccount.
func (s *GraphVirtualAccountService) AutoProvisionNGN(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error) {
	user, err := s.userProvider.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// Gate: user must have a Graph person
	if user.GraphPersonID == nil || *user.GraphPersonID == "" {
		return nil, fmt.Errorf("no Graph person — complete identity verification first")
	}
	personID := *user.GraphPersonID

	// Idempotent: return existing active/pending NGN account
	if existing, _ := s.virtualAccountRepo.GetProvisionedByUserIDAndCurrency(ctx, userID, "NGN"); existing != nil {
		return existing, nil
	}

	// Verify the person is active and verified in Graph
	person, err := s.graphClient.FetchPerson(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("fetch graph person: %w", err)
	}
	if person.Status != "active" || person.KYCStatus != "verified" {
		return nil, fmt.Errorf("graph person not ready: status=%s kyc_status=%s", person.Status, person.KYCStatus)
	}

	// Check if a bank account already exists in Graph but not in our DB (orphaned)
	accounts, listErr := s.graphClient.ListBankAccounts(ctx, personID)
	if listErr != nil {
		s.logger.Warn("failed to list Graph bank accounts during auto-provision",
			"user_id", userID, "person_id", personID, "error", listErr)
		// Fall through to create a new account — Graph may have one, but we can't check
	}
	for _, acct := range accounts {
		if acct.Currency == "NGN" {
			vaStatus := mapGraphAccountStatus(acct.Status)
			if vaStatus == entities.VirtualAccountStatusActive || vaStatus == entities.VirtualAccountStatusPending {
				now := time.Now()
				va := &entities.VirtualAccount{
					ID:              uuid.New(),
					UserID:          userID,
					Provider:        entities.VirtualAccountProviderGraph,
					GraphPersonID:   &personID,
					GraphAccountID:  &acct.ID,
					AccountNumber:   acct.AccountNumber,
					RoutingNumber:   acct.RoutingNumber,
					BankCode:        acct.BankCode,
					BankName:        acct.BankName,
					BeneficiaryName: acct.AccountName,
					Status:          vaStatus,
					Currency:        "NGN",
					CreatedAt:       now,
					UpdatedAt:       now,
				}
				if va.BeneficiaryName == "" && user.FirstName != nil && user.LastName != nil {
					va.BeneficiaryName = *user.FirstName + " " + *user.LastName
				}
				if err := s.virtualAccountRepo.Create(ctx, va); err != nil {
					if isUniqueViolation(err) {
						return s.virtualAccountRepo.GetProvisionedByUserIDAndCurrency(ctx, userID, "NGN")
					}
					return nil, fmt.Errorf("store recovered virtual account: %w", err)
				}
				return va, nil
			}
		}
	}

	// Create the NGN bank account
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
		UserID:          userID,
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
		if isUniqueViolation(err) {
			return s.virtualAccountRepo.GetProvisionedByUserIDAndCurrency(ctx, userID, "NGN")
		}
		return nil, fmt.Errorf("store virtual account: %w", err)
	}

	// Tier promotion if needed (idempotent)
	if user.KYCTier < entities.KYCTierLevelBasic {
		_ = s.userProvider.UpdateKYCTier(ctx, userID, entities.KYCTierLevelBasic)
	}

	return va, nil
}
