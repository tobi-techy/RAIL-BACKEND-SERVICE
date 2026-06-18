package blend

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"go.uber.org/zap"
)

// ActionStep is one on-chain transaction in a Blend action plan.
// We accept the shape from /quote/deposit and /quote/withdraw payloads.
type ActionStep struct {
	ChainID        int64  `json:"chainId"`
	To             string `json:"to"`
	Data           string `json:"data"`
	Value          string `json:"value"`
	IsDelegateCall bool   `json:"isDelegateCall,omitempty"`
	Description    string `json:"description,omitempty"`
}

// ActionPlan is the on-chain plan a session expects the user's signer to execute.
// Different Blend endpoints embed it under slightly different keys; ParseActionPlan
// normalizes them.
type ActionPlan struct {
	Steps []ActionStep `json:"steps"`
}

// ContractExecutor signs and broadcasts EVM contract calls from a Circle wallet.
type ContractExecutor interface {
	ExecuteContract(ctx context.Context, req *circlepkg.CreateContractExecutionRequest) (*circlepkg.Transaction, error)
	GetTransaction(ctx context.Context, txID string) (*circlepkg.Transaction, error)
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

// PlanExecutor walks a Blend action plan and executes each step through Circle.
type PlanExecutor struct {
	circle    ContractExecutor
	allowlist *Allowlist
	logger    *zap.Logger
}

// NewPlanExecutor wires a plan executor. allowlist is REQUIRED in production; nil/empty
// allowlist is permitted only for sandbox bring-up and emits warnings.
func NewPlanExecutor(circle ContractExecutor, allowlist *Allowlist, logger *zap.Logger) *PlanExecutor {
	return &PlanExecutor{circle: circle, allowlist: allowlist, logger: logger}
}

// ExecutedTx is the result of a single executed step.
type ExecutedTx struct {
	ChainID       int64
	TxHash        string
	TransactionID string
}

// Execute runs each step of a plan via Circle, returning the resulting tx hashes
// in submission order. The caller is responsible for handing these to /intent/submit.
//
// Idempotency: idempotencyPrefix is combined with the step index to produce a stable
// Circle idempotency key per step. Re-runs with the same prefix are safe.
func (e *PlanExecutor) Execute(ctx context.Context, walletID string, plan *ActionPlan, idempotencyPrefix string) ([]ExecutedTx, error) {
	if e == nil || e.circle == nil {
		return nil, errors.New("blend executor not configured")
	}
	if plan == nil || len(plan.Steps) == 0 {
		return nil, errors.New("blend plan: no steps to execute")
	}
	if strings.TrimSpace(walletID) == "" {
		return nil, errors.New("blend plan: circle wallet id required")
	}

	if e.allowlist.Empty() {
		e.logger.Warn("Blend executor running without contract allowlist — DEV ONLY")
	}

	results := make([]ExecutedTx, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		if !isHexAddress(step.To) {
			return results, fmt.Errorf("blend plan step %d: invalid `to` address %q", i, step.To)
		}
		if !e.allowlist.Allows(step.To) {
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

		idemKey := fmt.Sprintf("%s:%d:%s", idempotencyPrefix, i, txDigest(step))
		req := &circlepkg.CreateContractExecutionRequest{
			IdempotencyKey:  idemKey,
			WalletID:        walletID,
			ContractAddress: step.To,
			CallData:        callData,
			Amount:          value, // wei value sent with the call
			FeeLevel:        "MEDIUM",
		}
		e.logger.Info("Blend executor submitting step",
			zap.Int("step", i),
			zap.Int64("chain_id", step.ChainID),
			zap.String("to", step.To),
			zap.String("desc", step.Description),
			zap.Bool("has_calldata", callData != ""),
			zap.String("value_wei", value),
		)
		tx, err := e.circle.ExecuteContract(ctx, req)
		if err != nil {
			return results, fmt.Errorf("blend plan step %d: circle execute: %w", i, err)
		}
		results = append(results, ExecutedTx{
			ChainID:       step.ChainID,
			TxHash:        tx.TxHash,
			TransactionID: tx.ID,
		})
	}
	return results, nil
}

// ParseActionPlan extracts an action plan from a Blend session payload. Blend's
// SDK exposes `depositQuoteToActionPlan(payload, eoa)` — we mirror that logic
// here by accepting a few known payload shapes:
//   - { "steps": [...] }
//   - { "actionPlan": { "steps": [...] } }
//   - { "transactions": [{to, data, value, chainId}, ...] }
//   - top-level [{to, data, value, chainId}, ...]
//
// Returns the normalized plan. If no shape matches, returns the raw payload as
// an error so callers can capture it and we can extend the parser.
func ParseActionPlan(raw json.RawMessage) (*ActionPlan, error) {
	if len(raw) == 0 {
		return nil, errors.New("blend payload is empty")
	}
	// Shape 1: { steps: [...] }
	var shape1 struct {
		Steps []ActionStep `json:"steps"`
	}
	if err := json.Unmarshal(raw, &shape1); err == nil && len(shape1.Steps) > 0 {
		return &ActionPlan{Steps: shape1.Steps}, nil
	}
	// Shape 2: { actionPlan: { steps: [...] } }
	var shape2 struct {
		ActionPlan struct {
			Steps []ActionStep `json:"steps"`
		} `json:"actionPlan"`
	}
	if err := json.Unmarshal(raw, &shape2); err == nil && len(shape2.ActionPlan.Steps) > 0 {
		return &ActionPlan{Steps: shape2.ActionPlan.Steps}, nil
	}
	// Shape 3: { transactions: [...] }
	var shape3 struct {
		Transactions []ActionStep `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &shape3); err == nil && len(shape3.Transactions) > 0 {
		return &ActionPlan{Steps: shape3.Transactions}, nil
	}
	// Shape 4: top-level array
	var shape4 []ActionStep
	if err := json.Unmarshal(raw, &shape4); err == nil && len(shape4) > 0 {
		return &ActionPlan{Steps: shape4}, nil
	}
	return nil, fmt.Errorf("blend payload shape not recognized: %s", truncate(string(raw), 256))
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
