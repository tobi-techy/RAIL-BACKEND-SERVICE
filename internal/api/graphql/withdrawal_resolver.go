package graphql

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type WithdrawalService interface {
	InitiateCryptoWithdrawal(ctx context.Context, req *entities.InitiateCryptoWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	InitiateFiatWithdrawal(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, userID, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
}

type WithdrawalResolver struct {
	service WithdrawalService
}

func NewWithdrawalResolver(service WithdrawalService) *WithdrawalResolver {
	return &WithdrawalResolver{service: service}
}

type InitiateCryptoWithdrawalInput struct {
	Amount             float64
	DestinationAddress string
	DestinationChain   string
	SourceAccount      string
	CircleWalletID     string
}

type InitiateFiatWithdrawalInput struct {
	Amount        float64
	Currency      string
	RoutingNumber string
	SourceAccount string
}

func (r *WithdrawalResolver) InitiateCryptoWithdrawal(ctx context.Context, input InitiateCryptoWithdrawalInput) (*entities.InitiateWithdrawalResponse, error) {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return nil, ErrUnauthorized
	}

	sourceAccount := entities.WithdrawalSourceSpendingBalance
	if input.SourceAccount == "stash_balance" {
		sourceAccount = entities.WithdrawalSourceStashBalance
	}

	req := &entities.InitiateCryptoWithdrawalRequest{
		UserID:             userID,
		Amount:             decimal.NewFromFloat(input.Amount),
		DestinationAddress: input.DestinationAddress,
		DestinationChain:   input.DestinationChain,
		SourceAccount:      sourceAccount,
		CircleWalletID:     input.CircleWalletID,
	}

	return r.service.InitiateCryptoWithdrawal(ctx, req)
}

func (r *WithdrawalResolver) InitiateFiatWithdrawal(ctx context.Context, input InitiateFiatWithdrawalInput) (*entities.InitiateWithdrawalResponse, error) {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return nil, ErrUnauthorized
	}

	sourceAccount := entities.WithdrawalSourceSpendingBalance
	if input.SourceAccount == "stash_balance" {
		sourceAccount = entities.WithdrawalSourceStashBalance
	}

	currency := entities.WithdrawalCurrencyUSD
	if input.Currency == "EUR" {
		currency = entities.WithdrawalCurrencyEUR
	}

	req := &entities.InitiateFiatWithdrawalRequest{
		UserID:        userID,
		Amount:        decimal.NewFromFloat(input.Amount),
		Currency:      currency,
		RoutingNumber: input.RoutingNumber,
		SourceAccount: sourceAccount,
	}

	return r.service.InitiateFiatWithdrawal(ctx, req)
}

func (r *WithdrawalResolver) Withdrawal(ctx context.Context, id string) (*entities.Withdrawal, error) {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return nil, ErrUnauthorized
	}

	withdrawalID, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return r.service.GetWithdrawal(ctx, userID, withdrawalID)
}

var ErrUnauthorized = &struct{ error }{error: nil}
