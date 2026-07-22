package blend

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"go.uber.org/zap"
)

// Deploy types for an ActionPlan, mirroring @blend-money/core.
const (
	// deployDirect: individual transactions executed by the EOA/owner signer
	// (Blend deposits). Funds are pulled from the EOA into the Safe + vault.
	deployDirect = "direct"
	// deployMultisend: the steps act ON the user's Safe (redeem vault shares, etc.)
	// and MUST originate from the Safe — batched into one Safe MultiSend transaction.
	// Executing these as plain EOA calls redeems nothing (the Safe owns the shares).
	deployMultisend = "multisend"
)

// ActionStep is one on-chain transaction in a Blend action plan.
type ActionStep struct {
	ChainID        int64  `json:"chainId"`
	To             string `json:"to"`
	Data           string `json:"data"`
	Value          string `json:"value"`
	IsDelegateCall bool   `json:"isDelegateCall,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Description    string `json:"description,omitempty"`
}

// ActionPlan is the on-chain plan a session expects the partner to execute.
// DeployType determines HOW the steps are submitted (see deployDirect/deployMultisend).
type ActionPlan struct {
	DeployType string       `json:"deployType"`
	Steps      []ActionStep `json:"steps"`
}

// ContractExecutor signs and broadcasts EVM contract calls from a Circle wallet.
type ContractExecutor interface {
	ExecuteContract(ctx context.Context, req *circlepkg.CreateContractExecutionRequest) (*circlepkg.Transaction, error)
	GetTransaction(ctx context.Context, txID string) (*circlepkg.Transaction, error)
	ListTransactions(ctx context.Context, walletID string, operation string, state string) ([]circlepkg.Transaction, error)
}

// Allowlist of contract addresses the executor will call. Anything outside this set
// is rejected before signing — defense against a compromised Blend session payload
// telling us to drain funds to an attacker contract.
type Allowlist struct {
	addresses map[string]struct{}
}

// NewAllowlist creates an allowlist from a slice of hex addresses (case-insensitive).
func NewAllowlist(addresses []string) *Allowlist {
	set := make(map[string]struct{}, len(addresses))
	for _, a := range addresses {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" {
			set[a] = struct{}{}
		}
	}
	return &Allowlist{addresses: set}
}

// Allows reports whether addr is in the allowlist. Empty allowlist allows all
// (development only) and the executor logs a warning when this happens.
func (a *Allowlist) Allows(addr string) bool {
	if a == nil || len(a.addresses) == 0 {
		return true
	}
	_, ok := a.addresses[strings.ToLower(addr)]
	return ok
}

// Empty reports whether no addresses are configured.
func (a *Allowlist) Empty() bool {
	return a == nil || len(a.addresses) == 0
}

// TrustedSafe is a per-call dynamically-trusted target: the route's own Gnosis Safe.
// A deposit/withdraw action plan legitimately targets the user's Safe (e.g. the withdraw
// liquidityReset delegatecall), but the Safe address is per-user so it can't sit in the
// static allowlist. It is verified on-chain (contract + ownership) before being trusted.
type TrustedSafe struct {
	Address  string
	OwnerEOA string
	ChainID  int64
}

// PlanExecutor walks a Blend action plan and executes each step through Circle.
type PlanExecutor struct {
	circle    ContractExecutor
	allowlist *Allowlist
	verifier  SafeVerifier
	logger    *zap.Logger
}

// NewPlanExecutor wires a plan executor. allowlist is REQUIRED in production; nil/empty
// allowlist is permitted only for sandbox bring-up and emits warnings.
func NewPlanExecutor(circle ContractExecutor, allowlist *Allowlist, logger *zap.Logger) *PlanExecutor {
	return &PlanExecutor{circle: circle, allowlist: allowlist, logger: logger}
}

// SetSafeVerifier wires on-chain verification of dynamically-trusted Safe addresses.
// When set, a TrustedSafe is only honored after confirming it is a real Safe contract
// owned by the expected EOA. Required in production.
func (e *PlanExecutor) SetSafeVerifier(v SafeVerifier) {
	if e == nil {
		return
	}
	e.verifier = v
}

// ExecutedTx is the result of a single executed step.
type ExecutedTx struct {
	ChainID       int64
	TxHash        string
	TransactionID string
}

// WalletResolver maps a Blend chain ID to the user's Circle wallet on that chain.
// The executor uses this to execute steps on the correct chain (e.g. Ethereum vs Base).
type WalletResolver func(ctx context.Context, chainID int64) (walletID, address string, err error)

// Execute runs each step of a plan via Circle, returning the resulting tx hashes
// in submission order. The caller is responsible for handing these to /intent/submit.
//
// resolveWallet maps each step's chain ID to the correct Circle wallet for that chain.
// This ensures the tx executes on the chain where the vault contract lives, not always Base.
//
// trustedSafe (optional) is the route's own Safe, dynamically trusted for THIS call in
// addition to the static allowlist. On-chain verification is skipped for non-Base chains
// (Blend's ResolveSafe already validated the Safe on that chain).
//
// Idempotency: idempotencyPrefix is combined with the step index to produce a stable
// Circle idempotency key per step. Re-runs with the same prefix are safe.
func (e *PlanExecutor) Execute(ctx context.Context, resolveWallet WalletResolver, plan *ActionPlan, idempotencyPrefix string, trustedSafe *TrustedSafe) ([]ExecutedTx, error) {
	if e == nil || e.circle == nil {
		return nil, errors.New("blend executor not configured")
	}
	if plan == nil || len(plan.Steps) == 0 {
		return nil, errors.New("blend plan: no steps to execute")
	}
	if resolveWallet == nil {
		return nil, errors.New("blend plan: wallet resolver required")
	}

	if e.allowlist.Empty() {
		e.logger.Warn("Blend executor running without contract allowlist — DEV ONLY")
	}

	dynamic := make(map[string]struct{}, 1)
	if trustedSafe != nil && strings.TrimSpace(trustedSafe.Address) != "" {
		safeAddr := strings.TrimSpace(trustedSafe.Address)
		if !isHexAddress(safeAddr) {
			return nil, fmt.Errorf("blend plan: invalid trusted safe address %q", safeAddr)
		}
		auditFields := []zap.Field{
			zap.String("safe_address", safeAddr),
			zap.String("owner_eoa", trustedSafe.OwnerEOA),
			zap.Int64("chain_id", trustedSafe.ChainID),
			zap.String("idempotency_prefix", idempotencyPrefix),
		}
		// Only verify Safe on-chain for Base — for other chains Blend's ResolveSafe
		// already validated it, and we may not have an RPC endpoint for that chain.
		if e.verifier != nil && trustedSafe.ChainID == BaseMainnetChainID {
			if err := e.verifier.VerifySafe(ctx, trustedSafe.ChainID, safeAddr, trustedSafe.OwnerEOA); err != nil {
				e.logger.Error("Blend: dynamic Safe trust REJECTED — on-chain verification failed",
					append(auditFields, zap.Error(err))...)
				return nil, fmt.Errorf("blend plan: refusing to trust Safe %s: %w", safeAddr, err)
			}
			e.logger.Info("Blend: dynamic Safe trust granted (on-chain verified)", auditFields...)
		} else if e.verifier == nil {
			// No verifier configured — refuse to trust without on-chain verification.
			return nil, fmt.Errorf("blend plan: refusing to trust Safe %s without on-chain verification (verifier not configured)", safeAddr)
		} else {
			// Non-Base chain — Blend's ResolveSafe already validated; trust with a log.
			e.logger.Info("Blend: dynamic Safe trust granted (non-Base chain, Blend-validated)", auditFields...)
		}
		dynamic[strings.ToLower(safeAddr)] = struct{}{}
	}

	// Withdraw-style plans act ON the Safe and must originate FROM it. Batch the steps
	// into a single Safe MultiSend executed via Safe.execTransaction (the Safe is the
	// only contract Circle calls here, and it was just on-chain verified above).
	if plan.DeployType == deployMultisend {
		return e.executeMultisend(ctx, resolveWallet, plan, idempotencyPrefix, trustedSafe)
	}

	results := make([]ExecutedTx, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		if !isHexAddress(step.To) {
			return results, fmt.Errorf("blend plan step %d: invalid `to` address %q", i, step.To)
		}
		_, isDynamic := dynamic[strings.ToLower(step.To)]
		if !isDynamic && !e.allowlist.Allows(step.To) {
			return results, fmt.Errorf("blend plan step %d: contract %s not in allowlist", i, step.To)
		}
		callData, err := normalizeHex(step.Data)
		if err != nil {
			return results, fmt.Errorf("blend plan step %d: invalid calldata: %w", i, err)
		}
		value, err := normalizeWeiValue(step.Value)
		if err != nil {
			return results, fmt.Errorf("blend plan step %d: invalid value: %w", i, err)
		}

		// Resolve the correct wallet for this step's chain.
		walletID, _, wErr := resolveWallet(ctx, step.ChainID)
		if wErr != nil {
			return results, fmt.Errorf("blend plan step %d: resolve wallet for chain %d: %w", i, step.ChainID, wErr)
		}

		idemKey := uuid.NewSHA1(uuid.NameSpaceOID,
			[]byte(fmt.Sprintf("%s|%d|%s", idempotencyPrefix, i, txDigest(step)))).String()
		req := &circlepkg.CreateContractExecutionRequest{
			IdempotencyKey:  idemKey,
			WalletID:        walletID,
			ContractAddress: step.To,
			CallData:        callData,
			Amount:          value,
			FeeLevel:        "MEDIUM",
		}
		e.logger.Info("Blend executor submitting step",
			zap.Int("step", i),
			zap.Int64("chain_id", step.ChainID),
			zap.String("wallet_id", walletID),
			zap.String("to", step.To),
			zap.String("desc", step.Description),
			zap.Bool("has_calldata", callData != ""),
			zap.String("value_wei", value),
		)
		tx, err := e.circle.ExecuteContract(ctx, req)
		if err != nil {
			return results, fmt.Errorf("blend plan step %d: circle execute: %w", i, err)
		}
		e.logger.Info("Blend executor step result",
			zap.Int("step", i),
			zap.String("tx_id", tx.ID),
			zap.String("tx_hash", tx.TxHash),
			zap.String("state", string(tx.State)),
		)
		results = append(results, ExecutedTx{
			ChainID:       step.ChainID,
			TxHash:        tx.TxHash,
			TransactionID: tx.ID,
		})
	}
	return results, nil
}

// executeMultisend submits all steps as a single Safe.execTransaction that delegatecalls
// MultiSend — the non-4337 equivalent of Blend's batched Safe UserOp. The owner (this
// Circle wallet) authorizes via a pre-validated signature, valid because it is both the
// caller and the Safe's sole owner (verified on-chain above, incl. threshold==1).
func (e *PlanExecutor) executeMultisend(ctx context.Context, resolveWallet WalletResolver, plan *ActionPlan, idempotencyPrefix string, trustedSafe *TrustedSafe) ([]ExecutedTx, error) {
	if trustedSafe == nil || strings.TrimSpace(trustedSafe.Address) == "" {
		return nil, errors.New("blend: multisend plan requires the route's Safe")
	}
	if !isHexAddress(strings.TrimSpace(trustedSafe.OwnerEOA)) {
		return nil, fmt.Errorf("blend: multisend plan requires a valid Safe owner, got %q", trustedSafe.OwnerEOA)
	}
	if e.verifier == nil {
		return nil, fmt.Errorf("blend: refusing Safe multisend without on-chain verification (verifier not configured)")
	}

	// Resolve the wallet for the Safe's chain — the multisend tx must be submitted
	// from the same chain where the vault/Safe is deployed.
	walletID, _, wErr := resolveWallet(ctx, trustedSafe.ChainID)
	if wErr != nil {
		return nil, fmt.Errorf("blend: resolve wallet for Safe chain %d: %w", trustedSafe.ChainID, wErr)
	}

	// Validate every inner step target against the same allowlist the direct path uses,
	// so a poisoned withdraw quote can't make the Safe call an arbitrary contract. (An
	// empty allowlist allows all — dev only — matching Execute's behavior.)
	for i, step := range plan.Steps {
		if !isHexAddress(step.To) {
			return nil, fmt.Errorf("blend: multisend step %d: invalid `to` address %q", i, step.To)
		}
		if !e.allowlist.Allows(step.To) {
			return nil, fmt.Errorf("blend: multisend step %d: contract %s not in allowlist", i, step.To)
		}
	}

	multiSendData, err := encodeMultiSendData(plan.Steps)
	if err != nil {
		return nil, fmt.Errorf("blend: encode multisend: %w", err)
	}
	execData, err := encodeSafeExecTransaction(multiSendData, trustedSafe.OwnerEOA)
	if err != nil {
		return nil, fmt.Errorf("blend: encode execTransaction: %w", err)
	}

	idemKey := uuid.NewSHA1(uuid.NameSpaceOID,
		[]byte(fmt.Sprintf("%s|safe-multisend|%s", idempotencyPrefix, multisendDigest(plan.Steps)))).String()
	req := &circlepkg.CreateContractExecutionRequest{
		IdempotencyKey:  idemKey,
		WalletID:        walletID,
		ContractAddress: trustedSafe.Address,
		CallData:        hexEncode(execData),
		Amount:          "0",
		FeeLevel:        "MEDIUM",
	}
	e.logger.Info("Blend executor submitting Safe multisend",
		zap.String("safe", trustedSafe.Address),
		zap.String("owner_eoa", trustedSafe.OwnerEOA),
		zap.String("wallet_id", walletID),
		zap.Int("steps", len(plan.Steps)),
		zap.Int64("chain_id", trustedSafe.ChainID),
	)
	tx, err := e.circle.ExecuteContract(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("blend: safe multisend circle execute: %w", err)
	}

	// Circle contract-execution returns async: the tx is created but the hash
	// only becomes available once the on-chain tx reaches COMPLETE state.
	// Poll until we have a hash — Blend needs it to verify the receipt.
	if tx.TxHash == "" {
		// If CreateContractExecution returned no tx_id at all, find it via ListTransactions first.
		if tx.ID == "" {
			tx = e.findRecentContractExecution(ctx, walletID, tx)
		}
		// Poll GetTransaction until state=COMPLETE or tx_hash appears.
		if tx.ID != "" {
			tx = e.pollForComplete(ctx, tx)
		}
	}
	e.logger.Info("Blend executor Safe multisend result",
		zap.String("tx_id", tx.ID), zap.String("tx_hash", tx.TxHash), zap.String("state", string(tx.State)))
	if tx.TxHash == "" {
		return nil, fmt.Errorf("blend: Circle tx has no hash after polling (id=%s state=%s)", tx.ID, tx.State)
	}
	return []ExecutedTx{{ChainID: trustedSafe.ChainID, TxHash: tx.TxHash, TransactionID: tx.ID}}, nil
}

// findRecentContractExecution polls ListTransactions to find the most recent
// CONTRACT_EXECUTION for the wallet when CreateContractExecution returned an
// empty Transaction struct (no tx_id). Returns the found tx or the original empty one.
func (e *PlanExecutor) findRecentContractExecution(ctx context.Context, walletID string, fallback *circlepkg.Transaction) *circlepkg.Transaction {
	pollCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	e.logger.Info("Blend: Circle returned no tx_id, polling ListTransactions to find it",
		zap.String("wallet_id", walletID))
	for {
		select {
		case <-pollCtx.Done():
			e.logger.Warn("Blend: timed out polling ListTransactions for tx_id",
				zap.String("wallet_id", walletID))
			return fallback
		case <-ticker.C:
			txs, err := e.circle.ListTransactions(pollCtx, walletID, "CONTRACT_EXECUTION", "")
			if err != nil {
				e.logger.Warn("Blend: ListTransactions failed, retrying",
					zap.String("wallet_id", walletID), zap.Error(err))
				continue
			}
			for i := range txs {
				if txs[i].ID != "" {
					e.logger.Info("Blend: found tx from ListTransactions",
						zap.String("tx_id", txs[i].ID), zap.String("state", string(txs[i].State)),
						zap.String("tx_hash", txs[i].TxHash))
					return &txs[i]
				}
			}
			e.logger.Debug("Blend: no CONTRACT_EXECUTION found yet",
				zap.String("wallet_id", walletID), zap.Int("candidates", len(txs)))
		}
	}
}

// pollForComplete polls GetTransaction until state is COMPLETE or FAILED.
// The tx hash is only populated at COMPLETE.
func (e *PlanExecutor) pollForComplete(ctx context.Context, initial *circlepkg.Transaction) *circlepkg.Transaction {
	pollCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	tx := initial
	e.logger.Info("Blend: polling Circle for tx completion",
		zap.String("tx_id", tx.ID), zap.String("state", string(tx.State)))
	for {
		select {
		case <-pollCtx.Done():
			e.logger.Warn("Blend: timed out polling for tx completion",
				zap.String("tx_id", tx.ID), zap.String("last_state", string(tx.State)))
			return tx
		case <-ticker.C:
			updated, err := e.circle.GetTransaction(pollCtx, tx.ID)
			if err != nil {
				e.logger.Warn("Blend: GetTransaction failed, retrying",
					zap.String("tx_id", tx.ID), zap.Error(err))
				continue
			}
			tx = updated
			e.logger.Debug("Blend: tx poll",
				zap.String("tx_id", tx.ID), zap.String("state", string(tx.State)),
				zap.String("tx_hash", tx.TxHash))
			switch tx.State {
			case circlepkg.TransactionStateComplete:
				e.logger.Info("Blend: tx reached COMPLETE",
					zap.String("tx_id", tx.ID), zap.String("tx_hash", tx.TxHash))
				return tx
			case circlepkg.TransactionStateFailed, circlepkg.TransactionStateCancelled, circlepkg.TransactionStateDenied:
				e.logger.Error("Blend: tx reached terminal error state",
					zap.String("tx_id", tx.ID), zap.String("state", string(tx.State)))
				return tx
			}
		}
	}
}

// multisendDigest is a stable digest of all steps, so retries of the same withdraw batch
// reuse the same Circle idempotency key instead of re-broadcasting. The call/delegatecall
// operation is part of the digest so a plan that differs only in operation type yields a
// different key.
func multisendDigest(steps []ActionStep) string {
	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		op := "call"
		if s.IsDelegateCall {
			op = "delegatecall"
		}
		parts = append(parts, op+":"+txDigest(s))
	}
	return strings.Join(parts, ";")
}

// rawDepositQuote mirrors @blend-money/core DepositApiQuoteResult (the raw
// /quote/deposit payload). Steps are ordered approve→deposit and execute as direct
// EOA transactions.
type rawDepositQuote struct {
	OriginChainID int64 `json:"originChainId"`
	Steps         []struct {
		Kind        string `json:"kind"`
		Description string `json:"description"`
		To          string `json:"to"`
		Data        string `json:"data"`
		Value       string `json:"value"`
		ChainID     int64  `json:"chainId"`
	} `json:"steps"`
}

// rawWithdrawCalldata mirrors @blend-money/core WithdrawApiCalldataResult (the raw
// /quote/withdraw payload). Steps act on the Safe and execute as a Safe MultiSend;
// liquidityReset must be a delegatecall from the Safe.
type rawWithdrawCalldata struct {
	SafeAddress        string `json:"safeAddress"`
	DestinationChainID int64  `json:"destinationChainId"`
	Payloads           []struct {
		ChainID int64 `json:"chainId"`
		Steps   []struct {
			Kind         string `json:"kind"`
			Description  string `json:"description"`
			To           string `json:"to"`
			Data         string `json:"data"`
			Value        string `json:"value"`        // present on the bridge step
			DelegateCall bool   `json:"delegateCall"` // present on liquidityReset
			ChainID      int64  `json:"chainId"`      // present on the bridge step
		} `json:"steps"`
	} `json:"payloads"`
}

// ParseActionPlan normalizes a Blend session payload into an executable ActionPlan,
// replicating @blend-money/core's depositQuoteToActionPlan / withdrawCalldataToActionPlans.
//
// Two real shapes:
//   - deposit  → { originChainId, steps: [{kind, to, data, value, chainId}] }  → DeployType "direct"
//   - withdraw → { safeAddress, payloads: [{chainId, steps: [{kind, to, data, ...}]}] } → DeployType "multisend"
//
// Plus legacy/generic fallbacks ({steps}, {actionPlan.steps}, {transactions}, top-level
// array) treated as "direct". Unrecognized payloads return the raw bytes as an error.
func ParseActionPlan(raw json.RawMessage) (*ActionPlan, error) {
	if len(raw) == 0 {
		return nil, errors.New("blend payload is empty")
	}

	// If the payload is already a normalized ActionPlan (carries an explicit deployType),
	// honor it first so a multisend plan can never be misrouted to direct execution.
	var typed struct {
		DeployType string       `json:"deployType"`
		Steps      []ActionStep `json:"steps"`
	}
	if err := json.Unmarshal(raw, &typed); err == nil && len(typed.Steps) > 0 {
		switch typed.DeployType {
		case deployMultisend:
			return &ActionPlan{DeployType: deployMultisend, Steps: typed.Steps}, nil
		case deployDirect:
			return &ActionPlan{DeployType: deployDirect, Steps: typed.Steps}, nil
		}
	}

	// Withdraw: distinguished by the `payloads` array of per-chain step lists.
	var wd rawWithdrawCalldata
	if err := json.Unmarshal(raw, &wd); err == nil && hasWithdrawSteps(&wd) {
		steps := make([]ActionStep, 0)
		for _, p := range wd.Payloads {
			chainID := p.ChainID
			for _, s := range p.Steps {
				stepChain := chainID
				if s.ChainID != 0 {
					stepChain = s.ChainID
				}
				steps = append(steps, ActionStep{
					ChainID:        stepChain,
					To:             s.To,
					Data:           s.Data,
					Value:          s.Value,
					IsDelegateCall: s.DelegateCall,
					Kind:           s.Kind,
					Description:    s.Description,
				})
			}
		}
		if len(steps) > 0 {
			return &ActionPlan{DeployType: deployMultisend, Steps: steps}, nil
		}
	}

	// Deposit: top-level `steps` (with kind approve/deposit). Direct EOA execution.
	var dep rawDepositQuote
	if err := json.Unmarshal(raw, &dep); err == nil && len(dep.Steps) > 0 {
		steps := make([]ActionStep, 0, len(dep.Steps))
		for _, s := range dep.Steps {
			steps = append(steps, ActionStep{
				ChainID:     s.ChainID,
				To:          s.To,
				Data:        s.Data,
				Value:       s.Value,
				Kind:        s.Kind,
				Description: s.Description,
			})
		}
		return &ActionPlan{DeployType: deployDirect, Steps: steps}, nil
	}

	// Legacy/generic fallbacks (treated as direct).
	var shape2 struct {
		ActionPlan struct {
			Steps []ActionStep `json:"steps"`
		} `json:"actionPlan"`
	}
	if err := json.Unmarshal(raw, &shape2); err == nil && len(shape2.ActionPlan.Steps) > 0 {
		return &ActionPlan{DeployType: deployDirect, Steps: shape2.ActionPlan.Steps}, nil
	}
	var shape3 struct {
		Transactions []ActionStep `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &shape3); err == nil && len(shape3.Transactions) > 0 {
		return &ActionPlan{DeployType: deployDirect, Steps: shape3.Transactions}, nil
	}
	var shape4 []ActionStep
	if err := json.Unmarshal(raw, &shape4); err == nil && len(shape4) > 0 {
		return &ActionPlan{DeployType: deployDirect, Steps: shape4}, nil
	}
	return nil, fmt.Errorf("blend payload shape not recognized: %s", truncate(string(raw), 512))
}

func hasWithdrawSteps(wd *rawWithdrawCalldata) bool {
	for _, p := range wd.Payloads {
		if len(p.Steps) > 0 {
			return true
		}
	}
	return false
}

func normalizeHex(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0x", nil
	}
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		s = "0x" + s
	}
	if _, err := hex.DecodeString(s[2:]); err != nil {
		return "", err
	}
	return "0x" + strings.ToLower(s[2:]), nil
}

// normalizeWeiValue accepts decimal, hex (0x...) or empty and returns a decimal wei string.
func normalizeWeiValue(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "0x0" || s == "0x" {
		return "0", nil
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, ok := new(big.Int).SetString(s[2:], 16)
		if !ok {
			return "", fmt.Errorf("invalid hex value %q", s)
		}
		return v.String(), nil
	}
	if _, ok := new(big.Int).SetString(s, 10); !ok {
		return "", fmt.Errorf("invalid decimal value %q", s)
	}
	return s, nil
}

func txDigest(step ActionStep) string {
	return fmt.Sprintf("%d-%s-%s-%s", step.ChainID, strings.ToLower(step.To), strings.ToLower(step.Data), step.Value)
}
