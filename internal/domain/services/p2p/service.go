package p2p

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// UserLookup provides user lookup methods
type UserLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
	GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error)
	GetByPhone(ctx context.Context, phone string) (*entities.UserProfile, error)
	GetByRailTag(ctx context.Context, railTag string) (*entities.UserProfile, error)
}

// UserUpdater provides user update methods
type UserUpdater interface {
	SetRailTag(ctx context.Context, userID uuid.UUID, railTag string) error
}

// BalanceProvider provides balance operations
type BalanceProvider interface {
	GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// TransferExecutor executes the actual balance transfer
type TransferExecutor interface {
	TransferBetweenUsers(ctx context.Context, fromUserID, toUserID uuid.UUID, amount decimal.Decimal, description string) error
}

// Repository handles P2P transfer persistence
type Repository interface {
	Create(ctx context.Context, transfer *entities.P2PTransfer) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.P2PTransfer, error)
	GetByClaimToken(ctx context.Context, token string) (*entities.P2PTransfer, error)
	GetBySender(ctx context.Context, senderID uuid.UUID, limit, offset int) ([]*entities.P2PTransfer, error)
	GetPendingByIdentifier(ctx context.Context, email, phone string) ([]*entities.P2PTransfer, error)
	GetExpired(ctx context.Context) ([]*entities.P2PTransfer, error)
	Update(ctx context.Context, transfer *entities.P2PTransfer) error
	UpsertRecentRecipient(ctx context.Context, userID, recipientID uuid.UUID) error
	GetRecentRecipients(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.P2PRecentRecipientWithUser, error)
}

// NotificationSender sends notifications
type NotificationSender interface {
	SendP2PInvite(ctx context.Context, identifier, identifierType string, senderName string, amount decimal.Decimal, claimToken string) error
	SendP2PReceived(ctx context.Context, recipientID uuid.UUID, senderName string, amount decimal.Decimal, note *string) error
	SendP2PClaimed(ctx context.Context, senderID uuid.UUID, recipientName string, amount decimal.Decimal) error
	SendP2PExpired(ctx context.Context, senderID uuid.UUID, identifier string, amount decimal.Decimal) error
}

// Service handles P2P transfer operations
type Service struct {
	repo         Repository
	userLookup   UserLookup
	userUpdater  UserUpdater
	balance      BalanceProvider
	transfer     TransferExecutor
	notification NotificationSender
	logger       *zap.Logger
}

// NewService creates a new P2P service
func NewService(
	repo Repository,
	userLookup UserLookup,
	balance BalanceProvider,
	transfer TransferExecutor,
	notification NotificationSender,
	logger *zap.Logger,
) *Service {
	return &Service{
		repo:         repo,
		userLookup:   userLookup,
		balance:      balance,
		transfer:     transfer,
		notification: notification,
		logger:       logger,
	}
}

// SetUserUpdater sets the user updater (for RailTag operations)
func (s *Service) SetUserUpdater(updater UserUpdater) {
	s.userUpdater = updater
}

var (
	emailRegex   = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	phoneRegex   = regexp.MustCompile(`^\+?[1-9]\d{6,14}$`)
	railTagRegex = regexp.MustCompile(`^[a-z0-9]{3,30}$`)
)

// SetRailTag sets a user's RailTag
func (s *Service) SetRailTag(ctx context.Context, userID uuid.UUID, railTag string) error {
	railTag = strings.ToLower(strings.TrimSpace(railTag))

	if !railTagRegex.MatchString(railTag) {
		return fmt.Errorf("invalid railtag: must be 3-30 lowercase alphanumeric characters")
	}

	// Check if already taken
	existing, err := s.userLookup.GetByRailTag(ctx, railTag)
	if err == nil && existing != nil && existing.ID != userID {
		return fmt.Errorf("railtag already taken")
	}

	if s.userUpdater == nil {
		return fmt.Errorf("user updater not configured")
	}

	return s.userUpdater.SetRailTag(ctx, userID, railTag)
}

// CheckRailTagAvailable checks if a RailTag is available
func (s *Service) CheckRailTagAvailable(ctx context.Context, railTag string) (bool, error) {
	railTag = strings.ToLower(strings.TrimSpace(railTag))

	if !railTagRegex.MatchString(railTag) {
		return false, fmt.Errorf("invalid railtag format")
	}

	_, err := s.userLookup.GetByRailTag(ctx, railTag)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// LookupRecipient looks up a recipient by identifier (railtag, email, or phone)
func (s *Service) LookupRecipient(ctx context.Context, identifier string) (*entities.P2PLookupResponse, error) {
	identifier = strings.TrimSpace(identifier)
	
	// Determine identifier type
	identifierType, normalized := s.parseIdentifier(identifier)
	if identifierType == "" {
		return &entities.P2PLookupResponse{
			Found:   false,
			CanSend: false,
			Message: "Invalid identifier format",
		}, nil
	}

	// Look up user
	var user *entities.UserProfile
	var err error

	switch identifierType {
	case entities.P2PIdentifierRailTag:
		user, err = s.userLookup.GetByRailTag(ctx, normalized)
	case entities.P2PIdentifierEmail:
		user, err = s.userLookup.GetByEmail(ctx, normalized)
	case entities.P2PIdentifierPhone:
		user, err = s.userLookup.GetByPhone(ctx, normalized)
	}

	if err != nil && err != sql.ErrNoRows {
		s.logger.Error("Failed to lookup user", zap.Error(err), zap.String("identifier", identifier))
		return nil, fmt.Errorf("lookup failed: %w", err)
	}

	if user != nil {
		return &entities.P2PLookupResponse{
			Found:          true,
			IdentifierType: identifierType,
			CanSend:        true,
			User: &entities.P2PUserInfo{
				ID:        user.ID,
				RailTag:   user.RailTag,
				FirstName: user.FirstName,
				LastName:  user.LastName,
			},
		}, nil
	}

	// User not found - can still send if valid email/phone (not railtag)
	if identifierType == entities.P2PIdentifierRailTag {
		return &entities.P2PLookupResponse{
			Found:          false,
			IdentifierType: identifierType,
			CanSend:        false,
			Message:        "RailTag not found",
		}, nil
	}

	return &entities.P2PLookupResponse{
		Found:          false,
		IdentifierType: identifierType,
		CanSend:        true,
		Message:        "Will be invited to Rail",
	}, nil
}

// Send initiates a P2P transfer
func (s *Service) Send(ctx context.Context, senderID uuid.UUID, req *entities.P2PSendRequest) (*entities.P2PTransferResponse, error) {
	// Parse amount
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("invalid amount")
	}

	// Check sender balance
	balance, err := s.balance.GetSpendBalance(ctx, senderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}
	if balance.LessThan(amount) {
		return nil, fmt.Errorf("insufficient balance")
	}

	// Lookup recipient
	lookup, err := s.LookupRecipient(ctx, req.Identifier)
	if err != nil {
		return nil, err
	}
	if !lookup.CanSend {
		return nil, fmt.Errorf("cannot send to this identifier: %s", lookup.Message)
	}

	_, normalized := s.parseIdentifier(req.Identifier)
	var note *string
	if req.Note != "" {
		note = &req.Note
	}

	transfer := entities.NewP2PTransfer(senderID, normalized, lookup.IdentifierType, amount, note)

	if lookup.Found && lookup.User != nil {
		// Instant transfer to existing user
		transfer.RecipientID = &lookup.User.ID
		transfer.Status = entities.P2PStatusCompleted
		now := time.Now()
		transfer.CompletedAt = &now

		// Execute balance transfer
		desc := "P2P transfer"
		if note != nil {
			desc = fmt.Sprintf("P2P: %s", *note)
		}
		if err := s.transfer.TransferBetweenUsers(ctx, senderID, lookup.User.ID, amount, desc); err != nil {
			return nil, fmt.Errorf("transfer failed: %w", err)
		}

		// Update recent recipients
		_ = s.repo.UpsertRecentRecipient(ctx, senderID, lookup.User.ID)

		// Notify recipient
		sender, _ := s.userLookup.GetByID(ctx, senderID)
		senderName := "Someone"
		if sender != nil && sender.FirstName != nil {
			senderName = *sender.FirstName
		}
		_ = s.notification.SendP2PReceived(ctx, lookup.User.ID, senderName, amount, note)

	} else {
		// Pending transfer - generate claim token
		token, err := entities.GenerateClaimToken()
		if err != nil {
			return nil, fmt.Errorf("failed to generate claim token: %w", err)
		}
		transfer.ClaimToken = &token
		now := time.Now()
		transfer.ClaimLinkSentAt = &now

		// TODO: Debit sender balance to escrow account
		// For now, we'll handle this when implementing the ledger integration

		// Send invite notification
		sender, _ := s.userLookup.GetByID(ctx, senderID)
		senderName := "Someone"
		if sender != nil && sender.FirstName != nil {
			senderName = *sender.FirstName
		}
		_ = s.notification.SendP2PInvite(ctx, normalized, string(lookup.IdentifierType), senderName, amount, token)
	}

	// Save transfer
	if err := s.repo.Create(ctx, transfer); err != nil {
		return nil, fmt.Errorf("failed to save transfer: %w", err)
	}

	message := "Sent!"
	if transfer.Status == entities.P2PStatusPending {
		message = fmt.Sprintf("Sent! %s will be notified to claim.", normalized)
	}

	return &entities.P2PTransferResponse{
		Transfer: transfer,
		Message:  message,
	}, nil
}

// ClaimByToken claims a pending transfer using the claim token
func (s *Service) ClaimByToken(ctx context.Context, token string, claimerID uuid.UUID) (*entities.P2PTransfer, error) {
	transfer, err := s.repo.GetByClaimToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("transfer not found")
	}

	if !transfer.IsPending() {
		return nil, fmt.Errorf("transfer is not claimable (status: %s)", transfer.Status)
	}

	if transfer.IsExpired() {
		return nil, fmt.Errorf("transfer has expired")
	}

	return s.completeClaim(ctx, transfer, claimerID)
}

// ClaimPendingForUser claims all pending transfers for a user (called after signup)
func (s *Service) ClaimPendingForUser(ctx context.Context, userID uuid.UUID, email, phone string) (int, error) {
	transfers, err := s.repo.GetPendingByIdentifier(ctx, email, phone)
	if err != nil {
		return 0, fmt.Errorf("failed to get pending transfers: %w", err)
	}

	claimed := 0
	for _, transfer := range transfers {
		if _, err := s.completeClaim(ctx, transfer, userID); err != nil {
			s.logger.Error("Failed to claim transfer",
				zap.String("transfer_id", transfer.ID.String()),
				zap.Error(err))
			continue
		}
		claimed++
	}

	return claimed, nil
}

func (s *Service) completeClaim(ctx context.Context, transfer *entities.P2PTransfer, claimerID uuid.UUID) (*entities.P2PTransfer, error) {
	// Execute balance transfer from escrow to claimer
	desc := "P2P claim"
	if transfer.Note != nil {
		desc = fmt.Sprintf("P2P claim: %s", *transfer.Note)
	}
	if err := s.transfer.TransferBetweenUsers(ctx, transfer.SenderID, claimerID, transfer.Amount, desc); err != nil {
		return nil, fmt.Errorf("claim transfer failed: %w", err)
	}

	// Update transfer
	now := time.Now()
	transfer.RecipientID = &claimerID
	transfer.Status = entities.P2PStatusClaimed
	transfer.CompletedAt = &now

	if err := s.repo.Update(ctx, transfer); err != nil {
		return nil, fmt.Errorf("failed to update transfer: %w", err)
	}

	// Update recent recipients
	_ = s.repo.UpsertRecentRecipient(ctx, transfer.SenderID, claimerID)

	// Notify sender
	claimer, _ := s.userLookup.GetByID(ctx, claimerID)
	claimerName := "Someone"
	if claimer != nil && claimer.FirstName != nil {
		claimerName = *claimer.FirstName
	}
	_ = s.notification.SendP2PClaimed(ctx, transfer.SenderID, claimerName, transfer.Amount)

	return transfer, nil
}

// Cancel cancels a pending transfer
func (s *Service) Cancel(ctx context.Context, transferID, senderID uuid.UUID) error {
	transfer, err := s.repo.GetByID(ctx, transferID)
	if err != nil {
		return fmt.Errorf("transfer not found")
	}

	if transfer.SenderID != senderID {
		return fmt.Errorf("not authorized to cancel this transfer")
	}

	if !transfer.CanCancel() {
		return fmt.Errorf("transfer cannot be cancelled (status: %s)", transfer.Status)
	}

	// TODO: Refund from escrow to sender balance

	now := time.Now()
	transfer.Status = entities.P2PStatusCancelled
	transfer.CancelledAt = &now

	return s.repo.Update(ctx, transfer)
}

// GetTransfers returns transfers for a user
func (s *Service) GetTransfers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.P2PTransfer, error) {
	return s.repo.GetBySender(ctx, userID, limit, offset)
}

// GetRecentRecipients returns recent recipients for quick access
func (s *Service) GetRecentRecipients(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.P2PRecentRecipientWithUser, error) {
	if limit <= 0 {
		limit = 10
	}
	return s.repo.GetRecentRecipients(ctx, userID, limit)
}

// ProcessExpiredTransfers processes expired transfers (called by worker)
func (s *Service) ProcessExpiredTransfers(ctx context.Context) (int, error) {
	transfers, err := s.repo.GetExpired(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get expired transfers: %w", err)
	}

	processed := 0
	for _, transfer := range transfers {
		// TODO: Refund from escrow to sender

		transfer.Status = entities.P2PStatusExpired
		if err := s.repo.Update(ctx, transfer); err != nil {
			s.logger.Error("Failed to mark transfer expired",
				zap.String("transfer_id", transfer.ID.String()),
				zap.Error(err))
			continue
		}

		// Notify sender
		_ = s.notification.SendP2PExpired(ctx, transfer.SenderID, transfer.RecipientIdentifier, transfer.Amount)
		processed++
	}

	return processed, nil
}

// parseIdentifier determines the type and normalizes the identifier
func (s *Service) parseIdentifier(identifier string) (entities.P2PIdentifierType, string) {
	identifier = strings.TrimSpace(identifier)

	// Check for RailTag (starts with $ or matches pattern)
	if strings.HasPrefix(identifier, "$") {
		tag := strings.ToLower(strings.TrimPrefix(identifier, "$"))
		if railTagRegex.MatchString(tag) {
			return entities.P2PIdentifierRailTag, tag
		}
	}
	
	// Check if it's a plain railtag (no $)
	lower := strings.ToLower(identifier)
	if railTagRegex.MatchString(lower) && !strings.Contains(identifier, "@") && !strings.HasPrefix(identifier, "+") {
		// Could be railtag or just text - prefer email/phone detection first
	}

	// Check for email
	if emailRegex.MatchString(identifier) {
		return entities.P2PIdentifierEmail, strings.ToLower(identifier)
	}

	// Check for phone
	normalized := strings.ReplaceAll(identifier, " ", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	if phoneRegex.MatchString(normalized) {
		return entities.P2PIdentifierPhone, normalized
	}

	// Default to railtag if alphanumeric
	if railTagRegex.MatchString(lower) {
		return entities.P2PIdentifierRailTag, lower
	}

	return "", ""
}
