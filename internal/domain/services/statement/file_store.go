package statement

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

// FileStore abstracts file storage operations (S3, local, etc).
type FileStore interface {
	Upload(ctx context.Context, key string, data []byte, contentType string) (string, error)
	Download(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	GeneratePresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// S3FileStore implements FileStore using AWS S3.
type S3FileStore struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
	prefix    string
	logger    *zap.Logger
}

type S3Config struct {
	Region   string
	Bucket   string
	Prefix   string // e.g. "statements/"
	Endpoint string // optional, for S3-compatible (R2, MinIO)
}

func NewS3FileStore(ctx context.Context, cfg S3Config, logger *zap.Logger) (*S3FileStore, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("S3 bucket is required")
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "statements/"
	}

	opts := []func(*config.LoadOptions) error{config.WithRegion(cfg.Region)}
	if cfg.Endpoint != "" {
		opts = append(opts, config.WithBaseEndpoint(cfg.Endpoint))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)
	presigner := s3.NewPresignClient(client)

	return &S3FileStore{
		client:    client,
		presigner: presigner,
		bucket:    cfg.Bucket,
		prefix:    cfg.Prefix,
		logger:    logger,
	}, nil
}

func (s *S3FileStore) Upload(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	fullKey := s.prefix + key
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(fullKey),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("s3 upload: %w", err)
	}
	return fmt.Sprintf("s3://%s/%s", s.bucket, fullKey), nil
}

func (s *S3FileStore) Download(ctx context.Context, key string) ([]byte, error) {
	fullKey := s.prefix + key
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(fullKey),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 download: %w", err)
	}
	defer result.Body.Close()
	return io.ReadAll(io.LimitReader(result.Body, 25*1024*1024))
}

func (s *S3FileStore) Delete(ctx context.Context, key string) error {
	fullKey := s.prefix + key
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(fullKey),
	})
	return err
}

func (s *S3FileStore) GeneratePresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	fullKey := s.prefix + key
	presignResult, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(fullKey),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return presignResult.URL, nil
}

// LocalFileStore implements FileStore for development/testing (stores in memory).
type LocalFileStore struct {
	mu    sync.RWMutex
	files map[string][]byte
}

func NewLocalFileStore() *LocalFileStore {
	return &LocalFileStore{files: make(map[string][]byte)}
}

func (l *LocalFileStore) Upload(_ context.Context, key string, data []byte, _ string) (string, error) {
	l.mu.Lock()
	l.files[key] = data
	l.mu.Unlock()
	return "local://" + key, nil
}

func (l *LocalFileStore) Download(_ context.Context, key string) ([]byte, error) {
	l.mu.RLock()
	data, ok := l.files[key]
	l.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("file not found: %s", key)
	}
	return data, nil
}

func (l *LocalFileStore) Delete(_ context.Context, key string) error {
	l.mu.Lock()
	delete(l.files, key)
	l.mu.Unlock()
	return nil
}

func (l *LocalFileStore) GeneratePresignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "local://" + key, nil
}
