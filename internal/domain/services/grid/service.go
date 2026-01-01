package grid

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/grid"
	"github.com/rail-service/rail_service/pkg/crypto"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Service handles Grid business logic
type Service struct {
	gridClient    GridClient
	gridRepo      Repository
	encryptionKey string
	logger        *zap.Logger
}

// NewService creates a new Grid service
func NewService(
	gridClient GridClient,
	gridRepo Repository,
	encryptionKey string,
	logger *zap.Logger,
) *Service {
	return &Service{
		gridClient:    gridClient,
		gridRepo:      gridRepo,
		encryptionKey: encryptionKey,
		logger:        logger,
	}
}

// AccountCreationStatus represents the status of account creation
type AccountCreationStatus struct {
	Email     string    `json:"email"`
	OTPSent   bool      `json:"otp_sent"`
	ExpiresAt time.Time `json:"expires_at"`
}

// KYCInitiationResponse represents the response from initiating KYC
type KYCInitiationResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// WithdrawalRequest represents a withdrawal request
type WithdrawalRequest struct {
	UserID        uuid.UUID       `json:"user_id"`
	Amount        decimal.Decimal `json:"amount"`
	Currency      string          `json:"currency"`
	DestType      string          `json:"dest_type"` // ach, wire, sepa
	AccountNumber string          `json:"account_number"`
	RoutingNumber string          `json:"routing_number,omitempty"`
	IBAN          string          `json:"iban,omitempty"`
	BIC           string          `json:"bic,omitempty"`
}

// InitiateAccountCreation starts Grid account creation (sends OTP to email)
func (s *Service) InitiateAccountCreation(ctx context.Context, userID uuid.UUID, email string) (*AccountCreationStatus, error) {
	s.logger.Info("initiating Grid account creation", zap.String("email", email), zap.String("userID", userID.String()))

	// Check if account already exists
	existing, err := s.gridRepo.GetAccountByEmail(ctx, email)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("grid account already exists for email: %s", email)
	}

	// Call Grid API to send OTP
	resp, err := s.gridClient.CreateAccount(ctx, email)
	if err != nil {
		s.logger.Error("failed to initiate Grid account", zap.Error(err))
		return nil, fmt.Errorf("failed to initiate account creation: %w", err)
	}

	// Create pending account record
	account := &entities.GridAccount{
		ID:        uuid.New(),
		UserID:    userID,
		Email:     email,
		Status:    entities.GridAccountStatusPending,
		KYCStatus: entities.GridKYCStatusNone,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := s.gridRepo.CreateAccount(ctx, account); err != nil {
		s.logger.Error("failed to create pending Grid account", zap.Error(err))
		return nil, fmt.Errorf("failed to save account: %w", err)
	}

	return &AccountCreationStatus{
		Email:     resp.Email,
		OTPSent:   resp.OTPSent,
		ExpiresAt: resp.ExpiresAt,
	}, nil
}

// CompleteAccountCreation verifies OTP and completes account creation
func (s *Service) CompleteAccountCreation(ctx context.Context, email, otp string) (*entities.GridAccount, error) {
	s.logger.Info("completing Grid account creation", zap.String("email", email))

	// Get pending account
	account, err := s.gridRepo.GetAccountByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}

	if account.Status != entities.GridAccountStatusPending {
		return nil, fmt.Errorf("account already verified")
	}

	// Generate session secrets
	secrets, err := s.gridClient.GenerateSessionSecrets()
	if err != nil {
		s.logger.Error("failed to generate session secrets", zap.Error(err))
		return nil, fmt.Errorf("failed to generate session secrets: %w", err)
	}

	// Verify OTP with Grid
	gridAccount, err := s.gridClient.VerifyOTP(ctx, email, otp, secrets)
	if err != nil {
		s.logger.Error("failed to verify OTP", zap.Error(err))
		return nil, fmt.Errorf("failed to verify OTP: %w", err)
	}

	// Encrypt session secrets
	encryptedSecrets, err := s.encryptSecrets(secrets)
	if err != nil {
		s.logger.Error("failed to encrypt session secrets", zap.Error(err))
		return nil, fmt.Errorf("failed to encrypt secrets: %w", err)
	}

	// Update account
	account.Address = gridAccount.Address
	account.Status = entities.GridAccountStatusActive
	account.EncryptedSessionSecret = encryptedSecrets
	account.UpdatedAt = time.Now().UTC()

	if err := s.gridRepo.UpdateAccount(ctx, account); err != nil {
		s.logger.Error("failed to update Grid account", zap.Error(err))
		return nil, fmt.Errorf("failed to update account: %w", err)
	}

	s.logger.Info("Grid account created successfully", zap.String("address", account.Address))
	return account, nil
}

// GetOrRefreshSession retrieves and decrypts session secrets
func (s *Service) GetOrRefreshSession(ctx context.Context, userID uuid.UUID) (*grid.SessionSecrets, error) {
	account, err := s.gridRepo.GetAccountByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}

	if account.EncryptedSessionSecret == "" {
		return nil, fmt.Errorf("no session secrets stored")
	}

	return s.decryptSecrets(account.EncryptedSessionSecret)
}

// StoreSessionSecrets encrypts and stores session secrets
func (s *Service) StoreSessionSecrets(ctx context.Context, gridAccountID uuid.UUID, secrets *grid.SessionSecrets) error {
	account, err := s.gridRepo.GetAccountByUserID(ctx, gridAccountID)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}

	encrypted, err := s.encryptSecrets(secrets)
	if err != nil {
		return fmt.Errorf("failed to encrypt secrets: %w", err)
	}

	account.EncryptedSessionSecret = encrypted
	account.UpdatedAt = time.Now().UTC()

	return s.gridRepo.UpdateAccount(ctx, account)
}

// InitiateKYC requests KYC link from Grid
func (s *Service) InitiateKYC(ctx context.Context, userID uuid.UUID) (*KYCInitiationResponse, error) {
	s.logger.Info("initiating KYC", zap.String("userID", userID.String()))

	account, err := s.gridRepo.GetAccountByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}

	if account.Status != entities.GridAccountStatusActive {
		return nil, fmt.Errorf("account not active")
	}

	resp, err := s.gridClient.RequestKYCLink(ctx, account.Address)
	if err != nil {
		s.logger.Error("failed to request KYC link", zap.Error(err))
		return nil, fmt.Errorf("failed to request KYC link: %w", err)
	}

	// Update KYC status to pending
	account.KYCStatus = entities.GridKYCStatusPending
	account.UpdatedAt = time.Now().UTC()
	if err := s.gridRepo.UpdateAccount(ctx, account); err != nil {
		s.logger.Warn("failed to update KYC status", zap.Error(err))
	}

	return &KYCInitiationResponse{
		URL:       resp.URL,
		ExpiresAt: resp.ExpiresAt,
	}, nil
}

// GetKYCStatus checks current KYC status
func (s *Service) GetKYCStatus(ctx context.Context, userID uuid.UUID) (*grid.KYCStatus, error) {
	account, err := s.gridRepo.GetAccountByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}

	return s.gridClient.GetKYCStatus(ctx, account.Address)
}

// ProcessKYCWebhook handles Grid KYC status updates
func (s *Service) ProcessKYCWebhook(ctx context.Context, payload *entities.GridKYCWebhook) error {
	s.logger.Info("processing KYC webhook", zap.String("address", payload.Address), zap.String("status", payload.Status))

	account, err := s.gridRepo.GetAccountByAddress(ctx, payload.Address)
	if err != nil {
		return fmt.Errorf("account not found for address %s: %w", payload.Address, err)
	}

	// Map status
	var kycStatus entities.GridKYCStatus
	switch payload.Status {
	case "approved":
		kycStatus = entities.GridKYCStatusApproved
	case "rejected":
		kycStatus = entities.GridKYCStatusRejected
	case "pending":
		kycStatus = entities.GridKYCStatusPending
	default:
		kycStatus = entities.GridKYCStatusPending
	}

	account.KYCStatus = kycStatus
	account.UpdatedAt = time.Now().UTC()

	if err := s.gridRepo.UpdateAccount(ctx, account); err != nil {
		s.logger.Error("failed to update KYC status", zap.Error(err))
		return fmt.Errorf("failed to update KYC status: %w", err)
	}

	s.logger.Info("KYC status updated", zap.String("address", payload.Address), zap.String("status", string(kycStatus)))
	return nil
}

// SetupVirtualAccount creates virtual account after KYC approval
func (s *Service) SetupVirtualAccount(ctx context.Context, userID uuid.UUID) (*entities.GridVirtualAccount, error) {
	s.logger.Info("setting up virtual account", zap.String("userID", userID.String()))

	account, err := s.gridRepo.GetAccountByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}

	if account.KYCStatus != entities.GridKYCStatusApproved {
		return nil, fmt.Errorf("KYC not approved")
	}

	// Request virtual account from Grid
	va, err := s.gridClient.RequestVirtualAccount(ctx, account.Address)
	if err != nil {
		s.logger.Error("failed to request virtual account", zap.Error(err))
		return nil, fmt.Errorf("failed to request virtual account: %w", err)
	}

	// Store virtual account
	gridVA := &entities.GridVirtualAccount{
		ID:            uuid.New(),
		GridAccountID: account.ID,
		UserID:        userID,
		ExternalID:    va.ID,
		AccountNumber: va.AccountNumber,
		RoutingNumber: va.RoutingNumber,
		BankName:      va.BankName,
		Currency:      va.Currency,
		Status:        va.Status,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	if err := s.gridRepo.CreateVirtualAccount(ctx, gridVA); err != nil {
		s.logger.Error("failed to save virtual account", zap.Error(err))
		return nil, fmt.Errorf("failed to save virtual account: %w", err)
	}

	s.logger.Info("virtual account created", zap.String("accountNumber", va.AccountNumber))
	return gridVA, nil
}

// GetVirtualAccounts returns user's virtual accounts
func (s *Service) GetVirtualAccounts(ctx context.Context, userID uuid.UUID) ([]entities.GridVirtualAccount, error) {
	return s.gridRepo.GetVirtualAccountsByUserID(ctx, userID)
}

// InitiateWithdrawal creates payment intent for off-ramp
func (s *Service) InitiateWithdrawal(ctx context.Context, req *WithdrawalRequest) (*entities.GridPaymentIntent, error) {
	s.logger.Info("initiating withdrawal", zap.String("userID", req.UserID.String()), zap.String("amount", req.Amount.String()))

	account, err := s.gridRepo.GetAccountByUserID(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}

	if account.KYCStatus != entities.GridKYCStatusApproved {
		return nil, fmt.Errorf("KYC not approved")
	}

	// Create payment intent with Grid
	gridReq := &grid.PaymentIntentRequest{
		AccountAddress: account.Address,
		Amount:         req.Amount,
		Currency:       req.Currency,
		Destination: grid.PaymentDest{
			Type:          req.DestType,
			AccountNumber: req.AccountNumber,
			RoutingNumber: req.RoutingNumber,
			IBAN:          req.IBAN,
			BIC:           req.BIC,
		},
	}

	pi, err := s.gridClient.CreatePaymentIntent(ctx, gridReq)
	if err != nil {
		s.logger.Error("failed to create payment intent", zap.Error(err))
		return nil, fmt.Errorf("failed to create payment intent: %w", err)
	}

	// Store payment intent
	gridPI := &entities.GridPaymentIntent{
		ID:            uuid.New(),
		GridAccountID: account.ID,
		UserID:        req.UserID,
		ExternalID:    pi.ID,
		Amount:        pi.Amount.String(),
		Currency:      pi.Currency,
		Status:        pi.Status,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	if err := s.gridRepo.CreatePaymentIntent(ctx, gridPI); err != nil {
		s.logger.Error("failed to save payment intent", zap.Error(err))
		return nil, fmt.Errorf("failed to save payment intent: %w", err)
	}

	s.logger.Info("payment intent created", zap.String("intentID", pi.ID))
	return gridPI, nil
}

// GetAccount returns the Grid account for a user
func (s *Service) GetAccount(ctx context.Context, userID uuid.UUID) (*entities.GridAccount, error) {
	return s.gridRepo.GetAccountByUserID(ctx, userID)
}

// encryptSecrets encrypts session secrets using AES-256-GCM
func (s *Service) encryptSecrets(secrets *grid.SessionSecrets) (string, error) {
	jsonBytes, err := json.Marshal(secrets)
	if err != nil {
		return "", fmt.Errorf("failed to marshal secrets: %w", err)
	}
	return crypto.Encrypt(string(jsonBytes), s.encryptionKey)
}

// decryptSecrets decrypts session secrets
func (s *Service) decryptSecrets(encrypted string) (*grid.SessionSecrets, error) {
	decrypted, err := crypto.Decrypt(encrypted, s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt secrets: %w", err)
	}
	var secrets grid.SessionSecrets
	if err := json.Unmarshal([]byte(decrypted), &secrets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal secrets: %w", err)
	}
	return &secrets, nil
}
