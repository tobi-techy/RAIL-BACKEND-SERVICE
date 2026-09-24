package investmentowner

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
)

// CircleSigner uses the user's own custody wallet as the portfolio owner.
//
// This is the production posture: the owner account is the user's Circle Solana
// wallet, so the smart account is owned by the user's key and Rail never holds a
// second key of its own. The trade-off is that Rail can only complete a flow the
// custody provider can actually sign. Circle's developer wallets sign raw
// transactions (SignTransaction), which covers Solana enrollment (Model B), and
// sign plain-text messages (SignMessage → POST /w3s/developer/sign/message),
// which covers Glider's solana-message withdrawal authorizations: the returned
// signature is base58 Ed25519 over the message's UTF-8 bytes. EVM typed-data
// (ERC-1271) authorizations remain unsupported.
type CircleSigner struct {
	wallets WalletLookup
	signer  CircleTransactionSigner
	cfg     signerConfig
}

// CircleTransactionSigner is the slice of the Circle adapter this signer needs.
type CircleTransactionSigner interface {
	SignTransaction(ctx context.Context, walletID, rawTransaction, memo string) (*circle.SignedTransaction, error)
	// SignMessage signs a plain-text message with the wallet's key and returns
	// the chain-native signature encoding (base58 Ed25519 for Solana).
	SignMessage(ctx context.Context, walletID, message string) (string, error)
}

// NewCircleSigner builds the custody-backed owner signer.
func NewCircleSigner(wallets WalletLookup, signer CircleTransactionSigner, accountPrefix string, chain entities.WalletChain) (*CircleSigner, error) {
	if wallets == nil {
		return nil, fmt.Errorf("investment owner: circle signer requires a wallet lookup")
	}
	if signer == nil {
		return nil, fmt.Errorf("investment owner: circle signer requires a transaction signer")
	}
	return &CircleSigner{wallets: wallets, signer: signer, cfg: newSignerConfig(accountPrefix, chain)}, nil
}

// OwnerAccount returns the user's wallet address as a CAIP-10 owner account id.
func (s *CircleSigner) OwnerAccount(ctx context.Context, userID uuid.UUID) (string, error) {
	wallet, err := s.wallet(ctx, userID)
	if err != nil {
		return "", err
	}
	return s.cfg.ownerAccountID(wallet.Address)
}

// SignSolanaMessage signs a plain-text authorization (e.g. a Glider withdrawal
// stage-1 solana-message payload) with the user's custody wallet via Circle's
// sign-message endpoint. The returned base58 Ed25519 signature covers the
// message's UTF-8 bytes, which is exactly what the provider verifies.
func (s *CircleSigner) SignSolanaMessage(ctx context.Context, userID uuid.UUID, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("investment owner: nothing to sign")
	}
	wallet, err := s.wallet(ctx, userID)
	if err != nil {
		return "", err
	}
	return s.signer.SignMessage(ctx, wallet.CircleWalletID, message)
}

// SignSolanaTransaction signs the provider's unsigned transaction with the
// user's wallet key.
func (s *CircleSigner) SignSolanaTransaction(ctx context.Context, userID uuid.UUID, unsignedTx string) (string, error) {
	wallet, err := s.wallet(ctx, userID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(unsignedTx) == "" {
		return "", fmt.Errorf("investment owner: nothing to sign")
	}
	signed, err := s.signer.SignTransaction(ctx, wallet.CircleWalletID, unsignedTx, "")
	if err != nil {
		return "", fmt.Errorf("circle sign transaction: %w", err)
	}
	if signed == nil {
		return "", fmt.Errorf("investment owner: the custody provider returned no signature")
	}
	// Prefer the signed transaction when the flow echoes one back; fall back to
	// the raw signature when the provider only produced that.
	if strings.TrimSpace(signed.SignedTransaction) != "" {
		return signed.SignedTransaction, nil
	}
	if strings.TrimSpace(signed.Signature) != "" {
		return signed.Signature, nil
	}
	return "", fmt.Errorf("investment owner: the custody provider returned an empty signature")
}

func (s *CircleSigner) wallet(ctx context.Context, userID uuid.UUID) (*entities.ManagedWallet, error) {
	wallet, err := s.wallets.GetWalletByUserAndChain(ctx, userID, s.cfg.chain)
	if err != nil {
		return nil, fmt.Errorf("load owner wallet: %w", err)
	}
	if wallet == nil || strings.TrimSpace(wallet.CircleWalletID) == "" {
		return nil, fmt.Errorf("investment owner: this account has no custody wallet on %s", s.cfg.chain)
	}
	return wallet, nil
}
