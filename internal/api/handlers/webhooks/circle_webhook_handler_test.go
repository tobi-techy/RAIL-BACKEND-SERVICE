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

	nftTokenID string
	nftErr     error

	returnCalled      bool
	returnWalletID    string
	returnTokenID     string
	returnDestination string
	returnAmounts     []string
	returnIdempotency string
	returnUnsupported error

	returnNFTCalled   bool
	returnNFTTokenID  string
	returnNFTWalletID string
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

func (m *mockUnsupportedAssetService) GetNFTTokenID(_ context.Context, _, _ string) (string, error) {
	if m.nftErr != nil {
		return "", m.nftErr
	}
	return m.nftTokenID, nil
}

func (m *mockUnsupportedAssetService) ReturnUnsupportedNFT(_ context.Context, walletID, tokenID, destinationAddress string, amounts []string, nftTokenID, idempotencyKey string) error {
	m.returnNFTCalled = true
	m.returnNFTWalletID = walletID
	m.returnTokenID = tokenID
	m.returnDestination = destinationAddress
	m.returnAmounts = append([]string(nil), amounts...)
	m.returnNFTTokenID = nftTokenID
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
	assetService := &mockUnsupportedAssetService{getErr: errors.New("circle unavailable"), nftErr: errors.New("circle unavailable")}
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
	require.False(t, assetService.returnNFTCalled)
}

func TestCircleWebhookProcessInboundDeposit_NFTIsReturnedWithoutCrediting(t *testing.T) {
	userID := uuid.New()
	inboundID := uuid.NewString()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{getErr: errors.New("token not found in wallet balances"), nftTokenID: "nft-42"}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 inboundID,
			WalletID:           "wallet-1",
			TokenID:            "token-nft",
			Blockchain:         "SOL",
			TxHash:             "hash-nft",
			SourceAddress:      "sender",
			DestinationAddress: "rail-wallet",
			Amounts:            []string{"1"},
		},
	})

	require.NoError(t, err)
	require.False(t, processor.called)
	require.False(t, assetService.returnCalled)
	require.True(t, assetService.returnNFTCalled)
	require.Equal(t, "wallet-1", assetService.returnNFTWalletID)
	require.Equal(t, "token-nft", assetService.returnTokenID)
	require.Equal(t, "sender", assetService.returnDestination)
	require.Equal(t, []string{"1"}, assetService.returnAmounts)
	require.Equal(t, "nft-42", assetService.returnNFTTokenID)
	require.Equal(t, inboundID, assetService.returnIdempotency)
}

func TestCircleWebhookProcessInboundDeposit_UnresolvableTokenAcknowledgesWithoutError(t *testing.T) {
	userID := uuid.New()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{getErr: errors.New("token not found in wallet balances")}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 uuid.NewString(),
			WalletID:           "wallet-1",
			TokenID:            "token-unknown",
			Blockchain:         "SOL",
			TxHash:             "hash-unknown",
			SourceAddress:      "sender",
			DestinationAddress: "rail-wallet",
			Amounts:            []string{"1"},
		},
	})

	require.NoError(t, err)
	require.False(t, processor.called)
	require.False(t, assetService.returnCalled)
	require.False(t, assetService.returnNFTCalled)
}

func TestCircleWebhookProcessInboundDeposit_NFTReturnFailureSurfacesError(t *testing.T) {
	userID := uuid.New()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{
		getErr:            errors.New("token not found in wallet balances"),
		nftTokenID:        "nft-42",
		returnUnsupported: errors.New("circle transfer rejected"),
	}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 uuid.NewString(),
			WalletID:           "wallet-1",
			TokenID:            "token-nft",
			Blockchain:         "SOL",
			TxHash:             "hash-nft",
			SourceAddress:      "sender",
			DestinationAddress: "rail-wallet",
			Amounts:            []string{"1"},
		},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "return unsupported Circle NFT")
	require.False(t, processor.called)
	require.True(t, assetService.returnNFTCalled)
}

func TestCircleWebhookProcessInboundDeposit_NFTWithoutSourceAddressAcknowledges(t *testing.T) {
	userID := uuid.New()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{getErr: errors.New("token not found in wallet balances"), nftTokenID: "nft-42"}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 uuid.NewString(),
			WalletID:           "wallet-1",
			TokenID:            "token-nft",
			Blockchain:         "SOL",
			TxHash:             "hash-nft",
			SourceAddress:      "",
			DestinationAddress: "rail-wallet",
			Amounts:            []string{"1"},
		},
	})

	require.NoError(t, err)
	require.False(t, processor.called)
	require.False(t, assetService.returnNFTCalled)
}

func TestCircleWebhookProcessInboundDeposit_MintedNFTWithZeroAddressSourceAcknowledges(t *testing.T) {
	userID := uuid.New()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{getErr: errors.New("token not found in wallet balances"), nftTokenID: "1"}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 uuid.NewString(),
			WalletID:           "wallet-1",
			TokenID:            "token-nft",
			Blockchain:         "OP",
			TxHash:             "0x66383b3537076c28ed0088995acd3860481b4518ced336205876dcce67672d55",
			SourceAddress:      "0x0000000000000000000000000000000000000000",
			DestinationAddress: "0x52ec428e3d59fc311ba502f715a1c8cb2bc084f6",
			Amounts:            []string{"1"},
		},
	})

	require.NoError(t, err)
	require.False(t, processor.called)
	require.False(t, assetService.returnNFTCalled)
	require.False(t, assetService.returnCalled)
}

func TestCircleWebhookProcessInboundDeposit_MintedFungibleTokenWithZeroAddressSourceAcknowledges(t *testing.T) {
	userID := uuid.New()
	processor := &mockCircleDepositProcessor{}
	assetService := &mockUnsupportedAssetService{symbol: "SPAM"}
	handler := NewCircleWebhookHandler(processor, mockCircleWalletLookup{userID: userID}, zap.NewNop(), "", nil)
	handler.SetUnsupportedAssetService(assetService)

	err := handler.processInboundDeposit(context.Background(), &CircleWebhookEvent{
		Notification: CircleTransactionEvent{
			ID:                 uuid.NewString(),
			WalletID:           "wallet-1",
			TokenID:            "token-spam",
			Blockchain:         "OP",
			TxHash:             "hash-spam",
			SourceAddress:      "0x0000000000000000000000000000000000000000",
			DestinationAddress: "0x52ec428e3d59fc311ba502f715a1c8cb2bc084f6",
			Amounts:            []string{"1000"},
		},
	})

	require.NoError(t, err)
	require.False(t, processor.called)
	require.False(t, assetService.returnCalled)
	require.False(t, assetService.returnNFTCalled)
}

func TestIsUnreturnableSource(t *testing.T) {
	dest := "0x52ec428e3d59fc311ba502f715a1c8cb2bc084f6"
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "empty", source: "", want: true},
		{name: "whitespace", source: "   ", want: true},
		{name: "evm zero address", source: "0x0000000000000000000000000000000000000000", want: true},
		{name: "evm zero address uppercase prefix", source: "0X0000000000000000000000000000000000000000", want: true},
		{name: "evm zero address no prefix", source: "0x0", want: true},
		{name: "same as destination", source: dest, want: true},
		{name: "solana system program", source: "11111111111111111111111111111111", want: true},
		{name: "normal sender", source: "0x1234567890abcdef1234567890abcdef12345678", want: false},
		{name: "normal solana address", source: "GdPxZiUYcbEqCXbp3TQsbHiwBq4NfWk2V1KtZ9aQmNfz", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUnreturnableSource(tt.source, dest))
		})
	}
}
