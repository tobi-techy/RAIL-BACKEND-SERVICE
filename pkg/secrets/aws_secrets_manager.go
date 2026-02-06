package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// cachedSecret represents a cached secret value with expiration
type cachedSecret struct {
	value     string
	expiresAt time.Time
}

// AWSSecretsManagerProvider implements Provider using AWS Secrets Manager
type AWSSecretsManagerProvider struct {
	client   *secretsmanager.Client
	prefix   string
	cache    map[string]cachedSecret
	cacheMu  sync.RWMutex
	cacheTTL time.Duration
}

// NewAWSSecretsManagerProvider creates a new AWS Secrets Manager provider
func NewAWSSecretsManagerProvider(ctx context.Context, region, prefix string, cacheTTL time.Duration) (*AWSSecretsManagerProvider, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &AWSSecretsManagerProvider{
		client:   secretsmanager.NewFromConfig(cfg),
		prefix:   prefix,
		cache:    make(map[string]cachedSecret),
		cacheTTL: cacheTTL,
	}, nil
}

func (p *AWSSecretsManagerProvider) GetSecret(ctx context.Context, key string) (string, error) {
	// Check cache first
	p.cacheMu.RLock()
	if cached, ok := p.cache[key]; ok && time.Now().Before(cached.expiresAt) {
		p.cacheMu.RUnlock()
		return cached.value, nil
	}
	p.cacheMu.RUnlock()

	secretName := p.prefix + key
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}

	result, err := p.client.GetSecretValue(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s: %w", key, err)
	}

	var value string
	if result.SecretString != nil {
		value = *result.SecretString
	}

	// Cache the result
	p.cacheMu.Lock()
	p.cache[key] = cachedSecret{
		value:     value,
		expiresAt: time.Now().Add(p.cacheTTL),
	}
	p.cacheMu.Unlock()

	return value, nil
}

func (p *AWSSecretsManagerProvider) SetSecret(ctx context.Context, key, value string) error {
	secretName := p.prefix + key

	// Try to update existing secret first
	_, err := p.client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(secretName),
		SecretString: aws.String(value),
	})
	if err != nil {
		// If secret doesn't exist, create it
		_, err = p.client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
			Name:         aws.String(secretName),
			SecretString: aws.String(value),
		})
		if err != nil {
			return fmt.Errorf("failed to set secret %s: %w", key, err)
		}
	}

	// Invalidate cache
	p.cacheMu.Lock()
	delete(p.cache, key)
	p.cacheMu.Unlock()

	return nil
}

func (p *AWSSecretsManagerProvider) DeleteSecret(ctx context.Context, key string) error {
	secretName := p.prefix + key

	_, err := p.client.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId:                   aws.String(secretName),
		ForceDeleteWithoutRecovery: aws.Bool(false),
	})
	if err != nil {
		return fmt.Errorf("failed to delete secret %s: %w", key, err)
	}

	// Invalidate cache
	p.cacheMu.Lock()
	delete(p.cache, key)
	p.cacheMu.Unlock()

	return nil
}

// RotateSecret rotates a secret with a new value
func (p *AWSSecretsManagerProvider) RotateSecret(ctx context.Context, key string, newValue string) error {
	secretName := p.prefix + key

	// Create new version with staging label
	_, err := p.client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:      aws.String(secretName),
		SecretString:  aws.String(newValue),
		VersionStages: []string{"AWSPENDING"},
	})
	if err != nil {
		return fmt.Errorf("failed to create pending secret version: %w", err)
	}

	// Invalidate cache
	p.cacheMu.Lock()
	delete(p.cache, key)
	p.cacheMu.Unlock()

	return nil
}

// GetSecretJSON retrieves a secret and unmarshals it as JSON
func (p *AWSSecretsManagerProvider) GetSecretJSON(ctx context.Context, key string, v interface{}) error {
	value, err := p.GetSecret(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(value), v)
}

// ClearCache clears the secret cache
func (p *AWSSecretsManagerProvider) ClearCache() {
	p.cacheMu.Lock()
	p.cache = make(map[string]cachedSecret)
	p.cacheMu.Unlock()
}
