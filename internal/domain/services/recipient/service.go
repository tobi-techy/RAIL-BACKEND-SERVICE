package recipient

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Recipient represents a withdrawal destination
type Recipient struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"user_id"`
	ProviderID string    `json:"provider_id"` // Bridge wallet/address ID
	Name       string    `json:"name"`
	Schema     string    `json:"schema"`  // "evm" or "solana"
	Address    string    `json:"address"` // Blockchain address
	IsDefault  bool      `json:"is_default"`
	IsVerified bool      `json:"is_verified"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Repository defines recipient persistence operations
type Repository interface {
	Create(ctx context.Context, r *Recipient) error
	GetByID(ctx context.Context, id uuid.UUID) (*Recipient, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*Recipient, error)
	GetByProviderID(ctx context.Context, providerID string) (*Recipient, error)
	GetDefault(ctx context.Context, userID uuid.UUID) (*Recipient, error)
	SetDefault(ctx context.Context, userID, recipientID uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// CreateRequest represents a request to create a recipient
type CreateRequest struct {
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	Schema    string    `json:"schema"`  // "evm" or "solana"
	Address   string    `json:"address"`
	IsDefault bool      `json:"is_default"`
}

// Service handles recipient management
type Service struct {
	repo   Repository
	logger *zap.Logger
}

// NewService creates a new recipient service
func NewService(repo Repository, logger *zap.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

// Create creates a new recipient and stores locally
func (s *Service) Create(ctx context.Context, req *CreateRequest) (*Recipient, error) {
	if req.Schema != "evm" && req.Schema != "solana" {
		return nil, fmt.Errorf("invalid schema: must be 'evm' or 'solana'")
	}

	if err := s.validateAddress(req.Schema, req.Address); err != nil {
		return nil, fmt.Errorf("invalid address: %w", err)
	}

	// Generate provider ID (address-based for Bridge)
	providerID := fmt.Sprintf("%s:%s", req.Schema, req.Address)

	recipient := &Recipient{
		ID:         uuid.New(),
		UserID:     req.UserID,
		ProviderID: providerID,
		Name:       req.Name,
		Schema:     req.Schema,
		Address:    req.Address,
		IsDefault:  req.IsDefault,
		IsVerified: true, // Address validation passed
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := s.repo.Create(ctx, recipient); err != nil {
		return nil, fmt.Errorf("failed to create recipient: %w", err)
	}

	if req.IsDefault {
		if err := s.repo.SetDefault(ctx, req.UserID, recipient.ID); err != nil {
			s.logger.Warn("Failed to set default recipient", zap.Error(err))
		}
	}

	s.logger.Info("Recipient created",
		zap.String("recipient_id", recipient.ID.String()),
		zap.String("user_id", req.UserID.String()),
		zap.String("address", req.Address))

	return recipient, nil
}

// GetByID retrieves a recipient by ID
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*Recipient, error) {
	return s.repo.GetByID(ctx, id)
}

// GetByUserID retrieves all recipients for a user
func (s *Service) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*Recipient, error) {
	return s.repo.GetByUserID(ctx, userID)
}

// GetDefault retrieves the default recipient for a user
func (s *Service) GetDefault(ctx context.Context, userID uuid.UUID) (*Recipient, error) {
	return s.repo.GetDefault(ctx, userID)
}

// SetDefault sets a recipient as the default for a user
func (s *Service) SetDefault(ctx context.Context, userID, recipientID uuid.UUID) error {
	return s.repo.SetDefault(ctx, userID, recipientID)
}

// Delete removes a recipient
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// validateAddress validates blockchain address format
func (s *Service) validateAddress(schema, address string) error {
	switch schema {
	case "evm":
		if len(address) != 42 || !strings.HasPrefix(address, "0x") {
			return fmt.Errorf("invalid EVM address format")
		}
	case "solana":
		if len(address) < 32 || len(address) > 44 {
			return fmt.Errorf("invalid Solana address format")
		}
	default:
		return fmt.Errorf("unsupported schema: %s", schema)
	}
	return nil
}
