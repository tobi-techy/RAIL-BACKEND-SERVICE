package graphql

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type WithdrawalService interface {
	InitiateWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
}

type WithdrawalResolver struct {
	service WithdrawalService
}

func NewWithdrawalResolver(service WithdrawalService) *WithdrawalResolver {
	return &WithdrawalResolver{service: service}
}

type InitiateWithdrawalInput struct {
	AlpacaAccountID    string
	Amount             float64
	DestinationChain   string
	DestinationAddress string
}

func (r *WithdrawalResolver) InitiateWithdrawal(ctx context.Context, input InitiateWithdrawalInput) (*entities.InitiateWithdrawalResponse, error) {
	userID, ok := ctx.Value("user_id").(uuid.UUID)
	if !ok {
		return nil, ErrUnauthorized
	}

	req := &entities.InitiateWithdrawalRequest{
		UserID:             userID,
		AlpacaAccountID:    input.AlpacaAccountID,
		Amount:             decimal.NewFromFloat(input.Amount),
		DestinationChain:   input.DestinationChain,
		DestinationAddress: input.DestinationAddress,
	}

	return r.service.InitiateWithdrawal(ctx, req)
}

func (r *WithdrawalResolver) Withdrawal(ctx context.Context, id string) (*entities.Withdrawal, error) {
	withdrawalID, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return r.service.GetWithdrawal(ctx, withdrawalID)
}

var ErrUnauthorized = &struct{ error }{error: nil}
