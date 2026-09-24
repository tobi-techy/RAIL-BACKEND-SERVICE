package confirmation

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// PostgresDeviceStore persists enrolled approval keys in the
// confirmation_device_keys table (migrations/315) so restarts and
// multi-replica deploys don't silently drop enrollment and fall back to
// token-only approves.
type PostgresDeviceStore struct {
	db *sql.DB
}

// NewPostgresDeviceStore backs enrollment with Postgres. A nil db keeps the
// memory store (dev/test); callers must pass a real db in production.
func NewPostgresDeviceStore(db *sql.DB) *PostgresDeviceStore {
	return &PostgresDeviceStore{db: db}
}

// Enroll inserts one approval key. Multiple keys per user are allowed so a
// second iPhone can enroll without bricking the first (one row per device).
func (s *PostgresDeviceStore) Enroll(userID uuid.UUID, spki []byte) (*DeviceKey, error) {
	now := time.Now().UTC()
	k := &DeviceKey{
		ID:        uuid.New(),
		UserID:    userID,
		SPKI:      append([]byte(nil), spki...),
		CreatedAt: now,
		LastUsed:  now,
	}
	_, err := s.db.Exec(
		`INSERT INTO confirmation_device_keys (id, user_id, spki, created_at, last_used_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		k.ID, k.UserID, k.SPKI, k.CreatedAt, k.LastUsed,
	)
	if err != nil {
		return nil, err
	}
	return k, nil
}

// Keys lists every enrolled key for a user (empty = unenrolled).
func (s *PostgresDeviceStore) Keys(userID uuid.UUID) ([]DeviceKey, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, spki, created_at, last_used_at
		   FROM confirmation_device_keys WHERE user_id = $1 ORDER BY created_at`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []DeviceKey
	for rows.Next() {
		var k DeviceKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.SPKI, &k.CreatedAt, &k.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Touch refreshes last_used_at. Failures are best-effort (never fail an
// approval on an audit-column write).
func (s *PostgresDeviceStore) Touch(id uuid.UUID) {
	_, _ = s.db.Exec(
		`UPDATE confirmation_device_keys SET last_used_at = now() WHERE id = $1`,
		id,
	)
}

// Revoke deletes one key (lost phone, biometry change re-enroll). Remaining
// keys keep working; revoking the last key returns the user to the
// enroll-on-next-approve path (or strict-mode rejection).
func (s *PostgresDeviceStore) Revoke(id uuid.UUID) error {
	_, err := s.db.Exec(`DELETE FROM confirmation_device_keys WHERE id = $1`, id)
	return err
}
