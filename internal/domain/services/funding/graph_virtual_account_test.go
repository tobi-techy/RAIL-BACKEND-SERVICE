package funding

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/graph"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ── fakes ─────────────────────────────────────────────────────────

type fakeGraphClient struct {
	conversion    *graph.Conversion
	conversionErr error
	convCalls     int
}

func (f *fakeGraphClient) CreatePerson(ctx context.Context, req *graph.CreatePersonRequest) (*graph.Person, error) {
	return &graph.Person{ID: "person_1"}, nil
}
func (f *fakeGraphClient) FetchPerson(ctx context.Context, personID string) (*graph.Person, error) {
	return &graph.Person{ID: personID}, nil
}
func (f *fakeGraphClient) UpgradePersonKYC(ctx context.Context, personID string, req *graph.UpgradePersonKYCRequest) (*graph.Person, error) {
	return &graph.Person{ID: personID}, nil
}
func (f *fakeGraphClient) CreateDocument(ctx context.Context, req *graph.CreateDocumentRequest) (*graph.EntityDocument, error) {
	return &graph.EntityDocument{ID: "doc_1"}, nil
}
func (f *fakeGraphClient) CreateBankAccount(ctx context.Context, req *graph.CreateBankAccountRequest) (*graph.BankAccount, error) {
	return &graph.BankAccount{ID: "acct_1", AccountNumber: "0123456789", BankName: "Test Bank", Status: "pending"}, nil
}
func (f *fakeGraphClient) FetchBankAccount(ctx context.Context, accountID string) (*graph.BankAccount, error) {
	return &graph.BankAccount{ID: accountID, AccountNumber: "0123456789", BankName: "Test Bank", Status: "active"}, nil
}
func (f *fakeGraphClient) ListBankAccounts(ctx context.Context, personID string) ([]graph.BankAccount, error) {
	return nil, nil
}
func (f *fakeGraphClient) FetchRate(ctx context.Context, source, target string) (*graph.Rate, error) {
	return &graph.Rate{Source: source, Target: target, Rate: 0.00062}, nil
}
func (f *fakeGraphClient) CreateConversion(ctx context.Context, req *graph.CreateConversionRequest) (*graph.Conversion, error) {
	f.convCalls++
	if f.conversionErr != nil {
		return nil, f.conversionErr
	}
	return f.conversion, nil
}

type fakeVARepo struct {
	byGraphID map[string]*entities.VirtualAccount
	created   []*entities.VirtualAccount
}

func newFakeVARepo() *fakeVARepo {
	return &fakeVARepo{byGraphID: map[string]*entities.VirtualAccount{}}
}

func (r *fakeVARepo) Create(ctx context.Context, a *entities.VirtualAccount) error {
	r.created = append(r.created, a)
	if a.GraphAccountID != nil {
		r.byGraphID[*a.GraphAccountID] = a
	}
	return nil
}
func (r *fakeVARepo) Update(ctx context.Context, a *entities.VirtualAccount) error { return nil }
func (r *fakeVARepo) UpdateWithVersion(ctx context.Context, a *entities.VirtualAccount, old time.Time) error {
	return nil
}
func (r *fakeVARepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error) {
	return nil, nil
}
func (r *fakeVARepo) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error) {
	return nil, nil
}
func (r *fakeVARepo) GetByAlpacaAccountID(ctx context.Context, id string) (*entities.VirtualAccount, error) {
	return nil, nil
}
func (r *fakeVARepo) GetActiveByUserIDAndCurrency(ctx context.Context, userID uuid.UUID, currency string) (*entities.VirtualAccount, error) {
	return nil, nil
}
func (r *fakeVARepo) GetProvisionedByUserIDAndCurrency(ctx context.Context, userID uuid.UUID, currency string) (*entities.VirtualAccount, error) {
	for _, a := range r.created {
		if a.UserID == userID && a.Currency == currency && (a.Status == entities.VirtualAccountStatusActive || a.Status == entities.VirtualAccountStatusPending) {
			return a, nil
		}
	}
	return nil, nil
}
func (r *fakeVARepo) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.VirtualAccountStatus) error {
	return nil
}
func (r *fakeVARepo) ExistsByUserAndAlpacaAccount(ctx context.Context, userID uuid.UUID, id string) (bool, error) {
	return false, nil
}
func (r *fakeVARepo) GetByGraphAccountID(ctx context.Context, graphAccountID string) (*entities.VirtualAccount, error) {
	return r.byGraphID[graphAccountID], nil
}
func (r *fakeVARepo) GetFailedNGNByUserID(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error) {
	return nil, nil
}
func (r *fakeVARepo) DeleteByID(ctx context.Context, id uuid.UUID) error {
	return nil
}

type fakeDepositRepo struct {
	mu       sync.Mutex
	byKey    map[string]*entities.Deposit
	statuses map[uuid.UUID]string
}

func newFakeDepositRepo() *fakeDepositRepo {
	return &fakeDepositRepo{byKey: map[string]*entities.Deposit{}, statuses: map[uuid.UUID]string{}}
}

func (r *fakeDepositRepo) Create(ctx context.Context, d *entities.Deposit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byKey[d.IdempotencyKey]; ok {
		return errors.New("duplicate key value violates unique constraint")
	}
	r.byKey[d.IdempotencyKey] = d
	r.statuses[d.ID] = d.Status
	return nil
}
func (r *fakeDepositRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error) {
	return nil, nil
}
func (r *fakeDepositRepo) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error) {
	return nil, nil
}
func (r *fakeDepositRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[id] = status
	for _, d := range r.byKey {
		if d.ID == id {
			d.Status = status
		}
	}
	return nil
}
func (r *fakeDepositRepo) GetByTxHash(ctx context.Context, txHash string) (*entities.Deposit, error) {
	return nil, nil
}
func (r *fakeDepositRepo) GetByIdempotencyKey(ctx context.Context, key string) (*entities.Deposit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byKey[key], nil
}
func (r *fakeDepositRepo) GetRecentByUserIDAndAmount(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, within time.Duration) (*entities.Deposit, error) {
	return nil, nil
}
func (r *fakeDepositRepo) DeletePendingDeposit(ctx context.Context, id uuid.UUID) error { return nil }
func (r *fakeDepositRepo) UpdateDepositAmount(ctx context.Context, id uuid.UUID, amount decimal.Decimal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.byKey {
		if d.ID == id {
			d.Amount = amount
		}
	}
	return nil
}

type fakeAllocation struct{ calls int }

func (f *fakeAllocation) ProcessIncomingFunds(ctx context.Context, req *entities.IncomingFundsRequest) error {
	f.calls++
	return nil
}

type fakeLedger struct {
	mu       sync.Mutex
	calls    int
	spending decimal.Decimal
	stash    decimal.Decimal
	total    decimal.Decimal
}

func (f *fakeLedger) RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, chain, txHash string) error {
	return nil
}
func (f *fakeLedger) RecordDepositWithAllocation(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, depositType, ref string, spending, stash decimal.Decimal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.total = amount
	f.spending = spending
	f.stash = stash
	return nil
}
func (f *fakeLedger) CompensateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID) error {
	return nil
}
func (f *fakeLedger) GetUserBalance(ctx context.Context, userID uuid.UUID) (*LedgerBalanceView, error) {
	return nil, nil
}

// ── helpers ───────────────────────────────────────────────────────

func newTestService(gc GraphClient, va GraphVirtualAccountRepository, dr DepositRepository, ledger LedgerIntegration, alloc AllocationService) *GraphVirtualAccountService {
	return NewGraphVirtualAccountService(gc, va, dr, nil, alloc, ledger, 0, logger.NewLogger(zap.NewNop()))
}

func seedNGNAccount(r *fakeVARepo) *entities.VirtualAccount {
	id := "acct_1"
	va := &entities.VirtualAccount{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		Provider:       entities.VirtualAccountProviderGraph,
		GraphAccountID: &id,
		Currency:       "NGN",
		Status:         entities.VirtualAccountStatusActive,
	}
	r.byGraphID[id] = va
	return va
}

// ── tests ─────────────────────────────────────────────────────────

func TestProcessNGNDeposit_ConversionAnd7030Split(t *testing.T) {
	va := newFakeVARepo()
	seedNGNAccount(va)
	dr := newFakeDepositRepo()
	ledger := &fakeLedger{}
	alloc := &fakeAllocation{}
	gc := &fakeGraphClient{conversion: &graph.Conversion{TargetAmount: "100", Status: "completed"}}

	svc := newTestService(gc, va, dr, ledger, alloc)

	err := svc.ProcessNGNDeposit(context.Background(), &GraphNGNDepositEvent{
		GraphAccountID: "acct_1",
		TransactionID:  "tx_100",
		AmountNGN:      "160000",
		Direction:      "credit",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ledger.calls != 1 {
		t.Fatalf("expected 1 ledger call, got %d", ledger.calls)
	}
	if !ledger.total.Equal(decimal.NewFromInt(100)) {
		t.Errorf("expected total 100 USDC, got %s", ledger.total)
	}
	// 70/30 split.
	if !ledger.spending.Equal(decimal.NewFromInt(70)) {
		t.Errorf("expected spending 70, got %s", ledger.spending)
	}
	if !ledger.stash.Equal(decimal.NewFromInt(30)) {
		t.Errorf("expected stash 30, got %s", ledger.stash)
	}
	if !ledger.spending.Add(ledger.stash).Equal(ledger.total) {
		t.Errorf("split must sum to total")
	}
	if alloc.calls != 1 {
		t.Errorf("expected allocation side-effects once, got %d", alloc.calls)
	}
}

func TestProcessNGNDeposit_Idempotent(t *testing.T) {
	va := newFakeVARepo()
	seedNGNAccount(va)
	dr := newFakeDepositRepo()
	ledger := &fakeLedger{}
	alloc := &fakeAllocation{}
	gc := &fakeGraphClient{conversion: &graph.Conversion{TargetAmount: "100", Status: "completed"}}
	svc := newTestService(gc, va, dr, ledger, alloc)

	event := &GraphNGNDepositEvent{GraphAccountID: "acct_1", TransactionID: "tx_dup", AmountNGN: "160000", Direction: "credit"}

	if err := svc.ProcessNGNDeposit(context.Background(), event); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if err := svc.ProcessNGNDeposit(context.Background(), event); err != nil {
		t.Fatalf("second call should be a no-op, got: %v", err)
	}
	if ledger.calls != 1 {
		t.Fatalf("ledger must be credited exactly once for duplicate tx, got %d", ledger.calls)
	}
}

func TestProcessNGNDeposit_IgnoresDebit(t *testing.T) {
	va := newFakeVARepo()
	seedNGNAccount(va)
	ledger := &fakeLedger{}
	svc := newTestService(&fakeGraphClient{}, va, newFakeDepositRepo(), ledger, &fakeAllocation{})

	if err := svc.ProcessNGNDeposit(context.Background(), &GraphNGNDepositEvent{
		GraphAccountID: "acct_1", TransactionID: "tx_debit", AmountNGN: "160000", Direction: "debit",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ledger.calls != 0 {
		t.Fatalf("debit must not credit the ledger")
	}
}

func TestProcessNGNDeposit_BelowMinimum(t *testing.T) {
	va := newFakeVARepo()
	seedNGNAccount(va)
	ledger := &fakeLedger{}
	svc := newTestService(&fakeGraphClient{}, va, newFakeDepositRepo(), ledger, &fakeAllocation{})

	err := svc.ProcessNGNDeposit(context.Background(), &GraphNGNDepositEvent{
		GraphAccountID: "acct_1", TransactionID: "tx_small", AmountNGN: "100", Direction: "credit",
	})
	if err == nil {
		t.Fatalf("expected below-minimum error")
	}
	if ledger.calls != 0 {
		t.Fatalf("below-minimum deposit must not credit the ledger")
	}
}
