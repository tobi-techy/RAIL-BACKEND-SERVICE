package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ConfirmationAction is the closed set of high-stakes actions that share the
// single live iMessage confirmation card primitive. Adding a new action means
// adding a renderer (copy/amount fields) plus a backend executor — never new
// card infrastructure.
type ConfirmationAction string

const (
	ConfirmationActionInvestBuy     ConfirmationAction = "invest.buy"
	ConfirmationActionInvestSell    ConfirmationAction = "invest.sell"
	ConfirmationActionInvestCancel  ConfirmationAction = "invest.cancel"
	ConfirmationActionTransferSend  ConfirmationAction = "transfer.send"
	ConfirmationActionTransferReq   ConfirmationAction = "transfer.request"
	ConfirmationActionSaveSweep     ConfirmationAction = "save.sweep"
	ConfirmationActionSavePause     ConfirmationAction = "save.pause"
	ConfirmationActionLimitChange   ConfirmationAction = "limit.change"
	ConfirmationActionAccountLink   ConfirmationAction = "account.link"
	ConfirmationActionAccountUnlink ConfirmationAction = "account.unlink"
	ConfirmationActionMandateApprov ConfirmationAction = "mandate.approve"
	ConfirmationActionMandateRevoke ConfirmationAction = "mandate.revoke"
)

// AllConfirmationActions lists every action that may ride the shared card.
var AllConfirmationActions = []ConfirmationAction{
	ConfirmationActionInvestBuy,
	ConfirmationActionInvestSell,
	ConfirmationActionInvestCancel,
	ConfirmationActionTransferSend,
	ConfirmationActionTransferReq,
	ConfirmationActionSaveSweep,
	ConfirmationActionSavePause,
	ConfirmationActionLimitChange,
	ConfirmationActionAccountLink,
	ConfirmationActionAccountUnlink,
	ConfirmationActionMandateApprov,
	ConfirmationActionMandateRevoke,
}

// ValidConfirmationAction reports whether a is a known card action.
func ValidConfirmationAction(a ConfirmationAction) bool {
	switch a {
	case ConfirmationActionInvestBuy,
		ConfirmationActionInvestSell,
		ConfirmationActionInvestCancel,
		ConfirmationActionTransferSend,
		ConfirmationActionTransferReq,
		ConfirmationActionSaveSweep,
		ConfirmationActionSavePause,
		ConfirmationActionLimitChange,
		ConfirmationActionAccountLink,
		ConfirmationActionAccountUnlink,
		ConfirmationActionMandateApprov,
		ConfirmationActionMandateRevoke:
		return true
	}
	return false
}

// ConfirmationState is the lifecycle of one card. Terminal states never leave.
type ConfirmationState string

const (
	ConfirmationPending        ConfirmationState = "pending"        // waiting for Face ID
	ConfirmationAuthenticating ConfirmationState = "authenticating" // biometric prompt on screen
	ConfirmationApproved       ConfirmationState = "approved"       // biometrics passed, backend executing
	ConfirmationCompleted      ConfirmationState = "completed"      // filled / sent / saved
	ConfirmationRejected       ConfirmationState = "rejected"       // user cancelled Face ID
	ConfirmationFailed         ConfirmationState = "failed"         // backend error
	ConfirmationExpired        ConfirmationState = "expired"        // TTL hit, card dead
)

// TerminalConfirmationState reports whether s ends the lifecycle.
func TerminalConfirmationState(s ConfirmationState) bool {
	switch s {
	case ConfirmationCompleted, ConfirmationRejected, ConfirmationFailed, ConfirmationExpired:
		return true
	}
	return false
}

// ValidConfirmationTransition enforces the state machine. Only the listed
// edges exist; everything else is rejected so a card can never e.g. resurrect
// from completed back to pending.
func ValidConfirmationTransition(from, to ConfirmationState) bool {
	switch from {
	case ConfirmationPending:
		switch to {
		case ConfirmationAuthenticating, ConfirmationRejected, ConfirmationExpired:
			return true
		}
	case ConfirmationAuthenticating:
		switch to {
		case ConfirmationApproved, ConfirmationRejected, ConfirmationFailed, ConfirmationExpired:
			return true
		}
	case ConfirmationApproved:
		switch to {
		case ConfirmationCompleted, ConfirmationFailed, ConfirmationExpired:
			return true
		}
	case ConfirmationCompleted, ConfirmationRejected, ConfirmationFailed, ConfirmationExpired:
		// terminal: no outgoing edges
	}
	return false
}

// DefaultConfirmationTTL is the card lifetime for money movement.
const DefaultConfirmationTTL = 5 * time.Minute

// Confirmation is one live card: server-issued, single-use, idempotent by ID.
// Creating it never moves money — only a Face ID success plus server accept
// executes the action.
type Confirmation struct {
	ID            uuid.UUID          `json:"action_id"`
	UserID        uuid.UUID          `json:"user_id"`
	Action        ConfirmationAction `json:"action"`
	State         ConfirmationState  `json:"state"`
	Title         string             `json:"title"`
	Subtitle      string             `json:"subtitle"`
	Amount        string             `json:"amount,omitempty"`
	Asset         string             `json:"asset,omitempty"`
	Destination   string             `json:"destination,omitempty"`
	Fee           string             `json:"fee,omitempty"`
	RiskLine      string             `json:"risk_line,omitempty"`
	Payload       map[string]any     `json:"payload,omitempty"`
	ExecuteKey    string             `json:"-"` // idempotency key, defaults to ID
	ExpiresAt     time.Time          `json:"expires_at"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	CompletedAt   *time.Time         `json:"completed_at,omitempty"`
	ResultSummary string             `json:"result_summary,omitempty"`
	// Assurance is how strongly the approve is bound to the device owner:
	// token_only | enrolled | secure_enclave. Set at approve time, transient
	// (not part of the idempotency contract). The server never sees
	// biometrics — secure_enclave means the call carried a signature from a
	// Secure Enclave key whose use requires Face ID.
	Assurance string `json:"assurance,omitempty"`
	// EnrolledKeyID echoes the device key enrolled by THIS approve call
	// (trust-on-first-use), so the extension learns its server key id. Empty
	// on every other path.
	EnrolledKeyID  string `json:"enrolled_key_id,omitempty"`
	TokenUsed      bool   `json:"-"`
	CardEditFailed bool   `json:"card_edit_failed,omitempty"`
}

// IsExpired reports whether the TTL has passed. Callers must still transition
// the record to expired (ray ExpireConfirmations) so replays render dead.
func (c *Confirmation) IsExpired(now time.Time) bool {
	return !now.Before(c.ExpiresAt)
}

// IsTerminal reports whether the confirmation reached an end state.
func (c *Confirmation) IsTerminal() bool {
	return TerminalConfirmationState(c.State)
}

// PreviewLayout is the static-bubble half of the styling split: Apple
// MSMessageTemplateLayout slots only (caption, subcaption, trailing captions,
// JPEG image reference, summary). No buttons, no CSS — the Face ID button and
// all motion live in the Messages extension.
type PreviewLayout struct {
	Caption            string `json:"caption,omitempty"`
	Subcaption         string `json:"subcaption,omitempty"`
	TrailingCaption    string `json:"trailing_caption,omitempty"`
	TrailingSubcaption string `json:"trailing_subcaption,omitempty"`
	Image              string `json:"image,omitempty"` // branded JPEG asset name per state family
	ImageTitle         string `json:"image_title,omitempty"`
	ImageSubtitle      string `json:"image_subtitle,omitempty"`
	Summary            string `json:"summary,omitempty"`
}

// PreviewLayoutFor renders the static bubble for a confirmation in a state.
// One branded JPEG background per state family; copy comes from the record.
func PreviewLayoutFor(c *Confirmation, state ConfirmationState, expiresIn string) PreviewLayout {
	summary := fmt.Sprintf("%s: %s", c.Title, state)
	switch state {
	case ConfirmationPending:
		sub := c.Subtitle
		if expiresIn != "" {
			if sub != "" {
				sub += " · "
			}
			sub += "Expires in " + expiresIn
		}
		return PreviewLayout{
			Caption:    c.Title,
			Subcaption: sub,
			Image:      "confirm-pending.jpg",
			Summary:    fmt.Sprintf("%s — approve with Face ID: %s", c.Title, c.Subtitle),
		}
	case ConfirmationAuthenticating:
		return PreviewLayout{
			Caption:    c.Title,
			Subcaption: "Confirming…",
			Image:      "confirm-pending.jpg",
			Summary:    fmt.Sprintf("%s — confirming…", c.Title),
		}
	case ConfirmationApproved:
		return PreviewLayout{
			Caption:    c.Title,
			Subcaption: "Approved — working on it…",
			Image:      "confirm-working.jpg",
			Summary:    fmt.Sprintf("%s — approved, executing…", c.Title),
		}
	case ConfirmationCompleted:
		sub := c.ResultSummary
		if sub == "" {
			sub = "Done"
		}
		return PreviewLayout{
			Caption:    c.Title,
			Subcaption: sub,
			Image:      "confirm-done.jpg",
			Summary:    fmt.Sprintf("%s — done. %s", c.Title, sub),
		}
	case ConfirmationRejected:
		return PreviewLayout{
			Caption:    c.Title,
			Subcaption: "Cancelled",
			Image:      "confirm-muted.jpg",
			Summary:    fmt.Sprintf("%s — cancelled, nothing moved.", c.Title),
		}
	case ConfirmationFailed:
		sub := c.ResultSummary
		if sub == "" {
			sub = "Something went wrong"
		}
		return PreviewLayout{
			Caption:    c.Title,
			Subcaption: sub,
			Image:      "confirm-error.jpg",
			Summary:    fmt.Sprintf("%s — failed: %s", c.Title, sub),
		}
	case ConfirmationExpired:
		return PreviewLayout{
			Caption:    c.Title,
			Subcaption: "Expired — ask again",
			Image:      "confirm-muted.jpg",
			Summary:    fmt.Sprintf("%s — expired, nothing moved.", c.Title),
		}
	}
	return PreviewLayout{Caption: c.Title, Subcaption: c.Subtitle, Image: "confirm-pending.jpg", Summary: summary}
}

// ConfirmationAuditEntry records who did what on a card. Biometric outcome is
// pass/fail/cancel only — never biometric data.
type ConfirmationAuditEntry struct {
	ID             uuid.UUID          `json:"id"`
	ConfirmationID uuid.UUID          `json:"confirmation_id"`
	UserID         uuid.UUID          `json:"user_id"`
	Action         ConfirmationAction `json:"action"`
	FromState      ConfirmationState  `json:"from_state"`
	ToState        ConfirmationState  `json:"to_state"`
	Amount         string             `json:"amount,omitempty"`
	Biometric      string             `json:"biometric,omitempty"` // pass|fail|cancel|""
	Note           string             `json:"note,omitempty"`
	CardEditOK     *bool              `json:"card_edit_ok,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
}
