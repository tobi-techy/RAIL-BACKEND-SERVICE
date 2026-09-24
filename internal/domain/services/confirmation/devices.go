package confirmation

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Assurance levels for an approval. The server never sees biometrics —
// Face ID is local-only by Apple design. What the server CAN know is whether
// the approve call carries a signature from a Secure Enclave key whose use
// requires the device owner (biometryCurrentSet). That is what separates
// "secure_enclave" from a bare token claim.
const (
	AssuranceTokenOnly     = "token_only"     // signed URL possession only (legacy/stopgap)
	AssuranceEnrolled      = "enrolled"       // first approval: key enrolled via trust-on-first-use
	AssuranceSecureEnclave = "secure_enclave" // signature from an enrolled Enclave key
)

// DeviceKey is one iPhone's biometric-bound approval key. The private half
// lives in the Secure Enclave under biometryCurrentSet access control: using
// it re-prompts Face ID, and changing the enrolled face invalidates it
// (client re-enrolls via trust-on-first-use, audited).
type DeviceKey struct {
	ID        uuid.UUID `json:"device_key_id"`
	UserID    uuid.UUID `json:"user_id"`
	SPKI      []byte    `json:"-"` // PKIX SubjectPublicKeyInfo, P-256 ECDSA only
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used_at"`
}

// DeviceStore persists enrolled approval keys. Memory is the default;
// production should back this with the confirmation_device_keys table
// (migrations/315) so restarts don't force re-enrollment.
type DeviceStore interface {
	Enroll(userID uuid.UUID, spki []byte) (*DeviceKey, error)
	Keys(userID uuid.UUID) ([]DeviceKey, error)
	Touch(id uuid.UUID)
}

type memoryDeviceStore struct {
	mu   sync.RWMutex
	keys map[uuid.UUID][]DeviceKey // userID -> keys
}

func newMemoryDeviceStore() *memoryDeviceStore {
	return &memoryDeviceStore{keys: map[uuid.UUID][]DeviceKey{}}
}

func (s *memoryDeviceStore) Enroll(userID uuid.UUID, spki []byte) (*DeviceKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	k := DeviceKey{ID: uuid.New(), UserID: userID, SPKI: append([]byte(nil), spki...), CreatedAt: now, LastUsed: now}
	s.keys[userID] = append(s.keys[userID], k)
	cp := k
	return &cp, nil
}

func (s *memoryDeviceStore) Keys(userID uuid.UUID) ([]DeviceKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]DeviceKey(nil), s.keys[userID]...)
	return out, nil
}

func (s *memoryDeviceStore) Touch(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for uid, ks := range s.keys {
		for i := range ks {
			if ks[i].ID == id {
				ks[i].LastUsed = now
				s.keys[uid] = ks
				return
			}
		}
	}
}

// ParseDevicePublicKey decodes a base64 PKIX key and enforces P-256 ECDSA —
// the only algorithm the Secure Enclave produces. Anything else is refused so
// a software key can never enroll as a biometric key.
func ParseDevicePublicKey(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("device key is not base64: %w", err)
	}
	pub, err := x509.ParsePKIXPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("device key is not a PKIX public key: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok || ec.Curve.Params().Name != "P-256" {
		return nil, fmt.Errorf("device key must be P-256 ECDSA (Secure Enclave)")
	}
	return raw, nil
}

// VerifyDeviceSignature checks an ASN.1 ECDSA signature over message with the
// enrolled SPKI. Message is the same string the URL token signs:
// actionId + "." + expiryUnix.
func VerifyDeviceSignature(spki, message, sigASN1 []byte) error {
	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return fmt.Errorf("bad enrolled key: %w", err)
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("enrolled key is not ECDSA")
	}
	digest := sha256.Sum256(message)
	if !ecdsa.VerifyASN1(ec, digest[:], sigASN1) {
		return fmt.Errorf("device signature invalid")
	}
	return nil
}

// SignedMessage is the canonical bytes the extension signs: the same
// actionId.expiry the URL token carries, so a signature cannot be replayed
// onto a different card or past expiry.
func SignedMessage(actionID string, expiryUnix int64) []byte {
	return []byte(fmt.Sprintf("%s.%d", actionID, expiryUnix))
}
