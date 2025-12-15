package errors

// Application error codes for consistent error reporting
const (
	// General errors (1000-1999)
	CodeInternalError      = "ERR_1000"
	CodeUnknownError       = "ERR_1001"
	CodeInvalidInput       = "ERR_1002"
	CodeOperationFailed    = "ERR_1003"
	CodeTimeout            = "ERR_1004"
	CodeRateLimit          = "ERR_1005"

	// Authentication errors (2000-2099)
	CodeUnauthorized       = "ERR_2000"
	CodeInvalidToken       = "ERR_2001"
	CodeExpiredToken       = "ERR_2002"
	CodeInvalidCredentials = "ERR_2003"
	CodeSessionExpired     = "ERR_2004"
	CodeAccountLocked      = "ERR_2005"

	// Authorization errors (2100-2199)
	CodeForbidden          = "ERR_2100"
	CodeInsufficientPermissions = "ERR_2101"
	CodeKYCNotApproved     = "ERR_2102"
	CodeAccountSuspended   = "ERR_2103"

	// Resource errors (3000-3099)
	CodeNotFound           = "ERR_3000"
	CodeAlreadyExists      = "ERR_3001"
	CodeConflict           = "ERR_3002"
	CodeResourceDeleted    = "ERR_3003"

	// Validation errors (4000-4099)
	CodeValidationFailed   = "ERR_4000"
	CodeInvalidFormat      = "ERR_4001"
	CodeMissingField       = "ERR_4002"
	CodeInvalidValue       = "ERR_4003"
	CodeOutOfRange         = "ERR_4004"

	// Business logic errors (5000-5999)
	// Wallet errors (5000-5099)
	CodeInsufficientFunds  = "ERR_5000"
	CodeWalletNotFound     = "ERR_5001"
	CodeWalletCreationFailed = "ERR_5002"
	CodeInvalidAddress     = "ERR_5003"

	// Deposit errors (5100-5199)
	CodeDepositNotFound    = "ERR_5100"
	CodeDepositFailed      = "ERR_5101"
	CodeDepositPending     = "ERR_5102"
	CodeInvalidAmount      = "ERR_5103"

	// Withdrawal errors (5200-5299)
	CodeWithdrawalNotFound = "ERR_5200"
	CodeWithdrawalFailed   = "ERR_5201"
	CodeWithdrawalLimitExceeded = "ERR_5202"
	CodeInsufficientBalance = "ERR_5203"

	// Trading errors (5300-5399)
	CodeOrderNotFound      = "ERR_5300"
	CodeOrderFailed        = "ERR_5301"
	CodeInvalidOrderType   = "ERR_5302"
	CodeMarketClosed       = "ERR_5303"
	CodeInvalidSymbol      = "ERR_5304"

	// KYC errors (5400-5499)
	CodeKYCFailed          = "ERR_5400"
	CodeKYCPending         = "ERR_5401"
	CodeKYCRejected        = "ERR_5402"
	CodeDocumentRequired   = "ERR_5403"

	// External service errors (6000-6999)
	// Circle errors (6000-6099)
	CodeCircleAPIError     = "ERR_6000"
	CodeCircleTimeout      = "ERR_6001"
	CodeCircleRateLimit    = "ERR_6002"
	CodeCircleInvalidResponse = "ERR_6003"

	// Alpaca errors (6100-6199)
	CodeAlpacaAPIError     = "ERR_6100"
	CodeAlpacaTimeout      = "ERR_6101"
	CodeAlpacaRateLimit    = "ERR_6102"
	CodeAlpacaInvalidAccount = "ERR_6103"

	// Due errors (6200-6299)
	CodeDueAPIError        = "ERR_6200"
	CodeDueTimeout         = "ERR_6201"
	CodeDueAccountError    = "ERR_6202"

	// Database errors (7000-7099)
	CodeDatabaseError      = "ERR_7000"
	CodeQueryFailed        = "ERR_7001"
	CodeConnectionFailed   = "ERR_7002"
	CodeTransactionFailed  = "ERR_7003"
	CodeDuplicateKey       = "ERR_7004"
	CodeForeignKeyViolation = "ERR_7005"

	// Cache errors (7100-7199)
	CodeCacheError         = "ERR_7100"
	CodeCacheMiss          = "ERR_7101"
	CodeCacheConnectionFailed = "ERR_7102"
)

// ErrorCodeMap maps error codes to human-readable messages
var ErrorCodeMap = map[string]string{
	// General
	CodeInternalError:      "An internal server error occurred",
	CodeUnknownError:       "An unknown error occurred",
	CodeInvalidInput:       "Invalid input provided",
	CodeOperationFailed:    "Operation failed",
	CodeTimeout:            "Operation timed out",
	CodeRateLimit:          "Rate limit exceeded",

	// Authentication
	CodeUnauthorized:       "Authentication required",
	CodeInvalidToken:       "Invalid or expired token",
	CodeExpiredToken:       "Token has expired",
	CodeInvalidCredentials: "Invalid credentials",
	CodeSessionExpired:     "Session has expired",
	CodeAccountLocked:      "Account is locked",

	// Authorization
	CodeForbidden:          "Access denied",
	CodeInsufficientPermissions: "Insufficient permissions",
	CodeKYCNotApproved:     "KYC verification not approved",
	CodeAccountSuspended:   "Account is suspended",

	// Resource
	CodeNotFound:           "Resource not found",
	CodeAlreadyExists:      "Resource already exists",
	CodeConflict:           "Resource conflict",
	CodeResourceDeleted:    "Resource has been deleted",

	// Validation
	CodeValidationFailed:   "Validation failed",
	CodeInvalidFormat:      "Invalid format",
	CodeMissingField:       "Required field is missing",
	CodeInvalidValue:       "Invalid value",
	CodeOutOfRange:         "Value out of range",

	// Wallet
	CodeInsufficientFunds:  "Insufficient funds",
	CodeWalletNotFound:     "Wallet not found",
	CodeWalletCreationFailed: "Wallet creation failed",
	CodeInvalidAddress:     "Invalid wallet address",

	// Deposit
	CodeDepositNotFound:    "Deposit not found",
	CodeDepositFailed:      "Deposit failed",
	CodeDepositPending:     "Deposit is pending",
	CodeInvalidAmount:      "Invalid amount",

	// Withdrawal
	CodeWithdrawalNotFound: "Withdrawal not found",
	CodeWithdrawalFailed:   "Withdrawal failed",
	CodeWithdrawalLimitExceeded: "Withdrawal limit exceeded",
	CodeInsufficientBalance: "Insufficient balance",

	// Trading
	CodeOrderNotFound:      "Order not found",
	CodeOrderFailed:        "Order failed",
	CodeInvalidOrderType:   "Invalid order type",
	CodeMarketClosed:       "Market is closed",
	CodeInvalidSymbol:      "Invalid symbol",

	// KYC
	CodeKYCFailed:          "KYC verification failed",
	CodeKYCPending:         "KYC verification pending",
	CodeKYCRejected:        "KYC verification rejected",
	CodeDocumentRequired:   "Additional documents required",

	// External services
	CodeCircleAPIError:     "Circle API error",
	CodeCircleTimeout:      "Circle API timeout",
	CodeCircleRateLimit:    "Circle API rate limit exceeded",
	CodeCircleInvalidResponse: "Invalid response from Circle",
	
	CodeAlpacaAPIError:     "Alpaca API error",
	CodeAlpacaTimeout:      "Alpaca API timeout",
	CodeAlpacaRateLimit:    "Alpaca API rate limit exceeded",
	CodeAlpacaInvalidAccount: "Invalid Alpaca account",

	CodeDueAPIError:        "Due API error",
	CodeDueTimeout:         "Due API timeout",
	CodeDueAccountError:    "Due account error",

	// Database
	CodeConnectionFailed:   "Connection failed",
	CodeTransactionFailed:  "Transaction failed",
	CodeDuplicateKey:       "Duplicate entry",
	CodeForeignKeyViolation: "Foreign key constraint violation",

	// Cache
	CodeCacheError:         "Cache error",
	CodeCacheMiss:          "Cache miss",
	CodeCacheConnectionFailed: "Cache connection failed",
}

// GetErrorMessage returns the human-readable message for an error code
func GetErrorMessage(code string) string {
	if msg, ok := ErrorCodeMap[code]; ok {
		return msg
	}
	return "Unknown error"
}
