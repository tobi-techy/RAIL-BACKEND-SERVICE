// Package blend is a Go client for the Blend.money external server API
// (https://docs.blend.money/api-reference/svr). Blend coordinates non-custodial
// yield by deploying a per-user Gnosis Safe on EVM chains (Base by default) and
// routing USDC through DeFi vaults via installed Safe modules. The user's EOA
// is the Safe's sole owner; the platform never holds keys.
package blend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	defaultBaseURL  = "https://api.portal.blend.money"
	defaultTimeout  = 30 * time.Second
	defaultRetries  = 3
	headerAPIKey    = "X-API-Key"
	headerRequestID = "X-Request-ID"

	// BaseMainnetChainID is the EVM chain ID for Base.
	BaseMainnetChainID = 8453
	// BaseUSDCAddress is the canonical USDC contract on Base mainnet.
	BaseUSDCAddress = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"

	// IntentStatus values returned by Blend.
	IntentStatusOpen      = "OPEN"
	IntentStatusLocked    = "LOCKED"
	IntentStatusSubmitted = "SUBMITTED"
	IntentStatusSettled   = "SETTLED"
	IntentStatusFailed    = "FAILED"
	IntentStatusCancelled = "CANCELLED"

	// IntentType values.
	IntentTypeDeposit  = "DEPOSIT"
	IntentTypeWithdraw = "WITHDRAW"

	// SafeStatus values returned by /safe/resolve.
	SafeStatusValidated    = "validated"
	SafeStatusNotDeployed  = "not-deployed"
	SafeStatusInvalid      = "invalid"
	SafeStatusDisconnected = "disconnected"
)

// Config wires a Blend client.
type Config struct {
	BaseURL       string
	APIKey        string
	AccountTypeID string
	Timeout       time.Duration
	MaxRetries    int
}

// Client is the Blend server API client. Safe for concurrent use.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient creates a Blend client. APIKey and AccountTypeID are required.
// If BaseURL is empty the production URL is used.
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("blend: APIKey is required")
	}
	if strings.TrimSpace(cfg.AccountTypeID) == "" {
		return nil, errors.New("blend: AccountTypeID is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaultRetries
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		logger:     logger,
	}, nil
}

// envelope wraps every Blend response: {"status":"success","data":{...}} or
// {"status":"error","message":"..."}.
type envelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// APIError is returned for non-2xx responses. Callers can detect FLOWPLAN_CONFLICT
// (HTTP 409) and rate-limit retries by inspecting Status and Message.
type APIError struct {
	Status     int
	Message    string
	Body       string
	Method     string
	Path       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("blend %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
}

// IsRetryable returns true for 429 + 5xx + transient network errors.
func (e *APIError) IsRetryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// IsFlowplanConflict returns true when Blend reports an account is mid-rebalance.
// Per docs: "Do not retry blindly" — surface to user and let them retry later.
func (e *APIError) IsFlowplanConflict() bool {
	return e.Status == http.StatusConflict &&
		strings.Contains(strings.ToUpper(e.Message), "FLOWPLAN_CONFLICT")
}

// AccountTypeID exposes the configured account type slug.
func (c *Client) AccountTypeID() string {
	return c.cfg.AccountTypeID
}

// --- Account & Safe ---

// Account is returned by LookupOrCreateAccount and the Safe endpoints.
type Account struct {
	AccountID      string  `json:"accountId"`
	SafeAddress    string  `json:"safeAddress"`
	ChainsDeployed []int64 `json:"chainsDeployed"`
}

// SafeResolveResult describes the on-chain state of a user's Safe on a chain.
type SafeResolveResult struct {
	Status      string `json:"status"`
	AccountID   string `json:"accountId"`
	UserAddress string `json:"userAddress"`
	SafeAddress string `json:"safeAddress"`
	ChainID     int64  `json:"chainId"`
}

// LookupOrCreateAccount returns the Blend account for an EOA, creating it if needed.
// GET /extern/svr/{accountTypeId}/account?address=0x...
func (c *Client) LookupOrCreateAccount(ctx context.Context, eoaAddress string) (*Account, error) {
	if !isHexAddress(eoaAddress) {
		return nil, fmt.Errorf("blend: invalid EOA address %q", eoaAddress)
	}
	q := url.Values{}
	q.Set("address", eoaAddress)
	var out Account
	if err := c.do(ctx, http.MethodGet, c.path("/account"), q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResolveSafe checks on-chain state of the user's Safe for the given chain.
// GET /extern/svr/{accountTypeId}/account/{accountId}/safe/resolve?chainId=8453
func (c *Client) ResolveSafe(ctx context.Context, accountID string, chainID int64) (*SafeResolveResult, error) {
	if accountID == "" {
		return nil, errors.New("blend: accountID required")
	}
	q := url.Values{}
	q.Set("chainId", fmt.Sprintf("%d", chainID))
	var out SafeResolveResult
	if err := c.do(ctx, http.MethodGet, c.accountPath(accountID, "/safe/resolve"), q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RequestSafe asks Blend to deploy the Safe on a chain. Deployment is asynchronous;
// callers must poll ResolveSafe until status == validated.
// POST /extern/svr/{accountTypeId}/account/{accountId}/safe/request
func (c *Client) RequestSafe(ctx context.Context, accountID string, chainID int64) error {
	if accountID == "" {
		return errors.New("blend: accountID required")
	}
	body := map[string]any{"chainId": chainID}
	var out struct {
		Message string `json:"message"`
	}
	return c.do(ctx, http.MethodPost, c.accountPath(accountID, "/safe/request"), nil, body, &out)
}

// --- Intent Sessions ---

// Session represents a Blend deposit/withdraw intent.
// Most fields are passed through opaquely; the action plan lives inside Payload.
type Session struct {
	IntentID      string          `json:"intentId"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	ExpiresAt     time.Time       `json:"expiresAt"`
	CreatedAt     time.Time       `json:"createdAt"`
	ExternalRef   string          `json:"externalRef"`
	RequestParams json.RawMessage `json:"requestParams"`
	QuoteSummary  json.RawMessage `json:"quoteSummary"`
	LockedAt      *time.Time      `json:"lockedAt"`
	LockedBy      string          `json:"lockedBy"`
	TxHashes      []string        `json:"txHashes"`
	ChainIDs      []int64         `json:"chainIds"`
	SettledAt     *time.Time      `json:"settledAt"`
	ErrorMessage  string          `json:"errorMessage"`
	Payload       json.RawMessage `json:"payload"`
}

// GetOrCreateSession opens (or reuses) the user's single active session.
// externalRef is the partner's idempotency token; we use the deposit/withdrawal UUID.
// POST /extern/svr/{accountTypeId}/account/{accountId}/intent/session
func (c *Client) GetOrCreateSession(ctx context.Context, accountID, externalRef string, forceReset bool) (*Session, error) {
	if accountID == "" {
		return nil, errors.New("blend: accountID required")
	}
	body := map[string]any{"forceReset": forceReset}
	if externalRef != "" {
		body["externalRef"] = externalRef
	}
	var out Session
	if err := c.do(ctx, http.MethodPost, c.accountPath(accountID, "/intent/session"), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QuoteDeposit configures a session as a deposit and returns the action plan in Payload.
// Amount must be the integer count of base units (e.g. micro-USDC for USDC).
// POST /extern/svr/{accountTypeId}/account/{accountId}/intent/{intentId}/quote/deposit
func (c *Client) QuoteDeposit(ctx context.Context, accountID, intentID string, chainID int64, inputAsset, eoa string, amountUnits *big.Int) (*Session, error) {
	if accountID == "" || intentID == "" {
		return nil, errors.New("blend: accountID and intentID required")
	}
	if amountUnits == nil || amountUnits.Sign() <= 0 {
		return nil, errors.New("blend: amount must be positive")
	}
	body := map[string]any{
		"chainId":           chainID,
		"inputAssetAddress": inputAsset,
		"eoa":               eoa,
		"amount":            amountUnits.String(),
	}
	var out Session
	path := c.accountPath(accountID, fmt.Sprintf("/intent/%s/quote/deposit", intentID))
	if err := c.do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QuoteWithdraw configures a session as a withdrawal and returns calldata in Payload.
// POST /extern/svr/{accountTypeId}/account/{accountId}/intent/{intentId}/quote/withdraw
func (c *Client) QuoteWithdraw(ctx context.Context, accountID, intentID string, destinationChainID int64, amountUnits *big.Int, isMaxWithdraw bool) (*Session, error) {
	if accountID == "" || intentID == "" {
		return nil, errors.New("blend: accountID and intentID required")
	}
	if amountUnits == nil || amountUnits.Sign() <= 0 {
		return nil, errors.New("blend: amount must be positive")
	}
	body := map[string]any{
		"destinationChainId": destinationChainID,
		"amount":             amountUnits.String(),
		"isMaxWithdraw":      isMaxWithdraw,
	}
	var out Session
	path := c.accountPath(accountID, fmt.Sprintf("/intent/%s/quote/withdraw", intentID))
	if err := c.do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LockSession acquires the signer lock on an OPEN session, transitioning it to LOCKED.
// POST /extern/svr/{accountTypeId}/account/{accountId}/intent/{intentId}/lock
func (c *Client) LockSession(ctx context.Context, accountID, intentID, signerAddress string) (*Session, error) {
	body := map[string]any{"signerAddress": signerAddress}
	var out Session
	path := c.accountPath(accountID, fmt.Sprintf("/intent/%s/lock", intentID))
	if err := c.do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TxHashRef is the {hash, chainId} pair Blend expects on /submit.
type TxHashRef struct {
	Hash    string `json:"hash"`
	ChainID int64  `json:"chainId"`
}

// SubmitTxHashes hands Blend the on-chain tx hashes so it can verify receipts.
// POST /extern/svr/{accountTypeId}/account/{accountId}/intent/{intentId}/submit
func (c *Client) SubmitTxHashes(ctx context.Context, accountID, intentID string, hashes []TxHashRef) (*Session, error) {
	if len(hashes) == 0 {
		return nil, errors.New("blend: at least one tx hash required")
	}
	body := map[string]any{"txHashes": hashes}
	var out Session
	path := c.accountPath(accountID, fmt.Sprintf("/intent/%s/submit", intentID))
	if err := c.do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelSession cancels a session from any non-terminal state.
// POST /extern/svr/{accountTypeId}/account/{accountId}/intent/{intentId}/cancel
func (c *Client) CancelSession(ctx context.Context, accountID, intentID string) (*Session, error) {
	var out Session
	path := c.accountPath(accountID, fmt.Sprintf("/intent/%s/cancel", intentID))
	if err := c.do(ctx, http.MethodPost, path, nil, map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSession returns the current state of an intent session.
// GET /extern/svr/{accountTypeId}/account/{accountId}/intent/{intentId}
func (c *Client) GetSession(ctx context.Context, accountID, intentID string) (*Session, error) {
	var out Session
	path := c.accountPath(accountID, fmt.Sprintf("/intent/%s", intentID))
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Balances, Returns, Yield, Positions ---

// Balance is the aggregated Safe balance across all chains, with per-chain breakdown.
type Balance struct {
	AccountID   string            `json:"accountId"`
	SafeAddress string            `json:"safeAddress"`
	PerChain    []PerChainBalance `json:"perChain"`
	// Total changed from map[string]float64 to raw JSON because Blend's API
	// now returns nested objects like {"USD": {"value": "..."}} instead of
	// flat {"USD": 123.45}. We never read this field — aggregateUnderlying
	// uses TotalUnderlying instead.
	Total      json.RawMessage `json:"total"`
	HeldAssets []HeldAsset     `json:"heldAssets"`
}

type PerChainBalance struct {
	ChainID                 int64           `json:"chainId"`
	VaultAddress            string          `json:"vaultAddress"`
	Total                   json.RawMessage `json:"total"`
	TotalUnderlying         string          `json:"totalUnderlying"`
	TotalUnderlyingDecimals int             `json:"totalUnderlyingDecimals"`
	HeldAssets              []HeldAsset     `json:"heldAssets"`
}

type HeldAsset struct {
	Address string `json:"address"`
	Symbol  string `json:"symbol"`
	ChainID int64  `json:"chainId"`
}

// GetBalance fetches the user's Blend balance across all chains.
// GET /extern/svr/{accountTypeId}/account/{accountId}/balance
func (c *Client) GetBalance(ctx context.Context, accountID string) (*Balance, error) {
	var out Balance
	if err := c.do(ctx, http.MethodGet, c.accountPath(accountID, "/balance"), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BalanceSnapshot is one row from GET /balance/history.
type BalanceSnapshot struct {
	ID                        string             `json:"id"`
	CreatedAt                 time.Time          `json:"createdAt"`
	TotalValue                map[string]float64 `json:"totalValue"`
	TotalUnderlyingValue      decimal.Decimal    `json:"totalUnderlyingValue"`
	TotalYield                map[string]float64 `json:"totalYield"`
	TotalYieldUnderlying      decimal.Decimal    `json:"totalYieldUnderlying"`
	PrimaryUnderlyingSymbol   string             `json:"primaryUnderlyingSymbol"`
	PrimaryUnderlyingDecimals int                `json:"primaryUnderlyingDecimals"`
	PrimaryUnderlyingAddress  string             `json:"primaryUnderlyingAddress"`
	Breakdown                 json.RawMessage    `json:"breakdown"`
}

// GetBalanceHistory returns historical balance snapshots.
// GET /extern/svr/{accountTypeId}/account/{accountId}/balance/history
func (c *Client) GetBalanceHistory(ctx context.Context, accountID string) ([]BalanceSnapshot, error) {
	var out []BalanceSnapshot
	if err := c.do(ctx, http.MethodGet, c.accountPath(accountID, "/balance/history"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Returns expresses lifetime returns relative to net deposits.
type Returns struct {
	AccountID      string             `json:"accountId"`
	Current        map[string]float64 `json:"current"`
	TotalDeposited map[string]float64 `json:"totalDeposited"`
	TotalWithdrawn map[string]float64 `json:"totalWithdrawn"`
	NetDeposited   map[string]float64 `json:"netDeposited"`
	ReturnsAmount  map[string]float64 `json:"returns"`
	ReturnsPct     float64            `json:"returnsPct"`
}

// GetReturns returns the user's net P&L from Blend.
// GET /extern/svr/{accountTypeId}/account/{accountId}/returns
func (c *Client) GetReturns(ctx context.Context, accountID string) (*Returns, error) {
	var out Returns
	if err := c.do(ctx, http.MethodGet, c.accountPath(accountID, "/returns"), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PositionEvent is one entry from /positions: deposit, withdrawal, or rebalance.
type PositionEvent struct {
	Kind            string             `json:"kind"`
	ChainID         int64              `json:"chainId"`
	TransactionHash string             `json:"transactionHash"`
	BlockTime       time.Time          `json:"blockTime"`
	TokenSymbol     string             `json:"tokenSymbol,omitempty"`
	TokenDecimals   int                `json:"tokenDecimals,omitempty"`
	TokenAddress    string             `json:"tokenAddress,omitempty"`
	Amount          string             `json:"amount,omitempty"`
	Price           map[string]float64 `json:"price,omitempty"`
}

type PositionEventList struct {
	AccountID   string          `json:"accountId"`
	SafeAddress string          `json:"safeAddress"`
	Events      []PositionEvent `json:"events"`
}

// ListPositions returns the user's deposit/withdrawal/rebalance event history.
// GET /extern/svr/{accountTypeId}/account/{accountId}/positions
func (c *Client) ListPositions(ctx context.Context, accountID string) (*PositionEventList, error) {
	var out PositionEventList
	if err := c.do(ctx, http.MethodGet, c.accountPath(accountID, "/positions"), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// YieldBreakdown is the account-type-wide yield breakdown across vaults.
type YieldBreakdown struct {
	AccountTypeID  string       `json:"accountTypeId"`
	YieldBreakdown []VaultYield `json:"yieldBreakdown"`
}

type VaultYield struct {
	ChainID      int64              `json:"chainId"`
	VaultAddress string             `json:"vaultAddress"`
	Breakdown    YieldBreakdownData `json:"breakdown"`
	Summary      YieldSummary       `json:"summary"`
	HeldAssets   []HeldAsset        `json:"heldAssets"`
}

type YieldBreakdownData struct {
	Base      float64   `json:"base"`
	Positions []float64 `json:"positions"`
}

type YieldSummary struct {
	TheoreticalOverall float64 `json:"theoreticalOverall"`
	TheoreticalBoosted float64 `json:"theoreticalBoosted"`
	PctInVault         float64 `json:"pctInVault"`
	PctDeployed        float64 `json:"pctDeployed"`
}

// GetYieldBreakdown returns expected APY/yield breakdown for the account type.
// GET /extern/svr/{accountTypeId}/yield
func (c *Client) GetYieldBreakdown(ctx context.Context) (*YieldBreakdown, error) {
	var out YieldBreakdown
	path := fmt.Sprintf("/extern/svr/%s/yield", url.PathEscape(c.cfg.AccountTypeID))
	if err := c.doFullPath(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Discovery ---

type DepositChain struct {
	ChainID     int64  `json:"chainId"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	IconURL     string `json:"iconUrl"`
}

// ListDepositChains returns chains Blend accepts deposits on.
func (c *Client) ListDepositChains(ctx context.Context) ([]DepositChain, error) {
	var out []DepositChain
	path := fmt.Sprintf("/extern/svr/%s/intent/discovery/deposit/chains", url.PathEscape(c.cfg.AccountTypeID))
	if err := c.doFullPath(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type DepositToken struct {
	ChainID  int64              `json:"chainId"`
	Address  string             `json:"address"`
	Symbol   string             `json:"symbol"`
	Name     string             `json:"name"`
	Decimals int                `json:"decimals"`
	LogoURI  string             `json:"logoURI"`
	Price    map[string]float64 `json:"price"`
	Balance  string             `json:"balance,omitempty"`
	Amount   map[string]float64 `json:"amount,omitempty"`
}

// ListDepositTokens returns priced deposit tokens for a chain (optionally for an EOA).
func (c *Client) ListDepositTokens(ctx context.Context, chainID int64, eoa string) ([]DepositToken, error) {
	q := url.Values{}
	q.Set("chainId", fmt.Sprintf("%d", chainID))
	if eoa != "" {
		q.Set("eoa", eoa)
	}
	var out []DepositToken
	path := fmt.Sprintf("/extern/svr/%s/intent/discovery/deposit/tokens", url.PathEscape(c.cfg.AccountTypeID))
	if err := c.doFullPath(ctx, http.MethodGet, path, q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type WithdrawDestination struct {
	ChainID          int64  `json:"chainId"`
	Name             string `json:"name"`
	LoanTokenAddress string `json:"loanTokenAddress"`
}

// ListWithdrawDestinations returns chains the user can withdraw to.
func (c *Client) ListWithdrawDestinations(ctx context.Context) ([]WithdrawDestination, error) {
	var out []WithdrawDestination
	path := fmt.Sprintf("/extern/svr/%s/intent/discovery/withdraw/destinations", url.PathEscape(c.cfg.AccountTypeID))
	if err := c.doFullPath(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// --- HTTP plumbing ---

func (c *Client) path(suffix string) string {
	return fmt.Sprintf("/extern/svr/%s%s", url.PathEscape(c.cfg.AccountTypeID), suffix)
}

func (c *Client) accountPath(accountID, suffix string) string {
	return fmt.Sprintf("/extern/svr/%s/account/%s%s",
		url.PathEscape(c.cfg.AccountTypeID),
		url.PathEscape(accountID),
		suffix,
	)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	return c.doFullPath(ctx, method, path, query, body, out)
}

// doFullPath executes a request with retry on 429/5xx/transient network errors.
// The Blend envelope ({status,data}) is unwrapped automatically; out is populated
// with the inner data only.
func (c *Client) doFullPath(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	fullURL := c.cfg.BaseURL + path
	if len(query) > 0 {
		fullURL = fullURL + "?" + query.Encode()
	}

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("blend marshal body: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		var reader io.Reader
		if bodyBytes != nil {
			reader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
		if err != nil {
			return fmt.Errorf("blend new request: %w", err)
		}
		req.Header.Set(headerAPIKey, c.cfg.APIKey)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("blend http: %w", err)
			if !isTransientNetError(err) {
				return lastErr
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("blend read body: %w", readErr)
			continue
		}

		if resp.StatusCode >= 400 {
			apiErr := parseAPIError(method, path, resp, respBody)
			c.logger.Warn("Blend API error",
				zap.String("method", method),
				zap.String("path", path),
				zap.Int("status", resp.StatusCode),
				zap.String("body", truncate(string(respBody), 512)),
			)
			if apiErr.IsRetryable() && attempt < c.cfg.MaxRetries {
				lastErr = apiErr
				continue
			}
			return apiErr
		}

		if out == nil {
			return nil
		}
		var env envelope
		if err := json.Unmarshal(respBody, &env); err != nil {
			return fmt.Errorf("blend unmarshal envelope: %w (body=%s)", err, truncate(string(respBody), 256))
		}
		if env.Status != "" && env.Status != "success" {
			return &APIError{Status: resp.StatusCode, Message: env.Message, Body: string(respBody), Method: method, Path: path}
		}
		data := env.Data
		if len(data) == 0 {
			// Some endpoints may return data inline rather than under "data"; fall back.
			data = respBody
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("blend unmarshal data: %w (body=%s)", err, truncate(string(respBody), 256))
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("blend: exhausted retries")
	}
	return lastErr
}

func parseAPIError(method, path string, resp *http.Response, body []byte) *APIError {
	apiErr := &APIError{
		Status: resp.StatusCode,
		Body:   string(body),
		Method: method,
		Path:   path,
	}
	var env envelope
	if json.Unmarshal(body, &env) == nil && env.Message != "" {
		apiErr.Message = env.Message
	} else {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if d, err := time.ParseDuration(retryAfter + "s"); err == nil {
			apiErr.RetryAfter = d
		}
	}
	return apiErr
}

func backoffDelay(attempt int) time.Duration {
	base := time.Duration(1<<attempt) * 500 * time.Millisecond
	if base > 8*time.Second {
		base = 8 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(base) / 4))
	return base + jitter
}

func isTransientNetError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func isHexAddress(addr string) bool {
	if len(addr) != 42 || !strings.HasPrefix(addr, "0x") {
		return false
	}
	for _, c := range addr[2:] {
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !ok {
			return false
		}
	}
	return true
}

// USDCMicroUnits converts a USDC decimal amount to integer micro-USDC units (6 decimals).
func USDCMicroUnits(amount decimal.Decimal) *big.Int {
	micro := amount.Truncate(6).Mul(decimal.NewFromInt(1_000_000))
	return micro.BigInt()
}
