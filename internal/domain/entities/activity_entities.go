package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ActivityType represents the kind of activity.
type ActivityType string

const (
	ActivityTypeDeposit        ActivityType = "deposit"
	ActivityTypeWithdrawal     ActivityType = "withdrawal"
	ActivityTypeNairaFund      ActivityType = "naira_fund"      // PAJ onramp: NGN → USDC
	ActivityTypeNairaWithdraw  ActivityType = "naira_withdraw"  // PAJ offramp: USDC → NGN
	ActivityTypeP2PSend        ActivityType = "p2p_send"
	ActivityTypeP2PReceive     ActivityType = "p2p_receive"
	ActivityTypeInvestment     ActivityType = "investment"
	ActivityTypeCardPayment    ActivityType = "card_payment"
	ActivityTypeAllocation     ActivityType = "allocation"      // internal 70/30 split
)

// ActivityStatus is a normalized status across all transaction types.
type ActivityStatus string

const (
	ActivityStatusPending    ActivityStatus = "pending"
	ActivityStatusProcessing ActivityStatus = "processing"
	ActivityStatusCompleted  ActivityStatus = "completed"
	ActivityStatusFailed     ActivityStatus = "failed"
	ActivityStatusCancelled  ActivityStatus = "cancelled"
)

// ActivityDirection indicates money flow relative to the user.
type ActivityDirection string

const (
	ActivityDirectionIn  ActivityDirection = "in"
	ActivityDirectionOut ActivityDirection = "out"
)

// ActivityCurrencyPair shows both sides of a conversion (e.g., NGN → USDC).
// For simple transfers, only Primary is set.
type ActivityCurrencyPair struct {
	Primary   string `json:"primary"`             // e.g. "USDC", "NGN"
	Secondary string `json:"secondary,omitempty"`  // e.g. "USDC" when primary is "NGN"
}

// ActivityItem is a single normalized activity entry returned to the frontend.
type ActivityItem struct {
	ID          string               `json:"id"`
	Type        ActivityType         `json:"type"`
	Direction   ActivityDirection     `json:"direction"`
	Status      ActivityStatus       `json:"status"`
	Title       string               `json:"title"`
	Subtitle    string               `json:"subtitle,omitempty"`
	Amount      decimal.Decimal      `json:"amount"`                // primary amount (what user sees)
	Currency    ActivityCurrencyPair `json:"currency"`
	FiatAmount  *decimal.Decimal     `json:"fiatAmount,omitempty"`  // NGN amount for naira txns
	FeeAmount   *decimal.Decimal     `json:"feeAmount,omitempty"`
	Chain       string               `json:"chain,omitempty"`
	TxHash      string               `json:"txHash,omitempty"`
	Destination string               `json:"destination,omitempty"` // address, bank, railtag
	SourceID    string               `json:"sourceId"`              // original entity ID for detail fetch
	SourceType  string               `json:"sourceType"`            // "deposit", "withdrawal", "paj_order", "p2p_transfer"
	GroupID     string               `json:"groupId,omitempty"`     // links related txns (e.g., PAJ order + deposit)
	CreatedAt   time.Time            `json:"createdAt"`
	CompletedAt *time.Time           `json:"completedAt,omitempty"`
}

// ActivityFeedResponse is the paginated response for the activity endpoint.
type ActivityFeedResponse struct {
	Items      []ActivityItem `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
	HasMore    bool           `json:"hasMore"`
}

// NormalizeDepositToActivity converts a Deposit to an ActivityItem.
func NormalizeDepositToActivity(d *Deposit) ActivityItem {
	status := normalizeDepositStatus(d.Status)
	return ActivityItem{
		ID:         d.ID.String(),
		Type:       ActivityTypeDeposit,
		Direction:  ActivityDirectionIn,
		Status:     status,
		Title:      "Deposit received",
		Subtitle:   string(d.Chain) + " • " + string(d.Token),
		Amount:     d.Amount,
		Currency:   ActivityCurrencyPair{Primary: string(d.Token)},
		Chain:      string(d.Chain),
		TxHash:     d.TxHash,
		SourceID:   d.ID.String(),
		SourceType: "deposit",
		CreatedAt:  d.CreatedAt,
		CompletedAt: d.ConfirmedAt,
	}
}

// NormalizeWithdrawalToActivity converts a Withdrawal to an ActivityItem.
func NormalizeWithdrawalToActivity(w *Withdrawal) ActivityItem {
	status := normalizeWithdrawalStatus(w.Status)
	dest := ""
	if w.DestinationAddress != nil {
		dest = *w.DestinationAddress
	}
	title := "Crypto withdrawal"
	subtitle := w.DestinationChain
	if w.WithdrawalType == "fiat" {
		title = "Fiat withdrawal"
		subtitle = string(w.Currency)
	}
	fee := w.FeeAmount
	return ActivityItem{
		ID:          w.ID.String(),
		Type:        ActivityTypeWithdrawal,
		Direction:   ActivityDirectionOut,
		Status:      status,
		Title:       title,
		Subtitle:    subtitle,
		Amount:      w.Amount,
		Currency:    ActivityCurrencyPair{Primary: string(w.Currency)},
		FeeAmount:   &fee,
		Chain:       w.DestinationChain,
		TxHash:      derefStr(w.TxHash),
		Destination: dest,
		SourceID:    w.ID.String(),
		SourceType:  "withdrawal",
		CreatedAt:   w.CreatedAt,
		CompletedAt: w.CompletedAt,
	}
}

// PajOrderForActivity is the minimal PAJ order data needed for normalization.
type PajOrderForActivity struct {
	ID                string
	OrderType         string // "onramp" or "offramp"
	Status            string
	FiatAmount        float64
	TokenAmount       float64
	Currency          string // "NGN"
	Rate              float64
	Fee               float64
	BankAccountName   *string
	CreatedAt         time.Time
}

// NormalizePajOrderToActivity converts a PAJ order to an ActivityItem.
// For onramp: shows as "Funded with ₦X → $Y USDC" with dual currency.
// For offramp: shows as "Withdrew ₦X" with dual currency.
func NormalizePajOrderToActivity(o *PajOrderForActivity) ActivityItem {
	status := normalizePajStatus(o.Status)
	usdcAmount := decimal.NewFromFloat(o.TokenAmount)
	fiatAmount := decimal.NewFromFloat(o.FiatAmount)

	if o.OrderType == "onramp" {
		return ActivityItem{
			ID:         o.ID,
			Type:       ActivityTypeNairaFund,
			Direction:  ActivityDirectionIn,
			Status:     status,
			Title:      "Funded with Naira",
			Subtitle:   formatNaira(o.FiatAmount) + " → " + usdcAmount.StringFixed(2) + " USDC",
			Amount:     usdcAmount,
			Currency:   ActivityCurrencyPair{Primary: "NGN", Secondary: "USDC"},
			FiatAmount: &fiatAmount,
			SourceID:   o.ID,
			SourceType: "paj_order",
			GroupID:    o.ID, // deposits linked to this order share this groupID
			CreatedAt:  o.CreatedAt,
		}
	}

	// offramp
	bankName := ""
	if o.BankAccountName != nil {
		bankName = *o.BankAccountName
	}
	return ActivityItem{
		ID:          o.ID,
		Type:        ActivityTypeNairaWithdraw,
		Direction:   ActivityDirectionOut,
		Status:      status,
		Title:       "Withdrew to bank",
		Subtitle:    formatNaira(o.FiatAmount) + " • " + bankName,
		Amount:      usdcAmount,
		Currency:    ActivityCurrencyPair{Primary: "NGN", Secondary: "USDC"},
		FiatAmount:  &fiatAmount,
		SourceID:    o.ID,
		SourceType:  "paj_order",
		CreatedAt:   o.CreatedAt,
	}
}

// P2PTransferForActivity is the minimal P2P data needed for normalization.
type P2PTransferForActivity struct {
	ID                  uuid.UUID
	SenderID            uuid.UUID
	RecipientIdentifier string
	Amount              decimal.Decimal
	Note                *string
	Status              string
	CreatedAt           time.Time
	CompletedAt         *time.Time
}

// NormalizeP2PToActivity converts a P2P transfer to an ActivityItem.
func NormalizeP2PToActivity(t *P2PTransferForActivity, viewerID uuid.UUID) ActivityItem {
	isSender := t.SenderID == viewerID
	direction := ActivityDirectionOut
	title := "Sent to " + t.RecipientIdentifier
	actType := ActivityTypeP2PSend
	if !isSender {
		direction = ActivityDirectionIn
		title = "Received from friend"
		actType = ActivityTypeP2PReceive
	}
	subtitle := ""
	if t.Note != nil {
		subtitle = *t.Note
	}
	return ActivityItem{
		ID:          t.ID.String(),
		Type:        actType,
		Direction:   direction,
		Status:      normalizeP2PStatus(t.Status),
		Title:       title,
		Subtitle:    subtitle,
		Amount:      t.Amount,
		Currency:    ActivityCurrencyPair{Primary: "USDC"},
		Destination: t.RecipientIdentifier,
		SourceID:    t.ID.String(),
		SourceType:  "p2p_transfer",
		CreatedAt:   t.CreatedAt,
		CompletedAt: t.CompletedAt,
	}
}

// --- helpers ---

func normalizeDepositStatus(s string) ActivityStatus {
	switch s {
	case "confirmed":
		return ActivityStatusCompleted
	case "failed":
		return ActivityStatusFailed
	default:
		return ActivityStatusPending
	}
}

func normalizeWithdrawalStatus(s WithdrawalStatus) ActivityStatus {
	switch s {
	case WithdrawalStatusCompleted:
		return ActivityStatusCompleted
	case WithdrawalStatusFailed:
		return ActivityStatusFailed
	case WithdrawalStatusCancelled, "reversed":
		return ActivityStatusCancelled
	case WithdrawalStatusProcessing, WithdrawalStatusOnChainTransfer, WithdrawalStatusAwaitingConfirmation:
		return ActivityStatusProcessing
	default:
		return ActivityStatusPending
	}
}

func normalizePajStatus(s string) ActivityStatus {
	switch s {
	case "completed":
		return ActivityStatusCompleted
	case "failed":
		return ActivityStatusFailed
	case "paid", "processing":
		return ActivityStatusProcessing
	default:
		return ActivityStatusPending
	}
}

func normalizeP2PStatus(s string) ActivityStatus {
	switch s {
	case "completed", "claimed":
		return ActivityStatusCompleted
	case "cancelled", "expired":
		return ActivityStatusCancelled
	case "processing":
		return ActivityStatusProcessing
	default:
		return ActivityStatusPending
	}
}

func formatNaira(amount float64) string {
	return "₦" + decimal.NewFromFloat(amount).StringFixed(0)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
