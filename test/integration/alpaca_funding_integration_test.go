package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlpacaFundingFlow_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	db := setupTestDB(t)
	defer cleanupTestDB(t, db)

	// Setup repositories
	depositRepo := repositories.NewDepositRepository(db)
	balanceRepo := repositories.NewBalanceRepository(db)
	virtualAccountRepo := repositories.NewVirtualAccountRepository(db)

	// Create test user
	userID := uuid.New()
	
	// Create test virtual account
	virtualAccountID := uuid.New()
	alpacaAccountID := "test-alpaca-" + uuid.New().String()
	virtualAccount := &entities.VirtualAccount{
		ID:              virtualAccountID,
		UserID:          userID,
		AlpacaAccountID: alpacaAccountID,
		DueAccountID:    "due-" + uuid.New().String(),
		Status:          entities.VirtualAccountStatusActive,
		CreatedAt:       time.Now(),
	}
	err := virtualAccountRepo.Create(ctx, virtualAccount)
	require.NoError(t, err)

	// Create test deposit in off_ramp_completed status
	depositID := uuid.New()
	amount := decimal.NewFromFloat(500.00)
	now := time.Now()
	deposit := &entities.Deposit{
		ID:                 depositID,
		UserID:             userID,
		VirtualAccountID:   &virtualAccountID,
		Amount:             amount,
		Status:             "off_ramp_completed",
		Chain:              entities.ChainSolana,
		Token:              entities.StablecoinUSDC,
		TxHash:             "test-tx-hash",
		OffRampCompletedAt: &now,
		CreatedAt:          now,
	}
	err = depositRepo.Create(ctx, deposit)
	require.NoError(t, err)

	// Initialize balance
	err = balanceRepo.UpdateBuyingPower(ctx, userID, decimal.Zero)
	require.NoError(t, err)

	// Test: Simulate Alpaca funding (without actual API call)
	// In real scenario, this would be triggered by off-ramp webhook
	
	// Update deposit to broker_funded status
	fundingTxID := "alpaca-funding-" + uuid.New().String()
	fundedAt := time.Now()
	deposit.AlpacaFundingTxID = &fundingTxID
	deposit.AlpacaFundedAt = &fundedAt
	deposit.Status = "broker_funded"
	
	err = depositRepo.Update(ctx, deposit)
	require.NoError(t, err)

	// Update balance
	err = balanceRepo.UpdateBuyingPower(ctx, userID, amount)
	require.NoError(t, err)

	// Verify deposit status
	updatedDeposit, err := depositRepo.GetByID(ctx, depositID)
	require.NoError(t, err)
	assert.Equal(t, "broker_funded", updatedDeposit.Status)
	assert.NotNil(t, updatedDeposit.AlpacaFundingTxID)
	assert.NotNil(t, updatedDeposit.AlpacaFundedAt)
	assert.Equal(t, fundingTxID, *updatedDeposit.AlpacaFundingTxID)

	// Verify balance updated
	balance, err := balanceRepo.Get(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, amount.String(), balance.BuyingPower.String())
}

func TestAlpacaFundingFlow_AuditTrail(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	db := setupTestDB(t)
	defer cleanupTestDB(t, db)

	depositRepo := repositories.NewDepositRepository(db)
	
	userID := uuid.New()
	virtualAccountID := uuid.New()
	depositID := uuid.New()
	amount := decimal.NewFromFloat(250.00)

	// Create deposit with complete audit trail
	now := time.Now()
	offRampInitiated := now.Add(-10 * time.Minute)
	offRampCompleted := now.Add(-5 * time.Minute)
	alpacaFunded := now
	
	offRampTxID := "due-offramp-123"
	alpacaFundingTxID := "alpaca-funding-456"

	deposit := &entities.Deposit{
		ID:                   depositID,
		UserID:               userID,
		VirtualAccountID:     &virtualAccountID,
		Amount:               amount,
		Status:               "broker_funded",
		Chain:                entities.ChainSolana,
		Token:                entities.StablecoinUSDC,
		TxHash:               "blockchain-tx-hash",
		OffRampTxID:          &offRampTxID,
		OffRampInitiatedAt:   &offRampInitiated,
		OffRampCompletedAt:   &offRampCompleted,
		AlpacaFundingTxID:    &alpacaFundingTxID,
		AlpacaFundedAt:       &alpacaFunded,
		CreatedAt:            now.Add(-15 * time.Minute),
	}

	err := depositRepo.Create(ctx, deposit)
	require.NoError(t, err)

	// Verify complete audit trail
	retrieved, err := depositRepo.GetByID(ctx, depositID)
	require.NoError(t, err)
	
	assert.Equal(t, "broker_funded", retrieved.Status)
	assert.NotNil(t, retrieved.OffRampTxID)
	assert.NotNil(t, retrieved.OffRampInitiatedAt)
	assert.NotNil(t, retrieved.OffRampCompletedAt)
	assert.NotNil(t, retrieved.AlpacaFundingTxID)
	assert.NotNil(t, retrieved.AlpacaFundedAt)
	
	// Verify timestamps are in correct order
	assert.True(t, retrieved.OffRampInitiatedAt.Before(*retrieved.OffRampCompletedAt))
	assert.True(t, retrieved.OffRampCompletedAt.Before(*retrieved.AlpacaFundedAt))
}

func TestAlpacaFundingFlow_StatusProgression(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	db := setupTestDB(t)
	defer cleanupTestDB(t, db)

	depositRepo := repositories.NewDepositRepository(db)
	
	userID := uuid.New()
	virtualAccountID := uuid.New()
	depositID := uuid.New()
	amount := decimal.NewFromFloat(100.00)

	// Create initial deposit
	deposit := &entities.Deposit{
		ID:               depositID,
		UserID:           userID,
		VirtualAccountID: &virtualAccountID,
		Amount:           amount,
		Status:           "pending",
		Chain:            entities.ChainSolana,
		Token:            entities.StablecoinUSDC,
		TxHash:           "test-tx",
		CreatedAt:        time.Now(),
	}

	err := depositRepo.Create(ctx, deposit)
	require.NoError(t, err)

	// Progress through statuses
	statuses := []string{"confirmed", "off_ramp_initiated", "off_ramp_completed", "broker_funded"}
	
	for _, status := range statuses {
		deposit.Status = status
		err = depositRepo.Update(ctx, deposit)
		require.NoError(t, err)

		retrieved, err := depositRepo.GetByID(ctx, depositID)
		require.NoError(t, err)
		assert.Equal(t, status, retrieved.Status)
	}
}
