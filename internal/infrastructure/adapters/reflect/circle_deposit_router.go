package reflect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	routeStatusPending           = "pending"
	routeStatusProcessing        = "processing"
	routeStatusTransferSubmitted = "transfer_submitted"
	routeStatusTransferFailed    = "transfer_failed"
	routeStatusTransferComplete  = "transfer_complete"
	routeStatusMinting           = "minting"
	routeStatusMintFailed        = "mint_failed"
	routeStatusComplete          = "complete"
)

// CircleYieldSource is the Circle wallet API surface required to route user stash funds into Reflect.
type CircleYieldSource interface {
	GetWallet(ctx context.Context, walletID string) (*circlepkg.Wallet, error)
	ListCircleWalletsByRefID(ctx context.Context, refID string) ([]circlepkg.Wallet, error)
	GetUSDCTokenID(ctx context.Context, walletID string) (string, error)
	TransferUSDCWithIdempotency(ctx context.Context, walletID, tokenID, destinationAddress, amount, idempotencyKey string) (*circlepkg.Transaction, error)
	GetTransaction(ctx context.Context, txID string) (*circlepkg.Transaction, error)
	SignTransaction(ctx context.Context, walletID, rawTransaction, memo string) (*circlepkg.SignedTransaction, error)
}

// ChainRailsBridge is the ChainRails API surface required for non-Solana Circle wallets.
type ChainRailsBridge interface {
	CreateIntent(ctx context.Context, req *chainrailspkg.CreateIntentRequest) (*chainrailspkg.CreateIntentResponse, error)
	GetIntentStatus(ctx context.Context, intentAddress string) (*chainrailspkg.IntentStatus, error)
}

// YieldLedger records user-visible debits that are caused by yield routing.
type YieldLedger interface {
	DebitChainRailsFee(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal) error
}

// CircleDepositRouter moves the stash side of a Circle-backed deposit into Reflect.
type CircleDepositRouter struct {
	db                         *sqlx.DB
	circle                     CircleYieldSource
	reflect                    *Client
	reflectWallet              string
	allowedProgramIDs          []string
	ledger                     YieldLedger
	chainRails                 ChainRailsBridge
	chainRailsDestinationChain string
	configMu                   sync.RWMutex
	retryInterval              time.Duration
	batchSize                  int
	logger                     *zap.Logger
	stopCh                     chan struct{}
	stopOnce                   sync.Once
	reconcileMutex             sync.Mutex
	schemaUnavailable          bool
}

type depositRoute struct {
	ID                      uuid.UUID           `db:"id"`
	DepositID               uuid.UUID           `db:"deposit_id"`
	UserID                  uuid.UUID           `db:"user_id"`
	CircleWalletID          string              `db:"circle_wallet_id"`
	YieldCircleWalletID     sql.NullString      `db:"yield_circle_wallet_id"`
	YieldWalletAddress      sql.NullString      `db:"yield_wallet_address"`
	Amount                  decimal.Decimal     `db:"amount"`
	Status                  string              `db:"status"`
	CircleTransferID        sql.NullString      `db:"circle_transfer_id"`
	CircleTxHash            sql.NullString      `db:"circle_tx_hash"`
	ChainRailsIntentID      sql.NullInt64       `db:"chainrails_intent_id"`
	ChainRailsIntentAddress sql.NullString      `db:"chainrails_intent_address"`
	ChainRailsSourceChain   sql.NullString      `db:"chainrails_source_chain"`
	ChainRailsDestChain     sql.NullString      `db:"chainrails_destination_chain"`
	ChainRailsFundAmount    decimal.NullDecimal `db:"chainrails_fund_amount"`
	ChainRailsFeeAmount     decimal.NullDecimal `db:"chainrails_fee_amount"`
	ChainRailsFeeDebitedAt  sql.NullTime        `db:"chainrails_fee_debited_at"`
	ChainRailsTxHash        sql.NullString      `db:"chainrails_tx_hash"`
	ReflectTxHash           sql.NullString      `db:"reflect_tx_hash"`
	Attempts                int                 `db:"attempts"`
}

type chainRailsSource struct {
	chain string
	token string
}

type userYieldWallet struct {
	CircleWalletID string
	Address        string
}

var circleBlockchainToChainRails = map[string]chainRailsSource{
	"ETH-SEPOLIA":  {"ETHEREUM_TESTNET", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"},
	"ETH":          {"ETHEREUM_MAINNET", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"},
	"BASE-SEPOLIA": {"BASE_TESTNET", "0x036CbD53842c5426634e7929541eC2318f3dCF7e"},
	"BASE":         {"BASE_MAINNET", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"},
	"MATIC-AMOY":   {"POLYGON_MAINNET", "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"},
	"MATIC":        {"POLYGON_MAINNET", "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"},
	"ARB-SEPOLIA":  {"ARBITRUM_TESTNET", "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d"},
	"ARB":          {"ARBITRUM_MAINNET", "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"},
	"OP-SEPOLIA":   {"OPTIMISM_TESTNET", "0x5fd84259d66Cd46123540766Be93DFE6D43130D7"},
	"OP":           {"OPTIMISM_MAINNET", "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85"},
	"AVAX-FUJI":    {"AVALANCHE_TESTNET", "0x5425890298aed601595a70AB815c96711a31Bc65"},
	"AVAX":         {"AVALANCHE_MAINNET", "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E"},
}

// NewCircleDepositRouter creates a Circle-backed Reflect deposit router.
func NewCircleDepositRouter(db *sqlx.DB, circle CircleYieldSource, reflectClient *Client, reflectWallet string, allowedProgramIDs []string, logger *zap.Logger) *CircleDepositRouter {
	return &CircleDepositRouter{
		db:                db,
		circle:            circle,
		reflect:           reflectClient,
		reflectWallet:     strings.TrimSpace(reflectWallet),
		allowedProgramIDs: normalizeProgramIDs(allowedProgramIDs),
		retryInterval:     30 * time.Second,
		batchSize:         25,
		logger:            logger,
		stopCh:            make(chan struct{}),
	}
}

// SetYieldLedger wires ledger fee accounting for ChainRails-funded Reflect routes.
func (r *CircleDepositRouter) SetYieldLedger(ledger YieldLedger) {
	if r == nil {
		return
	}
	r.ledger = ledger
}

// SetChainRailsBridge enables cross-chain settlement for non-Solana Circle wallets.
func (r *CircleDepositRouter) SetChainRailsBridge(client ChainRailsBridge, destinationChain string) {
	if r == nil {
		return
	}
	r.configMu.Lock()
	defer r.configMu.Unlock()
	r.chainRails = client
	r.chainRailsDestinationChain = strings.TrimSpace(destinationChain)
}

// Start begins background retry for routes that failed after allocation.
func (r *CircleDepositRouter) Start() {
	ticker := time.NewTicker(r.retryInterval)
	go func() {
		defer ticker.Stop()
		r.processPending(context.Background())
		for {
			select {
			case <-ticker.C:
				r.processPending(context.Background())
			case <-r.stopCh:
				return
			}
		}
	}()
}

// Stop stops background retry.
func (r *CircleDepositRouter) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
}

// RouteDepositYield idempotently routes a deposit's stash allocation into Reflect.
func (r *CircleDepositRouter) RouteDepositYield(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal, metadata map[string]any) error {
	if err := r.EnsureDepositYieldRoute(ctx, userID, depositID, amount, metadata); err != nil {
		return err
	}
	route, err := r.getRoute(ctx, depositID)
	if err != nil {
		return err
	}
	return r.processRoute(ctx, route)
}

// EnsureDepositYieldRoute records the durable route before asynchronous settlement starts.
func (r *CircleDepositRouter) EnsureDepositYieldRoute(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal, metadata map[string]any) error {
	if r == nil || r.db == nil || r.circle == nil || r.reflect == nil {
		return fmt.Errorf("reflect deposit router is not configured")
	}
	if userID == uuid.Nil || depositID == uuid.Nil {
		return fmt.Errorf("user_id and deposit_id are required")
	}
	if !amount.GreaterThan(decimal.Zero) {
		return fmt.Errorf("amount must be positive")
	}
	circleWalletID := metadataString(metadata, "circle_wallet_id")
	if circleWalletID == "" {
		return fmt.Errorf("circle_wallet_id is required for Circle-backed Reflect routing")
	}
	if err := r.ensureRoute(ctx, userID, depositID, circleWalletID, amount); err != nil {
		return err
	}
	return nil
}

func (r *CircleDepositRouter) ensureRoute(ctx context.Context, userID, depositID uuid.UUID, circleWalletID string, amount decimal.Decimal) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO reflect_deposit_routes (
			id, deposit_id, user_id, circle_wallet_id, amount, status, next_retry_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (deposit_id) DO NOTHING
	`, uuid.New(), depositID, userID, circleWalletID, amount, routeStatusPending)
	if err != nil {
		return fmt.Errorf("create reflect deposit route: %w", err)
	}
	return nil
}

func (r *CircleDepositRouter) getRoute(ctx context.Context, depositID uuid.UUID) (*depositRoute, error) {
	var route depositRoute
	if err := r.db.GetContext(ctx, &route, `
		SELECT id, deposit_id, user_id, circle_wallet_id, yield_circle_wallet_id,
			yield_wallet_address, amount, status,
			circle_transfer_id, circle_tx_hash, chainrails_intent_id, chainrails_intent_address,
			chainrails_source_chain, chainrails_destination_chain, chainrails_fund_amount,
			chainrails_fee_amount, chainrails_fee_debited_at,
			chainrails_tx_hash, reflect_tx_hash, attempts
		FROM reflect_deposit_routes
		WHERE deposit_id = $1
	`, depositID); err != nil {
		return nil, fmt.Errorf("get reflect deposit route: %w", err)
	}
	return &route, nil
}

func (r *CircleDepositRouter) processRoute(ctx context.Context, route *depositRoute) error {
	if route.Status == routeStatusComplete {
		return nil
	}
	if route.Status == routeStatusMinting && !route.ReflectTxHash.Valid {
		return fmt.Errorf("reflect route %s is already minting and requires reconciliation before retry", route.DepositID)
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE reflect_deposit_routes
		SET status = $2, attempts = attempts + 1, last_error = NULL, updated_at = NOW()
		WHERE deposit_id = $1 AND status <> $3
	`, route.DepositID, routeStatusProcessing, routeStatusComplete); err != nil {
		return fmt.Errorf("mark reflect route processing: %w", err)
	}

	sourceWallet, err := r.circle.GetWallet(ctx, route.CircleWalletID)
	if err != nil {
		r.markFailed(ctx, route.DepositID, routeStatusTransferFailed, err)
		return fmt.Errorf("get source Circle wallet: %w", err)
	}
	yieldWallet, sourceIsSolana, err := r.resolveYieldWallet(ctx, route, sourceWallet)
	if err != nil {
		r.markFailed(ctx, route.DepositID, routeStatusTransferFailed, err)
		return err
	}
	if err := r.recordYieldWallet(ctx, route.DepositID, yieldWallet); err != nil {
		return err
	}

	transferID := strings.TrimSpace(route.CircleTransferID.String)
	var transfer *circlepkg.Transaction
	if transferID == "" && !sourceIsSolana {
		tokenID, tokenErr := r.circle.GetUSDCTokenID(ctx, route.CircleWalletID)
		if tokenErr != nil {
			r.markFailed(ctx, route.DepositID, routeStatusTransferFailed, tokenErr)
			return fmt.Errorf("get Circle USDC token id: %w", tokenErr)
		}

		destinationAddress, fundingAmount, prepareErr := r.prepareCircleTransfer(ctx, route, sourceWallet, yieldWallet)
		if prepareErr != nil {
			r.markFailed(ctx, route.DepositID, routeStatusTransferFailed, prepareErr)
			return prepareErr
		}

		transfer, err = r.circle.TransferUSDCWithIdempotency(
			ctx,
			route.CircleWalletID,
			tokenID,
			destinationAddress,
			fundingAmount,
			"reflect-deposit-"+route.DepositID.String(),
		)
		if err != nil {
			r.markFailed(ctx, route.DepositID, routeStatusTransferFailed, err)
			return fmt.Errorf("circle transfer for Reflect routing: %w", err)
		}
		transferID = transfer.ID
		if _, updateErr := r.db.ExecContext(ctx, `
			UPDATE reflect_deposit_routes
			SET status = $2, circle_transfer_id = $3, updated_at = NOW()
			WHERE deposit_id = $1
		`, route.DepositID, routeStatusTransferSubmitted, transferID); updateErr != nil {
			return fmt.Errorf("record Circle transfer id: %w", updateErr)
		}
	}

	if transferID != "" {
		transfer, err = r.waitForCircleTransfer(ctx, transferID, transfer)
		if err != nil {
			r.markRetry(ctx, route.DepositID, routeStatusTransferSubmitted, err)
			return err
		}
		if _, err := r.db.ExecContext(ctx, `
			UPDATE reflect_deposit_routes
			SET status = $2, circle_tx_hash = $3, updated_at = NOW()
			WHERE deposit_id = $1
		`, route.DepositID, routeStatusTransferComplete, nullableString(transfer.TxHash)); err != nil {
			return fmt.Errorf("record Circle transfer completion: %w", err)
		}
	} else if sourceIsSolana {
		if _, err := r.db.ExecContext(ctx, `
			UPDATE reflect_deposit_routes
			SET status = $2, updated_at = NOW()
			WHERE deposit_id = $1
		`, route.DepositID, routeStatusTransferComplete); err != nil {
			return fmt.Errorf("mark Solana Circle route ready for mint: %w", err)
		}
	}

	chainRailsTxHash, err := r.waitForChainRailsSettlement(ctx, route)
	if err != nil {
		r.markRetry(ctx, route.DepositID, routeStatusTransferComplete, err)
		return err
	}
	if chainRailsTxHash != "" {
		if _, err := r.db.ExecContext(ctx, `
			UPDATE reflect_deposit_routes
			SET chainrails_tx_hash = $2, updated_at = NOW()
			WHERE deposit_id = $1
		`, route.DepositID, chainRailsTxHash); err != nil {
			return fmt.Errorf("record ChainRails settlement tx: %w", err)
		}
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE reflect_deposit_routes
		SET status = $2, updated_at = NOW()
		WHERE deposit_id = $1
	`, route.DepositID, routeStatusMinting); err != nil {
		return fmt.Errorf("mark reflect route minting: %w", err)
	}

	reflectTxHash, err := r.mintWithUserWallet(ctx, route, yieldWallet)
	if err != nil {
		r.markFailed(ctx, route.DepositID, routeStatusMintFailed, err)
		return err
	}

	if err := r.recordMint(ctx, route, reflectTxHash); err != nil {
		return err
	}

	r.logger.Info("Circle deposit routed into Reflect",
		zap.String("deposit_id", route.DepositID.String()),
		zap.String("user_id", route.UserID.String()),
		zap.String("amount", route.Amount.StringFixed(6)),
		zap.String("circle_transfer_id", transferID),
		zap.String("reflect_tx", reflectTxHash))
	return nil
}

func (r *CircleDepositRouter) resolveYieldWallet(ctx context.Context, route *depositRoute, sourceWallet *circlepkg.Wallet) (userYieldWallet, bool, error) {
	if route.YieldCircleWalletID.Valid && strings.TrimSpace(route.YieldCircleWalletID.String) != "" &&
		route.YieldWalletAddress.Valid && strings.TrimSpace(route.YieldWalletAddress.String) != "" {
		sourceIsSolana := isSolanaCircleChain(strings.ToUpper(strings.TrimSpace(string(sourceWallet.Blockchain))))
		return userYieldWallet{
			CircleWalletID: strings.TrimSpace(route.YieldCircleWalletID.String),
			Address:        strings.TrimSpace(route.YieldWalletAddress.String),
		}, sourceIsSolana, nil
	}

	sourceBlockchain := strings.ToUpper(strings.TrimSpace(string(sourceWallet.Blockchain)))
	if isSolanaCircleChain(sourceBlockchain) {
		if strings.TrimSpace(sourceWallet.Address) == "" {
			return userYieldWallet{}, true, fmt.Errorf("source Solana Circle wallet %s has no address", sourceWallet.ID)
		}
		return userYieldWallet{CircleWalletID: sourceWallet.ID, Address: sourceWallet.Address}, true, nil
	}

	wallet, err := r.findUserSolanaWallet(ctx, route.UserID)
	if err != nil {
		return userYieldWallet{}, false, err
	}
	return wallet, false, nil
}

func (r *CircleDepositRouter) findUserSolanaWallet(ctx context.Context, userID uuid.UUID) (userYieldWallet, error) {
	var dbWallet struct {
		CircleWalletID sql.NullString `db:"circle_wallet_id"`
		Address        sql.NullString `db:"address"`
	}
	if err := r.db.GetContext(ctx, &dbWallet, `
		SELECT circle_wallet_id, address
		FROM managed_wallets
		WHERE user_id = $1
			AND chain IN ('SOL', 'SOL-DEVNET', 'solana')
			AND COALESCE(circle_wallet_id, '') <> ''
			AND COALESCE(address, '') <> ''
		ORDER BY CASE WHEN status = 'live' THEN 0 ELSE 1 END, created_at ASC
		LIMIT 1
	`, userID); err == nil {
		return userYieldWallet{
			CircleWalletID: strings.TrimSpace(dbWallet.CircleWalletID.String),
			Address:        strings.TrimSpace(dbWallet.Address.String),
		}, nil
	} else if err != sql.ErrNoRows {
		return userYieldWallet{}, fmt.Errorf("lookup user Solana managed wallet: %w", err)
	}

	if err := r.db.GetContext(ctx, &dbWallet, `
		SELECT circle_wallet_id, address
		FROM wallets
		WHERE user_id = $1
			AND chain IN ('SOL', 'SOL-DEVNET', 'solana')
			AND COALESCE(circle_wallet_id, '') <> ''
			AND COALESCE(address, '') <> ''
		ORDER BY CASE WHEN status = 'live' THEN 0 ELSE 1 END, created_at ASC
		LIMIT 1
	`, userID); err == nil {
		return userYieldWallet{
			CircleWalletID: strings.TrimSpace(dbWallet.CircleWalletID.String),
			Address:        strings.TrimSpace(dbWallet.Address.String),
		}, nil
	} else if err != sql.ErrNoRows {
		return userYieldWallet{}, fmt.Errorf("lookup user Solana wallet: %w", err)
	}

	circleWallets, err := r.circle.ListCircleWalletsByRefID(ctx, userID.String())
	if err != nil {
		return userYieldWallet{}, fmt.Errorf("lookup Circle wallets by user refId: %w", err)
	}
	for _, wallet := range circleWallets {
		if isSolanaCircleChain(strings.ToUpper(strings.TrimSpace(string(wallet.Blockchain)))) &&
			strings.TrimSpace(wallet.ID) != "" &&
			strings.TrimSpace(wallet.Address) != "" {
			return userYieldWallet{CircleWalletID: wallet.ID, Address: wallet.Address}, nil
		}
	}
	return userYieldWallet{}, fmt.Errorf("user %s has no Solana Circle wallet for Reflect yield", userID)
}

func (r *CircleDepositRouter) recordYieldWallet(ctx context.Context, depositID uuid.UUID, wallet userYieldWallet) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE reflect_deposit_routes
		SET yield_circle_wallet_id = $2, yield_wallet_address = $3, updated_at = NOW()
		WHERE deposit_id = $1
	`, depositID, wallet.CircleWalletID, wallet.Address)
	if err != nil {
		return fmt.Errorf("record Reflect yield wallet: %w", err)
	}
	return nil
}

func (r *CircleDepositRouter) mintWithUserWallet(ctx context.Context, route *depositRoute, wallet userYieldWallet) (string, error) {
	rawTransaction, err := r.reflect.GenerateMintTransaction(ctx, route.Amount, wallet.Address, wallet.Address)
	if err != nil {
		return "", fmt.Errorf("reflect generate user mint transaction: %w", err)
	}
	if err := validateReflectUserMintTransaction(rawTransaction, wallet.Address, route.Amount, r.allowedProgramIDs); err != nil {
		return "", fmt.Errorf("refusing unsafe Reflect mint transaction: %w", err)
	}
	signed, err := r.circle.SignTransaction(ctx, wallet.CircleWalletID, rawTransaction, "Deposit USDC into Reflect yield")
	if err != nil {
		return "", fmt.Errorf("Circle sign Reflect mint transaction: %w", err)
	}
	if strings.TrimSpace(signed.SignedTransaction) == "" {
		return "", fmt.Errorf("Circle returned empty signed Reflect mint transaction")
	}
	txHash, err := r.reflect.SubmitSignedTransaction(ctx, signed.SignedTransaction)
	if err != nil {
		return "", fmt.Errorf("reflect submit user-signed mint transaction: %w", err)
	}
	return txHash, nil
}

// RedeemStashYield burns user-held Reflect receipt tokens before a stash withdrawal spends USDC.
func (r *CircleDepositRouter) RedeemStashYield(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if r == nil || r.db == nil || r.circle == nil || r.reflect == nil {
		return fmt.Errorf("reflect deposit router is not configured")
	}
	if userID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}
	if !amount.GreaterThan(decimal.Zero) {
		return fmt.Errorf("amount must be positive")
	}
	var available decimal.Decimal
	if err := r.db.GetContext(ctx, &available, `
		SELECT COALESCE(SUM(principal_amount - redeemed_amount), 0)
		FROM user_yield_positions
		WHERE user_id = $1
			AND status = 'active'
	`, userID); err != nil {
		return fmt.Errorf("read user yield position: %w", err)
	}
	if available.LessThan(amount.Truncate(6)) {
		return fmt.Errorf("insufficient Reflect yield position: have %s, need %s", available.StringFixed(6), amount.Truncate(6).StringFixed(6))
	}
	if err := r.reserveUserRedemption(ctx, userID, amount.Truncate(6), idempotencyKey); err != nil {
		return err
	}
	wallet, err := r.findUserSolanaWallet(ctx, userID)
	if err != nil {
		return err
	}
	rawTransaction, err := r.reflect.GenerateBurnTransaction(ctx, amount, wallet.Address, wallet.Address)
	if err != nil {
		return fmt.Errorf("reflect generate user burn transaction: %w", err)
	}
	if err := validateReflectUserBurnTransaction(rawTransaction, wallet.Address, r.allowedProgramIDs); err != nil {
		return fmt.Errorf("refusing unsafe Reflect burn transaction: %w", err)
	}
	signed, err := r.circle.SignTransaction(ctx, wallet.CircleWalletID, rawTransaction, "Redeem Reflect yield for stash withdrawal")
	if err != nil {
		return fmt.Errorf("Circle sign Reflect burn transaction: %w", err)
	}
	if strings.TrimSpace(signed.SignedTransaction) == "" {
		return fmt.Errorf("Circle returned empty signed Reflect burn transaction")
	}
	txHash, err := r.reflect.SubmitSignedTransaction(ctx, signed.SignedTransaction)
	if err != nil {
		return fmt.Errorf("reflect submit user-signed burn transaction: %w", err)
	}
	if err := r.recordUserRedemption(ctx, userID, amount, txHash, idempotencyKey); err != nil {
		return err
	}
	return nil
}

func (r *CircleDepositRouter) prepareCircleTransfer(ctx context.Context, route *depositRoute, sourceWallet *circlepkg.Wallet, yieldWallet userYieldWallet) (string, string, error) {
	if route.ChainRailsIntentAddress.Valid && strings.TrimSpace(route.ChainRailsIntentAddress.String) != "" {
		fundAmount := route.Amount.Truncate(6)
		if route.ChainRailsFundAmount.Valid {
			fundAmount = route.ChainRailsFundAmount.Decimal
		}
		if err := r.ensureChainRailsFeeDebited(ctx, route, fundAmount.Sub(route.Amount.Truncate(6))); err != nil {
			return "", "", err
		}
		return strings.TrimSpace(route.ChainRailsIntentAddress.String), fundAmount.StringFixed(6), nil
	}

	blockchain := strings.ToUpper(strings.TrimSpace(string(sourceWallet.Blockchain)))
	if blockchain == "" {
		return "", "", fmt.Errorf("Circle wallet %s has no blockchain", route.CircleWalletID)
	}
	if sourceWallet.Address == "" {
		return "", "", fmt.Errorf("Circle wallet %s has no on-chain address", route.CircleWalletID)
	}

	intent, fundAmount, err := r.createChainRailsIntent(ctx, route, sourceWallet, yieldWallet, blockchain)
	if err != nil {
		return "", "", err
	}
	feeAmount := fundAmount.Sub(route.Amount.Truncate(6))
	if _, err := r.db.ExecContext(ctx, `
		UPDATE reflect_deposit_routes
		SET chainrails_intent_id = $2,
			chainrails_intent_address = $3,
			chainrails_source_chain = $4,
			chainrails_destination_chain = $5,
			chainrails_fund_amount = $6,
			chainrails_fee_amount = $7,
			updated_at = NOW()
		WHERE deposit_id = $1
	`, route.DepositID, intent.ID, intent.IntentAddress, intent.SourceChain, intent.DestinationChain, fundAmount, feeAmount); err != nil {
		return "", "", fmt.Errorf("record ChainRails intent: %w", err)
	}

	route.ChainRailsIntentID = sql.NullInt64{Int64: int64(intent.ID), Valid: true}
	route.ChainRailsIntentAddress = nullableString(intent.IntentAddress)
	route.ChainRailsSourceChain = nullableString(intent.SourceChain)
	route.ChainRailsDestChain = nullableString(intent.DestinationChain)
	route.ChainRailsFundAmount = decimal.NullDecimal{Decimal: fundAmount, Valid: true}
	route.ChainRailsFeeAmount = decimal.NullDecimal{Decimal: feeAmount, Valid: feeAmount.GreaterThan(decimal.Zero)}

	if err := r.ensureChainRailsFeeDebited(ctx, route, feeAmount); err != nil {
		return "", "", err
	}

	return intent.IntentAddress, fundAmount.StringFixed(6), nil
}

func (r *CircleDepositRouter) ensureChainRailsFeeDebited(ctx context.Context, route *depositRoute, feeAmount decimal.Decimal) error {
	feeAmount = feeAmount.Truncate(6)
	if !feeAmount.GreaterThan(decimal.Zero) {
		return nil
	}
	if route.ChainRailsFeeDebitedAt.Valid {
		return nil
	}
	if r.ledger == nil {
		return fmt.Errorf("ChainRails fee %s for Reflect route %s requires ledger accounting but no yield ledger is configured", feeAmount.StringFixed(6), route.DepositID)
	}
	if err := r.ledger.DebitChainRailsFee(ctx, route.UserID, route.DepositID, feeAmount); err != nil {
		return fmt.Errorf("debit ChainRails Reflect routing fee: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE reflect_deposit_routes
		SET chainrails_fee_amount = $2, chainrails_fee_debited_at = NOW(), updated_at = NOW()
		WHERE deposit_id = $1
	`, route.DepositID, feeAmount); err != nil {
		return fmt.Errorf("record ChainRails fee debit: %w", err)
	}
	route.ChainRailsFeeAmount = decimal.NullDecimal{Decimal: feeAmount, Valid: true}
	route.ChainRailsFeeDebitedAt = sql.NullTime{Time: time.Now(), Valid: true}
	return nil
}

func (r *CircleDepositRouter) createChainRailsIntent(ctx context.Context, route *depositRoute, wallet *circlepkg.Wallet, yieldWallet userYieldWallet, blockchain string) (*chainrailspkg.CreateIntentResponse, decimal.Decimal, error) {
	chainRails, destinationChain := r.getChainRailsConfig()
	if chainRails == nil {
		return nil, decimal.Zero, fmt.Errorf("ChainRails is required to route %s Circle wallet deposits into Reflect", blockchain)
	}
	if strings.TrimSpace(yieldWallet.Address) == "" {
		return nil, decimal.Zero, fmt.Errorf("user Solana Circle wallet address is required for Reflect routing")
	}

	source, ok := circleBlockchainToChainRails[blockchain]
	if !ok {
		return nil, decimal.Zero, fmt.Errorf("unsupported Circle blockchain for Reflect ChainRails routing: %s", blockchain)
	}
	destinationChain = normalizeReflectChainRailsDestination(source.chain, destinationChain)
	amountMicro := route.Amount.Truncate(6).Shift(6).IntPart()

	intent, err := chainRails.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Sender:           wallet.Address,
		Amount:           fmt.Sprintf("%d", amountMicro),
		AmountSymbol:     "USDC",
		TokenIn:          source.token,
		SourceChain:      source.chain,
		DestinationChain: destinationChain,
		Recipient:        yieldWallet.Address,
		RefundAddress:    wallet.Address,
		Metadata: map[string]interface{}{
			"deposit_id": route.DepositID.String(),
			"user_id":    route.UserID.String(),
			"route_id":   route.ID.String(),
			"type":       "reflect_deposit_route",
		},
	})
	if err != nil {
		return nil, decimal.Zero, fmt.Errorf("create ChainRails intent for Reflect route: %w", err)
	}
	if strings.TrimSpace(intent.IntentAddress) == "" {
		return nil, decimal.Zero, fmt.Errorf("ChainRails intent %d has no intent address", intent.ID)
	}

	fundAmount, err := chainRailsFundingAmount(intent, route.Amount)
	if err != nil {
		return nil, decimal.Zero, err
	}

	r.logger.Info("ChainRails intent created for Reflect deposit route",
		zap.String("deposit_id", route.DepositID.String()),
		zap.Int("intent_id", intent.ID),
		zap.String("intent_address", intent.IntentAddress),
		zap.String("source_chain", intent.SourceChain),
		zap.String("destination_chain", intent.DestinationChain),
		zap.String("fund_amount", fundAmount.StringFixed(6)))

	return intent, fundAmount, nil
}

func (r *CircleDepositRouter) getChainRailsConfig() (ChainRailsBridge, string) {
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	return r.chainRails, r.chainRailsDestinationChain
}

func (r *CircleDepositRouter) waitForCircleTransfer(ctx context.Context, transferID string, initial *circlepkg.Transaction) (*circlepkg.Transaction, error) {
	if initial != nil && isCircleTransferComplete(initial.State) {
		return initial, nil
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("circle transfer %s did not complete before timeout: %w", transferID, ctx.Err())
		case <-ticker.C:
			tx, err := r.circle.GetTransaction(ctx, transferID)
			if err != nil {
				r.logger.Warn("Failed to poll Circle transfer", zap.String("transfer_id", transferID), zap.Error(err))
				continue
			}
			switch {
			case isCircleTransferComplete(tx.State):
				return tx, nil
			case isCircleTransferTerminalFailure(tx.State):
				return nil, fmt.Errorf("circle transfer %s failed with state %s: %s", transferID, tx.State, tx.ErrorReason)
			default:
				continue
			}
		}
	}
}

func (r *CircleDepositRouter) waitForChainRailsSettlement(ctx context.Context, route *depositRoute) (string, error) {
	intentAddress := strings.TrimSpace(route.ChainRailsIntentAddress.String)
	if intentAddress == "" {
		return "", nil
	}
	chainRails, _ := r.getChainRailsConfig()
	if chainRails == nil {
		return "", fmt.Errorf("ChainRails is not configured to reconcile Reflect route %s", route.DepositID)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("ChainRails intent %s did not settle before timeout: %w", intentAddress, ctx.Err())
		case <-ticker.C:
			status, err := chainRails.GetIntentStatus(ctx, intentAddress)
			if err != nil {
				r.logger.Warn("Failed to poll ChainRails intent",
					zap.String("deposit_id", route.DepositID.String()),
					zap.String("intent_address", intentAddress),
					zap.Error(err))
				continue
			}
			switch {
			case isChainRailsComplete(status.Status):
				return strings.TrimSpace(status.TxHash), nil
			case isChainRailsTerminalFailure(status.Status):
				return "", fmt.Errorf("ChainRails intent %s failed with status %s", intentAddress, status.Status)
			default:
				continue
			}
		}
	}
}

func (r *CircleDepositRouter) recordMint(ctx context.Context, route *depositRoute, reflectTxHash string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reflect route tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_yield_positions (
			id, user_id, route_id, deposit_id, yield_circle_wallet_id,
			yield_wallet_address, principal_amount, receipt_tx_hash, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active')
		ON CONFLICT (route_id) DO NOTHING
	`, uuid.New(), route.UserID, route.ID, route.DepositID, route.YieldCircleWalletID, route.YieldWalletAddress, route.Amount, reflectTxHash); err != nil {
		return fmt.Errorf("insert user yield position: %w", err)
	}

	if err := recomputeReflectDepositedUSDC(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE reflect_deposit_routes
		SET status = $2, reflect_tx_hash = $3, last_error = NULL, updated_at = NOW()
		WHERE deposit_id = $1
	`, route.DepositID, routeStatusComplete, reflectTxHash); err != nil {
		return fmt.Errorf("mark reflect route complete: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reflect route tx: %w", err)
	}
	return nil
}

func (r *CircleDepositRouter) reserveUserRedemption(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return fmt.Errorf("redemption idempotency key is required")
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO user_yield_redemptions (id, user_id, amount, tx_hash, idempotency_key, status)
		VALUES ($1, $2, $3, '', $4, 'pending')
		ON CONFLICT (idempotency_key) DO NOTHING
	`, uuid.New(), userID, amount, idempotencyKey)
	if err != nil {
		return fmt.Errorf("reserve user yield redemption: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows > 0 {
		return nil
	}
	var status string
	if err := r.db.GetContext(ctx, &status, `
		SELECT status
		FROM user_yield_redemptions
		WHERE idempotency_key = $1
	`, idempotencyKey); err != nil {
		return fmt.Errorf("read existing user yield redemption: %w", err)
	}
	if strings.EqualFold(status, "complete") {
		return fmt.Errorf("Reflect redemption %s has already completed", idempotencyKey)
	}
	return fmt.Errorf("Reflect redemption %s is already pending reconciliation", idempotencyKey)
}

func (r *CircleDepositRouter) recordUserRedemption(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reflectTxHash, idempotencyKey string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reflect redemption tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE user_yield_redemptions
		SET tx_hash = $2, status = 'complete'
		WHERE idempotency_key = $3
			AND user_id = $1
			AND status = 'pending'
	`, userID, reflectTxHash, strings.TrimSpace(idempotencyKey))
	if err != nil {
		return fmt.Errorf("complete user yield redemption: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("user yield redemption %s was not pending", strings.TrimSpace(idempotencyKey))
	}
	if _, err := tx.ExecContext(ctx, `
		WITH selected AS (
			SELECT id, principal_amount, redeemed_amount,
				SUM(principal_amount - redeemed_amount) OVER (ORDER BY created_at, id) AS running_available
			FROM user_yield_positions
			WHERE user_id = $1
				AND status = 'active'
				AND principal_amount > redeemed_amount
			ORDER BY created_at, id
		), applied AS (
			SELECT id,
				LEAST(
					principal_amount - redeemed_amount,
					GREATEST($2::numeric - COALESCE(running_available - (principal_amount - redeemed_amount), 0), 0)
				) AS redeem_amount
			FROM selected
			WHERE running_available - (principal_amount - redeemed_amount) < $2::numeric
		)
		UPDATE user_yield_positions p
		SET redeemed_amount = p.redeemed_amount + applied.redeem_amount,
			status = CASE WHEN p.principal_amount <= p.redeemed_amount + applied.redeem_amount THEN 'redeemed' ELSE p.status END,
			updated_at = NOW()
		FROM applied
		WHERE p.id = applied.id
			AND applied.redeem_amount > 0
	`, userID, amount); err != nil {
		return fmt.Errorf("apply user yield redemption: %w", err)
	}
	if err := recomputeReflectDepositedUSDC(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reflect redemption tx: %w", err)
	}
	return nil
}

func recomputeReflectDepositedUSDC(ctx context.Context, tx *sqlx.Tx) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE yield_state
		SET value = (
			SELECT COALESCE(SUM(principal_amount - redeemed_amount), 0)
			FROM user_yield_positions
			WHERE status = 'active'
		) + (
			SELECT COALESCE(SUM(CASE
				WHEN operation = 'deposit' THEN amount
				WHEN operation = 'withdraw' THEN -amount
				ELSE 0
			END), 0)
			FROM treasury_positions
		),
			updated_at = NOW()
		WHERE key = 'reflect_deposited_usdc'
	`)
	if err != nil {
		return fmt.Errorf("recompute reflect deposited usdc: %w", err)
	}
	return nil
}

func (r *CircleDepositRouter) processPending(ctx context.Context) {
	if r == nil || r.db == nil {
		return
	}
	if r.schemaUnavailable {
		return
	}
	if !r.reconcileMutex.TryLock() {
		return
	}
	defer r.reconcileMutex.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var routes []depositRoute
	if err := r.db.SelectContext(ctx, &routes, `
		SELECT id, deposit_id, user_id, circle_wallet_id, yield_circle_wallet_id,
			yield_wallet_address, amount, status,
			circle_transfer_id, circle_tx_hash, chainrails_intent_id, chainrails_intent_address,
			chainrails_source_chain, chainrails_destination_chain, chainrails_fund_amount,
			chainrails_fee_amount, chainrails_fee_debited_at,
			chainrails_tx_hash, reflect_tx_hash, attempts
		FROM reflect_deposit_routes
		WHERE status IN ($1, $2, $3, $4, $5)
			AND next_retry_at <= NOW()
			AND attempts < 10
		ORDER BY created_at ASC
		LIMIT $6
	`, routeStatusPending, routeStatusTransferFailed, routeStatusTransferSubmitted, routeStatusTransferComplete, routeStatusMintFailed, r.batchSize); err != nil {
		if isUndefinedTableError(err) {
			r.schemaUnavailable = true
			r.logger.Warn("Reflect deposit router disabled because required tables are missing; apply migration 189 before enabling it", zap.Error(err))
			return
		}
		r.logger.Error("Failed to list pending Reflect deposit routes", zap.Error(err))
		return
	}

	for i := range routes {
		route := routes[i]
		if err := r.processRoute(ctx, &route); err != nil {
			r.logger.Error("Reflect deposit route retry failed",
				zap.String("deposit_id", route.DepositID.String()),
				zap.String("status", route.Status),
				zap.Error(err))
		}
	}
}

func isUndefinedTableError(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "42P01"
}

func (r *CircleDepositRouter) markFailed(ctx context.Context, depositID uuid.UUID, status string, err error) {
	if updateErr := r.updateRouteError(ctx, depositID, status, err, time.Now().Add(time.Minute)); updateErr != nil {
		r.logger.Error("Failed to mark Reflect route error", zap.String("deposit_id", depositID.String()), zap.Error(updateErr))
	}
}

func (r *CircleDepositRouter) markRetry(ctx context.Context, depositID uuid.UUID, status string, err error) {
	if updateErr := r.updateRouteError(ctx, depositID, status, err, time.Now().Add(30*time.Second)); updateErr != nil {
		r.logger.Error("Failed to schedule Reflect route retry", zap.String("deposit_id", depositID.String()), zap.Error(updateErr))
	}
}

func (r *CircleDepositRouter) updateRouteError(ctx context.Context, depositID uuid.UUID, status string, routeErr error, nextRetry time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE reflect_deposit_routes
		SET status = $2, last_error = $3, next_retry_at = $4, updated_at = NOW()
		WHERE deposit_id = $1
	`, depositID, status, routeErr.Error(), nextRetry)
	return err
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func nullableString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: strings.TrimSpace(v) != ""}
}

func normalizeProgramIDs(ids []string) []string {
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			normalized = append(normalized, id)
		}
	}
	return normalized
}

func isCircleTransferComplete(state circlepkg.TransactionState) bool {
	return state == circlepkg.TransactionStateComplete || strings.EqualFold(string(state), "COMPLETED")
}

func isCircleTransferTerminalFailure(state circlepkg.TransactionState) bool {
	return state == circlepkg.TransactionStateFailed ||
		state == circlepkg.TransactionStateCancelled ||
		state == circlepkg.TransactionStateDenied
}

func isSolanaCircleChain(blockchain string) bool {
	return blockchain == string(circlepkg.BlockchainSOL) || blockchain == string(circlepkg.BlockchainSOLDevnet)
}

func normalizeReflectChainRailsDestination(sourceChain, configured string) string {
	destination := strings.TrimSpace(configured)
	if destination == "" {
		destination = "SOLANA_MAINNET"
	}
	if strings.Contains(sourceChain, "TESTNET") && strings.Contains(destination, "MAINNET") {
		return strings.Replace(destination, "MAINNET", "TESTNET", 1)
	}
	return destination
}

func chainRailsFundingAmount(intent *chainrailspkg.CreateIntentResponse, requested decimal.Decimal) (decimal.Decimal, error) {
	if strings.TrimSpace(intent.TotalAmountInAssetToken) == "" || intent.AssetTokenDecimals <= 0 {
		return decimal.Zero, fmt.Errorf("ChainRails intent %d did not return total funding amount", intent.ID)
	}
	totalUnits, ok := new(big.Int).SetString(intent.TotalAmountInAssetToken, 10)
	if !ok {
		return decimal.Zero, fmt.Errorf("failed to parse ChainRails funding amount %q", intent.TotalAmountInAssetToken)
	}
	amount := decimal.NewFromBigInt(totalUnits, -int32(intent.AssetTokenDecimals))
	if !amount.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("ChainRails intent %d returned non-positive funding amount %s", intent.ID, amount.String())
	}
	if amount.LessThan(requested.Truncate(6)) {
		return decimal.Zero, fmt.Errorf("ChainRails intent %d funding amount %s is less than requested amount %s", intent.ID, amount.StringFixed(6), requested.Truncate(6).StringFixed(6))
	}
	return amount, nil
}

func isChainRailsComplete(status string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	return normalized == "COMPLETED" ||
		normalized == "COMPLETE" ||
		normalized == "SUCCESS" ||
		normalized == "SUCCEEDED" ||
		normalized == "SETTLED" ||
		normalized == "FULFILLED"
}

func isChainRailsTerminalFailure(status string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	return normalized == "FAILED" ||
		normalized == "FAILURE" ||
		normalized == "CANCELLED" ||
		normalized == "CANCELED" ||
		normalized == "EXPIRED" ||
		normalized == "REJECTED" ||
		normalized == "REFUNDED"
}
