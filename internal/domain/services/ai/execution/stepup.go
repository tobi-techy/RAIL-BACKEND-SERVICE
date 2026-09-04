package execution

import (
	"context"
	"errors"
	"sync"
)

var ErrStepUpRequired = errors.New("step-up verification required")

type stepUpTokenKey struct{}

func WithStepUpToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, stepUpTokenKey{}, token)
}

func StepUpTokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(stepUpTokenKey{}).(string); ok {
		return v
	}
	return ""
}

type emailLinkVerifiedKey struct{}

func WithEmailLinkVerification(ctx context.Context) context.Context {
	return context.WithValue(ctx, emailLinkVerifiedKey{}, true)
}

func IsEmailLinkVerified(ctx context.Context) bool {
	_, ok := ctx.Value(emailLinkVerifiedKey{}).(bool)
	return ok
}

var (
	FundMovingActionsMu sync.RWMutex
	FundMovingActions   = map[string]bool{}
)

func RegisterFundMovingAction(name string) {
	FundMovingActionsMu.Lock()
	defer FundMovingActionsMu.Unlock()
	FundMovingActions[name] = true
}

func IsFundMovingAction(action string) bool {
	FundMovingActionsMu.RLock()
	defer FundMovingActionsMu.RUnlock()
	return FundMovingActions[action]
}

func init() {
	for _, name := range []string{
		"transfer_funds", "initiate_withdrawal", "execute_investment",
		"optimize_yield", "copy_trader", "setup_bill_autopay", "pay_bill",
		"automate_bill", "send_money", "split_receipt", "create_automation",
		"book_flight", "request_flight_refund", "send_to_bank", "send_crypto",
	} {
		FundMovingActions[name] = true
	}
}
