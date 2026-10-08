package passcode

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/crypto"
)

// The step-up token that authorises money movement must survive Redis being
// unavailable. It used to live only in Redis: an Upstash quota breach made
// VerifyPasscode fail while creating the session, so the client never got a
// token and the withdrawal was rejected with 403. These tests run the real
// service against the real Postgres store with no Redis in the picture at all,
// which is exactly the outage condition.
type fakeUserRepo struct {
	hashed   *string
	locked   *time.Time
	failures int
}

func (f *fakeUserRepo) GetPasscodeMetadata(context.Context, uuid.UUID) (*entities.PasscodeMetadata, error) {
	return &entities.PasscodeMetadata{
		HashedPasscode: f.hashed,
		FailedAttempts: f.failures,
		LockedUntil:    f.locked,
	}, nil
}

func (f *fakeUserRepo) UpdatePasscodeHash(_ context.Context, _ uuid.UUID, hash string, _ time.Time) error {
	f.hashed = &hash
	return nil
}

func (f *fakeUserRepo) ResetPasscodeFailures(context.Context, uuid.UUID) error {
	f.failures = 0
	f.locked = nil
	return nil
}

func (f *fakeUserRepo) IncrementPasscodeFailures(context.Context, uuid.UUID, int, *time.Time) (*entities.PasscodeMetadata, error) {
	f.failures++
	return &entities.PasscodeMetadata{HashedPasscode: f.hashed, FailedAttempts: f.failures}, nil
}

func (f *fakeUserRepo) ClearPasscode(context.Context, uuid.UUID) error {
	f.hashed = nil
	return nil
}

type fakeSessionStore struct {
	rows map[string]time.Time
}

func newFakeSessionStore() *fakeSessionStore { return &fakeSessionStore{rows: map[string]time.Time{}} }

func (s *fakeSessionStore) CreatePasscodeSession(_ context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	s.rows[userID.String()+":"+tokenHash] = expiresAt
	return nil
}

func (s *fakeSessionStore) PasscodeSessionExists(_ context.Context, userID uuid.UUID, tokenHash string, now time.Time) (bool, error) {
	expiresAt, ok := s.rows[userID.String()+":"+tokenHash]
	return ok && expiresAt.After(now), nil
}

func (s *fakeSessionStore) DeletePasscodeSession(_ context.Context, userID uuid.UUID, tokenHash string) error {
	delete(s.rows, userID.String()+":"+tokenHash)
	return nil
}

func (s *fakeSessionStore) DeleteExpiredPasscodeSessions(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func newPasscodeServiceWithFakeStore(t *testing.T) (*Service, *fakeSessionStore) {
	t.Helper()
	hash, err := crypto.HashPassword("1234")
	require.NoError(t, err)
	store := newFakeSessionStore()
	svc := NewService(&fakeUserRepo{hashed: &hash}, store, zap.NewNop())
	return svc, store
}

func TestVerifyPasscode_IssuesATokenWithoutRedis(t *testing.T) {
	svc, _ := newPasscodeServiceWithFakeStore(t)
	ctx := context.Background()
	userID := uuid.New()

	token, expiresAt, err := svc.VerifyPasscode(ctx, userID, "1234")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.True(t, expiresAt.After(time.Now()))

	valid, err := svc.ValidateSession(ctx, userID, token)
	require.NoError(t, err)
	require.True(t, valid)

	// Single use: the middleware consumes the token on success.
	require.NoError(t, svc.InvalidateSession(ctx, userID, token))
	valid, err = svc.ValidateSession(ctx, userID, token)
	require.NoError(t, err)
	require.False(t, valid)
}

func TestValidateSession_ScopedToUserAndStore(t *testing.T) {
	svc, _ := newPasscodeServiceWithFakeStore(t)
	ctx := context.Background()
	userID := uuid.New()

	token, _, err := svc.VerifyPasscode(ctx, userID, "1234")
	require.NoError(t, err)

	// A token issued to one user must never validate for another.
	valid, err := svc.ValidateSession(ctx, uuid.New(), token)
	require.NoError(t, err)
	require.False(t, valid)

	// An unconfigured store is an explicit error, not a silent "invalid token".
	hash, err := crypto.HashPassword("1234")
	require.NoError(t, err)
	broken := NewService(&fakeUserRepo{hashed: &hash}, nil, zap.NewNop())
	_, _, err = broken.VerifyPasscode(ctx, userID, "1234")
	require.ErrorIs(t, err, ErrPasscodeSessionStoreUnavailable)
}

func TestVerifyPasscode_WrongPasscodeDoesNotIssueAToken(t *testing.T) {
	svc, store := newPasscodeServiceWithFakeStore(t)

	_, _, err := svc.VerifyPasscode(context.Background(), uuid.New(), "9999")
	require.ErrorIs(t, err, ErrPasscodeMismatch)
	require.Empty(t, store.rows)
}
