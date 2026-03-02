package entitysecret

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// Service handles entity secret encryption for Circle API requests
type Service struct {
	logger       *zap.Logger
	entitySecret []byte
	publicKeyPEM string
}

// NewService creates a new EntitySecretService
func NewService(logger *zap.Logger, entitySecretCiphertext, publicKeyPEM string) (*Service, error) {
	// Validate required configuration
	entitySecretHex := strings.TrimSpace(entitySecretCiphertext)
	if entitySecretHex == "" {
		return nil, errors.New("entity secret ciphertext is required")
	}

	publicKey := normalizePEM(strings.TrimSpace(publicKeyPEM))
	if publicKey == "" {
		return nil, errors.New("public key PEM is required")
	}

	// Decode the entity secret (32-byte hex string)
	entitySecret, err := hex.DecodeString(entitySecretHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode entity secret: %w", err)
	}

	// Validate entity secret length (must be exactly 32 bytes for Circle API)
	if len(entitySecret) != 32 {
		return nil, fmt.Errorf("invalid entity secret length: expected 32 bytes, got %d bytes", len(entitySecret))
	}

	// Validate public key PEM format
	block, _ := pem.Decode([]byte(publicKey))
	if block == nil {
		return nil, errors.New("failed to parse public key PEM: invalid PEM block")
	}

	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("invalid PEM block type: expected PUBLIC KEY, got %s", block.Type)
	}

	// Validate that the public key can be parsed
	_, err = x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	return &Service{
		logger:       logger,
		entitySecret: entitySecret,
		publicKeyPEM: publicKey,
	}, nil
}

// GenerateEntitySecretCiphertext generates entity secret ciphertext using the configured entity secret
func (s *Service) GenerateEntitySecretCiphertext(ctx context.Context) (string, error) {
	// Parse the public key from configuration
	pubKey, err := s.parseRSAPublicKeyFromPEM([]byte(s.publicKeyPEM))
	if err != nil {
		return "", fmt.Errorf("failed to parse public key: %w", err)
	}

	// Encrypt the entity secret
	cipher, err := s.encryptOAEP(pubKey, s.entitySecret)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt entity secret: %w", err)
	}

	// Return base64 encoded ciphertext
	ciphertext := base64.StdEncoding.EncodeToString(cipher)

	// SECURITY: Never log the entity secret itself
	s.logger.Debug("Generated entity secret ciphertext",
		zap.String("ciphertext_length", fmt.Sprintf("%d", len(ciphertext))),
		zap.String("public_key_fingerprint", s.getPublicKeyFingerprint(pubKey)))

	return ciphertext, nil
}

// getPublicKeyFingerprint generates a fingerprint of the public key for logging purposes
// This is safe to log and can be used to verify the correct key is being used
func (s *Service) getPublicKeyFingerprint(pubKey *rsa.PublicKey) string {
	// Generate a simple fingerprint based on the modulus
	// This is a non-cryptographic hash for identification only
	modulusBytes := pubKey.N.Bytes()
	if len(modulusBytes) < 8 {
		return "invalid_key"
	}
	// Use first and last 4 bytes as fingerprint
	fingerprint := hex.EncodeToString(modulusBytes[:4]) + "..." + hex.EncodeToString(modulusBytes[len(modulusBytes)-4:])
	return fingerprint
}

// parseRSAPublicKeyFromPEM parses an RSA public key from PEM format.
func (s *Service) parseRSAPublicKeyFromPEM(pubPEM []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pubPEM)
	if block == nil {
		return nil, errors.New("failed to parse PEM block containing the key")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unable to parse public key DER: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("key type parsed is not RSA")
	}
	return rsaPub, nil
}

// encryptOAEP performs RSA-OAEP encryption using SHA-256.
func (s *Service) encryptOAEP(pubKey *rsa.PublicKey, message []byte) ([]byte, error) {
	random := rand.Reader
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), random, pubKey, message, nil)
	if err != nil {
		return nil, fmt.Errorf("rsa.EncryptOAEP failed: %w", err)
	}
	return ciphertext, nil
}

// normalizePEM reconstructs proper PEM formatting from a potentially single-line env var value.
func normalizePEM(raw string) string {
	raw = strings.ReplaceAll(raw, "\\n", "\n")
	if strings.Contains(raw, "\n") {
		return raw
	}
	// Strip header/footer, re-wrap base64 at 64 chars
	s := raw
	s = strings.ReplaceAll(s, "-----BEGIN PUBLIC KEY-----", "")
	s = strings.ReplaceAll(s, "-----END PUBLIC KEY-----", "")
	s = strings.ReplaceAll(s, " ", "")
	var lines []string
	for i := 0; i < len(s); i += 64 {
		end := i + 64
		if end > len(s) {
			end = len(s)
		}
		lines = append(lines, s[i:end])
	}
	return "-----BEGIN PUBLIC KEY-----\n" + strings.Join(lines, "\n") + "\n-----END PUBLIC KEY-----"
}
