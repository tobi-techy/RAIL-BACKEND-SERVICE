package storage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type NamespaceManager struct {
	namespaces map[string]*Namespace
	mu         sync.RWMutex
	logger     *zap.Logger
}

type Namespace struct {
	ID          uuid.UUID              `json:"id"`
	Name        string                 `json:"name"`
	OwnerID     uuid.UUID              `json:"owner_id"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Status      string                 `json:"status"`
	Metadata    map[string]string      `json:"metadata"`
	ACL         []AccessControl        `json:"acl"`
	StorageIDs  []string               `json:"storage_ids"`
	Quota       QuotaLimits            `json:"quota"`
}

type AccessControl struct {
	UserID      uuid.UUID `json:"user_id"`
	Permissions []string  `json:"permissions"`
}

type QuotaLimits struct {
	MaxStorage  int64 `json:"max_storage"`
	UsedStorage int64 `json:"used_storage"`
	MaxObjects  int64 `json:"max_objects"`
	UsedObjects int64 `json:"used_objects"`
}

func NewNamespaceManager(logger *zap.Logger) *NamespaceManager {
	return &NamespaceManager{
		namespaces: make(map[string]*Namespace),
		logger:     logger,
	}
}

func (m *NamespaceManager) CreateNamespace(ctx context.Context, name string, ownerID uuid.UUID) (*Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.namespaces[name]; exists {
		return nil, fmt.Errorf("namespace already exists: %s", name)
	}

	ns := &Namespace{
		ID:        uuid.New(),
		Name:      name,
		OwnerID:   ownerID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Status:    "active",
		Metadata:  make(map[string]string),
		ACL: []AccessControl{
			{UserID: ownerID, Permissions: []string{"read", "write", "delete"}},
		},
		StorageIDs: []string{},
		Quota: QuotaLimits{
			MaxStorage:  10 * 1024 * 1024 * 1024,
			MaxObjects:  10000,
		},
	}

	m.namespaces[name] = ns
	m.logger.Info("namespace created", zap.String("name", name), zap.String("owner", ownerID.String()))
	return ns, nil
}

func (m *NamespaceManager) GetNamespace(ctx context.Context, name string) (*Namespace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ns, exists := m.namespaces[name]
	if !exists {
		return nil, fmt.Errorf("namespace not found: %s", name)
	}
	return ns, nil
}

func (m *NamespaceManager) DeleteNamespace(ctx context.Context, name string, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, exists := m.namespaces[name]
	if !exists {
		return fmt.Errorf("namespace not found: %s", name)
	}

	if ns.OwnerID != userID {
		return fmt.Errorf("permission denied")
	}

	delete(m.namespaces, name)
	m.logger.Info("namespace deleted", zap.String("name", name))
	return nil
}

func (m *NamespaceManager) AddStorageID(ctx context.Context, name, storageID string, size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, exists := m.namespaces[name]
	if !exists {
		return fmt.Errorf("namespace not found: %s", name)
	}

	if ns.Quota.UsedStorage+size > ns.Quota.MaxStorage {
		return fmt.Errorf("quota exceeded")
	}

	if ns.Quota.UsedObjects+1 > ns.Quota.MaxObjects {
		return fmt.Errorf("object limit exceeded")
	}

	ns.StorageIDs = append(ns.StorageIDs, storageID)
	ns.Quota.UsedStorage += size
	ns.Quota.UsedObjects++
	ns.UpdatedAt = time.Now()

	return nil
}

func (m *NamespaceManager) CheckAccess(ctx context.Context, name string, userID uuid.UUID, permission string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ns, exists := m.namespaces[name]
	if !exists {
		return false
	}

	for _, acl := range ns.ACL {
		if acl.UserID == userID {
			for _, perm := range acl.Permissions {
				if perm == permission || perm == "*" {
					return true
				}
			}
		}
	}
	return false
}

func (m *NamespaceManager) HealthCheck(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.logger.Debug("namespace health check", zap.Int("count", len(m.namespaces)))
	return nil
}

func (m *NamespaceManager) GetMetrics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalStorage := int64(0)
	totalObjects := int64(0)
	for _, ns := range m.namespaces {
		totalStorage += ns.Quota.UsedStorage
		totalObjects += ns.Quota.UsedObjects
	}

	return map[string]interface{}{
		"namespaces":    len(m.namespaces),
		"total_storage": totalStorage,
		"total_objects": totalObjects,
		"timestamp":     time.Now(),
	}
}