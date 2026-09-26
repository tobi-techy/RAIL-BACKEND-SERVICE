package entities

import (
	"errors"
	"fmt"
)

// Email delivery failures are classified into two kinds, because the difference
// is user-visible and money-adjacent:
//
//   - Permanent, recipient-level: the provider will never deliver to this
//     address (suppressed after a bounce or complaint, invalid, blocked). The
//     only fix is a different address, so retrying is wasted effort and telling
//     the person to "try again" is a lie. These must not consume their OTP
//     rate-limit budget either, or one undeliverable address locks them out for
//     a day.
//   - Transient: a provider hiccup or our own configuration problem. A retry may
//     work, and a retry MAY duplicate delivery, so it must never silently fall
//     through to a second provider.
var (
	// ErrEmailRecipientUndeliverable marks a permanent rejection of the
	// recipient address by an email provider.
	ErrEmailRecipientUndeliverable = errors.New("email recipient undeliverable")
	// ErrEmailDeliveryTransient marks an email failure a retry could fix.
	ErrEmailDeliveryTransient = errors.New("email delivery failed transiently")
)

// Machine-readable reason codes carried by EmailDeliveryError. They are stable
// identifiers safe for logs and metrics; the provider's raw message is not
// (it embeds the recipient address).
const (
	// EmailReasonRecipientSuppressed means the provider has the address on its
	// account-level suppression list, normally from a hard bounce or a spam
	// complaint. Clearing it is a provider-dashboard action, not a code change.
	EmailReasonRecipientSuppressed = "recipient_suppressed"
	// EmailReasonRecipientUnsubscribed means the address opted out of mail from
	// this sender.
	EmailReasonRecipientUnsubscribed = "recipient_unsubscribed"
	// EmailReasonRecipientInvalid means the provider rejected the address as
	// malformed, unknown, or otherwise undeliverable.
	EmailReasonRecipientInvalid = "recipient_invalid"
)

// EmailDeliveryError describes a failed send. It implements Is so callers can
// classify a failure with errors.Is instead of matching provider strings:
//
//	err := emailService.SendVerificationEmail(ctx, to, code)
//	if entities.IsPermanentEmailDeliveryError(err) {
//		// ask the person for a different address; do not retry, do not charge
//		// the attempt against their rate limit
//	}
type EmailDeliveryError struct {
	Provider   string // provider that rejected the send, e.g. "unosend"
	Recipient  string
	StatusCode int
	Reason     string // one of the EmailReason* codes, when known
	Permanent  bool
}

func (e *EmailDeliveryError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "unspecified"
	}
	kind := "transient"
	if e.Permanent {
		kind = "permanent"
	}
	if e.Recipient != "" {
		return fmt.Sprintf("email delivery %s failure via %s for %s (status %d, reason %s)",
			kind, e.Provider, e.Recipient, e.StatusCode, reason)
	}
	return fmt.Sprintf("email delivery %s failure via %s (status %d, reason %s)",
		kind, e.Provider, e.StatusCode, reason)
}

// Is answers for the class sentinel matching the error's permanence, so
// errors.Is(err, ErrEmailRecipientUndeliverable) is the whole contract.
func (e *EmailDeliveryError) Is(target error) bool {
	if e.Permanent {
		return target == ErrEmailRecipientUndeliverable
	}
	return target == ErrEmailDeliveryTransient
}

// IsPermanentEmailDeliveryError reports whether err is a permanent,
// recipient-level delivery failure. Nil-safe and non-email errors are false.
func IsPermanentEmailDeliveryError(err error) bool {
	return errors.Is(err, ErrEmailRecipientUndeliverable)
}
