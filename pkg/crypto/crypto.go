package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword hashes a password using bcrypt
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(hash), nil
}

// ValidatePassword validates a password against its hash
func ValidatePassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ValidatePasswordStrength checks that a password meets minimum policy requirements:
// at least 8 characters and at least 3 of uppercase, lowercase, digit, or special character.
func ValidatePasswordStrength(password string) error {
	if len(password) > 72 {
		return fmt.Errorf("password must not exceed 72 characters")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, c := range password {
		switch {
		case unicode.IsUpper(c):
			hasUpper = true
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsDigit(c):
			hasDigit = true
		case unicode.IsPunct(c) || unicode.IsSymbol(c):
			hasSpecial = true
		}
	}
	complexityCount := 0
	for _, ok := range []bool{hasUpper, hasLower, hasDigit, hasSpecial} {
		if ok {
			complexityCount++
		}
	}
	if complexityCount < 3 {
		return fmt.Errorf("password must contain at least 3 of: uppercase, lowercase, digit, special character")
	}
	return nil
}

// Encrypt encrypts data using AES-GCM
func Encrypt(data, key string) (string, error) {
	// Convert key to 32 bytes using SHA-256
	keyBytes := sha256.Sum256([]byte(key))

	// Create cipher
	block, err := aes.NewCipher(keyBytes[:])
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Create nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to create nonce: %w", err)
	}

	// Encrypt
	ciphertext := gcm.Seal(nonce, nonce, []byte(data), nil)

	// Return as hex string
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts data using AES-GCM
func Decrypt(encryptedHex, key string) (string, error) {
	// Convert key to 32 bytes using SHA-256
	keyBytes := sha256.Sum256([]byte(key))

	// Decode hex
	ciphertext, err := hex.DecodeString(encryptedHex)
	if err != nil {
		return "", fmt.Errorf("failed to decode hex: %w", err)
	}

	// Create cipher
	block, err := aes.NewCipher(keyBytes[:])
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// GenerateRandomString generates a random string of specified length
func GenerateRandomString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random string: %w", err)
	}
	return hex.EncodeToString(bytes)[:length], nil
}

// GenerateSecureToken generates a secure token
func GenerateSecureToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate secure token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// SelectorVerifierToken contains the components of a selector-verifier token
type SelectorVerifierToken struct {
	Selector     string // Public, indexed for fast DB lookup (16 bytes hex = 32 chars)
	Verifier     string // Secret, hashed before storage (32 bytes hex = 64 chars)
	VerifierHash string // bcrypt hash of verifier for storage
	FullToken    string // selector:verifier combined token sent to user
}

// GenerateSelectorVerifierToken generates a token using the selector-verifier pattern
// This allows fast DB lookup via selector, then single bcrypt comparison of verifier
func GenerateSelectorVerifierToken() (*SelectorVerifierToken, error) {
	// Generate 16-byte selector (32 hex chars)
	selectorBytes := make([]byte, 16)
	if _, err := rand.Read(selectorBytes); err != nil {
		return nil, fmt.Errorf("failed to generate selector: %w", err)
	}
	selector := hex.EncodeToString(selectorBytes)

	// Generate 32-byte verifier (64 hex chars)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("failed to generate verifier: %w", err)
	}
	verifier := hex.EncodeToString(verifierBytes)

	// Hash the verifier for storage
	verifierHash, err := HashPassword(verifier)
	if err != nil {
		return nil, fmt.Errorf("failed to hash verifier: %w", err)
	}

	return &SelectorVerifierToken{
		Selector:     selector,
		Verifier:     verifier,
		VerifierHash: verifierHash,
		FullToken:    selector + ":" + verifier,
	}, nil
}

// ParseSelectorVerifierToken splits a full token into selector and verifier
func ParseSelectorVerifierToken(fullToken string) (selector, verifier string, err error) {
	// Find the colon separator
	for i := 0; i < len(fullToken); i++ {
		if fullToken[i] == ':' {
			if i == 0 || i == len(fullToken)-1 {
				return "", "", fmt.Errorf("invalid token format")
			}
			return fullToken[:i], fullToken[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("invalid token format: missing separator")
}

// DecodeJWTClaims is intentionally removed.
// Decoding JWT claims without signature verification is a security risk.
// Use pkg/auth.ValidateToken() instead which verifies the signature.

// (splitJWT and base64URLDecode removed — were only used by DecodeJWTClaims)
