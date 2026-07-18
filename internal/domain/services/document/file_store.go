package document

import (
	"context"
	"fmt"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/r2"
)

// FileStore abstracts original-document storage. Originals live in object
// storage; Postgres only keeps the key.
type FileStore interface {
	Upload(ctx context.Context, key string, data []byte, contentType string) error
	Download(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// R2FileStore implements FileStore backed by the shared Cloudflare R2 client.
type R2FileStore struct {
	client *r2.Client
}

// NewR2FileStore builds a document store on top of an existing R2 client.
func NewR2FileStore(client *r2.Client) (*R2FileStore, error) {
	if client == nil {
		return nil, fmt.Errorf("r2 client is nil")
	}
	return &R2FileStore{client: client}, nil
}

// Upload stores bytes at the given key.
func (s *R2FileStore) Upload(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, key, data, contentType)
	return err
}

// Download fetches bytes for a key.
func (s *R2FileStore) Download(ctx context.Context, key string) ([]byte, error) {
	return s.client.GetObject(ctx, key)
}

// Delete removes an object.
func (s *R2FileStore) Delete(ctx context.Context, key string) error {
	return s.client.DeleteObject(ctx, key)
}

// SignedURL returns a time-limited download URL.
func (s *R2FileStore) SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return s.client.GetSignedURL(ctx, key, int(expiry.Minutes()))
}
