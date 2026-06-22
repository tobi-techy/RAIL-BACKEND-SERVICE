package blend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// SafeVerifier confirms that an address we are about to dynamically trust is genuinely
// a Gnosis Safe contract owned by the expected EOA. This is the security gate behind
// the executor's dynamic allowlist: the per-user Safe address comes from Blend's
// authenticated API and our DB, and this re-verifies it on-chain so a poisoned API
// response or tampered DB row can never turn the dynamic allow into an arbitrary-contract
// signing bypass.
type SafeVerifier interface {
	VerifySafe(ctx context.Context, chainID int64, safeAddr, ownerEOA string) error
}

// isOwnerSelector is keccak256("isOwner(address)")[:4]. Verified via golang.org/x/crypto/sha3.
const isOwnerSelector = "2f54bf6e"

// EVMSafeVerifier verifies Safes over a Base JSON-RPC endpoint using eth_getCode
// (contract bytecode presence) and eth_call to isOwner(address) (ownership).
// Successful (safe, owner) pairs are cached with a 1-hour TTL.
type EVMSafeVerifier struct {
	rpcURL     string
	httpClient *http.Client
	logger     *zap.Logger
	mu         sync.Mutex
	verified   map[string]time.Time // "safe|owner" -> verification time
}

// NewEVMSafeVerifier builds a verifier against the given Base RPC URL.
func NewEVMSafeVerifier(rpcURL string, logger *zap.Logger) *EVMSafeVerifier {
	return &EVMSafeVerifier{
		rpcURL:     strings.TrimSpace(rpcURL),
		httpClient: &http.Client{Timeout: 15 * time.Second},
		logger:     logger,
		verified:   make(map[string]time.Time),
	}
}

// VerifySafe runs verifySafeContract then verifySafeOwnership. Results are cached on success.
func (v *EVMSafeVerifier) VerifySafe(ctx context.Context, chainID int64, safeAddr, ownerEOA string) error {
	if v == nil || v.rpcURL == "" {
		return fmt.Errorf("blend: safe verifier not configured")
	}
	if !isHexAddress(safeAddr) {
		return fmt.Errorf("blend: invalid safe address %q", safeAddr)
	}
	if !isHexAddress(ownerEOA) {
		return fmt.Errorf("blend: invalid owner address %q", ownerEOA)
	}
	key := strings.ToLower(safeAddr) + "|" + strings.ToLower(ownerEOA)
	v.mu.Lock()
	if t, ok := v.verified[key]; ok && time.Since(t) < time.Hour {
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()
	if err := v.verifySafeContract(ctx, safeAddr); err != nil {
		return err
	}
	if err := v.verifySafeOwnership(ctx, safeAddr, ownerEOA); err != nil {
		return err
	}
	v.mu.Lock()
	v.verified[key] = time.Now()
	v.mu.Unlock()
	return nil
}

// verifySafeContract confirms the address holds contract bytecode (not an EOA or an
// undeployed address). This is the primary guard: it prevents trusting a plain account.
func (v *EVMSafeVerifier) verifySafeContract(ctx context.Context, safeAddr string) error {
	code, err := v.ethGetCode(ctx, safeAddr)
	if err != nil {
		return fmt.Errorf("blend: eth_getCode for safe %s: %w", safeAddr, err)
	}
	stripped := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(code)), "0x")
	if stripped == "" || strings.Trim(stripped, "0") == "" {
		return fmt.Errorf("blend: address %s has no contract bytecode (not a Safe)", safeAddr)
	}
	return nil
}

// verifySafeOwnership confirms ownerEOA is an owner of the Safe via isOwner(address).
func (v *EVMSafeVerifier) verifySafeOwnership(ctx context.Context, safeAddr, ownerEOA string) error {
	padded, err := leftPad32Hex(ownerEOA)
	if err != nil {
		return err
	}
	data := "0x" + isOwnerSelector + padded
	result, err := v.ethCall(ctx, safeAddr, data)
	if err != nil {
		return fmt.Errorf("blend: isOwner eth_call for safe %s: %w", safeAddr, err)
	}
	stripped := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(result)), "0x")
	if stripped == "" {
		return fmt.Errorf("blend: isOwner returned empty result for safe %s", safeAddr)
	}
	val, ok := new(big.Int).SetString(stripped, 16)
	if !ok {
		return fmt.Errorf("blend: could not parse isOwner result %q", result)
	}
	if val.Sign() == 0 {
		return fmt.Errorf("blend: EOA %s is NOT an owner of Safe %s", ownerEOA, safeAddr)
	}
	return nil
}

// --- minimal JSON-RPC ---

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result string `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (v *EVMSafeVerifier) ethGetCode(ctx context.Context, addr string) (string, error) {
	return v.call(ctx, "eth_getCode", []any{addr, "latest"})
}

func (v *EVMSafeVerifier) ethCall(ctx context.Context, to, data string) (string, error) {
	return v.call(ctx, "eth_call", []any{map[string]string{"to": to, "data": data}, "latest"})
}

func (v *EVMSafeVerifier) call(ctx context.Context, method string, params []any) (string, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.rpcURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if len(raw) >= 1<<20 {
		return "", fmt.Errorf("rpc %s response too large (>1MB), possible attack or malformed response", method)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("rpc %s HTTP %d: %s", method, resp.StatusCode, truncate(string(raw), 256))
	}
	var out rpcResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("rpc %s decode: %w", method, err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("rpc %s error %d: %s", method, out.Error.Code, out.Error.Message)
	}
	return out.Result, nil
}

// leftPad32Hex left-pads a 20-byte hex address to a 32-byte (64 hex char) ABI word.
func leftPad32Hex(addr string) (string, error) {
	a := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(addr)), "0x")
	if len(a) != 40 {
		return "", fmt.Errorf("blend: address %q is not 20 bytes", addr)
	}
	return strings.Repeat("0", 24) + a, nil
}
