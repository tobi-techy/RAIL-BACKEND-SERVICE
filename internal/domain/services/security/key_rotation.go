package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	keyRotationInterval = 90 * 24 * time.Hour // 90 days
	maxActiveKeys       = 3
)

type EncryptionKey struct {
	ID        uuid.UUID
	KeyHash   string // Hash of the key for identification
	Version   int
	IsActive  bool
	CreatedAt time.Time
	ExpiresAt time.Time
}

type KeyRotationService struct {
	db            *sql.DB
	logger        *zap.Logger
	currentKey    []byte
	previousKeys  map[int][]byte
}

func NewKeyRotationService(db *sql.DB, logger *zap.Logger, masterKey string) *KeyRotationService {
	return &KeyRotationService{
		db:           db,
		logger:       logger,
		currentKey:   []byte(masterKey),
		previousKeys: make(map[int][]byte),
	}
}

// EncryptWithVersion encrypts data and includes key version
func (s *KeyRotationService) EncryptWithVersion(data []byte) ([]byte, int, error) {
	block, err := aes.NewCipher(s.currentKey[:32])
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, 1, nil // Version 1 is current
}

// DecryptWithVersion decrypts data using the appropriate key version
func (s *KeyRotationService) DecryptWithVersion(ciphertext []byte, version int) ([]byte, error) {
	var key []byte
	if version == 1 {
		key = s.currentKey
	} else if k, ok := s.previousKeys[version]; ok {
		key = k
	} else {
		return nil, fmt.Errorf("unknown key version: %d", version)
	}

	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

// GenerateNewKey generates a new encryption key
func GenerateNewKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// RotateKey performs key rotation (should be called by admin/scheduled job)
func (s *KeyRotationService) RotateKey(ctx context.Context, newKey string) error {
	// Store current key as previous
	s.previousKeys[1] = s.currentKey
	
	// Set new key as current
	newKeyBytes, err := hex.DecodeString(newKey)
	if err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}
	s.currentKey = newKeyBytes

	s.logger.Info("Encryption key rotated successfully")
	return nil
}

// ReEncryptData re-encrypts data with the current key
func (s *KeyRotationService) ReEncryptData(ctx context.Context, data []byte, oldVersion int) ([]byte, int, error) {
	// Decrypt with old key
	plaintext, err := s.DecryptWithVersion(data, oldVersion)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to decrypt with old key: %w", err)
	}

	// Encrypt with new key
	return s.EncryptWithVersion(plaintext)
}

// GetKeyStatus returns current key rotation status
func (s *KeyRotationService) GetKeyStatus() map[string]interface{} {
	return map[string]interface{}{
		"current_version":  1,
		"previous_versions": len(s.previousKeys),
		"rotation_interval": keyRotationInterval.String(),
	}
}
