package recipient

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/adapters/due"
	"go.uber.org/zap"
)

// Recipient represents a withdrawal destination
type Recipient struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"user_id"`
	DueID      string    `json:"due_id"`
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
	GetByDueID(ctx context.Context, dueID string) (*Recipient, error)
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
	repo      Repository
	dueClient *due.Client
	logger    *zap.Logger
}

// NewService creates a new recipient service
func NewService(repo Repository, dueClient *due.Client, logger *zap.Logger) *Service {
	return &Service{repo: repo, dueClient: dueClient, logger: logger}
}

// Create creates a new recipient in Due and stores locally
func (s *Service) Create(ctx context.Context, req *CreateRequest) (*Recipient, error) {
	// Validate schema
	if req.Schema != "evm" && req.Schema != "solana" {
		return nil, fmt.Errorf("invalid schema: must be 'evm' or 'solana'")
	}

	// Validate address format
	if err := s.validateAddress(req.Schema, req.Address); err != nil {
		return nil, fmt.Errorf("invalid address: %w", err)
	}

	// Create recipient in Due API
	dueReq := &due.CreateRecipientRequest{
		Name: req.Name,
		Details: due.RecipientDetails{
			Schema:  req.Schema,
			Address: req.Address,
		},
		IsExternal: true,
	}

	dueResp, err := s.dueClient.CreateRecipient(ctx, dueReq)
	if err != nil {
		s.logger.Error("Failed to create recipient in Due", zap.Error(err))
		return nil, fmt.Errorf("failed to create recipient: %w", err)
	}

	// Store locally
	recipient := &Recipient{
		ID:         uuid.New(),
		UserID:     req.UserID,
		DueID:      dueResp.ID,
		Name:       req.Name,
		Schema:     req.Schema,
		Address:    req.Address,
		IsDefault:  req.IsDefault,
		IsVerified: dueResp.IsActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := s.repo.Create(ctx, recipient); err != nil {
		s.logger.Error("Failed to store recipient", zap.Error(err))
		return nil, fmt.Errorf("failed to store recipient: %w", err)
	}

	// Set as default if requested
	if req.IsDefault {
		_ = s.repo.SetDefault(ctx, req.UserID, recipient.ID)
	}

	s.logger.Info("Recipient created",
		zap.String("id", recipient.ID.String()),
		zap.String("due_id", dueResp.ID))

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

// GetDueRecipientID returns the Due recipient ID for withdrawals
func (s *Service) GetDueRecipientID(ctx context.Context, userID uuid.UUID, recipientID *uuid.UUID) (string, error) {
	var recipient *Recipient
	var err error

	if recipientID != nil {
		recipient, err = s.repo.GetByID(ctx, *recipientID)
	} else {
		recipient, err = s.repo.GetDefault(ctx, userID)
	}

	if err != nil {
		return "", fmt.Errorf("recipient not found: %w", err)
	}
	if recipient == nil {
		return "", fmt.Errorf("no recipient configured")
	}

	return recipient.DueID, nil
}

// validateAddress validates blockchain address format
func (s *Service) validateAddress(schema, address string) error {
	switch schema {
	case "evm":
		if len(address) != 42 || address[:2] != "0x" {
			return fmt.Errorf("invalid EVM address format")
		}
	case "solana":
		if len(address) < 32 || len(address) > 44 {
			return fmt.Errorf("invalid Solana address format")
		}
	}
	return nil
}
