package unit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/adapters/due"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockOffRampDueAdapter struct {
	mock.Mock
}

func (m *MockOffRampDueAdapter) InitiateOffRamp(ctx context.Context, req *due.OffRampRequest) (*due.OffRampResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*due.OffRampResponse), args.Error(1)
}

func (m *MockOffRampDueAdapter) GetTransferStatus(ctx context.Context, transferID string) (*due.OffRampResponse, error) {
	args := m.Called(ctx, transferID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*due.OffRampResponse), args.Error(1)
}

type MockOffRampAlpacaAdapter struct {
	mock.Mock
}

func (m *MockOffRampAlpacaAdapter) InitiateFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.AlpacaInstantFundingResponse), args.Error(1)
}

type MockOffRampDepositRepo struct {
	mock.Mock
}

func (m *MockOffRampDepositRepo) Create(ctx context.Context, deposit *entities.Deposit) error {
	args := m.Called(ctx, deposit)
	return args.Error(0)
}

func (m *MockOffRampDepositRepo) GetByOffRampTxID(ctx context.Context, txID string) (*entities.Deposit, error) {
	args := m.Called(ctx, txID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Deposit), args.Error(1)
}

func (m *MockOffRampDepositRepo) Update(ctx context.Context, deposit *entities.Deposit) error {
	args := m.Called(ctx, deposit)
	return args.Error(0)
}

type MockOffRampVirtualAccountRepo struct {
	mock.Mock
}

func (m *MockOffRampVirtualAccountRepo) GetByDueAccountID(ctx context.Context, dueAccountID string) (*entities.VirtualAccount, error) {
	args := m.Called(ctx, dueAccountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.VirtualAccount), args.Error(1)
}

func (m *MockOffRampVirtualAccountRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.VirtualAccount), args.Error(1)
}

func TestInitiateOffRamp_Success(t *testing.T) {
	mockDue := new(MockOffRampDueAdapter)
	mockDeposit := new(MockOffRampDepositRepo)
	mockVA := new(MockOffRampVirtualAccountRepo)
	log := logger.New("debug", "test")

	vaID := uuid.New()
	userID := uuid.New()
	amount := decimal.NewFromFloat(100.0)

	mockVA.On("GetByDueAccountID", mock.Anything, "va_123").Return(&entities.VirtualAccount{
		ID:              vaID,
		UserID:          userID,
		AlpacaAccountID: "alpaca_123",
	}, nil)

	mockDeposit.On("Create", mock.Anything, mock.AnythingOfType("*entities.Deposit")).Return(nil)

	mockDue.On("InitiateOffRamp", mock.Anything, mock.AnythingOfType("*due.OffRampRequest")).Return(&due.OffRampResponse{
		TransferID:   "transfer_123",
		Status:       due.TransferStatusPending,
		SourceAmount: amount,
		DestAmount:   decimal.NewFromFloat(99.5),
	}, nil)

	mockDeposit.On("Update", mock.Anything, mock.AnythingOfType("*entities.Deposit")).Return(nil)

	err := initiateOffRampTest(context.Background(), mockDue, mockDeposit, mockVA, log, "va_123", "100.0")

	assert.NoError(t, err)
	mockVA.AssertExpectations(t)
	mockDeposit.AssertExpectations(t)
	mockDue.AssertExpectations(t)
}

func TestInitiateOffRamp_VirtualAccountNotFound(t *testing.T) {
	mockDue := new(MockOffRampDueAdapter)
	mockDeposit := new(MockOffRampDepositRepo)
	mockVA := new(MockOffRampVirtualAccountRepo)
	log := logger.New("debug", "test")

	mockVA.On("GetByDueAccountID", mock.Anything, "va_invalid").Return(nil, errors.New("not found"))

	err := initiateOffRampTest(context.Background(), mockDue, mockDeposit, mockVA, log, "va_invalid", "100.0")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get virtual account")
	mockVA.AssertExpectations(t)
}

func TestHandleTransferCompleted_Success(t *testing.T) {
	mockDue := new(MockOffRampDueAdapter)
	mockAlpaca := new(MockOffRampAlpacaAdapter)
	mockDeposit := new(MockOffRampDepositRepo)
	mockVA := new(MockOffRampVirtualAccountRepo)

	depositID := uuid.New()
	vaID := uuid.New()
	transferID := "transfer_123"

	mockDeposit.On("GetByOffRampTxID", mock.Anything, transferID).Return(&entities.Deposit{
		ID:               depositID,
		VirtualAccountID: &vaID,
		Status:           "off_ramp_initiated",
	}, nil)

	mockDue.On("GetTransferStatus", mock.Anything, transferID).Return(&due.OffRampResponse{
		TransferID: transferID,
		Status:     due.TransferStatusCompleted,
		DestAmount: decimal.NewFromFloat(99.5),
	}, nil)

	mockDeposit.On("Update", mock.Anything, mock.AnythingOfType("*entities.Deposit")).Return(nil).Times(2)

	mockVA.On("GetByID", mock.Anything, vaID).Return(&entities.VirtualAccount{
		ID:              vaID,
		AlpacaAccountID: "alpaca_123",
	}, nil)

	mockAlpaca.On("InitiateFunding", mock.Anything, mock.AnythingOfType("*entities.AlpacaInstantFundingRequest")).Return(&entities.AlpacaInstantFundingResponse{
		ID:        "funding_123",
		AccountNo: "alpaca_123",
		Status:    "PENDING",
	}, nil)

	err := handleTransferCompletedTest(context.Background(), mockDue, mockAlpaca, mockDeposit, mockVA, transferID)

	assert.NoError(t, err)
	mockDeposit.AssertExpectations(t)
	mockDue.AssertExpectations(t)
	mockVA.AssertExpectations(t)
	mockAlpaca.AssertExpectations(t)
}

func initiateOffRampTest(ctx context.Context, dueAdapter *MockOffRampDueAdapter, depositRepo *MockOffRampDepositRepo, vaRepo *MockOffRampVirtualAccountRepo, log *logger.Logger, vaID, amount string) error {
	va, err := vaRepo.GetByDueAccountID(ctx, vaID)
	if err != nil {
		return errors.New("failed to get virtual account: " + err.Error())
	}

	amountDecimal, _ := decimal.NewFromString(amount)
	deposit := &entities.Deposit{
		ID:               uuid.New(),
		UserID:           va.UserID,
		VirtualAccountID: &va.ID,
		Amount:           amountDecimal,
		Token:            entities.StablecoinUSDC,
		Status:           "off_ramp_initiated",
		CreatedAt:        time.Now(),
	}

	if err := depositRepo.Create(ctx, deposit); err != nil {
		return err
	}

	req := &due.OffRampRequest{
		VirtualAccountID: vaID,
		RecipientID:      va.AlpacaAccountID,
		Amount:           amountDecimal,
	}

	resp, err := dueAdapter.InitiateOffRamp(ctx, req)
	if err != nil {
		return err
	}

	deposit.OffRampTxID = &resp.TransferID
	return depositRepo.Update(ctx, deposit)
}

func handleTransferCompletedTest(ctx context.Context, dueAdapter *MockOffRampDueAdapter, alpacaAdapter *MockOffRampAlpacaAdapter, depositRepo *MockOffRampDepositRepo, vaRepo *MockOffRampVirtualAccountRepo, transferID string) error {
	deposit, err := depositRepo.GetByOffRampTxID(ctx, transferID)
	if err != nil {
		return err
	}

	_, err = dueAdapter.GetTransferStatus(ctx, transferID)
	if err != nil {
		return err
	}

	deposit.Status = "off_ramp_completed"
	if err := depositRepo.Update(ctx, deposit); err != nil {
		return err
	}

	va, err := vaRepo.GetByID(ctx, *deposit.VirtualAccountID)
	if err != nil {
		return err
	}

	fundReq := &entities.AlpacaInstantFundingRequest{
		AccountNo: va.AlpacaAccountID,
		Amount:    decimal.NewFromFloat(99.5),
	}

	_, err = alpacaAdapter.InitiateFunding(ctx, fundReq)
	if err != nil {
		return err
	}

	deposit.Status = "broker_funded"
	return depositRepo.Update(ctx, deposit)
}
