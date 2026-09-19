package entities

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrIdentityAlreadyLinked reports that a platform identity is already bound —
// either that handle belongs to a different Rail user, or that user already has
// a linked identity on this platform.
//
// It is raised from the unique constraints on platform_identities rather than
// from a lookup, so a race between two concurrent claims is reported as a
// conflict instead of surfacing a raw database error to the person.
var ErrIdentityAlreadyLinked = errors.New("platform identity already linked")

type Platform string

const (
	PlatformIMessage Platform = "imessage"
	PlatformWhatsApp Platform = "whatsapp"
	PlatformTelegram Platform = "telegram"
)

type PlatformIdentity struct {
	ID                 uuid.UUID  `db:"id" json:"id"`
	UserID             uuid.UUID  `db:"user_id" json:"user_id"`
	Platform           Platform   `db:"platform" json:"platform"`
	PlatformUserID     string     `db:"platform_user_id" json:"platform_user_id"`
	PlatformUsername   *string    `db:"platform_username" json:"platform_username,omitempty"`
	HandshakeTokenHash *string    `db:"handshake_token_hash" json:"-"`
	HandshakeExpiresAt *time.Time `db:"handshake_expires_at" json:"-"`
	LinkedAt           *time.Time `db:"linked_at" json:"linked_at,omitempty"`
	LastUsedAt         *time.Time `db:"last_used_at" json:"last_used_at,omitempty"`
	CreatedAt          time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at" json:"updated_at"`
}

func (p Platform) String() string {
	return string(p)
}
