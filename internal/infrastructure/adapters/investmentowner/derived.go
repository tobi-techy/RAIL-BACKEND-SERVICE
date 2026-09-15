package investmentowner

import (
	"context"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
	"strings"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/base58"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// DerivedSigner holds a per-user owner key derived from a master seed.
//
// It exists for simulation and development, and as the fallback when the
// custody provider cannot sign the payload the provider hands us. It must not be
// used in production unless the operator has explicitly accepted custodial owner
// keys and stored the seed in a secret manager: whoever holds the seed can move
// every user's portfolio.
type DerivedSigner struct {
	masterSeed []byte
	cfg        signerConfig
}

// NewDerivedSigner builds a deterministic per-user signer from a master seed.
func NewDerivedSigner(masterSeed string, accountPrefix string, chain entities.WalletChain) (*DerivedSigner, error) {
	seed := strings.TrimSpace(masterSeed)
	if seed == "" {
		return nil, fmt.Errorf("investment owner: derived signer requires a master seed")
	}
	if len(seed) < 32 {
		return nil, fmt.Errorf("investment owner: master seed must be at least 32 characters")
	}
	return &DerivedSigner{masterSeed: []byte(seed), cfg: newSignerConfig(accountPrefix, chain)}, nil
}

// ownerKey derives the user's ed25519 key. Derivation is domain-separated per
// user so one user's key never reveals another's.
func (s *DerivedSigner) ownerKey(userID uuid.UUID) (solana.PrivateKey, error) {
	seed, err := hkdf.Key(sha256.New, s.masterSeed, nil, "rail-investment-owner:"+userID.String(), ed25519.SeedSize)
	if err != nil {
		return nil, fmt.Errorf("derive owner key: %w", err)
	}
	// ed25519.NewKeyFromSeed returns seed||publicKey, which is exactly Solana's
	// private key layout.
	return solana.PrivateKey(ed25519.NewKeyFromSeed(seed)), nil
}

// OwnerAccount returns the derived account's CAIP-10 id.
func (s *DerivedSigner) OwnerAccount(_ context.Context, userID uuid.UUID) (string, error) {
	key, err := s.ownerKey(userID)
	if err != nil {
		return "", err
	}
	return s.cfg.ownerAccountID(key.PublicKey().String())
}

// SignSolanaMessage signs an ed25519 message and returns a base58 signature,
// which is the encoding Solana-rooted authorization flows expect.
func (s *DerivedSigner) SignSolanaMessage(_ context.Context, userID uuid.UUID, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("investment owner: nothing to sign")
	}
	key, err := s.ownerKey(userID)
	if err != nil {
		return "", err
	}
	signature, err := key.Sign([]byte(message))
	if err != nil {
		return "", fmt.Errorf("sign message: %w", err)
	}
	return signature.String(), nil
}

// SignSolanaTransaction signs an unsigned Solana transaction and returns it
// base64-encoded, ready to be echoed back to the provider.
func (s *DerivedSigner) SignSolanaTransaction(_ context.Context, userID uuid.UUID, unsignedTx string) (string, error) {
	key, err := s.ownerKey(userID)
	if err != nil {
		return "", err
	}
	return signSolanaTransaction(unsignedTx, key)
}

// signSolanaTransaction decodes, signs and re-encodes a transaction. The
// provider hands us base64; base58 is accepted as a fallback because Solana
// wire format shows up in both encodings.
func signSolanaTransaction(unsignedTx string, key solana.PrivateKey) (string, error) {
	raw := strings.TrimSpace(unsignedTx)
	if raw == "" {
		return "", fmt.Errorf("investment owner: nothing to sign")
	}
	tx, err := solana.TransactionFromBase64(raw)
	if err != nil {
		decoded, b58Err := base58.Decode(raw)
		if b58Err != nil {
			return "", fmt.Errorf("decode transaction: %w", err)
		}
		tx, err = solana.TransactionFromDecoder(bin.NewBinDecoder(decoded))
		if err != nil {
			return "", fmt.Errorf("decode transaction: %w", err)
		}
	}
	publicKey := key.PublicKey()
	// Partial-sign: the provider's pooled agent co-signs after us (Glider
	// Model B), so other required signers in the transaction are expected to
	// still be missing. Strict tx.Sign would reject the transaction here.
	if _, err := tx.PartialSign(func(candidate solana.PublicKey) *solana.PrivateKey {
		if candidate.Equals(publicKey) {
			return &key
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("sign transaction: %w", err)
	}
	encoded, err := tx.ToBase64()
	if err != nil {
		return "", fmt.Errorf("encode signed transaction: %w", err)
	}
	return encoded, nil
}
