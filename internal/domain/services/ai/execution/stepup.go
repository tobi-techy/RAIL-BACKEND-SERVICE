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

// emailLinkVerifiedKey marks a context as having been verified via a one-time
// email confirmation link. When present, ConfirmAction accepts it as an
// alternative to the passcode/Face ID step-up token — the email link itself
// is the step-up verification.
type emailLinkVerifiedKey struct{}

// WithEmailLinkVerification marks the context as verified via a one-time
// email confirmation link. Used by the ConfirmHandler (email link page)
// so fund-moving actions can be confirmed without a passcode session.
func WithEmailLinkVerification(ctx context.Context) context.Context {
	return context.WithValue(ctx, emailLinkVerifiedKey{}, true)
}

// IsEmailLinkVerified reports whether the context carries an email-link
// verification stamp.
func IsEmailLinkVerified(ctx context.Context) bool {
	v, _ := ctx.Value(emailLinkVerifiedKey{}).(bool)
	return v
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
		"book_flight", "request_flight_refund",
		"send_to_bank", "send_crypto",
	} {
		FundMovingActions[name] = true
	}
}
