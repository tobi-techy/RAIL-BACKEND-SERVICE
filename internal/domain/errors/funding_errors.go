package errors

import "errors"

// Funding-specific errors
var (
	// Wallet errors
	ErrWalletNotFound         = errors.New("wallet not found")
	ErrWalletAlreadyExists    = errors.New("wallet already exists")
	ErrWalletProvisioningFailed = errors.New("wallet provisioning failed")
	ErrWalletInactive         = errors.New("wallet is inactive")
	ErrInvalidWalletChain     = errors.New("invalid wallet chain")

	// Deposit errors
	ErrDepositNotFound        = errors.New("deposit not found")
	ErrDepositAlreadyProcessed = errors.New("deposit already processed")
	ErrDepositFailed          = errors.New("deposit failed")
	ErrInvalidDepositAmount   = errors.New("invalid deposit amount")
	ErrMinimumDepositNotMet   = errors.New("minimum deposit amount not met")

	// Withdrawal errors
	ErrWithdrawalNotFound     = errors.New("withdrawal not found")
	ErrWithdrawalFailed       = errors.New("withdrawal failed")
	ErrInsufficientFunds      = errors.New("insufficient funds")
	ErrWithdrawalLimitExceeded = errors.New("withdrawal limit exceeded")
	ErrInvalidWithdrawalAddress = errors.New("invalid withdrawal address")
	ErrWithdrawalPending      = errors.New("withdrawal is pending")

	// Balance errors
	ErrBalanceNotFound        = errors.New("balance not found")
	ErrBalanceUpdateFailed    = errors.New("balance update failed")
	ErrNegativeBalance        = errors.New("balance cannot be negative")

	// Conversion errors
	ErrConversionNotFound     = errors.New("conversion not found")
	ErrConversionFailed       = errors.New("conversion failed")
	ErrUnsupportedConversion  = errors.New("unsupported conversion pair")
)

// WalletNotFoundError creates a wallet not found error
func WalletNotFoundError(walletID string) *DomainError {
	return &DomainError{
		Err:     ErrWalletNotFound,
		Code:    "WALLET_NOT_FOUND",
		Message: "wallet not found",
		Details: map[string]interface{}{
			"wallet_id": walletID,
		},
	}
}

// WalletChainNotFoundError creates an error for missing wallet on a specific chain
func WalletChainNotFoundError(chain string) *DomainError {
	return &DomainError{
		Err:     ErrWalletNotFound,
		Code:    "WALLET_CHAIN_NOT_FOUND",
		Message: "no wallet found for specified chain",
		Details: map[string]interface{}{
			"chain": chain,
		},
	}
}

// InsufficientFundsError creates an insufficient funds error
func InsufficientFundsError(available, required string) *DomainError {
	return &DomainError{
		Err:     ErrInsufficientFunds,
		Code:    "INSUFFICIENT_FUNDS",
		Message: "insufficient funds for this operation",
		Details: map[string]interface{}{
			"available": available,
			"required":  required,
		},
	}
}

// WithdrawalLimitError creates a withdrawal limit error
func WithdrawalLimitError(limit, requested string, window string) *DomainError {
	return &DomainError{
		Err:     ErrWithdrawalLimitExceeded,
		Code:    "WITHDRAWAL_LIMIT_EXCEEDED",
		Message: "withdrawal limit exceeded",
		Details: map[string]interface{}{
			"limit":     limit,
			"requested": requested,
			"window":    window,
		},
	}
}

// DepositAlreadyProcessedError creates a duplicate deposit error
func DepositAlreadyProcessedError(txHash string) *DomainError {
	return &DomainError{
		Err:     ErrDepositAlreadyProcessed,
		Code:    "DEPOSIT_ALREADY_PROCESSED",
		Message: "deposit has already been processed",
		Details: map[string]interface{}{
			"tx_hash": txHash,
		},
	}
}

// MinimumDepositError creates a minimum deposit error
func MinimumDepositError(minimum, provided string) *DomainError {
	return &DomainError{
		Err:     ErrMinimumDepositNotMet,
		Code:    "MINIMUM_DEPOSIT_NOT_MET",
		Message: "deposit amount is below minimum",
		Details: map[string]interface{}{
			"minimum":  minimum,
			"provided": provided,
		},
	}
}

// InvalidChainError creates an invalid chain error
func InvalidChainError(chain string, supportedChains []string) *DomainError {
	return &DomainError{
		Err:     ErrInvalidWalletChain,
		Code:    "INVALID_CHAIN",
		Message: "invalid blockchain network",
		Details: map[string]interface{}{
			"chain":            chain,
			"supported_chains": supportedChains,
		},
	}
}

// Error checking helpers

// IsWalletNotFound checks if error is wallet not found
func IsWalletNotFound(err error) bool {
	return errors.Is(err, ErrWalletNotFound)
}

// IsInsufficientFunds checks if error is insufficient funds
func IsInsufficientFunds(err error) bool {
	return errors.Is(err, ErrInsufficientFunds)
}

// IsDepositAlreadyProcessed checks if deposit was already processed
func IsDepositAlreadyProcessed(err error) bool {
	return errors.Is(err, ErrDepositAlreadyProcessed)
}

// IsWithdrawalLimitExceeded checks if withdrawal limit was exceeded
func IsWithdrawalLimitExceeded(err error) bool {
	return errors.Is(err, ErrWithdrawalLimitExceeded)
}
