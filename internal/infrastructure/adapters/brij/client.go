package brij

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/compute-budget"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
	"go.uber.org/zap"
)

// Defaults for the HTTP + x402 payment client.
const (
	defaultBaseURL = "https://travel.brij.fi"
	defaultRPC     = rpc.MainNetBeta_RPC
	defaultTimeout = 30 * time.Second
	defaultRetries = 2
	maxPayAttempts = 3 // times we rebuild a signature against a fresh 402
	computeUnitCap = 400_000
	priorityFee    = 0 // no priority fee; keeps us under any sponsor cap
)

// Config configures the BRIJ client. FundingPrivateKey is the base58-encoded
// ed25519 keypair of Rail's Solana funding wallet — it funds x402 micropayments
// and is the funding_wallet of every booking intent. Sponsored fee paying means
// the wallet only needs a USDC balance, not SOL (unless the fee payer is us).
type Config struct {
	BaseURL             string
	SolanaRPC           string
	FundingPrivateKey   string
	HTTPTimeout         time.Duration
	MaxRetries          int
	MaxPaymentBaseUnits int64 // per-request x402 payment ceiling in base units; 0 = unlimited
}

// Client is the BRIJ Travel flight API client. All mutating calls are paid with
// x402 (exact USDC on Solana mainnet); the payment is transparent to callers.
type Client struct {
	baseURL             string
	http                *http.Client
	rpc                 *rpc.Client
	keypair             solana.PrivateKey
	pubkey              solana.PublicKey
	logger              *zap.Logger
	retries             int
	maxPaymentBaseUnits int64
}

// NewClient builds a BRIJ client. The funding keypair is required and validated
// here so misconfiguration fails fast at startup.
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("brij: invalid base_url: %w", err)
	}
	priv, err := solana.PrivateKeyFromBase58(strings.TrimSpace(cfg.FundingPrivateKey))
	if err != nil {
		return nil, fmt.Errorf("brij: funding_private_key is not a valid base58 Solana keypair: %w", err)
	}
	rpcURL := cfg.SolanaRPC
	if rpcURL == "" {
		rpcURL = defaultRPC
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	retries := cfg.MaxRetries
	if retries < 0 {
		retries = defaultRetries
	}
	return &Client{
		baseURL:             baseURL,
		http:                &http.Client{Timeout: timeout},
		rpc:                 rpc.New(rpcURL),
		keypair:             priv,
		pubkey:              priv.PublicKey(),
		logger:              logger,
		retries:             retries,
		maxPaymentBaseUnits: cfg.MaxPaymentBaseUnits,
	}, nil
}

// FundingWallet returns the base58 address that funds payments and intents.
func (c *Client) FundingWallet() string { return c.pubkey.String() }

// Health checks BRIJ process liveness.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("brij health: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("brij health: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, readErr := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		if readErr != nil {
			return fmt.Errorf("brij health: read body: %w (status %d)", readErr, resp.StatusCode)
		}
		return apiErrorFrom(resp.StatusCode, bodyBytes)
	}
	if readErr != nil {
		return fmt.Errorf("brij health: read body: %w", readErr)
	}
	return nil
}

// Search queries live flight offers (x402-paid, price is load-scaled).
func (c *Client) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	var out SearchResponse
	if err := c.doX402(ctx, http.MethodPost, "/air/search", req, &out); err != nil {
		return nil, err
	}
	return &out.Search, nil
}

// CreateIntent locks an offer and derives its escrow (x402-paid, fixed 0.10
// USDC). The response carries intent_id and customer_support_code once — both
// must be persisted by the caller.
func (c *Client) CreateIntent(ctx context.Context, req CreateIntentRequest) (*BookingIntent, error) {
	if req.FundingWallet == "" {
		req.FundingWallet = c.FundingWallet()
	}
	var out IntentResponse
	if err := c.doX402(ctx, http.MethodPost, "/air/intents", req, &out); err != nil {
		return nil, err
	}
	return &out.Intent, nil
}

// Book pays the intent's escrow and submits exactly one passenger (x402-paid,
// amount = the intent's expected escrow amount). Booking is async: poll
// GetIntent until the intent reaches booked or refunded.
func (c *Client) Book(ctx context.Context, req BookRequest) (*RequestBookingResponse, error) {
	var out RequestBookingResponse
	if err := c.doX402(ctx, http.MethodPost, "/air/book", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RequestRefund files a manual refund request (x402-paid, 0.10 USDC). Requires
// the customer support code captured at intent creation and the passenger
// family name as submitted at /book.
func (c *Client) RequestRefund(ctx context.Context, req RefundRequest, supportCode, familyName string) (*RefundResponse, error) {
	headers := map[string]string{
		"X-Customer-Support-Code": supportCode,
		"X-Passenger-Family-Name": familyName,
	}
	var out RefundResponse
	if err := c.doX402(ctx, http.MethodPost, "/air/refund-requests", req, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetIntent fetches a booking intent by id. Not an x402-paid endpoint.
func (c *Client) GetIntent(ctx context.Context, intentID string) (*BookingIntent, error) {
	var out IntentResponse
	if err := c.do(ctx, http.MethodGet, "/air/intents/"+url.PathEscape(intentID), nil, &out, nil); err != nil {
		return nil, err
	}
	return &out.Intent, nil
}

// GetOrder fetches the airline order status (PNR). Requires the support code.
func (c *Client) GetOrder(ctx context.Context, orderID, supportCode string) (*OrderStatus, error) {
	headers := map[string]string{"X-Customer-Support-Code": supportCode}
	var out OrderResponse
	if err := c.do(ctx, http.MethodGet, "/air/orders/"+url.PathEscape(orderID), nil, &out, headers); err != nil {
		return nil, err
	}
	return &out.Order, nil
}

// --- x402 exact-SVM payment ---

// paymentRequirement mirrors the base64 PAYMENT-REQUIRED header payload.
type paymentRequirement struct {
	X402Version int               `json:"x402Version"`
	Error       string            `json:"error,omitempty"`
	Resource    string            `json:"resource,omitempty"`
	Accepts     []acceptedPayment `json:"accepts"`
	Extensions  map[string]any    `json:"extensions,omitempty"`
}

type acceptedPayment struct {
	Scheme            string         `json:"scheme"`
	Network           string         `json:"network"`
	Asset             string         `json:"asset"`
	Amount            int64          `json:"amount"`
	PayTo             string         `json:"payTo"`
	MaxTimeoutSeconds int64          `json:"maxTimeoutSeconds"`
	Extra             map[string]any `json:"extra,omitempty"`
}

// doX402 performs an x402-paid round trip: it sends the request, and on 402 it
// builds the exact-USDC Solana transaction, attaches it via PAYMENT-SIGNATURE,
// and resends. A fresh challenge is re-read after every 402 (the price is
// dynamic and may change between attempts).
func (c *Client) doX402(ctx context.Context, method, path string, body any, out any, extraHeaders ...map[string]string) error {
	var headers map[string]string
	if len(extraHeaders) > 0 {
		headers = extraHeaders[0]
	}
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		resp, err := c.send(ctx, method, path, body, "", headers)
		if err != nil {
			lastErr = err
			c.backoff(attempt)
			continue
		}
		if resp.StatusCode != http.StatusPaymentRequired {
			return c.finish(resp, out)
		}
		if err := c.payLoop(ctx, method, path, body, headers, out, resp); err != nil {
			if isRetryableNetworkErr(err) {
				lastErr = err
				c.backoff(attempt)
				continue
			}
			return err
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("brij: request failed after %d attempts", c.retries+1)
	}
	return lastErr
}

// payLoop resends the request with a PAYMENT-SIGNATURE header, rebuilding the
// signature against each fresh 402 challenge.
func (c *Client) payLoop(ctx context.Context, method, path string, body any, headers map[string]string, out any, first *http.Response) error {
	resp := first
	for attempt := 0; attempt < maxPayAttempts; attempt++ {
		sig, err := c.buildPaymentSignature(ctx, resp, path)
		drainAndClose(resp)
		if err != nil {
			return err
		}
		next, err := c.send(ctx, method, path, body, sig, headers)
		if err != nil {
			return err // transient network error: caller may retry the whole flow
		}
		resp = next
		if resp.StatusCode == http.StatusPaymentRequired {
			// Re-read the current challenge before signing again — the price may
			// have moved between attempts.
			continue
		}
		return c.finish(resp, out)
	}
	drainAndClose(resp)
	return &PaymentVerificationError{
		Code:    "payment_not_settled",
		Message: "payment did not settle after repeated attempts",
	}
}

// buildPaymentSignature builds, signs, and base64-encodes the partially signed
// Solana transaction that effects the exact USDC payment required by the 402.
func (c *Client) buildPaymentSignature(ctx context.Context, resp *http.Response, path string) (string, error) {
	encoded := resp.Header.Get("PAYMENT-REQUIRED")
	if encoded == "" {
		return "", &PaymentVerificationError{Code: "missing_payment_required", Message: "402 without a PAYMENT-REQUIRED header"}
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("brij: decode PAYMENT-REQUIRED: %w", err)
	}
	var pr paymentRequirement
	if err := json.Unmarshal(raw, &pr); err != nil {
		return "", fmt.Errorf("brij: parse PAYMENT-REQUIRED: %w", err)
	}
	if pr.Error != "" {
		return "", &PaymentVerificationError{Code: "challenge_error", Message: pr.Error}
	}
	if len(pr.Accepts) == 0 {
		return "", &PaymentVerificationError{Code: "no_accepted_scheme", Message: "challenge carries no accepted payment schemes"}
	}
	ac := pr.Accepts[0]
	if ac.Scheme != "exact" || !strings.HasPrefix(ac.Network, "solana:") {
		return "", &PaymentVerificationError{
			Code:    "unsupported_scheme",
			Message: fmt.Sprintf("unsupported x402 scheme %q on %q", ac.Scheme, ac.Network),
		}
	}
	if ac.Amount <= 0 {
		return "", &PaymentVerificationError{Code: "bad_amount", Message: "challenge carries a non-positive amount"}
	}
	// The funding wallet pays exactly what the challenge demands — a
	// misconfigured base URL or a compromised endpoint must never be able to
	// drain it, so the asset and amount are pinned before any transfer is built.
	if !strings.EqualFold(ac.Asset, USDCAccount) {
		return "", &PaymentVerificationError{
			Code:    "unsupported_asset",
			Message: fmt.Sprintf("challenge asset %q is not Solana mainnet USDC", ac.Asset),
		}
	}
	if c.maxPaymentBaseUnits > 0 && ac.Amount > c.maxPaymentBaseUnits {
		return "", &PaymentVerificationError{
			Code:    "amount_over_cap",
			Message: fmt.Sprintf("challenge amount %d exceeds the configured per-request cap %d", ac.Amount, c.maxPaymentBaseUnits),
		}
	}

	mint, err := solana.PublicKeyFromBase58(ac.Asset)
	if err != nil {
		return "", fmt.Errorf("brij: invalid asset %q: %w", ac.Asset, err)
	}
	payTo, err := solana.PublicKeyFromBase58(ac.PayTo)
	if err != nil {
		return "", fmt.Errorf("brij: invalid payTo %q: %w", ac.PayTo, err)
	}

	// Source + destination ATAs (USDC associated token accounts).
	srcATA, _, err := solana.FindAssociatedTokenAddress(c.pubkey, mint)
	if err != nil {
		return "", fmt.Errorf("brij: derive source ATA: %w", err)
	}
	dstATA, _, err := solana.FindAssociatedTokenAddress(payTo, mint)
	if err != nil {
		return "", fmt.Errorf("brij: derive destination ATA: %w", err)
	}

	// Sponsor pays the fee; it must never appear in any instruction.
	feePayer, err := sponsorFeePayer(ac.Extra)
	if err != nil {
		return "", err
	}

	// Blockhash: prefer the challenge's recentBlockhash to save an RPC round
	// trip; otherwise fetch one.
	blockhash, err := c.paymentBlockhash(ctx, ac.Extra)
	if err != nil {
		return "", err
	}

	// Memo: seller-defined when provided, otherwise binds the payment to the
	// requested resource and amount so each transaction is self-describing and
	// distinct even when the challenge nonce is identical.
	memoText, err := paymentMemo(ac.Extra, path, ac.Amount)
	if err != nil {
		return "", err
	}

	instructions := make([]solana.Instruction, 0, 4)
	for _, build := range []func() (solana.Instruction, error){
		func() (solana.Instruction, error) {
			return computebudget.NewSetComputeUnitLimitInstructionBuilder().SetUnits(computeUnitCap).ValidateAndBuild()
		},
		func() (solana.Instruction, error) {
			return computebudget.NewSetComputeUnitPriceInstructionBuilder().SetMicroLamports(priorityFee).ValidateAndBuild()
		},
		func() (solana.Instruction, error) {
			return token.NewTransferCheckedInstruction(
				uint64(ac.Amount),
				USDCDecimals,
				srcATA,
				mint,
				dstATA,
				c.pubkey,
				nil,
			).ValidateAndBuild()
		},
	} {
		ix, err := build()
		if err != nil {
			return "", fmt.Errorf("brij: build payment instruction: %w", err)
		}
		instructions = append(instructions, ix)
	}
	instructions = append(instructions, solana.NewInstruction(solana.MemoProgramID, nil, []byte(memoText)))

	tx, err := solana.NewTransaction(instructions, blockhash, solana.TransactionPayer(feePayer))
	if err != nil {
		return "", fmt.Errorf("brij: build payment tx: %w", err)
	}
	if _, err := tx.PartialSign(func(key solana.PublicKey) *solana.PrivateKey {
		if key == c.pubkey {
			return &c.keypair
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("brij: sign payment tx: %w", err)
	}
	return tx.ToBase64()
}

// sponsorFeePayer extracts the sponsor fee payer from the challenge extra.
func sponsorFeePayer(extra map[string]any) (solana.PublicKey, error) {
	fp, ok := extra["feePayer"].(string)
	if !ok || fp == "" {
		return solana.PublicKey{}, &PaymentVerificationError{Code: "no_fee_payer", Message: "challenge carries no feePayer sponsor"}
	}
	pk, err := solana.PublicKeyFromBase58(fp)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("brij: invalid feePayer %q: %w", fp, err)
	}
	return pk, nil
}

// paymentBlockhash returns the challenge blockhash when present, else fetches
// a recent one from the configured RPC.
func (c *Client) paymentBlockhash(ctx context.Context, extra map[string]any) (solana.Hash, error) {
	if bh, ok := extra["recentBlockhash"].(string); ok && bh != "" {
		h, err := solana.HashFromBase58(bh)
		if err != nil {
			c.logger.Warn("brij: ignoring invalid challenge recentBlockhash", zap.String("blockhash", bh), zap.Error(err))
		} else if !h.IsZero() {
			return h, nil
		}
	}
	recent, err := c.rpc.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return solana.Hash{}, fmt.Errorf("brij: get recent blockhash: %w", err)
	}
	return recent.Value.Blockhash, nil
}

// paymentMemo returns the seller-defined memo, else a memo that binds the
// payment to the requested resource and amount plus a random nonce (>= 16
// bytes) to make the transaction unique.
func paymentMemo(extra map[string]any, resource string, amount int64) (string, error) {
	if m, ok := extra["memo"].(string); ok && m != "" {
		if len(m) > 256 {
			return "", &PaymentVerificationError{Code: "memo_too_long", Message: "challenge memo exceeds 256 bytes"}
		}
		return m, nil
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("brij: generate payment nonce: %w", err)
	}
	return fmt.Sprintf("rail:%s:%d:%s", strings.TrimPrefix(resource, "/"), amount, hex.EncodeToString(nonce)), nil
}

// --- plain (unpaid) request path ---

// do performs an unpaid request and decodes a 2xx response into out.
func (c *Client) do(ctx context.Context, method, path string, body any, out any, headers map[string]string) error {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		resp, err := c.send(ctx, method, path, body, "", headers)
		if err != nil {
			lastErr = err
			c.backoff(attempt)
			continue
		}
		if err := c.finish(resp, out); err != nil {
			if e, ok := err.(*Error); ok && e.IsRetryable() {
				lastErr = err
				c.backoff(attempt)
				continue
			}
			return err
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("brij: request failed after %d attempts", c.retries+1)
	}
	return lastErr
}

// send builds and executes an HTTP request, optionally attaching the x402
// PAYMENT-SIGNATURE header. It returns the response for inspection (body is
// buffered and closed by the caller via finish).
func (c *Client) send(ctx context.Context, method, path string, body any, paymentSignature string, headers map[string]string) (*http.Response, error) {
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("brij: encode request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("brij: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "rail-service/1.0")
	if paymentSignature != "" {
		req.Header.Set("PAYMENT-SIGNATURE", paymentSignature)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brij: %s %s: %w", method, path, err)
	}
	return resp, nil
}

// finish reads the response body and decodes a 2xx response into out. Non-2xx
// responses become a *Error (or *PaymentVerificationError on 402). A read
// failure on a 2xx response is an error, never a silent zero-value success.
func (c *Client) finish(resp *http.Response, out any) error {
	defer resp.Body.Close()
	bodyBytes, readErr := readBody(resp)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if readErr != nil {
			return fmt.Errorf("brij: read response body: %w", readErr)
		}
		if out != nil && len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, out); err != nil {
				return fmt.Errorf("brij: decode response: %w", err)
			}
		}
		c.logPayments(resp)
		return nil
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		return &PaymentVerificationError{
			Code:    "payment_required",
			Message: "payment could not be completed",
		}
	}
	return apiErrorFrom(resp.StatusCode, bodyBytes)
}

// logPayments captures the settlement signature BRIJ returns on success.
func (c *Client) logPayments(resp *http.Response) {
	if sig := resp.Header.Get("PAYMENT-RESPONSE"); sig != "" {
		c.logger.Debug("brij: x402 settlement", zap.String("payment_response", sig))
	}
}

func (c *Client) backoff(attempt int) {
	if attempt <= 0 {
		return
	}
	time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
}

// drainAndClose consumes and closes a response body so the connection can be
// reused. 402 challenge bodies are read only for headers, so they must be
// drained here rather than via finish.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// readBody returns the response body, keeping the error so a failed read is
// never mistaken for an empty successful payload.
func readBody(resp *http.Response) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return buf.Bytes(), err
	}
	return buf.Bytes(), nil
}

func isRetryableNetworkErr(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*Error); ok {
		return false
	}
	if _, ok := err.(*PaymentVerificationError); ok {
		return false
	}
	return true
}
