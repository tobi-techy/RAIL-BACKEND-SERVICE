package secrets

import (
	"context"
	"fmt"
	"os"
	"time"
)

type Provider interface {
	GetSecret(ctx context.Context, key string) (string, error)
	SetSecret(ctx context.Context, key, value string) error
	DeleteSecret(ctx context.Context, key string) error
}

type EnvProvider struct{}

func NewEnvProvider() *EnvProvider {
	return &EnvProvider{}
}

func (p *EnvProvider) GetSecret(ctx context.Context, key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", fmt.Errorf("secret not found: %s", key)
	}
	return value, nil
}

func (p *EnvProvider) SetSecret(ctx context.Context, key, value string) error {
	return os.Setenv(key, value)
}

func (p *EnvProvider) DeleteSecret(ctx context.Context, key string) error {
	return os.Unsetenv(key)
}

type CachedProvider struct {
	provider Provider
	cache    map[string]cachedSecret
	ttl      time.Duration
}

func NewCachedProvider(provider Provider, ttl time.Duration) *CachedProvider {
	return &CachedProvider{
		provider: provider,
		cache:    make(map[string]cachedSecret),
		ttl:      ttl,
	}
}

func (p *CachedProvider) GetSecret(ctx context.Context, key string) (string, error) {
	if cached, ok := p.cache[key]; ok && time.Now().Before(cached.expiresAt) {
		return cached.value, nil
	}

	value, err := p.provider.GetSecret(ctx, key)
	if err != nil {
		return "", err
	}

	p.cache[key] = cachedSecret{
		value:     value,
		expiresAt: time.Now().Add(p.ttl),
	}

	return value, nil
}

func (p *CachedProvider) SetSecret(ctx context.Context, key, value string) error {
	delete(p.cache, key)
	return p.provider.SetSecret(ctx, key, value)
}

func (p *CachedProvider) DeleteSecret(ctx context.Context, key string) error {
	delete(p.cache, key)
	return p.provider.DeleteSecret(ctx, key)
}

type Manager struct {
	provider Provider
}

func NewManager(provider Provider) *Manager {
	return &Manager{provider: provider}
}

func (m *Manager) GetZeroGStorageKey(ctx context.Context) (string, error) {
	return m.provider.GetSecret(ctx, "ZEROG_STORAGE_PRIVATE_KEY")
}

func (m *Manager) GetZeroGComputeKey(ctx context.Context) (string, error) {
	return m.provider.GetSecret(ctx, "ZEROG_COMPUTE_PRIVATE_KEY")
}

func (m *Manager) GetCircleAPIKey(ctx context.Context) (string, error) {
	return m.provider.GetSecret(ctx, "CIRCLE_API_KEY")
}

func (m *Manager) GetDatabasePassword(ctx context.Context) (string, error) {
	return m.provider.GetSecret(ctx, "DATABASE_PASSWORD")
}

func (m *Manager) GetJWTSecret(ctx context.Context) (string, error) {
	return m.provider.GetSecret(ctx, "JWT_SECRET")
}

func (m *Manager) GetEncryptionKey(ctx context.Context) (string, error) {
	return m.provider.GetSecret(ctx, "ENCRYPTION_KEY")
}
