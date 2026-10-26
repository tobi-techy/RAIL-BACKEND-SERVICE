package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mr-tron/base58"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
)

// --- fakes ---

type fakePayoutStore struct {
	mu             sync.Mutex
	byID           map[uuid.UUID]*entities.OpsCryptoPayout
	bySource       map[string]uuid.UUID
	suppressed     map[uuid.UUID]bool
	forceCreateErr error
}

func newFakePayoutStore() *fakePayoutStore {
	return &fakePayoutStore{
		byID:       map[uuid.UUID]*entities.OpsCryptoPayout{},
		bySource:   map[string]uuid.UUID{},
		suppressed: map[uuid.UUID]bool{},
	}
}

func clonePayout(p *entities.OpsCryptoPayout) *entities.OpsCryptoPayout {
	cp := *p
	if p.SuppressionDepositID != nil {
		id := *p.SuppressionDepositID
		cp.SuppressionDepositID = &id
	}
	if p.FundingAmount != nil {
		d := *p.FundingAmount
		cp.FundingAmount = &d
	}
	if p.FeeAmount != nil {
		d := *p.FeeAmount
		cp.FeeAmount = &d
	}
	if p.IntentAddress != nil {
		s := *p.IntentAddress
		cp.IntentAddress = &s
	}
	if p.CircleTransferID != nil {
		s := *p.CircleTransferID
		cp.CircleTransferID = &s
	}
	if p.ChainRailsIntentID != nil {
		i := *p.ChainRailsIntentID
		cp.ChainRailsIntentID = &i
	}
	return &cp
}

func (f *fakePayoutStore) CreatePayoutWithSuppression(_ context.Context, p *entities.OpsCryptoPayout) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.forceCreateErr != nil {
		return false, f.forceCreateErr
	}
	if _, ok := f.bySource[p.SourceCircleTxID]; ok {
		return false, nil
	}
	cp := clonePayout(p)
	dep := uuid.New()
	cp.SuppressionDepositID = &dep
	f.suppressed[dep] = true
	f.byID[cp.ID] = cp
	f.bySource[cp.SourceCircleTxID] = cp.ID
	return true, nil
}

func (f *fakePayoutStore) GetPayoutBySourceTxID(_ context.Context, sourceTxID string) (*entities.OpsCryptoPayout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.bySource[sourceTxID]
	if !ok {
		return nil, nil
	}
	return clonePayout(f.byID[id]), nil
}

func (f *fakePayoutStore) GetPayoutByID(_ context.Context, id uuid.UUID) (*entities.OpsCryptoPayout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return clonePayout(p), nil
}

func (f *fakePayoutStore) ClaimForIntent(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return false, fmt.Errorf("payout not found")
	}
	if p.Status != entities.OpsPayoutStatusCreated &&
		!(p.Status == entities.OpsPayoutStatusIntentCreating && p.UpdatedAt.Before(time.Now().Add(-5*time.Minute))) {
		return false, nil
	}
	p.Status = entities.OpsPayoutStatusIntentCreating
	return true, nil
}

func (f *fakePayoutStore) ResetFailedForRetry(_ context.Context, id uuid.UUID, amount decimal.Decimal, destination, reason, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byID[id]
	if !ok {
		return fmt.Errorf("payout not found")
	}
	if p.Status != entities.OpsPayoutStatusFailed {
		return fmt.Errorf("payout is not failed")
	}
	p.Status = entities.OpsPayoutStatusCreated
	p.Amount = amount
	p.DestinationAddress = destination
	p.Reason = reason
	p.Actor = actor
	p.FundingAmount = nil
	p.FeeAmount = nil
	p.IntentAddress = nil
	p.ChainRailsIntentID = nil
	p.CircleTransferID = nil
	p.ErrorMessage = nil
	p.Attempts++
	if p.SuppressionDepositID == nil {
		dep := uuid.New()
		p.SuppressionDepositID = &dep
	}
	f.suppressed[*p.SuppressionDepositID] = true
	return nil
}

func (f *fakePayoutStore) MarkIntentCreated(_ context.Context, id uuid.UUID, intentID int, intentAddress string, fundingAmount, feeAmount decimal.Decimal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.byID[id]
	p.Status = entities.OpsPayoutStatusIntentCreated
	p.ChainRailsIntentID = &intentID
	p.IntentAddress = &intentAddress
	fund := fundingAmount
	fee := feeAmount
	p.FundingAmount = &fund
	p.FeeAmount = &fee
	// Mirrors the SQL: the row records what the recipient actually receives,
	// not the pre-quote anchor ("all" mode anchors to the source amount).
	p.Amount = fundingAmount.Sub(feeAmount).Truncate(6)
	return nil
}

func (f *fakePayoutStore) MarkFunding(_ context.Context, id uuid.UUID, transferID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.byID[id]
	if p.Status == entities.OpsPayoutStatusIntentCreated ||
		p.Status == entities.OpsPayoutStatusFunding ||
		p.Status == entities.OpsPayoutStatusFundingUncertain {
		p.Status = entities.OpsPayoutStatusFunding
	}
	if transferID != "" {
		p.CircleTransferID = &transferID
	}
	return nil
}

func (f *fakePayoutStore) MarkFunded(_ context.Context, id uuid.UUID, transferID, txHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.byID[id]
	p.Status = entities.OpsPayoutStatusFunded
	if transferID != "" {
		p.CircleTransferID = &transferID
	}
	if txHash != "" {
		p.CircleTxHash = &txHash
	}
	return nil
}

func (f *fakePayoutStore) MarkFundingUncertain(_ context.Context, id uuid.UUID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.byID[id]
	p.Status = entities.OpsPayoutStatusFundingUncertain
	p.ErrorMessage = &message
	return nil
}

func (f *fakePayoutStore) MarkCompleted(_ context.Context, id uuid.UUID, txHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.byID[id]
	p.Status = entities.OpsPayoutStatusCompleted
	if txHash != "" {
		p.CircleTxHash = &txHash
	}
	return nil
}

func (f *fakePayoutStore) FailPayout(_ context.Context, id uuid.UUID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.byID[id]
	p.Status = entities.OpsPayoutStatusFailed
	p.ErrorMessage = &message
	if p.SuppressionDepositID != nil {
		delete(f.suppressed, *p.SuppressionDepositID)
		p.SuppressionDepositID = nil
	}
	return nil
}

func (f *fakePayoutStore) suppressionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.suppressed)
}

func (f *fakePayoutStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byID)
}

type bridgeTransferCall struct {
	walletID, tokenID, destination, amount, idemKey string
}

type fakeBridgeCircle struct {
	mu          sync.Mutex
	sourceTx    *circlepkg.Transaction
	transfer    *circlepkg.Transaction
	transferErr error
	balances    []circlepkg.TokenBalance
	calls       []bridgeTransferCall
}

func (f *fakeBridgeCircle) TransferUSDCWithIdempotency(_ context.Context, walletID, tokenID, destination, amount, idemKey string) (*circlepkg.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, bridgeTransferCall{walletID, tokenID, destination, amount, idemKey})
	if f.transferErr != nil {
		return nil, f.transferErr
	}
	cp := *f.transfer
	return &cp, nil
}

func (f *fakeBridgeCircle) GetTransaction(_ context.Context, txID string) (*circlepkg.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sourceTx != nil && txID == f.sourceTx.ID {
		cp := *f.sourceTx
		return &cp, nil
	}
	if f.transfer == nil {
		return nil, fmt.Errorf("no transfer %s", txID)
	}
	cp := *f.transfer
	cp.ID = txID
	return &cp, nil
}

func (f *fakeBridgeCircle) GetUSDCTokenIDOnchain(context.Context, string) (string, error) {
	return "token-usdc", nil
}

func (f *fakeBridgeCircle) GetTokenSymbol(context.Context, string, string) (string, error) {
	return "USDC", nil
}

func (f *fakeBridgeCircle) GetTokenBalanceOnchain(context.Context, string) ([]circlepkg.TokenBalance, error) {
	return f.balances, nil
}

func (f *fakeBridgeCircle) transferCalls() []bridgeTransferCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]bridgeTransferCall, len(f.calls))
	copy(out, f.calls)
	return out
}

type fakeBridgeChainRails struct {
	mu       sync.Mutex
	quote    func(req *chainrails.CreateIntentRequest) (*chainrails.CreateIntentResponse, error)
	requests []chainrails.CreateIntentRequest
	status   *chainrails.IntentStatus
	nextID   int
}

func (f *fakeBridgeChainRails) CreateIntent(_ context.Context, req *chainrails.CreateIntentRequest) (*chainrails.CreateIntentResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *req
	f.requests = append(f.requests, cp)
	if f.quote == nil {
		return nil, fmt.Errorf("no quote configured")
	}
	resp, err := f.quote(req)
	if err != nil {
		return nil, err
	}
	f.nextID++
	resp.ID = f.nextID
	resp.IntentAddress = fmt.Sprintf("intent-address-%d", f.nextID)
	return resp, nil
}

func (f *fakeBridgeChainRails) GetIntentStatus(context.Context, string) (*chainrails.IntentStatus, error) {
	return f.status, nil
}

func (f *fakeBridgeChainRails) TriggerIntentProcessing(context.Context, string) error { return nil }

func (f *fakeBridgeChainRails) intentRequests() []chainrails.CreateIntentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]chainrails.CreateIntentRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// --- helpers ---

// testSolanaAddress is a base58 string that decodes to 32 bytes.
func testSolanaAddress() string {
	return base58.Encode(make([]byte, 32))
}

func feeQuote(fee decimal.Decimal) func(req *chainrails.CreateIntentRequest) (*chainrails.CreateIntentResponse, error) {
	return func(req *chainrails.CreateIntentRequest) (*chainrails.CreateIntentResponse, error) {
		units, ok := new(big.Int).SetString(req.Amount, 10)
		if !ok {
			return nil, fmt.Errorf("bad amount %q", req.Amount)
		}
		amount := decimal.NewFromBigInt(units, -6)
		total := amount.Add(fee)
		return &chainrails.CreateIntentResponse{
			TotalAmountInAssetToken: total.Truncate(6).Shift(6).BigInt().String(),
			AssetTokenDecimals:      6,
		}, nil
	}
}

func bridgeFixture(t *testing.T, available string) (*CryptoBridgeHandlers, *fakePayoutStore, *fakeBridgeCircle, *fakeBridgeChainRails, *circlepkg.Transaction) {
	t.Helper()
	tx := baseInboundTx("0x88f6d31857af17bc3b32f46d18d7ebcaa2ac49abc3a6398ba24e1533b218ca0c", "5.00")
	tx.ID = "cb703d3e-7b1c-5d08-b803-99c2ceb0b187"

	ops := &fakeOpsStore{wallets: []opsWallet{
		{ID: "wallet-row", Chain: "BASE", Address: "0x52ec", CircleWalletID: "circle-wallet-base"},
		{ID: "sol-row", Chain: "SOL", Address: testSolanaAddress()},
	}}
	payouts := newFakePayoutStore()
	circle := &fakeBridgeCircle{
		sourceTx: &tx,
		transfer: &circlepkg.Transaction{
			ID:     "circle-transfer-1",
			State:  "COMPLETE",
			TxHash: "0xfundinghash",
		},
		balances: []circlepkg.TokenBalance{{Token: circlepkg.TokenInfo{ID: "token-usdc", Symbol: "USDC"}, Amount: available}},
	}
	cr := &fakeBridgeChainRails{quote: feeQuote(decimal.RequireFromString("0.10"))}
	h := NewCryptoBridgeHandlers(ops, payouts, circle, cr, zap.NewNop())
	return h, payouts, circle, cr, &tx
}

func bridgeRequest(t *testing.T, tx *circlepkg.Transaction, userID uuid.UUID, amount string, dryRun bool, all bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"user_id":             userID.String(),
		"source_circle_tx_id": tx.ID,
		"amount":              amount,
		"all":                 all,
		"destination_address": testSolanaAddress(),
		"reason":              "user cannot authenticate; moving their own funds",
		"dry_run":             dryRun,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	c, rec := newOpsTestContext(t, http.MethodPost, "/internal/crypto/bridge", body, nil)
	c.Request.Header.Set("X-Internal-Actor", "ops-runner")
	return c, rec
}

func decodeBody(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

// --- tests ---

// A dry run must be completely inert: no payout row, no suppression row, no
// provider call, and it must say so.
func TestCryptoBridge_DryRunWritesNothing(t *testing.T) {
	h, payouts, circle, cr, tx := bridgeFixture(t, "5.00")
	c, rec := bridgeRequest(t, tx, uuid.New(), "5.00", true, false)

	h.Bridge(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec.Body.Bytes())
	if body["dry_run"] != true || body["would_bridge"] != true {
		t.Errorf("expected a dry-run plan, got %+v", body)
	}
	if body["ledger_touched"] != false {
		t.Errorf("ledger_touched = %v, want false", body["ledger_touched"])
	}
	if payouts.count() != 0 || payouts.suppressionCount() != 0 {
		t.Errorf("dry run wrote %d payouts and %d suppressions", payouts.count(), payouts.suppressionCount())
	}
	if len(circle.transferCalls()) != 0 || len(cr.intentRequests()) != 0 {
		t.Errorf("dry run called providers: transfers=%d intents=%d", len(circle.transferCalls()), len(cr.intentRequests()))
	}
}

// The live path must write the suppression before moving money, create the
// intent, fund it with the quoted total, and never touch the ledger.
func TestCryptoBridge_LiveExplicitAmountFundsIntent(t *testing.T) {
	h, payouts, circle, cr, tx := bridgeFixture(t, "5.00")
	userID := uuid.New()
	c, rec := bridgeRequest(t, tx, userID, "4.90", false, false)

	h.Bridge(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec.Body.Bytes())
	if body["status"] != entities.OpsPayoutStatusFunded {
		t.Errorf("status = %v, want funded", body["status"])
	}
	if body["ledger_touched"] != false {
		t.Errorf("ledger_touched = %v, want false", body["ledger_touched"])
	}
	if body["amount"] != "4.900000" {
		t.Errorf("amount = %v, want 4.900000", body["amount"])
	}
	if body["funding_amount"] != "5.000000" {
		t.Errorf("funding_amount = %v, want 5.000000", body["funding_amount"])
	}
	if body["fee_amount"] != "0.100000" {
		t.Errorf("fee_amount = %v, want 0.100000", body["fee_amount"])
	}
	if payouts.suppressionCount() != 1 {
		t.Errorf("suppression rows = %d, want 1", payouts.suppressionCount())
	}

	calls := circle.transferCalls()
	if len(calls) != 1 {
		t.Fatalf("transfer calls = %d, want 1", len(calls))
	}
	if calls[0].amount != "5.000000" {
		t.Errorf("funded %s, want the quoted total 5.000000", calls[0].amount)
	}
	if calls[0].walletID != "circle-wallet-base" {
		t.Errorf("funded from %q, want circle-wallet-base", calls[0].walletID)
	}
	if calls[0].idemKey == "" {
		t.Error("transfer had no idempotency key")
	}
	if calls[0].destination == "" || calls[0].destination != "intent-address-1" {
		t.Errorf("funded destination = %q, want the intent address", calls[0].destination)
	}

	reqs := cr.intentRequests()
	if len(reqs) != 1 {
		t.Fatalf("intent requests = %d, want 1", len(reqs))
	}
	if reqs[0].Recipient != testSolanaAddress() {
		t.Errorf("recipient = %q, want the Solana destination", reqs[0].Recipient)
	}
	if reqs[0].DestinationChain != "SOLANA_MAINNET" {
		t.Errorf("destination chain = %q", reqs[0].DestinationChain)
	}
	if reqs[0].SourceChain != "BASE_MAINNET" {
		t.Errorf("source chain = %q", reqs[0].SourceChain)
	}
}

// If a deposit row already exists for the transaction, the bridge must refuse:
// the money may already be credited, and paying it out again would double-pay.
func TestCryptoBridge_RefusesWhenSuppressionConflicts(t *testing.T) {
	h, payouts, circle, _, tx := bridgeFixture(t, "5.00")
	payouts.forceCreateErr = fmt.Errorf("%w (tx 0xdead)", entities.ErrOpsPayoutSuppressionConflict)

	c, rec := bridgeRequest(t, tx, uuid.New(), "5.00", false, false)
	h.Bridge(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if len(circle.transferCalls()) != 0 {
		t.Errorf("moved money despite a suppression conflict")
	}
}

// A transaction belonging to a different wallet must never be paid out.
func TestCryptoBridge_RefusesForeignTransaction(t *testing.T) {
	h, _, circle, _, tx := bridgeFixture(t, "5.00")
	tx.DestinationAddress = "0x9999999999999999999999999999999999999999"
	tx.WalletID = "someone-elses-wallet"

	c, rec := bridgeRequest(t, tx, uuid.New(), "5.00", false, false)
	h.Bridge(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if len(circle.transferCalls()) != 0 {
		t.Errorf("moved money for a foreign transaction")
	}
}

// A payout that already funded must not fund again.
func TestCryptoBridge_RefusesAlreadyFundedPayout(t *testing.T) {
	h, payouts, circle, _, tx := bridgeFixture(t, "5.00")
	userID := uuid.New()

	// Seed a completed payout for the same source transaction.
	existing := &entities.OpsCryptoPayout{
		ID:               uuid.New(),
		UserID:           userID,
		SourceChain:      "BASE",
		SourceCircleTxID: tx.ID,
		Status:           entities.OpsPayoutStatusFunded,
		Amount:           decimal.RequireFromString("5"),
		Reason:           "prior run",
		Actor:            "ops-runner",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	payouts.byID[existing.ID] = existing
	payouts.bySource[tx.ID] = existing.ID

	c, rec := bridgeRequest(t, tx, userID, "5.00", false, false)
	h.Bridge(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if len(circle.transferCalls()) != 0 {
		t.Errorf("funded a second time")
	}
	body := decodeBody(t, rec.Body.Bytes())
	if body["payout"] == nil {
		t.Error("response should include the existing payout")
	}
}

// If fees push the funding amount above the wallet balance, nothing is sent,
// the payout is marked failed, and the suppression is released so the normal
// credit path still works.
func TestCryptoBridge_ShortfallReleasesSuppression(t *testing.T) {
	h, payouts, circle, _, tx := bridgeFixture(t, "5.00")

	c, rec := bridgeRequest(t, tx, uuid.New(), "5.00", false, false)
	h.Bridge(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if len(circle.transferCalls()) != 0 {
		t.Errorf("attempted a transfer that could not be funded")
	}
	if payouts.suppressionCount() != 0 {
		t.Errorf("suppression still held after a definitive pre-transfer failure")
	}
	body := decodeBody(t, rec.Body.Bytes())
	if body["status"] != entities.OpsPayoutStatusFailed {
		t.Errorf("status = %v, want failed", body["status"])
	}
	if body["funding_amount"] != "5.100000" {
		t.Errorf("funding_amount = %v, want 5.100000", body["funding_amount"])
	}
}

// "all" must size the transfer so the quoted total fits the balance.
func TestCryptoBridge_AllModeSizesToBalance(t *testing.T) {
	h, payouts, circle, cr, tx := bridgeFixture(t, "5.00")

	body, _ := json.Marshal(map[string]interface{}{
		"user_id":             uuid.New().String(),
		"source_circle_tx_id": tx.ID,
		"all":                 true,
		"destination_address": testSolanaAddress(),
		"reason":              "recover the whole transfer",
		"dry_run":             false,
	})
	c, rec := newOpsTestContext(t, http.MethodPost, "/internal/crypto/bridge", body, nil)
	c.Request.Header.Set("X-Internal-Actor", "ops-runner")

	h.Bridge(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeBody(t, rec.Body.Bytes())
	if resp["amount"] != "4.900000" {
		t.Errorf("amount = %v, want 4.900000", resp["amount"])
	}
	if resp["funding_amount"] != "5.000000" {
		t.Errorf("funding_amount = %v, want 5.000000", resp["funding_amount"])
	}
	// First quote (5.00 recipient) needs 5.10; second (4.90) needs 5.00.
	if got := len(cr.intentRequests()); got != 2 {
		t.Errorf("intent requests = %d, want 2 quotes", got)
	}
	calls := circle.transferCalls()
	if len(calls) != 1 || calls[0].amount != "5.000000" {
		t.Fatalf("transfers = %+v, want one for 5.000000", calls)
	}

	// The persisted row must record what the recipient actually gets, not the
	// pre-quote anchor (the source transfer amount). Otherwise the audit trail
	// claims 5.00 was delivered when 4.90 was.
	if payouts.count() != 1 {
		t.Fatalf("payouts = %d, want 1", payouts.count())
	}
	for _, stored := range payouts.byID {
		if got := stored.Amount.StringFixed(6); got != "4.900000" {
			t.Errorf("stored payout amount = %s, want 4.900000", got)
		}
		if stored.FundingAmount == nil || stored.FundingAmount.StringFixed(6) != "5.000000" {
			t.Errorf("stored funding_amount = %v, want 5.000000", stored.FundingAmount)
		}
	}
}

// An ambiguous transfer must keep the suppression and resume the SAME intent
// with the SAME idempotency key, so a retry can never create a second transfer.
func TestCryptoBridge_AmbiguousTransferReusesIntentAndKey(t *testing.T) {
	h, payouts, circle, cr, tx := bridgeFixture(t, "5.00")
	userID := uuid.New()

	circle.transferErr = fmt.Errorf("connection reset")
	c1, rec1 := bridgeRequest(t, tx, userID, "4.90", false, false)
	h.Bridge(c1)

	if rec1.Code != http.StatusBadGateway {
		t.Fatalf("first call status = %d, want 502; body = %s", rec1.Code, rec1.Body.String())
	}
	first := decodeBody(t, rec1.Body.Bytes())
	if first["status"] != entities.OpsPayoutStatusFundingUncertain {
		t.Errorf("status = %v, want funding_uncertain", first["status"])
	}
	if payouts.suppressionCount() != 1 {
		t.Fatalf("suppression released while money may be in flight")
	}

	circle.transferErr = nil
	c2, rec2 := bridgeRequest(t, tx, userID, "4.90", false, false)
	h.Bridge(c2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200; body = %s", rec2.Code, rec2.Body.String())
	}
	if got := len(cr.intentRequests()); got != 1 {
		t.Errorf("retry created a second intent (%d requests); it must resume the first", got)
	}
	calls := circle.transferCalls()
	if len(calls) != 2 {
		t.Fatalf("transfer attempts = %d, want 2", len(calls))
	}
	if calls[0].idemKey == "" || calls[0].idemKey != calls[1].idemKey {
		t.Errorf("idempotency keys differ across retries: %q vs %q", calls[0].idemKey, calls[1].idemKey)
	}
	if calls[0].destination != calls[1].destination {
		t.Errorf("retry funded a different intent: %q vs %q", calls[0].destination, calls[1].destination)
	}
}

func TestCryptoBridge_RejectsInvalidSolanaDestination(t *testing.T) {
	h, _, circle, _, tx := bridgeFixture(t, "5.00")

	body, _ := json.Marshal(map[string]interface{}{
		"user_id":             uuid.New().String(),
		"source_circle_tx_id": tx.ID,
		"amount":              "4.90",
		"destination_address": "not-a-solana-address",
		"reason":              "typo test",
		"dry_run":             false,
	})
	c, rec := newOpsTestContext(t, http.MethodPost, "/internal/crypto/bridge", body, nil)
	c.Request.Header.Set("X-Internal-Actor", "ops-runner")

	h.Bridge(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if len(circle.transferCalls()) != 0 {
		t.Errorf("moved money to an invalid destination")
	}
}

func TestCryptoBridge_RequiresActorAndReason(t *testing.T) {
	h, _, _, _, tx := bridgeFixture(t, "5.00")

	body, _ := json.Marshal(map[string]interface{}{
		"user_id":             uuid.New().String(),
		"source_circle_tx_id": tx.ID,
		"amount":              "4.90",
	})
	c, rec := newOpsTestContext(t, http.MethodPost, "/internal/crypto/bridge", body, nil)
	h.Bridge(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing actor: status = %d, want 400", rec.Code)
	}

	c2, rec2 := newOpsTestContext(t, http.MethodPost, "/internal/crypto/bridge", body, nil)
	c2.Request.Header.Set("X-Internal-Actor", "ops-runner")
	h.Bridge(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("missing reason: status = %d, want 400", rec2.Code)
	}
}

func TestCryptoBridge_RejectsAmountAboveCap(t *testing.T) {
	h, _, _, _, tx := bridgeFixture(t, "50000")
	c, rec := bridgeRequest(t, tx, uuid.New(), "10000.01", false, false)
	h.Bridge(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}
