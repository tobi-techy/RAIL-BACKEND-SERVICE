package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// P2PNotificationSender implements the P2P notification interface
type P2PNotificationSender struct {
	emailService *EmailService
	userRepo     interface {
		GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
	}
	baseURL string
	logger  *zap.Logger
}

// NewP2PNotificationSender creates a new P2P notification sender
func NewP2PNotificationSender(
	emailService *EmailService,
	userRepo interface {
		GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
	},
	baseURL string,
	logger *zap.Logger,
) *P2PNotificationSender {
	return &P2PNotificationSender{
		emailService: emailService,
		userRepo:     userRepo,
		baseURL:      baseURL,
		logger:       logger,
	}
}

// SendP2PInvite sends an invite to a non-Rail user
func (n *P2PNotificationSender) SendP2PInvite(ctx context.Context, identifier, identifierType string, senderName string, amount decimal.Decimal, claimToken string) error {
	if identifierType != "email" {
		n.logger.Info("Skipping P2P invite for non-email identifier", zap.String("type", identifierType))
		return nil // Only email invites supported for now
	}

	// Validate claimToken is a valid UUID before embedding in URL
	if _, err := uuid.Parse(claimToken); err != nil {
		n.logger.Error("Invalid claimToken - not a valid UUID",
			zap.String("claimToken", claimToken),
			zap.Error(err))
		return fmt.Errorf("invalid claim token: must be a valid UUID")
	}

	claimURL := fmt.Sprintf("%s/claim/%s", n.baseURL, claimToken)
	amountStr := fmt.Sprintf("$%s", amount.StringFixed(2))

	return n.emailService.SendP2PInviteEmail(ctx, identifier, senderName, amountStr, claimURL)
}

// SendP2PReceived notifies a user they received money
func (n *P2PNotificationSender) SendP2PReceived(ctx context.Context, recipientID uuid.UUID, senderName string, amount decimal.Decimal, note *string) error {
	recipient, err := n.userRepo.GetByID(ctx, recipientID)
	if err != nil {
		n.logger.Warn("Failed to get recipient for P2P notification", zap.Error(err))
		return nil
	}

	amountStr := fmt.Sprintf("$%s", amount.StringFixed(2))
	return n.emailService.SendP2PReceivedEmail(ctx, recipient.Email, senderName, amountStr, note)
}

// SendP2PClaimed notifies sender that their transfer was claimed
func (n *P2PNotificationSender) SendP2PClaimed(ctx context.Context, senderID uuid.UUID, recipientName string, amount decimal.Decimal) error {
	sender, err := n.userRepo.GetByID(ctx, senderID)
	if err != nil {
		n.logger.Warn("Failed to get sender for P2P notification", zap.Error(err))
		return nil
	}

	amountStr := fmt.Sprintf("$%s", amount.StringFixed(2))
	return n.emailService.SendP2PClaimedEmail(ctx, sender.Email, recipientName, amountStr)
}

// SendP2PExpired notifies sender that their transfer expired
func (n *P2PNotificationSender) SendP2PExpired(ctx context.Context, senderID uuid.UUID, identifier string, amount decimal.Decimal) error {
	sender, err := n.userRepo.GetByID(ctx, senderID)
	if err != nil {
		n.logger.Warn("Failed to get sender for P2P notification", zap.Error(err))
		return nil
	}

	amountStr := fmt.Sprintf("$%s", amount.StringFixed(2))
	return n.emailService.SendP2PExpiredEmail(ctx, sender.Email, identifier, amountStr)
}
