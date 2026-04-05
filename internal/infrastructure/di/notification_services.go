package di

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/shopspring/decimal"
)

// FundingNotificationAdapter adapts NotificationService to funding.FundingNotificationService
type FundingNotificationAdapter struct {
	svc *services.NotificationService
}

func (a *FundingNotificationAdapter) NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error {
	return a.svc.NotifyDepositConfirmed(ctx, userID, amount, chain, txHash)
}

func (a *FundingNotificationAdapter) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	return a.svc.NotifyLargeBalanceChange(ctx, userID, changeType, amount, newBalance)
}

func (a *FundingNotificationAdapter) NotifyAllocationFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, reason string) error {
	return a.svc.NotifyAllocationFailed(ctx, userID, amount, depositID, reason)
}

// WithdrawalNotificationAdapter adapts NotificationService to withdrawal.WithdrawalNotificationService
type WithdrawalNotificationAdapter struct {
	svc *services.NotificationService
}

func (a *WithdrawalNotificationAdapter) NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination string) error {
	return a.svc.NotifyWithdrawalCompleted(ctx, userID, amount.String(), destination)
}

func (a *WithdrawalNotificationAdapter) NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reason string) error {
	return a.svc.NotifyWithdrawalFailed(ctx, userID, amount.String(), reason)
}

func (a *WithdrawalNotificationAdapter) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	return a.svc.NotifyLargeBalanceChange(ctx, userID, changeType, amount, newBalance)
}

// bridgeWebhookNotifierAdapter adapts NotificationService to BridgeWebhookNotifier
type bridgeWebhookNotifierAdapter struct {
	svc *services.NotificationService
}

func (a *bridgeWebhookNotifierAdapter) NotifyDepositReceived(ctx *gin.Context, userID uuid.UUID, amount, currency string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.NotifyDepositConfirmed(ctx.Request.Context(), userID, amount+" "+currency, "", "")
}

func (a *bridgeWebhookNotifierAdapter) NotifyKYCStatusChanged(ctx *gin.Context, userID uuid.UUID, status string) error {
	if a.svc == nil {
		return nil
	}
	switch status {
	case "active":
		return a.svc.NotifyKYCApproved(ctx.Request.Context(), userID)
	case "rejected":
		return a.svc.NotifyKYCRejected(ctx.Request.Context(), userID)
	}
	return nil
}
