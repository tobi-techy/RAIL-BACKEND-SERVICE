package entities

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// WalletChain represents supported blockchain networks for Circle integration
type WalletChain string

const (
	// Circle-supported chains (testnet)
	WalletChainSOLDevnet WalletChain = "SOL-DEVNET"
	
	// Circle-supported chains (mainnet) - all wallets via Circle
	WalletChainEthereum  WalletChain = "ETH"
	WalletChainPolygon   WalletChain = "MATIC"
	WalletChainAvalanche WalletChain = "AVAX"
	WalletChainArbitrum  WalletChain = "ARB"
	WalletChainBase      WalletChain = "BASE"
	WalletChainOptimism  WalletChain = "OP"
	WalletChainSolana    WalletChain = "SOL"
	WalletChainAptos     WalletChain = "APT"
	WalletChainNear      WalletChain = "NEAR"
	
	// USDC Token Addresses by Chain
	USDCTokenAddressSOLDevnet = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
)

// GetUSDCTokenAddress returns the USDC token address for the chain
func (c WalletChain) GetUSDCTokenAddress() string {
	switch c {
	case WalletChainSOLDevnet:
		return USDCTokenAddressSOLDevnet
	default:
		return ""
	}
}

// GetMainnetChains returns Circle-supported production chains
func GetMainnetChains() []WalletChain {
	return []WalletChain{
		WalletChainEthereum, WalletChainPolygon, WalletChainAvalanche, WalletChainArbitrum,
		WalletChainBase, WalletChainOptimism, WalletChainSolana, WalletChainAptos,
	}
}

// GetTestnetChains returns testnet chains
func GetTestnetChains() []WalletChain {
	return []WalletChain{WalletChainSOLDevnet}
}

// IsValid checks if the chain is supported
func (c WalletChain) IsValid() bool {
	validChains := append(GetMainnetChains(), GetTestnetChains()...)
	for _, chain := range validChains {
		if chain == c {
			return true
		}
	}
	return false
}

// IsTestnet checks if the chain is a testnet
func (c WalletChain) IsTestnet() bool {
	testnets := GetTestnetChains()
	for _, testnet := range testnets {
		if testnet == c {
			return true
		}
	}
	return false
}

// GetChainFamily returns the chain family
func (c WalletChain) GetChainFamily() string {
	switch c {
	case WalletChainSOLDevnet, WalletChainSolana:
		return "Solana"
	case WalletChainEthereum, WalletChainPolygon, WalletChainAvalanche, WalletChainArbitrum, WalletChainBase, WalletChainOptimism:
		return "EVM"
	case WalletChainAptos:
		return "Aptos"
	case WalletChainNear:
		return "Near"
	default:
		return "Unknown"
	}
}

// WalletAccountType represents the type of wallet account
type WalletAccountType string

const (
	AccountTypeEOA WalletAccountType = "EOA" // Externally Owned Account
	AccountTypeSCA WalletAccountType = "SCA" // Smart Contract Account
)

// IsValid checks if account type is valid
func (t WalletAccountType) IsValid() bool {
	return t == AccountTypeEOA || t == AccountTypeSCA
}

// WalletStatus represents the status of a wallet
type WalletStatus string

const (
	WalletStatusCreating WalletStatus = "creating"
	WalletStatusLive     WalletStatus = "live"
	WalletStatusFailed   WalletStatus = "failed"
)

// IsValid checks if wallet status is valid
func (s WalletStatus) IsValid() bool {
	return s == WalletStatusCreating || s == WalletStatusLive || s == WalletStatusFailed
}

// WalletSetStatus represents the status of a wallet set
type WalletSetStatus string

const (
	WalletSetStatusActive   WalletSetStatus = "active"
	WalletSetStatusInactive WalletSetStatus = "inactive"
)

// WalletSet represents a Circle wallet set
type WalletSet struct {
	ID                     uuid.UUID       `json:"id" db:"id"`
	Name                   string          `json:"name" db:"name" validate:"required"`
	CircleWalletSetID      string          `json:"circle_wallet_set_id" db:"circle_wallet_set_id"`
	EntitySecretCiphertext string          `json:"-" db:"entity_secret_ciphertext"` // Never expose in JSON
	Status                 WalletSetStatus `json:"status" db:"status"`
	CreatedAt              time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at" db:"updated_at"`
}

// Validate performs validation on wallet set
func (ws *WalletSet) Validate() error {
	if ws.Name == "" {
		return fmt.Errorf("wallet set name is required")
	}

	if len(ws.Name) > 100 {
		return fmt.Errorf("wallet set name cannot exceed 100 characters")
	}

	if ws.CircleWalletSetID == "" {
		return fmt.Errorf("circle wallet set ID is required")
	}

	// Entity secret is now generated dynamically, no validation needed

	if ws.Status != WalletSetStatusActive && ws.Status != WalletSetStatusInactive {
		return fmt.Errorf("invalid wallet set status: %s", ws.Status)
	}

	return nil
}

// WalletProvider represents the provider of a wallet
type WalletProvider string

const (
	WalletProviderCircle WalletProvider = "circle"
	WalletProviderGrid   WalletProvider = "grid"
)

// ManagedWallet represents a managed wallet (Circle or Grid)
type ManagedWallet struct {
	ID             uuid.UUID         `json:"id" db:"id"`
	UserID         uuid.UUID         `json:"user_id" db:"user_id"`
	Chain          WalletChain       `json:"chain" db:"chain"`
	Address        string            `json:"address" db:"address"`
	CircleWalletID string            `json:"circle_wallet_id" db:"circle_wallet_id"`
	BridgeWalletID string            `json:"bridge_wallet_id" db:"bridge_wallet_id"`
	WalletSetID    uuid.UUID         `json:"wallet_set_id" db:"wallet_set_id"`
	AccountType    WalletAccountType `json:"account_type" db:"account_type"`
	Provider       WalletProvider    `json:"provider" db:"provider"`
	Status         WalletStatus      `json:"status" db:"status"`
	CreatedAt      time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at" db:"updated_at"`

	// Related entities (not stored in DB)
	WalletSet *WalletSet `json:"wallet_set,omitempty"`
}

// Validate performs validation on managed wallet
func (w *ManagedWallet) Validate() error {
	if w.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}

	if !w.Chain.IsValid() {
		return fmt.Errorf("invalid chain: %s", w.Chain)
	}

	if w.Address == "" {
		return fmt.Errorf("wallet address is required")
	}

	// CircleWalletID only required for Circle wallets
	if w.Provider == WalletProviderCircle && w.CircleWalletID == "" {
		return fmt.Errorf("circle wallet ID is required for Circle wallets")
	}

	// WalletSetID only required for Circle wallets
	if w.Provider == WalletProviderCircle && w.WalletSetID == uuid.Nil {
		return fmt.Errorf("wallet set ID is required for Circle wallets")
	}

	if !w.AccountType.IsValid() {
		return fmt.Errorf("invalid account type: %s", w.AccountType)
	}

	if !w.Status.IsValid() {
		return fmt.Errorf("invalid wallet status: %s", w.Status)
	}

	return nil
}

// IsReady checks if wallet is ready for use
func (w *ManagedWallet) IsReady() bool {
	return w.Status == WalletStatusLive && w.Address != ""
}

// CanReceive checks if wallet can receive funds
func (w *ManagedWallet) CanReceive() bool {
	return w.IsReady()
}

// GetDisplayAddress returns a user-friendly display of the address
func (w *ManagedWallet) GetDisplayAddress() string {
	if len(w.Address) <= 8 {
		return w.Address
	}
	return fmt.Sprintf("%s...%s", w.Address[:6], w.Address[len(w.Address)-4:])
}

// WalletProvisioningJobStatus represents the status of wallet provisioning
type WalletProvisioningJobStatus string

const (
	ProvisioningStatusQueued     WalletProvisioningJobStatus = "queued"
	ProvisioningStatusInProgress WalletProvisioningJobStatus = "in_progress"
	ProvisioningStatusCompleted  WalletProvisioningJobStatus = "completed"
	ProvisioningStatusFailed     WalletProvisioningJobStatus = "failed"
	ProvisioningStatusRetry      WalletProvisioningJobStatus = "retry"
)

// WalletProvisioningJob represents an async wallet provisioning job
type WalletProvisioningJob struct {
	ID             uuid.UUID                   `json:"id" db:"id"`
	UserID         uuid.UUID                   `json:"user_id" db:"user_id"`
	Chains         []string                    `json:"chains" db:"chains"`
	Status         WalletProvisioningJobStatus `json:"status" db:"status"`
	AttemptCount   int                         `json:"attempt_count" db:"attempt_count"`
	MaxAttempts    int                         `json:"max_attempts" db:"max_attempts"`
	CircleRequests map[string]any              `json:"circle_requests" db:"circle_requests"`
	ErrorMessage   *string                     `json:"error_message" db:"error_message"`
	NextRetryAt    *time.Time                  `json:"next_retry_at" db:"next_retry_at"`
	StartedAt      *time.Time                  `json:"started_at" db:"started_at"`
	CompletedAt    *time.Time                  `json:"completed_at" db:"completed_at"`
	CreatedAt      time.Time                   `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time                   `json:"updated_at" db:"updated_at"`
}

// CanRetry checks if the job can be retried
func (job *WalletProvisioningJob) CanRetry() bool {
	return job.Status == ProvisioningStatusFailed &&
		job.AttemptCount < job.MaxAttempts
}

// MarkStarted marks the job as started
func (job *WalletProvisioningJob) MarkStarted() {
	now := time.Now()
	job.Status = ProvisioningStatusInProgress
	job.StartedAt = &now
	job.AttemptCount++
	job.UpdatedAt = now
}

// MarkCompleted marks the job as completed
func (job *WalletProvisioningJob) MarkCompleted() {
	now := time.Now()
	job.Status = ProvisioningStatusCompleted
	job.CompletedAt = &now
	job.UpdatedAt = now
}

// MarkFailed marks the job as failed
func (job *WalletProvisioningJob) MarkFailed(errorMsg string, retryDelay time.Duration) {
	now := time.Now()
	job.Status = ProvisioningStatusFailed
	job.ErrorMessage = &errorMsg
	job.UpdatedAt = now

	if job.CanRetry() {
		nextRetry := now.Add(retryDelay)
		job.NextRetryAt = &nextRetry
		job.Status = ProvisioningStatusRetry
	}
}

// AddCircleRequest adds a Circle API request/response to the log
func (job *WalletProvisioningJob) AddCircleRequest(operation string, request, response any) {
	if job.CircleRequests == nil {
		job.CircleRequests = make(map[string]any)
	}

	if requests, ok := job.CircleRequests["requests"].([]map[string]any); ok {
		job.CircleRequests["requests"] = append(requests, map[string]any{
			"timestamp": time.Now(),
			"operation": operation,
			"request":   request,
			"response":  response,
		})
	} else {
		job.CircleRequests["requests"] = []map[string]any{{
			"timestamp": time.Now(),
			"operation": operation,
			"request":   request,
			"response":  response,
		}}
	}
}

// === API Request/Response Models ===

// WalletAddressesRequest represents request for wallet addresses
type WalletAddressesRequest struct {
	Chain *WalletChain `json:"chain,omitempty" validate:"omitempty"`
}

// WalletAddressResponse represents a single wallet address
type WalletAddressResponse struct {
	Chain   WalletChain `json:"chain"`
	Address string      `json:"address"`
	Status  string      `json:"status"`
}

// WalletAddressesResponse represents response with wallet addresses
type WalletAddressesResponse struct {
	Wallets []WalletAddressResponse `json:"wallets"`
}

// ExternalWallet represents a wallet from an external provider (e.g., Grid)
type ExternalWallet struct {
	Chain    WalletChain    `json:"chain"`
	Address  string         `json:"address"`
	Provider WalletProvider `json:"provider"`
}

// WalletStatusResponse represents wallet status for all chains
type WalletStatusResponse struct {
	UserID          uuid.UUID                      `json:"userId"`
	TotalWallets    int                            `json:"totalWallets"`
	ReadyWallets    int                            `json:"readyWallets"`
	PendingWallets  int                            `json:"pendingWallets"`
	FailedWallets   int                            `json:"failedWallets"`
	WalletsByChain  map[string]WalletChainStatus   `json:"walletsByChain"`
	ProvisioningJob *WalletProvisioningJobResponse `json:"provisioningJob,omitempty"`
}

// WalletChainStatus represents status for a specific chain
type WalletChainStatus struct {
	Chain     WalletChain `json:"chain"`
	Address   *string     `json:"address,omitempty"`
	Status    string      `json:"status"`
	CreatedAt *time.Time  `json:"createdAt,omitempty"`
	Error     *string     `json:"error,omitempty"`
}

// WalletProvisioningJobResponse represents provisioning job status
type WalletProvisioningJobResponse struct {
	ID           uuid.UUID  `json:"id"`
	Status       string     `json:"status"`
	Progress     string     `json:"progress"`
	AttemptCount int        `json:"attemptCount"`
	MaxAttempts  int        `json:"maxAttempts"`
	ErrorMessage *string    `json:"errorMessage,omitempty"`
	NextRetryAt  *time.Time `json:"nextRetryAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// WalletInitiationRequest represents request to initiate wallet creation after passcode verification
type WalletInitiationRequest struct {
	Chains []string `json:"chains,omitempty" validate:"omitempty,dive,oneof=SOL-DEVNET APTOS-TESTNET MATIC-AMOY BASE-SEPOLIA"`
}

// WalletInitiationResponse represents response for wallet initiation
type WalletInitiationResponse struct {
	Message string                         `json:"message"`
	UserID  string                         `json:"user_id"`
	Chains  []string                       `json:"chains"`
	Job     *WalletProvisioningJobResponse `json:"job,omitempty"`
}

// WalletProvisioningRequest represents request to provision wallets
type WalletProvisioningRequest struct {
	Chains []string `json:"chains,omitempty" validate:"omitempty,dive,oneof=ETH ETH-SEPOLIA MATIC MATIC-AMOY SOL SOL-DEVNET APTOS APTOS-TESTNET AVAX BASE BASE-SEPOLIA"`
}

// WalletProvisioningResponse represents response for wallet provisioning
type WalletProvisioningResponse struct {
	Message string                        `json:"message"`
	Job     WalletProvisioningJobResponse `json:"job"`
}

// === Circle API Models ===

// CircleWalletSetRequest represents Circle wallet set creation request
type CircleWalletSetRequest struct {
	IdempotencyKey         string `json:"idempotencyKey,omitempty"`
	EntitySecretCiphertext string `json:"entitySecretCiphertext"`
	Name                   string `json:"name"`
}

// CircleWalletSetResponse represents Circle wallet set response
type CircleWalletSetResponse struct {
	WalletSet CircleWalletSetData `json:"walletSet"`
}

// UnmarshalJSON normalizes Circle wallet set responses that may wrap data
func (r *CircleWalletSetResponse) UnmarshalJSON(data []byte) error {
	aux := struct {
		Data struct {
			WalletSet CircleWalletSetData `json:"walletSet"`
		} `json:"data"`
		WalletSet *CircleWalletSetData `json:"walletSet"`
	}{}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Check if wrapped in data.walletSet first
	if aux.Data.WalletSet.ID != "" {
		r.WalletSet = aux.Data.WalletSet
		return nil
	}

	// Fallback to direct walletSet field
	if aux.WalletSet != nil && aux.WalletSet.ID != "" {
		r.WalletSet = *aux.WalletSet
		return nil
	}

	// Default empty structure
	r.WalletSet = CircleWalletSetData{}
	return nil
}

// CircleWalletSetData represents Circle wallet set data
type CircleWalletSetData struct {
	ID          string    `json:"id"`
	CustodyType string    `json:"custodyType"`
	Name        string    `json:"name"`
	CreatedDate time.Time `json:"createDate"`
	UpdatedDate time.Time `json:"updateDate"`
}

// CircleWalletCreateRequest represents Circle wallet creation request
type CircleWalletCreateRequest struct {
	IdempotencyKey         string   `json:"idempotencyKey,omitempty"`
	EntitySecretCiphertext string   `json:"entitySecretCipherText"`
	Blockchains            []string `json:"blockchains"`
	Count                  int      `json:"count,omitempty"`
	AccountType            string   `json:"accountType"`
	WalletSetID            string   `json:"walletSetId"`
}

// CircleWalletCreateResponse represents Circle wallet creation response
type CircleWalletCreateResponse struct {
	Wallet CircleWalletData `json:"wallet"`
}

// CircleWalletCreateBulkResponse represents Circle wallet creation response for bulk operations
type CircleWalletCreateBulkResponse struct {
	Wallets []CircleWalletData `json:"wallets"`
}

// UnmarshalJSON normalizes Circle wallet responses that may wrap data
func (r *CircleWalletCreateResponse) UnmarshalJSON(data []byte) error {
	aux := struct {
		Data struct {
			Wallets []CircleWalletData `json:"wallets"`
		} `json:"data"`
		Wallet  *CircleWalletData  `json:"wallet"`
		Wallets []CircleWalletData `json:"wallets"`
	}{}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Check if wrapped in data.wallets first (most common format)
	if len(aux.Data.Wallets) > 0 {
		r.Wallet = aux.Data.Wallets[0] // Take first wallet for single response
		return nil
	}

	// Check direct wallets array
	if len(aux.Wallets) > 0 {
		r.Wallet = aux.Wallets[0]
		return nil
	}

	// Fallback to direct wallet field
	if aux.Wallet != nil && aux.Wallet.ID != "" {
		r.Wallet = *aux.Wallet
		return nil
	}

	// Default empty structure
	r.Wallet = CircleWalletData{}
	return nil
}

// UnmarshalJSON normalizes Circle bulk wallet responses that may wrap data
func (r *CircleWalletCreateBulkResponse) UnmarshalJSON(data []byte) error {
	type alias CircleWalletCreateBulkResponse
	aux := struct {
		Data    *alias             `json:"data"`
		Wallets []CircleWalletData `json:"wallets"`
	}{}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	switch {
	case aux.Data != nil && len(aux.Data.Wallets) > 0:
		r.Wallets = aux.Data.Wallets
	case len(aux.Wallets) > 0:
		r.Wallets = aux.Wallets
	default:
		r.Wallets = []CircleWalletData{}
	}

	return nil
}

// CircleWalletData represents Circle wallet data
type CircleWalletData struct {
	ID          string                `json:"id"`
	State       string                `json:"state"`
	WalletSetId string                `json:"walletSetId"`
	CustodyType string                `json:"custodyType"`
	AccountType string                `json:"accountType,omitempty"`
	Addresses   []CircleWalletAddress `json:"addresses,omitempty"`
	Address     string                `json:"address,omitempty"` // For single address responses
	Blockchain  string                `json:"blockchain,omitempty"`
	CreatedDate time.Time             `json:"createDate"`
	UpdatedDate time.Time             `json:"updateDate"`
}

// CircleWalletAddress represents a wallet address for a specific blockchain
type CircleWalletAddress struct {
	Address    string `json:"address"`
	Blockchain string `json:"blockchain"`
	Chain      string `json:"chain,omitempty"`
}

// CircleErrorResponse represents Circle API error response
type CircleErrorResponse struct {
	Code    int                `json:"code"`
	Message string             `json:"message"`
	Errors  []CircleFieldError `json:"errors,omitempty"`
}

// CircleFieldError represents field-specific error
type CircleFieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements error interface
func (e CircleErrorResponse) Error() string {
	if len(e.Errors) > 0 {
		var details []string
		for _, fieldErr := range e.Errors {
			details = append(details, fmt.Sprintf("%s: %s", fieldErr.Field, fieldErr.Message))
		}
		return fmt.Sprintf("Circle API error %d: %s (%s)", e.Code, e.Message, strings.Join(details, ", "))
	}
	return fmt.Sprintf("Circle API error %d: %s", e.Code, e.Message)
}

// CircleAPIError represents a comprehensive Circle API error with type information
type CircleAPIError struct {
	Code       int                `json:"code"`
	Message    string             `json:"message"`
	Errors     []CircleFieldError `json:"errors,omitempty"`
	RequestID  string             `json:"request_id,omitempty"`
	RetryAfter *time.Duration     `json:"retry_after,omitempty"`
	Type       string             `json:"type"`
}

// Error implements error interface
func (e CircleAPIError) Error() string {
	if len(e.Errors) > 0 {
		var details []string
		for _, fieldErr := range e.Errors {
			details = append(details, fmt.Sprintf("%s: %s", fieldErr.Field, fieldErr.Message))
		}
		return fmt.Sprintf("Circle %s error %d: %s (%s)", e.Type, e.Code, e.Message, strings.Join(details, ", "))
	}
	return fmt.Sprintf("Circle %s error %d: %s", e.Type, e.Code, e.Message)
}

// IsRetryable returns true if the error is retryable
func (e CircleAPIError) IsRetryable() bool {
	switch e.Code {
	case 429: // Rate limit
		return true
	case 500, 502, 503, 504: // Server errors
		return true
	default:
		return false
	}
}

// GetRetryAfter returns the retry delay for rate limit errors
func (e CircleAPIError) GetRetryAfter() time.Duration {
	if e.RetryAfter != nil {
		return *e.RetryAfter
	}
	// Default backoff for server errors
	if e.Code >= 500 {
		return 5 * time.Second
	}
	return 0
}

// CircleAuthError represents authentication/authorization errors
type CircleAuthError struct {
	CircleAPIError
}

// CircleValidationError represents validation errors
type CircleValidationError struct {
	CircleAPIError
}

// CircleRateLimitError represents rate limit errors
type CircleRateLimitError struct {
	CircleAPIError
}

// CircleConflictError represents conflict errors (duplicate resources)
type CircleConflictError struct {
	CircleAPIError
}

// CircleServerError represents server errors
type CircleServerError struct {
	CircleAPIError
}

// NewCircleAPIError creates a new Circle API error with proper typing
func NewCircleAPIError(code int, message string, requestID string, retryAfter *time.Duration) error {
	baseError := CircleAPIError{
		Code:       code,
		Message:    message,
		RequestID:  requestID,
		RetryAfter: retryAfter,
	}

	switch {
	case code == 401 || code == 403:
		baseError.Type = "auth"
		return CircleAuthError{baseError}
	case code == 400:
		baseError.Type = "validation"
		return CircleValidationError{baseError}
	case code == 429:
		baseError.Type = "rate_limit"
		return CircleRateLimitError{baseError}
	case code == 409:
		baseError.Type = "conflict"
		return CircleConflictError{baseError}
	case code >= 500:
		baseError.Type = "server"
		return CircleServerError{baseError}
	default:
		baseError.Type = "client"
		return baseError
	}
}

// WalletListFilters represents filters for wallet listing
type WalletListFilters struct {
	UserID      *uuid.UUID `json:"user_id,omitempty"`
	WalletSetID *uuid.UUID `json:"wallet_set_id,omitempty"`
	Chain       string     `json:"chain,omitempty"`
	AccountType string     `json:"account_type,omitempty"`
	Status      string     `json:"status,omitempty"`
	Limit       int        `json:"limit"`
	Offset      int        `json:"offset"`
}

// CircleTransferRequest represents a request to transfer funds using Circle API
type CircleTransferRequest struct {
	IDempotencyKey         string   `json:"idempotencyKey"`
	EntitySecretCiphertext string   `json:"entitySecretCiphertext"`
	WalletID               string   `json:"walletId"`
	TokenID                string   `json:"tokenId"`
	Amounts                []string `json:"amounts"`
	DestinationAddress     string   `json:"destinationAddress,omitempty"`
	DestinationWalletID    string   `json:"destinationWalletId,omitempty"`
	DestinationTag         string   `json:"destinationTag,omitempty"`
	DestinationMemo        string   `json:"destinationMemo,omitempty"`
	DestinationMemoType    string   `json:"destinationMemoType,omitempty"`
	RefID                  string   `json:"refId,omitempty"`
	Fee                    string   `json:"fee,omitempty"`
	FeeLevel               string   `json:"feeLevel,omitempty"`
	MaxFee                 string   `json:"maxFee,omitempty"`
	PriorityFee            string   `json:"priorityFee,omitempty"`
	GasPrice               string   `json:"gasPrice,omitempty"`
	GasLimit               string   `json:"gasLimit,omitempty"`
	Nonce                  string   `json:"nonce,omitempty"`
	Note                   string   `json:"note,omitempty"`
	AutoGas                bool     `json:"autoGas,omitempty"`
	NetworkFee             string   `json:"networkFee,omitempty"`
	ReplaceTxByHash        string   `json:"replaceTxByHash,omitempty"`
	SequenceID             string   `json:"sequenceId,omitempty"`
	SourceAddress          string   `json:"sourceAddress,omitempty"`
	SourceAddressTag       string   `json:"sourceAddressTag,omitempty"`
	SourceAddressMemo      string   `json:"sourceAddressMemo,omitempty"`
	SourceAddressMemoType  string   `json:"sourceAddressMemoType,omitempty"`
	SourceWalletID         string   `json:"sourceWalletId,omitempty"`
	SourceTag              string   `json:"sourceTag,omitempty"`
	SourceMemo             string   `json:"sourceMemo,omitempty"`
	SourceMemoType         string   `json:"sourceMemoType,omitempty"`
	TrackingRef            string   `json:"trackingRef,omitempty"`
	TxHash                 string   `json:"txHash,omitempty"`
	TxType                 string   `json:"txType,omitempty"`
	WalletSetID            string   `json:"walletSetId,omitempty"`
}

// CircleTokenInfo represents token metadata from Circle API
type CircleTokenInfo struct {
	ID           string    `json:"id"`
	Blockchain   string    `json:"blockchain"`
	TokenAddress string    `json:"tokenAddress,omitempty"`
	Standard     string    `json:"standard,omitempty"`
	Name         string    `json:"name"`
	Symbol       string    `json:"symbol"`
	Decimals     int       `json:"decimals"`
	IsNative     bool      `json:"isNative"`
	UpdateDate   time.Time `json:"updateDate"`
	CreateDate   time.Time `json:"createDate"`
}

// CircleTokenBalance represents a single token balance from Circle API
type CircleTokenBalance struct {
	Token      CircleTokenInfo `json:"token"`
	Amount     string          `json:"amount"`
	UpdateDate time.Time       `json:"updateDate"`
}

// CircleWalletBalancesResponse represents the Circle API response for wallet balances
type CircleWalletBalancesResponse struct {
	TokenBalances []CircleTokenBalance `json:"tokenBalances"`
}

// UnmarshalJSON normalizes Circle balance responses that wrap data
func (r *CircleWalletBalancesResponse) UnmarshalJSON(data []byte) error {
	aux := struct {
		Data struct {
			TokenBalances []CircleTokenBalance `json:"tokenBalances"`
		} `json:"data"`
		TokenBalances []CircleTokenBalance `json:"tokenBalances"`
	}{}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Check if wrapped in data.tokenBalances first
	if len(aux.Data.TokenBalances) > 0 {
		r.TokenBalances = aux.Data.TokenBalances
		return nil
	}

	// Fallback to direct tokenBalances field
	if len(aux.TokenBalances) > 0 {
		r.TokenBalances = aux.TokenBalances
		return nil
	}

	// Default to empty array (not nil)
	r.TokenBalances = []CircleTokenBalance{}
	return nil
}

// GetUSDCBalance extracts USDC balance from token balances
func (r *CircleWalletBalancesResponse) GetUSDCBalance() string {
	for _, balance := range r.TokenBalances {
		// Check if token is USDC (case-insensitive)
		if strings.EqualFold(balance.Token.Symbol, "USDC") {
			return balance.Amount
		}
	}
	return "0"
}

// GetNativeBalance extracts native token balance (e.g., MATIC, ETH)
func (r *CircleWalletBalancesResponse) GetNativeBalance() string {
	for _, balance := range r.TokenBalances {
		if balance.Token.IsNative {
			return balance.Amount
		}
	}
	return "0"
}
