package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
)

type fakeOpsStore struct {
	wallets   []opsWallet
	deposits  map[string]*opsDeposit
	sweeps    map[string]*opsSweep
	balances  map[string]string
	walletErr error
}

func (f *fakeOpsStore) WalletsForUser(context.Context, uuid.UUID) ([]opsWallet, error) {
	return f.wallets, f.walletErr
}

func (f *fakeOpsStore) DepositByTxHash(_ context.Context, txHash string) (*opsDeposit, error) {
	if f.deposits == nil {
		return nil, nil
	}
	return f.deposits[txHash], nil
}

func (f *fakeOpsStore) SweepByDepositID(_ context.Context, depositID string) (*opsSweep, error) {
	if f.sweeps == nil {
		return nil, nil
	}
	return f.sweeps[depositID], nil
}

func (f *fakeOpsStore) LedgerBalances(context.Context, uuid.UUID) (map[string]string, error) {
	return f.balances, nil
}

type fakeInboundSource struct {
	txs      map[string][]circlepkg.Transaction
	balances map[string][]circlepkg.TokenBalance
	symbols  map[string]string
}

func (f *fakeInboundSource) ListTransactions(_ context.Context, walletID, _, _ string) ([]circlepkg.Transaction, error) {
	return f.txs[walletID], nil
}

func (f *fakeInboundSource) GetTokenSymbol(_ context.Context, _, tokenID string) (string, error) {
	if s, ok := f.symbols[tokenID]; ok {
		return s, nil
	}
	return "", context.DeadlineExceeded
}

func (f *fakeInboundSource) GetTokenBalanceOnchain(_ context.Context, walletID string) ([]circlepkg.TokenBalance, error) {
	return f.balances[walletID], nil
}

type creditCall struct {
	userID         uuid.UUID
	amount         decimal.Decimal
	token          entities.Stablecoin
	chain          string
	txHash         string
	circleWalletID string
}

type fakeCreditor struct {
	mu    sync.Mutex
	calls []creditCall
	err   error
}

func (f *fakeCreditor) ProcessCircleDeposit(_ context.Context, userID uuid.UUID, amount decimal.Decimal, token entities.Stablecoin, chain, txHash, circleWalletID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, creditCall{userID, amount, token, chain, txHash, circleWalletID})
	return f.err
}

func newOpsTestContext(t *testing.T, method, path string, body []byte, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	c.Request = req
	c.Params = params
	return c, rec
}

func baseInboundTx(txHash string, amount string) circlepkg.Transaction {
	return circlepkg.Transaction{
		ID:                 "circle-tx-" + txHash,
		State:              "COMPLETE",
		TxHash:             txHash,
		Blockchain:         "BASE",
		WalletID:           "circle-wallet-base",
		DestinationAddress: "0x52ec",
		TokenID:            "token-usdc",
		Amounts:            []string{amount},
		CreateDate:         time.Now().Add(-24 * time.Hour),
	}
}

// The diagnosis endpoint is what tells ops where the money actually is, so it
// must report the on-chain balance and whether each inbound transfer has a
// deposit row behind it.
func TestCryptoStatus_ReportsOnchainBalanceAndUncreditedInbound(t *testing.T) {
	userID := uuid.New()
	store := &fakeOpsStore{
		wallets:  []opsWallet{{ID: "wallet-row", Chain: "BASE", Address: "0x52ec", CircleWalletID: "circle-wallet-base"}},
		balances: map[string]string{"spending_balance": "0", "stash_balance": "0"},
	}
	source := &fakeInboundSource{
		txs:      map[string][]circlepkg.Transaction{"circle-wallet-base": {baseInboundTx("0xdeadbeef", "5.00")}},
		balances: map[string][]circlepkg.TokenBalance{"circle-wallet-base": {{Token: circlepkg.TokenInfo{ID: "token-usdc", Symbol: "USDC"}, Amount: "5.00"}}},
	}
	h := NewDepositBackfillHandlers(store, source, &fakeCreditor{}, false, zap.NewNop())

	c, rec := newOpsTestContext(t, http.MethodGet, "/internal/users/"+userID.String()+"/crypto-status", nil,
		gin.Params{{Key: "id", Value: userID.String()}})
	h.CryptoStatus(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ChainrailsConfigured bool                     `json:"chainrails_configured"`
		LedgerBalances       map[string]string        `json:"ledger_balances"`
		Wallets              []map[string]interface{} `json:"wallets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ChainrailsConfigured {
		t.Error("chainrails_configured should be false when no chainrails client is wired")
	}
	if resp.LedgerBalances["spending_balance"] != "0" {
		t.Errorf("spending balance = %q, want 0", resp.LedgerBalances["spending_balance"])
	}
	if len(resp.Wallets) != 1 {
		t.Fatalf("wallets = %d, want 1", len(resp.Wallets))
	}
	if got := resp.Wallets[0]["usdc_onchain"]; got != "5" {
		t.Errorf("usdc_onchain = %v, want 5", got)
	}
	inbound, ok := resp.Wallets[0]["inbound"].([]interface{})
	if !ok || len(inbound) != 1 {
		t.Fatalf("inbound = %v, want one transfer", resp.Wallets[0]["inbound"])
	}
	row := inbound[0].(map[string]interface{})
	if row["already_credited"] != false {
		t.Errorf("already_credited = %v, want false (no deposit row)", row["already_credited"])
	}
}

// A dry run must never reach the credit path, even when it finds money that
// needs crediting.
func TestBackfillDeposits_DryRunDoesNotCredit(t *testing.T) {
	userID := uuid.New()
	store := &fakeOpsStore{
		wallets: []opsWallet{{ID: "wallet-row", Chain: "BASE", Address: "0x52ec", CircleWalletID: "circle-wallet-base"}},
	}
	creditor := &fakeCreditor{}
	source := &fakeInboundSource{
		txs:     map[string][]circlepkg.Transaction{"circle-wallet-base": {baseInboundTx("0xdeadbeef", "5.00")}},
		symbols: map[string]string{"token-usdc": "USDC"},
	}
	h := NewDepositBackfillHandlers(store, source, creditor, true, zap.NewNop())

	body, _ := json.Marshal(map[string]interface{}{"user_id": userID.String()})
	c, rec := newOpsTestContext(t, http.MethodPost, "/internal/deposits/backfill", body, nil)
	h.BackfillDeposits(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(creditor.calls) != 0 {
		t.Fatalf("dry run credited %d times, want 0", len(creditor.calls))
	}
	var resp struct {
		DryRun     bool               `json:"dry_run"`
		Candidates []inboundCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.DryRun {
		t.Error("dry_run should default to true")
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Action != "would_credit" {
		t.Fatalf("candidates = %+v, want one would_credit", resp.Candidates)
	}
}

// An explicit dry_run=false replays through the normal credit path exactly once.
func TestBackfillDeposits_LiveRunCreditsThroughNormalPath(t *testing.T) {
	userID := uuid.New()
	store := &fakeOpsStore{
		wallets: []opsWallet{{ID: "wallet-row", Chain: "BASE", Address: "0x52ec", CircleWalletID: "circle-wallet-base"}},
	}
	creditor := &fakeCreditor{}
	source := &fakeInboundSource{
		txs:     map[string][]circlepkg.Transaction{"circle-wallet-base": {baseInboundTx("0xdeadbeef", "5.00")}},
		symbols: map[string]string{"token-usdc": "USDC"},
	}
	h := NewDepositBackfillHandlers(store, source, creditor, true, zap.NewNop())

	body, _ := json.Marshal(map[string]interface{}{"user_id": userID.String(), "dry_run": false})
	c, rec := newOpsTestContext(t, http.MethodPost, "/internal/deposits/backfill", body, nil)
	h.BackfillDeposits(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(creditor.calls) != 1 {
		t.Fatalf("credited %d times, want 1", len(creditor.calls))
	}
	call := creditor.calls[0]
	if call.txHash != "0xdeadbeef" {
		t.Errorf("tx hash = %q, want 0xdeadbeef", call.txHash)
	}
	if call.chain != "BASE" {
		t.Errorf("chain = %q, want BASE", call.chain)
	}
	if call.token != entities.StablecoinUSDC {
		t.Errorf("token = %q, want USDC", call.token)
	}
	if !call.amount.Equal(decimal.RequireFromString("5.00")) {
		t.Errorf("amount = %s, want 5.00", call.amount)
	}
	if call.circleWalletID != "circle-wallet-base" {
		t.Errorf("circle wallet id = %q, want circle-wallet-base", call.circleWalletID)
	}
}

// Replaying a transfer that already has a confirmed deposit must not credit a
// second time — that is the whole point of routing through the idempotent
// ProcessCircleDeposit path.
func TestBackfillDeposits_SkipsAlreadyCreditedTransfer(t *testing.T) {
	userID := uuid.New()
	store := &fakeOpsStore{
		wallets:  []opsWallet{{ID: "wallet-row", Chain: "BASE", Address: "0x52ec", CircleWalletID: "circle-wallet-base"}},
		deposits: map[string]*opsDeposit{"0xdeadbeef": {ID: "dep-1", Status: "confirmed", Amount: "5.00", Chain: "BASE"}},
		sweeps:   map[string]*opsSweep{"dep-1": {ID: "sweep-1", Status: "pending"}},
	}
	creditor := &fakeCreditor{}
	source := &fakeInboundSource{
		txs:     map[string][]circlepkg.Transaction{"circle-wallet-base": {baseInboundTx("0xdeadbeef", "5.00")}},
		symbols: map[string]string{"token-usdc": "USDC"},
	}
	h := NewDepositBackfillHandlers(store, source, creditor, true, zap.NewNop())

	body, _ := json.Marshal(map[string]interface{}{"user_id": userID.String(), "dry_run": false})
	c, rec := newOpsTestContext(t, http.MethodPost, "/internal/deposits/backfill", body, nil)
	h.BackfillDeposits(c)

	if len(creditor.calls) != 0 {
		t.Fatalf("credited %d times, want 0 (deposit already confirmed)", len(creditor.calls))
	}
	var resp struct {
		Candidates []inboundCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(resp.Candidates))
	}
	cand := resp.Candidates[0]
	if cand.Action != "skipped" || !cand.AlreadyCredited {
		t.Errorf("candidate = %+v, want skipped + already_credited", cand)
	}
	if cand.SweepStatus != "pending" {
		t.Errorf("sweep status = %q, want pending", cand.SweepStatus)
	}
}

// A non-stablecoin inbound transfer must never be credited as USDC.
func TestBackfillDeposits_RefusesUnsupportedToken(t *testing.T) {
	userID := uuid.New()
	tx := baseInboundTx("0xfeedface", "5.00")
	store := &fakeOpsStore{
		wallets: []opsWallet{{ID: "wallet-row", Chain: "BASE", Address: "0x52ec", CircleWalletID: "circle-wallet-base"}},
	}
	creditor := &fakeCreditor{}
	source := &fakeInboundSource{
		txs:     map[string][]circlepkg.Transaction{"circle-wallet-base": {tx}},
		symbols: map[string]string{"token-usdc": "SHIB"},
	}
	h := NewDepositBackfillHandlers(store, source, creditor, true, zap.NewNop())

	body, _ := json.Marshal(map[string]interface{}{"user_id": userID.String(), "dry_run": false})
	c, rec := newOpsTestContext(t, http.MethodPost, "/internal/deposits/backfill", body, nil)
	h.BackfillDeposits(c)

	if len(creditor.calls) != 0 {
		t.Fatalf("credited %d times, want 0 for a non-stablecoin token", len(creditor.calls))
	}
	var resp struct {
		Candidates []inboundCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Action != "skipped" {
		t.Fatalf("candidates = %+v, want one skipped", resp.Candidates)
	}
}

// A chain filter must not reach into wallets on other chains.
func TestBackfillDeposits_ChainFilterNarrowsTheScan(t *testing.T) {
	userID := uuid.New()
	store := &fakeOpsStore{
		wallets: []opsWallet{
			{ID: "sol", Chain: "SOL", Address: "sol-addr", CircleWalletID: "circle-wallet-sol"},
			{ID: "base", Chain: "BASE", Address: "0x52ec", CircleWalletID: "circle-wallet-base"},
		},
	}
	creditor := &fakeCreditor{}
	source := &fakeInboundSource{
		txs:     map[string][]circlepkg.Transaction{"circle-wallet-base": {baseInboundTx("0xdeadbeef", "5.00")}},
		symbols: map[string]string{"token-usdc": "USDC"},
	}
	h := NewDepositBackfillHandlers(store, source, creditor, true, zap.NewNop())

	body, _ := json.Marshal(map[string]interface{}{"user_id": userID.String(), "dry_run": false, "chain": "sol"})
	c, _ := newOpsTestContext(t, http.MethodPost, "/internal/deposits/backfill", body, nil)
	h.BackfillDeposits(c)

	if len(creditor.calls) != 0 {
		t.Fatalf("credited %d times, want 0 when filtering to SOL", len(creditor.calls))
	}
}
