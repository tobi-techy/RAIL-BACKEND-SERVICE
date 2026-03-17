package p2p

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

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

// WalletLookup provides wallet lookup for finding Bridge wallet IDs
type WalletLookup interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
}

// TransferExecutor executes the actual balance transfer
type TransferExecutor interface {
	TransferBetweenUsers(ctx context.Context, fromUserID, toUserID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error
	ReserveFunds(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error
	CreditUserFromSystem(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error
}

// Repository handles P2P transfer persistence
type Repository interface {
	Create(ctx context.Context, transfer *entities.P2PTransfer) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.P2PTransfer, error)
	GetByClaimToken(ctx context.Context, token string) (*entities.P2PTransfer, error)
	GetBySender(ctx context.Context, senderID uuid.UUID, limit, offset int) ([]*entities.P2PTransfer, error)
	GetPendingByIdentifier(ctx context.Context, email, phone string) ([]*entities.P2PTransfer, error)
	GetExpired(ctx context.Context) ([]*entities.P2PTransfer, error)
	AcquirePendingByID(ctx context.Context, id uuid.UUID) (*entities.P2PTransfer, error)
	AcquirePendingByClaimToken(ctx context.Context, token string) (*entities.P2PTransfer, error)
	ReleaseProcessing(ctx context.Context, id uuid.UUID) error
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

// BridgeOfframp sends USDC to a recipient's bank account via Bridge
type BridgeOfframp interface {
	CreateRecipient(ctx context.Context, req map[string]interface{}) (string, error)
	InitiateTransfer(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error)
}

// ClaimToBankRequest holds the recipient's bank details for a no-app claim
type ClaimToBankRequest struct {
	AccountHolderName string
	RoutingNumber     string
	AccountNumber     string
}

// Service handles P2P transfer operations
type Service struct {
	repo          Repository
	userLookup    UserLookup
	userUpdater   UserUpdater
	balance       BalanceProvider
	walletLookup  WalletLookup
	transfer      TransferExecutor
	notification  NotificationSender
	bridgeOfframp BridgeOfframp
	logger        *zap.Logger
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

// SetBridgeOfframp wires the Bridge offramp adapter (optional — enables bank claim flow)
func (s *Service) SetBridgeOfframp(b BridgeOfframp) {
	s.bridgeOfframp = b
}

// SetUserUpdater sets the user updater (for RailTag operations)
func (s *Service) SetUserUpdater(updater UserUpdater) {
	s.userUpdater = updater
}

// SetWalletLookup sets the wallet lookup (required for bank claim flow)
func (s *Service) SetWalletLookup(w WalletLookup) {
	s.walletLookup = w
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

	// Validate note: max 500 chars, strip whitespace, reject suspicious patterns
	var note *string
	if req.Note != "" {
		// Strip leading/trailing whitespace
		cleanedNote := strings.TrimSpace(req.Note)

		// Check maximum length
		if len(cleanedNote) > 500 {
			return nil, fmt.Errorf("note exceeds maximum length of 500 characters")
		}

		// Reject suspicious patterns (HTML tags, script tags, javascript:, etc.)
		lowerNote := strings.ToLower(cleanedNote)
		suspiciousPatterns := []string{"<script", "javascript:", "onerror=", "onclick=", "<iframe", "eval(", "expression("}
		for _, pattern := range suspiciousPatterns {
			if strings.Contains(lowerNote, pattern) {
				return nil, fmt.Errorf("note contains invalid characters or patterns")
			}
		}

		// Reject excessive special characters (more than 30% special chars)
		specialChars := 0
		for _, c := range cleanedNote {
			if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !unicode.IsSpace(c) {
				specialChars++
			}
		}
		if len(cleanedNote) > 0 && float64(specialChars)/float64(len(cleanedNote)) > 0.3 {
			return nil, fmt.Errorf("note contains too many special characters")
		}

		note = &cleanedNote
	}

	transfer := entities.NewP2PTransfer(senderID, normalized, lookup.IdentifierType, amount, note)
	var afterCreate func()
	var rollbackOnCreateFailure func()

	if lookup.Found && lookup.User != nil {
		if lookup.User.ID == senderID {
			return nil, fmt.Errorf("cannot send money to yourself")
		}

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
		if err := s.transfer.TransferBetweenUsers(ctx, senderID, lookup.User.ID, amount, desc, "p2p-send-"+transfer.ID.String()); err != nil {
			return nil, fmt.Errorf("transfer failed: %w", err)
		}

		sender, _ := s.userLookup.GetByID(ctx, senderID)
		senderName := "Someone"
		if sender != nil && sender.FirstName != nil {
			senderName = *sender.FirstName
		}
		recipientID := lookup.User.ID
		afterCreate = func() {
			_ = s.repo.UpsertRecentRecipient(ctx, senderID, recipientID)
			_ = s.notification.SendP2PReceived(ctx, recipientID, senderName, amount, note)
		}
		rollbackOnCreateFailure = func() {
			rollbackDesc := "P2P transfer rollback"
			if err := s.transfer.TransferBetweenUsers(ctx, recipientID, senderID, amount, rollbackDesc, "p2p-send-rollback-"+transfer.ID.String()); err != nil {
				s.logger.Error("Failed to roll back P2P transfer after persistence error",
					zap.String("transfer_id", transfer.ID.String()),
					zap.Error(err))
			}
		}

	} else {
		// Pending transfer - generate claim token before reserving funds so failures do not strand balances.
		token, err := entities.GenerateClaimToken()
		if err != nil {
			return nil, fmt.Errorf("failed to generate claim token: %w", err)
		}
		transfer.ClaimToken = &token
		now := time.Now()
		transfer.ClaimLinkSentAt = &now

		desc := "P2P pending transfer"
		if note != nil {
			desc = fmt.Sprintf("P2P pending: %s", *note)
		}
		if err := s.transfer.ReserveFunds(ctx, senderID, amount, desc, "p2p-reserve-"+transfer.ID.String()); err != nil {
			return nil, fmt.Errorf("failed to reserve funds: %w", err)
		}
		rollbackOnCreateFailure = func() {
			rollbackDesc := "P2P pending rollback"
			if err := s.transfer.CreditUserFromSystem(ctx, senderID, amount, rollbackDesc, "p2p-reserve-rollback-"+transfer.ID.String()); err != nil {
				s.logger.Error("Failed to release reserved P2P funds after persistence error",
					zap.String("transfer_id", transfer.ID.String()),
					zap.Error(err))
			}
		}

		// Send invite notification
		sender, _ := s.userLookup.GetByID(ctx, senderID)
		senderName := "Someone"
		if sender != nil && sender.FirstName != nil {
			senderName = *sender.FirstName
		}
		afterCreate = func() {
			_ = s.notification.SendP2PInvite(ctx, normalized, string(lookup.IdentifierType), senderName, amount, token)
		}
	}

	// Save transfer
	if err := s.repo.Create(ctx, transfer); err != nil {
		if rollbackOnCreateFailure != nil {
			rollbackOnCreateFailure()
		}
		return nil, fmt.Errorf("failed to save transfer: %w", err)
	}
	if afterCreate != nil {
		afterCreate()
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

// ClaimInfo is returned to the claim web page before the recipient submits bank details
type ClaimInfo struct {
	Amount     decimal.Decimal
	Currency   string
	SenderName string
	Note       *string
}

// GetClaimInfo returns public info about a pending transfer for the claim page
func (s *Service) GetClaimInfo(ctx context.Context, token string) (*ClaimInfo, error) {
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

	sender, _ := s.userLookup.GetByID(ctx, transfer.SenderID)
	senderName := "Someone"
	if sender != nil && sender.FirstName != nil {
		senderName = *sender.FirstName
	}
	return &ClaimInfo{
		Amount:     transfer.Amount,
		Currency:   transfer.Currency,
		SenderName: senderName,
		Note:       transfer.Note,
	}, nil
}

// ClaimToBank pays out a pending transfer directly to the recipient's bank via Bridge.
// No Rail account is required — the recipient just provides their bank details.
func (s *Service) ClaimToBank(ctx context.Context, token string, req ClaimToBankRequest) error {
	if s.bridgeOfframp == nil {
		return fmt.Errorf("bank claim not available")
	}

	transfer, err := s.repo.AcquirePendingByClaimToken(ctx, token)
	if err != nil {
		return fmt.Errorf("transfer not found")
	}
	defer func() {
		if transfer != nil && transfer.Status == entities.P2PStatusProcessing {
			if releaseErr := s.repo.ReleaseProcessing(ctx, transfer.ID); releaseErr != nil {
				s.logger.Error("Failed to release P2P bank claim lock",
					zap.String("transfer_id", transfer.ID.String()),
					zap.Error(releaseErr))
			}
		}
	}()
	if transfer.IsExpired() {
		return fmt.Errorf("transfer has expired")
	}

	// Resolve sender's Bridge wallet ID
	sourceWalletID, err := s.senderBridgeWalletID(ctx, transfer.SenderID)
	if err != nil {
		return fmt.Errorf("could not resolve sender wallet: %w", err)
	}

	senderProfile, err := s.userLookup.GetByID(ctx, transfer.SenderID)
	if err != nil {
		return fmt.Errorf("failed to load sender profile: %w", err)
	}
	if senderProfile == nil || senderProfile.BridgeCustomerID == nil || strings.TrimSpace(*senderProfile.BridgeCustomerID) == "" {
		return fmt.Errorf("sender bridge customer profile is not configured")
	}

	// Register the recipient's bank account with Bridge
	recipientID, err := s.bridgeOfframp.CreateRecipient(ctx, map[string]interface{}{
		"customer_id":         strings.TrimSpace(*senderProfile.BridgeCustomerID),
		"account_holder_name": strings.TrimSpace(req.AccountHolderName),
		"routing_number":      strings.ReplaceAll(strings.TrimSpace(req.RoutingNumber), " ", ""),
		"account_number":      strings.ReplaceAll(strings.TrimSpace(req.AccountNumber), " ", ""),
	})
	if err != nil {
		return fmt.Errorf("failed to register bank account: %w", err)
	}

	// Initiate the ACH payout via Bridge (USDC → USD ACH)
	initiateResult, err := s.bridgeOfframp.InitiateTransfer(ctx, map[string]interface{}{
		"amount":           transfer.Amount.StringFixed(2),
		"recipient_id":     recipientID,
		"source_wallet_id": sourceWalletID,
		"on_behalf_of":     transfer.SenderID.String(),
	})
	if err != nil {
		return fmt.Errorf("failed to initiate bank transfer: %w", err)
	}

	// Mark transfer claimed
	senderID := transfer.SenderID
	amount := transfer.Amount
	if providerTransferID := strings.TrimSpace(fmt.Sprintf("%v", initiateResult["id"])); providerTransferID != "" && providerTransferID != "<nil>" {
		transfer.ProviderTransferID = &providerTransferID
	}
	if providerStatus := strings.TrimSpace(fmt.Sprintf("%v", initiateResult["status"])); providerStatus != "" && providerStatus != "<nil>" {
		transfer.ProviderStatus = &providerStatus
	}
	now := time.Now()
	transfer.Status = entities.P2PStatusClaimed
	transfer.CompletedAt = &now
	if err := s.repo.Update(ctx, transfer); err != nil {
		return fmt.Errorf("failed to update transfer: %w", err)
	}
	transfer = nil

	_ = s.notification.SendP2PClaimed(ctx, senderID, req.AccountHolderName, amount)

	return nil
}

// senderBridgeWalletID returns the Bridge wallet ID for the sender's Solana spend wallet.
func (s *Service) senderBridgeWalletID(ctx context.Context, senderID uuid.UUID) (string, error) {
	if s.walletLookup == nil {
		return "", fmt.Errorf("wallet lookup not configured")
	}
	wallets, err := s.walletLookup.GetByUserID(ctx, senderID)
	if err != nil {
		return "", err
	}
	// Prefer Solana wallet (primary payout rail for Bridge)
	for _, w := range wallets {
		if string(w.Chain) == "SOL" && w.BridgeWalletID != "" {
			return w.BridgeWalletID, nil
		}
	}
	for _, w := range wallets {
		if w.BridgeWalletID != "" {
			return w.BridgeWalletID, nil
		}
	}
	return "", fmt.Errorf("no Bridge wallet found for user")
}

// ClaimByToken claims a pending transfer using the claim token
func (s *Service) ClaimByToken(ctx context.Context, token string, claimerID uuid.UUID) (*entities.P2PTransfer, error) {
	transfer, err := s.repo.AcquirePendingByClaimToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("transfer not found")
	}
	defer func() {
		if transfer != nil && transfer.Status == entities.P2PStatusProcessing {
			if releaseErr := s.repo.ReleaseProcessing(ctx, transfer.ID); releaseErr != nil {
				s.logger.Error("Failed to release P2P claim lock",
					zap.String("transfer_id", transfer.ID.String()),
					zap.Error(releaseErr))
			}
		}
	}()
	if transfer.IsExpired() {
		return nil, fmt.Errorf("transfer has expired")
	}

	claimer, err := s.userLookup.GetByID(ctx, claimerID)
	if err != nil {
		return nil, fmt.Errorf("failed to load claimer profile: %w", err)
	}
	if !s.transferMatchesUser(transfer, claimer) {
		return nil, fmt.Errorf("you are not eligible to claim this transfer")
	}

	claimed, err := s.completeClaim(ctx, transfer, claimerID)
	if err != nil {
		return nil, err
	}
	transfer = nil
	return claimed, nil
}

// ClaimPendingForUser claims all pending transfers for a user (called after signup)
func (s *Service) ClaimPendingForUser(ctx context.Context, userID uuid.UUID, email, phone string) (int, error) {
	transfers, err := s.repo.GetPendingByIdentifier(ctx, email, phone)
	if err != nil {
		return 0, fmt.Errorf("failed to get pending transfers: %w", err)
	}

	claimed := 0
	for _, transfer := range transfers {
		lockedTransfer, err := s.repo.AcquirePendingByID(ctx, transfer.ID)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			s.logger.Error("Failed to acquire pending transfer",
				zap.String("transfer_id", transfer.ID.String()),
				zap.Error(err))
			continue
		}

		if _, err := s.completeClaim(ctx, lockedTransfer, userID); err != nil {
			_ = s.repo.ReleaseProcessing(ctx, lockedTransfer.ID)
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
	// Release reserved funds from system buffer to claimer.
	desc := "P2P claim"
	if transfer.Note != nil {
		desc = fmt.Sprintf("P2P claim: %s", *transfer.Note)
	}
	if err := s.transfer.CreditUserFromSystem(ctx, claimerID, transfer.Amount, desc, "p2p-claim-"+transfer.ID.String()); err != nil {
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
	transfer, err := s.repo.AcquirePendingByID(ctx, transferID)
	if err != nil {
		return fmt.Errorf("transfer not found")
	}
	defer func() {
		if transfer != nil && transfer.Status == entities.P2PStatusProcessing {
			if releaseErr := s.repo.ReleaseProcessing(ctx, transfer.ID); releaseErr != nil {
				s.logger.Error("Failed to release P2P cancel lock",
					zap.String("transfer_id", transfer.ID.String()),
					zap.Error(releaseErr))
			}
		}
	}()
	if transfer.IsExpired() {
		return fmt.Errorf("transfer has expired")
	}

	if transfer.SenderID != senderID {
		return fmt.Errorf("not authorized to cancel this transfer")
	}

	desc := "P2P cancellation refund"
	if transfer.Note != nil {
		desc = fmt.Sprintf("P2P cancellation refund: %s", *transfer.Note)
	}
	if err := s.transfer.CreditUserFromSystem(ctx, transfer.SenderID, transfer.Amount, desc, "p2p-cancel-"+transfer.ID.String()); err != nil {
		return fmt.Errorf("failed to refund reserved funds: %w", err)
	}

	now := time.Now()
	transfer.Status = entities.P2PStatusCancelled
	transfer.CancelledAt = &now

	if err := s.repo.Update(ctx, transfer); err != nil {
		return err
	}
	transfer = nil
	return nil
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
		lockedTransfer, err := s.repo.AcquirePendingByID(ctx, transfer.ID)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			s.logger.Error("Failed to acquire expired transfer",
				zap.String("transfer_id", transfer.ID.String()),
				zap.Error(err))
			continue
		}

		desc := "P2P expiry refund"
		if lockedTransfer.Note != nil {
			desc = fmt.Sprintf("P2P expiry refund: %s", *lockedTransfer.Note)
		}
		if err := s.transfer.CreditUserFromSystem(ctx, lockedTransfer.SenderID, lockedTransfer.Amount, desc, "p2p-expire-"+lockedTransfer.ID.String()); err != nil {
			_ = s.repo.ReleaseProcessing(ctx, lockedTransfer.ID)
			s.logger.Error("Failed to refund expired transfer",
				zap.String("transfer_id", lockedTransfer.ID.String()),
				zap.Error(err))
			continue
		}

		lockedTransfer.Status = entities.P2PStatusExpired
		if err := s.repo.Update(ctx, lockedTransfer); err != nil {
			s.logger.Error("Failed to mark transfer expired",
				zap.String("transfer_id", lockedTransfer.ID.String()),
				zap.Error(err))
			_ = s.repo.ReleaseProcessing(ctx, lockedTransfer.ID)
			continue
		}

		// Notify sender
		_ = s.notification.SendP2PExpired(ctx, lockedTransfer.SenderID, lockedTransfer.RecipientIdentifier, lockedTransfer.Amount)
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

func (s *Service) transferMatchesUser(transfer *entities.P2PTransfer, user *entities.UserProfile) bool {
	if transfer == nil || user == nil {
		return false
	}

	switch transfer.IdentifierType {
	case entities.P2PIdentifierEmail:
		return strings.EqualFold(strings.TrimSpace(user.Email), strings.TrimSpace(transfer.RecipientIdentifier))
	case entities.P2PIdentifierPhone:
		if user.Phone == nil {
			return false
		}
		_, normalizedPhone := s.parseIdentifier(*user.Phone)
		return normalizedPhone != "" && normalizedPhone == transfer.RecipientIdentifier
	default:
		return false
	}
}
