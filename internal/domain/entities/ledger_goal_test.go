package entities

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountTypeGoalBalance_IsValid(t *testing.T) {
	assert.NoError(t, AccountTypeGoalBalance.Validate())
	assert.True(t, AccountTypeGoalBalance.IsValid())
	assert.True(t, AccountTypeGoalBalance.IsUserAccountType())
	assert.False(t, AccountTypeGoalBalance.IsSystemAccountType())
}

func TestLedgerAccount_GoalBalanceRequiresGoalID(t *testing.T) {
	userID := uuid.New()
	acct := &LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: AccountTypeGoalBalance,
		GoalID:      nil, // missing
		Currency:    "USDC",
		Balance:     decimal.Zero,
	}

	err := acct.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goal_balance account requires goal_id")
}

func TestLedgerAccount_GoalBalanceValid(t *testing.T) {
	userID := uuid.New()
	goalID := uuid.New()
	acct := &LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: AccountTypeGoalBalance,
		GoalID:      &goalID,
		Currency:    "USDC",
		Balance:     decimal.NewFromInt(100),
	}

	require.NoError(t, acct.Validate())
}

func TestLedgerAccount_NonGoalAccountCannotHaveGoalID(t *testing.T) {
	userID := uuid.New()
	goalID := uuid.New()
	acct := &LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: AccountTypeStashBalance,
		GoalID:      &goalID, // should not be set
		Currency:    "USDC",
		Balance:     decimal.Zero,
	}

	err := acct.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-goal account cannot have goal_id")
}

func TestLedgerAccount_SpendingBalanceStillValid(t *testing.T) {
	userID := uuid.New()
	acct := &LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: AccountTypeSpendingBalance,
		Currency:    "USDC",
		Balance:     decimal.NewFromInt(500),
	}
	require.NoError(t, acct.Validate())
}
