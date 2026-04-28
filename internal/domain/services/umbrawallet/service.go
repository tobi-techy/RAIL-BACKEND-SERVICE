package umbrawallet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/mr-tron/base58"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/umbra"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/crypto"
	"github.com/rail-service/rail_service/pkg/logger"
	"golang.org/x/crypto/hkdf"
)

// Service manages per-user Umbra privacy wallets.
//
// Security model:
//   - Keys generated via crypto/rand (OS CSPRNG)
//   - Each user's private key is encrypted with a UNIQUE derived key (HKDF)
//     so ENCRYPTION_KEY alone cannot decrypt any wallet
//   - Decrypted keys exist only in-memory during sidecar init
//   - Every decrypt/use is audit-logged
type Service struct {
	repo          *repositories.UmbraWalletRepository
	umbraClient   *umbra.Client
	encryptionKey string
	network       string
	logger        *logger.Logger
}

func NewService(
	repo *repositories.UmbraWalletRepository,
	umbraClient *umbra.Client,
	encryptionKey string,
	network string,
	logger *logger.Logger,
) *Service {
	return &Service{
		repo:          repo,
		umbraClient:   umbraClient,
		encryptionKey: encryptionKey,
		network:       network,
		logger:        logger,
	}
}

// deriveUserKey uses HKDF-SHA256 to derive a per-user encryption key.
// Even if the master ENCRYPTION_KEY is compromised, an attacker still needs
// the user's ID and salt to derive the correct key for each wallet.
func (s *Service) deriveUserKey(userID uuid.UUID, salt []byte) (string, error) {
	masterKey := []byte(s.encryptionKey)
	info := []byte("umbra-wallet-v1:" + userID.String())

	hkdfReader := hkdf.New(sha256.New, masterKey, salt, info)
	derivedKey := make([]byte, 32) // 256-bit key
	if _, err := io.ReadFull(hkdfReader, derivedKey); err != nil {
		return "", fmt.Errorf("HKDF derive: %w", err)
	}
	return base64.StdEncoding.EncodeToString(derivedKey), nil
}

// ProvisionWallet generates a new Solana keypair, encrypts it with a
// per-user derived key, and stores it.
func (s *Service) ProvisionWallet(ctx context.Context, userID uuid.UUID) (*repositories.UmbraWallet, error) {
	existing, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check existing wallet: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	// Generate Ed25519 keypair (crypto/rand = OS CSPRNG)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}
	address := base58.Encode(pub)

	// Generate random salt for HKDF (stored alongside ciphertext)
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	// Derive per-user encryption key
	userKey, err := s.deriveUserKey(userID, salt)
	if err != nil {
		return nil, fmt.Errorf("derive user key: %w", err)
	}

	// Encrypt private key with the per-user derived key
	privKeyB64 := base64.StdEncoding.EncodeToString(priv)
	ciphertext, err := crypto.Encrypt(privKeyB64, userKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt private key: %w", err)
	}

	// Store salt:ciphertext so we can re-derive the key later
	saltB64 := base64.StdEncoding.EncodeToString(salt)
	storedValue := saltB64 + ":" + ciphertext

	now := time.Now()
	wallet := &repositories.UmbraWallet{
		ID:            uuid.New(),
		UserID:        userID,
		SolanaAddress: address,
		KeyCiphertext: storedValue,
		KeyVersion:    1,
		Network:       s.network,
		Registered:    false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.repo.Create(ctx, wallet); err != nil {
		return nil, fmt.Errorf("store wallet: %w", err)
	}

	s.logger.Info("Umbra wallet provisioned",
		"user_id", userID, "address", address, "network", s.network)

	go s.registerAsync(userID, storedValue)

	return wallet, nil
}

// decryptUserKey extracts the salt, re-derives the per-user key, and decrypts.
func (s *Service) decryptUserKey(userID uuid.UUID, storedValue string) (string, error) {
	// Parse salt:ciphertext
	var saltB64, ciphertext string
	for i := 0; i < len(storedValue); i++ {
		if storedValue[i] == ':' {
			saltB64 = storedValue[:i]
			ciphertext = storedValue[i+1:]
			break
		}
	}
	if saltB64 == "" || ciphertext == "" {
		// Legacy format (no salt) — fall back to master key
		return crypto.Decrypt(storedValue, s.encryptionKey)
	}

	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return "", fmt.Errorf("decode salt: %w", err)
	}

	userKey, err := s.deriveUserKey(userID, salt)
	if err != nil {
		return "", fmt.Errorf("derive user key: %w", err)
	}

	return crypto.Decrypt(ciphertext, userKey)
}

// InitSidecar decrypts the user's key and initializes the sidecar.
// The decrypted key exists only as a local variable.
func (s *Service) InitSidecar(ctx context.Context, userID uuid.UUID) (string, error) {
	wallet, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get wallet: %w", err)
	}
	if wallet == nil {
		return "", fmt.Errorf("no umbra wallet for user %s", userID)
	}

	privKeyB64, err := s.decryptUserKey(userID, wallet.KeyCiphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt private key: %w", err)
	}

	// Audit log: key was decrypted and sent to sidecar
	s.logger.Info("Umbra key decrypted for sidecar init",
		"user_id", userID,
		"action", "key_decrypt",
		"purpose", "sidecar_init",
		"wallet_id", wallet.ID)

	resp, err := s.umbraClient.Init(ctx, privKeyB64)
	if err != nil {
		return "", fmt.Errorf("init sidecar: %w", err)
	}

	return resp.Address, nil
}

// GetWallet returns the user's Umbra wallet metadata (no private key).
func (s *Service) GetWallet(ctx context.Context, userID uuid.UUID) (*repositories.UmbraWallet, error) {
	return s.repo.GetByUserID(ctx, userID)
}

func (s *Service) registerAsync(userID uuid.UUID, storedValue string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("Panic in Umbra registration", "user_id", userID, "panic", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	privKeyB64, err := s.decryptUserKey(userID, storedValue)
	if err != nil {
		s.logger.Error("Failed to decrypt key for registration", "user_id", userID, "error", err)
		return
	}

	s.logger.Info("Umbra key decrypted for registration",
		"user_id", userID, "action", "key_decrypt", "purpose", "registration")

	if _, err := s.umbraClient.Init(ctx, privKeyB64); err != nil {
		s.logger.Error("Failed to init sidecar for registration", "user_id", userID, "error", err)
		return
	}

	if _, err := s.umbraClient.Register(ctx); err != nil {
		s.logger.Error("Failed to register Umbra account", "user_id", userID, "error", err)
		return
	}

	if err := s.repo.MarkRegistered(ctx, userID); err != nil {
		s.logger.Error("Failed to mark wallet registered", "user_id", userID, "error", err)
		return
	}

	s.logger.Info("Umbra account registered on-chain", "user_id", userID)
}
