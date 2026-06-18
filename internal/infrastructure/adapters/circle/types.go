package circle

import "time"

// --- Blockchain identifiers (from Circle SDK v0.23) ---

// Blockchain represents a Circle-supported blockchain identifier.
type Blockchain string

const (
	BlockchainETH         Blockchain = "ETH"
	BlockchainETHSepolia  Blockchain = "ETH-SEPOLIA"
	BlockchainSOL         Blockchain = "SOL"
	BlockchainSOLDevnet   Blockchain = "SOL-DEVNET"
	BlockchainMATIC       Blockchain = "MATIC"
	BlockchainMATICAmoy   Blockchain = "MATIC-AMOY"
	BlockchainARB         Blockchain = "ARB"
	BlockchainARBSepolia  Blockchain = "ARB-SEPOLIA"
	BlockchainBASE        Blockchain = "BASE"
	BlockchainBASESepolia Blockchain = "BASE-SEPOLIA"
	BlockchainAVAX        Blockchain = "AVAX"
	BlockchainAVAXFuji    Blockchain = "AVAX-FUJI"
	BlockchainOP          Blockchain = "OP"
	BlockchainOPSepolia   Blockchain = "OP-SEPOLIA"
)

// WalletState represents the state of a Circle wallet.
type WalletState string

const (
	WalletStateLive   WalletState = "LIVE"
	WalletStateFrozen WalletState = "FROZEN"
)

// TransactionState represents the state of a Circle transaction.
type TransactionState string

const (
	TransactionStateInitiated TransactionState = "INITIATED"
	TransactionStateQueued    TransactionState = "QUEUED"
	TransactionStateSent      TransactionState = "SENT"
	TransactionStateComplete  TransactionState = "COMPLETE"
	TransactionStateFailed    TransactionState = "FAILED"
	TransactionStateCancelled TransactionState = "CANCELLED"
	TransactionStateDenied    TransactionState = "DENIED"
)

// --- API response wrapper ---

type apiResponse[T any] struct {
	Data T `json:"data"`
}

// --- Wallet Set ---

type CreateWalletSetRequest struct {
	IdempotencyKey         string `json:"idempotencyKey"`
	Name                   string `json:"name"`
	EntitySecretCiphertext string `json:"entitySecretCiphertext"`
}

type WalletSet struct {
	ID          string    `json:"id"`
	CustodyType string    `json:"custodyType"`
	Name        string    `json:"name,omitempty"`
	CreateDate  time.Time `json:"createDate"`
	UpdateDate  time.Time `json:"updateDate"`
}

type WalletSetData struct {
	WalletSet WalletSet `json:"walletSet"`
}

// --- Wallets ---

type WalletMetadata struct {
	Name  string `json:"name,omitempty"`
	RefID string `json:"refId,omitempty"`
}

type CreateWalletsRequest struct {
	IdempotencyKey         string           `json:"idempotencyKey"`
	EntitySecretCiphertext string           `json:"entitySecretCiphertext"`
	WalletSetID            string           `json:"walletSetId"`
	Blockchains            []Blockchain     `json:"blockchains"`
	Count                  int              `json:"count"`
	AccountType            string           `json:"accountType,omitempty"` // "EOA" or "SCA"
	Metadata               []WalletMetadata `json:"metadata,omitempty"`
}

type Wallet struct {
	ID          string      `json:"id"`
	Address     string      `json:"address"`
	Blockchain  Blockchain  `json:"blockchain"`
	State       WalletState `json:"state"`
	WalletSetID string      `json:"walletSetId"`
	CustodyType string      `json:"custodyType"`
	AccountType string      `json:"accountType,omitempty"`
	Name        string      `json:"name,omitempty"`
	RefID       string      `json:"refId,omitempty"`
	CreateDate  time.Time   `json:"createDate"`
	UpdateDate  time.Time   `json:"updateDate"`
}

type WalletsData struct {
	Wallets []Wallet `json:"wallets"`
}

type WalletData struct {
	Wallet Wallet `json:"wallet"`
}

// --- Token Balance ---

type TokenBalance struct {
	Token  TokenInfo `json:"token"`
	Amount string    `json:"amount"`
}

type TokenInfo struct {
	ID         string     `json:"id"`
	Blockchain Blockchain `json:"blockchain"`
	Name       string     `json:"name"`
	Symbol     string     `json:"symbol"`
	Decimals   int        `json:"decimals"`
	Standard   string     `json:"standard,omitempty"`
}

type TokenBalancesData struct {
	TokenBalances []TokenBalance `json:"tokenBalances"`
}

// --- Transfers ---

type FeeConfig struct {
	Type   string         `json:"type"`
	Config FeeConfigLevel `json:"config"`
}

type FeeConfigLevel struct {
	FeeLevel string `json:"feeLevel"` // LOW, MEDIUM, HIGH
}

type CreateTransferRequest struct {
	IdempotencyKey         string `json:"idempotencyKey"`
	EntitySecretCiphertext string `json:"entitySecretCiphertext"`
	// REST API fields (walletId + tokenId)
	WalletID string `json:"walletId,omitempty"`
	TokenID  string `json:"tokenId,omitempty"`
	// SDK-style fields (blockchain + walletAddress + tokenAddress)
	Blockchain         string     `json:"blockchain,omitempty"`
	WalletAddress      string     `json:"walletAddress,omitempty"`
	TokenAddress       string     `json:"tokenAddress,omitempty"`
	DestinationAddress string     `json:"destinationAddress"`
	Amounts            []string   `json:"amounts"`
	FeeLevel           string     `json:"feeLevel,omitempty"`
	Fee                *FeeConfig `json:"fee,omitempty"`
}

type Transaction struct {
	ID                 string           `json:"id"`
	State              TransactionState `json:"state"`
	TxHash             string           `json:"txHash,omitempty"`
	Blockchain         Blockchain       `json:"blockchain,omitempty"`
	WalletID           string           `json:"walletId,omitempty"`
	SourceAddress      string           `json:"sourceAddress,omitempty"`
	DestinationAddress string           `json:"destinationAddress,omitempty"`
	TokenID            string           `json:"tokenId,omitempty"`
	Amounts            []string         `json:"amounts,omitempty"`
	ErrorReason        string           `json:"errorReason,omitempty"`
	CreateDate         time.Time        `json:"createDate"`
	UpdateDate         time.Time        `json:"updateDate"`
}

type TransactionData struct {
	Transaction Transaction `json:"transaction"`
}

// --- Transaction Signing ---

type SignTransactionRequest struct {
	EntitySecretCiphertext string     `json:"entitySecretCiphertext"`
	WalletID               string     `json:"walletId,omitempty"`
	RawTransaction         string     `json:"rawTransaction,omitempty"`
	Transaction            string     `json:"transaction,omitempty"`
	Memo                   string     `json:"memo,omitempty"`
	Blockchain             Blockchain `json:"blockchain,omitempty"`
	WalletAddress          string     `json:"walletAddress,omitempty"`
}

type SignedTransaction struct {
	Signature         string `json:"signature"`
	SignedTransaction string `json:"signedTransaction"`
	TxHash            string `json:"txHash,omitempty"`
}

type SignedTransactionData struct {
	Signature         string `json:"signature"`
	SignedTransaction string `json:"signedTransaction"`
	TxHash            string `json:"txHash,omitempty"`
}

// --- Fee Estimation ---

type EstimateFeeRequest struct {
	WalletID           string   `json:"walletId"`
	TokenID            string   `json:"tokenId"`
	DestinationAddress string   `json:"destinationAddress"`
	Amounts            []string `json:"amounts"`
}

type FeeEstimate struct {
	Low    string `json:"low"`
	Medium string `json:"medium"`
	High   string `json:"high"`
}

// --- Entity Public Key ---

type EntityPublicKeyData struct {
	PublicKey string `json:"publicKey"`
}

// --- Contract Execution (arbitrary EVM contract calls) ---

// CreateContractExecutionRequest signs and broadcasts an EVM contract call from a Circle wallet.
// Used for non-transfer operations (e.g. ERC20 approve, DeFi protocol calls).
type CreateContractExecutionRequest struct {
	IdempotencyKey         string     `json:"idempotencyKey"`
	EntitySecretCiphertext string     `json:"entitySecretCiphertext"`
	WalletID               string     `json:"walletId"`
	ContractAddress        string     `json:"contractAddress"`
	AbiFunctionSignature   string     `json:"abiFunctionSignature,omitempty"`
	AbiParameters          []any      `json:"abiParameters,omitempty"`
	CallData               string     `json:"callData,omitempty"`
	Amount                 string     `json:"amount,omitempty"`
	FeeLevel               string     `json:"feeLevel,omitempty"`
	Fee                    *FeeConfig `json:"fee,omitempty"`
	RefID                  string     `json:"refId,omitempty"`
}
