package alpaca

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	alpacaAdapter "github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// AccountRepository interface for Alpaca account persistence
type AccountRepository interface {
	Create(ctx context.Context, account *entities.AlpacaAccount) error
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error)
	GetByAlpacaID(ctx context.Context, alpacaAccountID string) (*entities.AlpacaAccount, error)
	Update(ctx context.Context, account *entities.AlpacaAccount) error
	UpdateStatus(ctx context.Context, userID uuid.UUID, status entities.AlpacaAccountStatus) error
}

// UserProfileRepository interface for user profile data
type UserProfileRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.UserProfile, error)
}

// AccountService handles Alpaca account lifecycle management
type AccountService struct {
	alpacaClient    *alpacaAdapter.Client
	accountRepo     AccountRepository
	userProfileRepo UserProfileRepository
	logger          *zap.Logger
}

func NewAccountService(
	alpacaClient *alpacaAdapter.Client,
	accountRepo AccountRepository,
	userProfileRepo UserProfileRepository,
	logger *zap.Logger,
) *AccountService {
	return &AccountService{
		alpacaClient:    alpacaClient,
		accountRepo:     accountRepo,
		userProfileRepo: userProfileRepo,
		logger:          logger,
	}
}

// CreateAccountForUser creates an Alpaca brokerage account for a user
func (s *AccountService) CreateAccountForUser(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccountResponse, error) {
	// Check if user already has an Alpaca account
	existing, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check existing account: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("user already has an Alpaca account")
	}

	// Get user profile data
	profile, err := s.userProfileRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user profile: %w", err)
	}
	if profile == nil {
		return nil, fmt.Errorf("user profile not found")
	}

	// Build Alpaca account request from profile
	req := s.buildAccountRequest(profile)

	// Create account via Alpaca API
	alpacaResp, err := s.alpacaClient.CreateAccount(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create Alpaca account", zap.String("user_id", userID.String()), zap.Error(err))
		return nil, fmt.Errorf("create Alpaca account: %w", err)
	}

	// Store account mapping
	now := time.Now()
	account := &entities.AlpacaAccount{
		ID:                  uuid.New(),
		UserID:              userID,
		AlpacaAccountID:     alpacaResp.ID,
		AlpacaAccountNumber: alpacaResp.AccountNumber,
		Status:              alpacaResp.Status,
		AccountType:         entities.AlpacaAccountTypeTradingCash,
		Currency:            alpacaResp.Currency,
		BuyingPower:         alpacaResp.BuyingPower,
		Cash:                alpacaResp.Cash,
		PortfolioValue:      alpacaResp.PortfolioValue,
		TradingBlocked:      alpacaResp.TradingBlocked,
		TransfersBlocked:    alpacaResp.TransfersBlocked,
		AccountBlocked:      alpacaResp.AccountBlocked,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := s.accountRepo.Create(ctx, account); err != nil {
		s.logger.Error("Failed to store Alpaca account", zap.Error(err))
		return nil, fmt.Errorf("store account: %w", err)
	}

	s.logger.Info("Created Alpaca account for user",
		zap.String("user_id", userID.String()),
		zap.String("alpaca_account_id", alpacaResp.ID))

	return alpacaResp, nil
}

// GetUserAccount retrieves the Alpaca account for a user
func (s *AccountService) GetUserAccount(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error) {
	account, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, nil
	}

	// Optionally sync with Alpaca for fresh data
	if account.LastSyncedAt == nil || time.Since(*account.LastSyncedAt) > 5*time.Minute {
		if err := s.SyncAccount(ctx, account); err != nil {
			s.logger.Warn("Failed to sync account", zap.Error(err))
		}
	}

	return account, nil
}

// UpdateAccountStatus updates the status of a user's Alpaca account
func (s *AccountService) UpdateAccountStatus(ctx context.Context, userID uuid.UUID, status entities.AlpacaAccountStatus) error {
	return s.accountRepo.UpdateStatus(ctx, userID, status)
}

// SyncAccount syncs local account data with Alpaca
func (s *AccountService) SyncAccount(ctx context.Context, account *entities.AlpacaAccount) error {
	alpacaAccount, err := s.alpacaClient.GetAccount(ctx, account.AlpacaAccountID)
	if err != nil {
		return fmt.Errorf("get Alpaca account: %w", err)
	}

	now := time.Now()
	account.Status = alpacaAccount.Status
	account.BuyingPower = alpacaAccount.BuyingPower
	account.Cash = alpacaAccount.Cash
	account.PortfolioValue = alpacaAccount.PortfolioValue
	account.TradingBlocked = alpacaAccount.TradingBlocked
	account.TransfersBlocked = alpacaAccount.TransfersBlocked
	account.AccountBlocked = alpacaAccount.AccountBlocked
	account.LastSyncedAt = &now

	return s.accountRepo.Update(ctx, account)
}

// GetAccountByAlpacaID retrieves account by Alpaca account ID
func (s *AccountService) GetAccountByAlpacaID(ctx context.Context, alpacaAccountID string) (*entities.AlpacaAccount, error) {
	return s.accountRepo.GetByAlpacaID(ctx, alpacaAccountID)
}

func (s *AccountService) buildAccountRequest(profile *entities.UserProfile) *entities.AlpacaCreateAccountRequest {
	now := time.Now().Format(time.RFC3339)

	// Build contact info
	contact := entities.AlpacaContact{
		EmailAddress: profile.Email,
		Country:      "USA", // Default
	}

	if profile.Phone != nil {
		contact.PhoneNumber = *profile.Phone
	}

	// Build identity from profile
	identity := entities.AlpacaIdentity{}

	if profile.FirstName != nil {
		identity.GivenName = *profile.FirstName
	}
	if profile.LastName != nil {
		identity.FamilyName = *profile.LastName
	}
	if profile.DateOfBirth != nil {
		identity.DateOfBirth = profile.DateOfBirth.Format("2006-01-02")
	}

	identity.FundingSource = []string{"employment_income"}

	// Standard disclosures for retail investor
	disclosures := entities.AlpacaDisclosures{
		IsControlPerson:             false,
		IsAffiliatedExchangeOrFINRA: false,
		IsPoliticallyExposed:        false,
		ImmediateFamilyExposed:      false,
		EmploymentStatus:            "employed",
	}

	// Required agreements
	agreements := []entities.AlpacaAgreement{
		{Agreement: "customer_agreement", SignedAt: now, IPAddress: "0.0.0.0"},
		{Agreement: "account_agreement", SignedAt: now, IPAddress: "0.0.0.0"},
	}

	return &entities.AlpacaCreateAccountRequest{
		Contact:     contact,
		Identity:    identity,
		Disclosures: disclosures,
		Agreements:  agreements,
	}
}

// GetBuyingPower returns the current buying power for a user
func (s *AccountService) GetBuyingPower(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	account, err := s.GetUserAccount(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	if account == nil {
		return decimal.Zero, fmt.Errorf("no Alpaca account found")
	}
	return account.BuyingPower, nil
}

// EnsureAccountExists creates an Alpaca account if one doesn't exist
func (s *AccountService) EnsureAccountExists(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error) {
	account, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if account != nil {
		return account, nil
	}

	// Create new account
	_, err = s.CreateAccountForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	return s.accountRepo.GetByUserID(ctx, userID)
}
