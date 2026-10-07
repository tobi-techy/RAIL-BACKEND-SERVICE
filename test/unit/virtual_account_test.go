package unit

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
)

// TestVirtualAccountEntity tests the virtual account entity structure
func TestVirtualAccountEntity(t *testing.T) {
	userID := uuid.New()

	virtualAccount := &entities.VirtualAccount{
		ID:               uuid.New(),
		UserID:           userID,
		BridgeCustomerID: "due-test-123",
		AccountNumber:    "1234567890",
		RoutingNumber:    "021000021",
		Status:           entities.VirtualAccountStatusActive,
		Currency:         "USD",
	}

	assert.NotNil(t, virtualAccount)
	assert.Equal(t, userID, virtualAccount.UserID)
	assert.Equal(t, entities.VirtualAccountStatusActive, virtualAccount.Status)
	assert.Equal(t, "USD", virtualAccount.Currency)
}

// TestVirtualAccountStatuses tests virtual account status constants
func TestVirtualAccountStatuses(t *testing.T) {
	assert.Equal(t, entities.VirtualAccountStatus("pending"), entities.VirtualAccountStatusPending)
	assert.Equal(t, entities.VirtualAccountStatus("active"), entities.VirtualAccountStatusActive)
	assert.Equal(t, entities.VirtualAccountStatus("closed"), entities.VirtualAccountStatusClosed)
	assert.Equal(t, entities.VirtualAccountStatus("failed"), entities.VirtualAccountStatusFailed)
}

// TestCreateVirtualAccountRequest tests the request structure
func TestCreateVirtualAccountRequest(t *testing.T) {
	userID := uuid.New()
	req := &entities.CreateVirtualAccountRequest{
		UserID:           userID,
		BridgeCustomerID: "bridge-customer-123",
	}

	assert.NotNil(t, req)
	assert.Equal(t, userID, req.UserID)
	assert.Equal(t, "bridge-customer-123", req.BridgeCustomerID)
}
