package alpaca

import (
	"context"
	"fmt"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// BrokerageOnboardingService handles Alpaca account creation from STACK KYC data
type BrokerageOnboardingService struct {
	alpacaService *alpaca.Service
	logger        *zap.Logger
}

func NewBrokerageOnboardingService(alpacaService *alpaca.Service, logger *zap.Logger) *BrokerageOnboardingService {
	return &BrokerageOnboardingService{
		alpacaService: alpacaService,
		logger:        logger,
	}
}

// CreateBrokerageAccount maps STACK KYC data to Alpaca account format
func (s *BrokerageOnboardingService) CreateBrokerageAccount(ctx context.Context, user *entities.User, kyc *entities.KYCSubmission) (*entities.AlpacaAccountResponse, error) {
	s.logger.Info("Creating Alpaca brokerage account",
		zap.String("user_id", user.ID.String()),
		zap.String("email", user.Email))

	// Extract KYC data from verification_data map
	verificationData := kyc.VerificationData
	if verificationData == nil {
		verificationData = make(map[string]any)
	}

	// Helper to safely get string from map
	getString := func(key string) string {
		if val, ok := verificationData[key].(string); ok {
			return val
		}
		return ""
	}

	phoneNumber := ""
	if user.Phone != nil {
		phoneNumber = *user.Phone
	}

	// Map STACK KYC data to Alpaca format
	req := &entities.AlpacaCreateAccountRequest{
		Contact: entities.AlpacaContact{
			EmailAddress:  user.Email,
			PhoneNumber:   phoneNumber,
			StreetAddress: []string{getString("address")},
			City:          getString("city"),
			State:         getString("state"),
			PostalCode:    getString("postal_code"),
			Country:       getString("country"),
		},
		Identity: entities.AlpacaIdentity{
			GivenName:             getString("given_name"),
			FamilyName:            getString("family_name"),
			DateOfBirth:           getString("date_of_birth"),
			TaxID:                 getString("tax_id"),
			TaxIDType:             "USA_SSN",
			CountryOfCitizenship:  getString("country"),
			CountryOfBirth:        getString("country"),
			CountryOfTaxResidence: getString("country"),
			FundingSource:         []string{"employment_income"},
		},
		Disclosures: entities.AlpacaDisclosures{
			IsControlPerson:             false,
			IsAffiliatedExchangeOrFINRA: false,
			IsPoliticallyExposed:        false,
			ImmediateFamilyExposed:      false,
		},
		Agreements: []entities.AlpacaAgreement{
			{
				Agreement: "customer_agreement",
				SignedAt:  time.Now().Format(time.RFC3339),
				IPAddress: getString("ip_address"),
			},
			{
				Agreement: "margin_agreement",
				SignedAt:  time.Now().Format(time.RFC3339),
				IPAddress: getString("ip_address"),
			},
		},
	}

	account, err := s.alpacaService.CreateAccount(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create Alpaca account",
			zap.String("user_id", user.ID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("create brokerage account: %w", err)
	}

	s.logger.Info("Alpaca account created successfully",
		zap.String("user_id", user.ID.String()),
		zap.String("alpaca_account_id", account.ID),
		zap.String("account_number", account.AccountNumber))

	return account, nil
}
