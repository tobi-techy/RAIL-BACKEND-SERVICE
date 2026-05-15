package webhooks

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockCircleDepositProcessor struct {
	called         bool
	userID         uuid.UUID
	amount         decimal.Decimal
	token          entities.Stablecoin
	chain          string
	txHash         string
	circleWalletID string
}

func (m *mockCircleDepositProcessor) ProcessCircleDeposit(_ context.Context, userID uuid.UUID, amount decimal.Decimal, token entities.Stablecoin, chain, txHash, circleWalletID string) error {
	m.called = true
	m.userID = userID
	m.amount = amount
	m.token = token
	m.chain = chain
	m.txHash = txHash
	m.circleWalletID = circleWalletID
	return nil
}

type mockCircleWalletLookup struct {
	userID uuid.UUID
	err    error
}

func (m mockCircleWalletLookup) GetUserByCircleWalletID(_ context.Context, _ string) (uuid.UUID, error) {
	if m.err != nil {
		return uuid.Nil, m.err
	}
	return m.userID, nil
}

type mockUnsupportedAssetService struct {
	symbol string
	getErr error

	returnCalled      bool
	returnWalletID    string
	returnTokenID     string
	returnDestination string
	returnAmounts     []string
	returnIdempotency string
	returnUnsupported error
}

func (m *mockUnsupportedAssetService) GetTokenSymbol(_ context.Context, _, _ string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.symbol, nil
}

func (m *mockUnsupportedAssetService) ReturnUnsupportedToken(_ context.Context, walletID, tokenID, destinationAddress string, amounts []string, idempotencyKey string) error {
	m.returnCalled = true
	m.returnWalletID = walletID
	m.returnTokenID = tokenID
	m.returnDestination = destinationAddress
	m.returnAmounts = append([]string(nil), amounts...)
	m.returnIdempotency = idempotencyKey
	return m.returnUnsupported
}

func TestCircleWebhookProcessInboundDeposit_SupportedUSDCProcessesDeposit(t *testing.T) {
	userID := uuid.New()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{symbol: "USDC"}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 uuid.NewString(),
			WalletID:           "wallet-1",
			TokenID:            "token-usdc",
			Blockchain:         "SOL",
			TxHash:             "hash-1",
			SourceAddress:      "sender",
			DestinationAddress: "rail-wallet",
			Amounts:            []string{"90.7284276364"},
		},
	})

	require.NoError(t, err)
	require.True(t, processor.called)
	require.Equal(t, userID, processor.userID)
	require.Equal(t, entities.StablecoinUSDC, processor.token)
	require.Equal(t, "SOL", processor.chain)
	require.Equal(t, "hash-1", processor.txHash)
	require.Equal(t, "wallet-1", processor.circleWalletID)
	require.Equal(t, "90.7284276364", processor.amount.String())
	require.False(t, assetService.returnCalled)
}

func TestCircleWebhookProcessInboundDeposit_SupportedNonUSDCStablecoinProcessesDeposit(t *testing.T) {
	userID := uuid.New()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{symbol: "PYUSD"}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 uuid.NewString(),
			WalletID:           "wallet-1",
			TokenID:            "token-pyusd",
			Blockchain:         "SOL",
			TxHash:             "hash-2",
			SourceAddress:      "sender",
			DestinationAddress: "rail-wallet",
			Amounts:            []string{"25"},
		},
	})

	require.NoError(t, err)
	require.True(t, processor.called)
	require.Equal(t, entities.StablecoinPYUSD, processor.token)
	require.Equal(t, "25", processor.amount.String())
	require.False(t, assetService.returnCalled)
}

func TestCircleWebhookProcessInboundDeposit_UnsupportedTokenReturnsWithoutCrediting(t *testing.T) {
	userID := uuid.New()
	inboundID := uuid.NewString()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{symbol: "PITCHERS"}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 inboundID,
			WalletID:           "wallet-1",
			TokenID:            "token-pitchers",
			Blockchain:         "SOL",
			TxHash:             "hash-1",
			SourceAddress:      "sender",
			DestinationAddress: "rail-wallet",
			Amounts:            []string{"90.7284276364"},
		},
	})

	require.NoError(t, err)
	require.False(t, processor.called)
	require.True(t, assetService.returnCalled)
	require.Equal(t, "wallet-1", assetService.returnWalletID)
	require.Equal(t, "token-pitchers", assetService.returnTokenID)
	require.Equal(t, "sender", assetService.returnDestination)
	require.Equal(t, []string{"90.7284276364"}, assetService.returnAmounts)
	require.Equal(t, inboundID, assetService.returnIdempotency)
}

func TestCircleWebhookProcessInboundDeposit_TokenValidationFailsClosed(t *testing.T) {
	userID := uuid.New()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{getErr: errors.New("circle unavailable")}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 uuid.NewString(),
			WalletID:           "wallet-1",
			TokenID:            "token-usdc",
			Blockchain:         "SOL",
			TxHash:             "hash-1",
			SourceAddress:      "sender",
			DestinationAddress: "rail-wallet",
			Amounts:            []string{"10"},
		},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "circle token validation failed")
	require.False(t, processor.called)
	require.False(t, assetService.returnCalled)
}
