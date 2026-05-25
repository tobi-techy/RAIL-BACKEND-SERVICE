package miriam

import (
	"context"

	"github.com/google/uuid"
)

type InMemoryNotificationPrefStore struct {
	prefs map[uuid.UUID]*NotificationPreferences
}

func NewInMemoryNotificationPrefStore() *InMemoryNotificationPrefStore {
	return &InMemoryNotificationPrefStore{prefs: make(map[uuid.UUID]*NotificationPreferences)}
}

func (s *InMemoryNotificationPrefStore) GetPreferences(_ context.Context, userID uuid.UUID) (*NotificationPreferences, error) {
	if p, ok := s.prefs[userID]; ok {
		return p, nil
	}
	return nil, nil
}

func (s *InMemoryNotificationPrefStore) SavePreferences(_ context.Context, p *NotificationPreferences) error {
	s.prefs[p.UserID] = p
	return nil
}

type InMemoryNotificationDigestStore struct {
	digests map[uuid.UUID][]NotificationDigest
}

func NewInMemoryNotificationDigestStore() *InMemoryNotificationDigestStore {
	return &InMemoryNotificationDigestStore{digests: make(map[uuid.UUID][]NotificationDigest)}
}

func (s *InMemoryNotificationDigestStore) SaveDigest(_ context.Context, d *NotificationDigest) error {
	s.digests[d.UserID] = append(s.digests[d.UserID], *d)
	return nil
}

func (s *InMemoryNotificationDigestStore) GetRecentDigests(_ context.Context, userID uuid.UUID, limit int) ([]NotificationDigest, error) {
	digests := s.digests[userID]
	if len(digests) > limit {
		digests = digests[len(digests)-limit:]
	}
	return digests, nil
}
