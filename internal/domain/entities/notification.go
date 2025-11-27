package entities

import (
	"time"

	"github.com/google/uuid"
)

type NotificationType string
type NotificationChannel string
type NotificationPriority string

const (
	NotificationTypeDeposit     NotificationType = "deposit"
	NotificationTypeWithdrawal  NotificationType = "withdrawal"
	NotificationTypeTrade       NotificationType = "trade"
	NotificationTypeKYC         NotificationType = "kyc"
	NotificationTypeSecurity    NotificationType = "security"
	NotificationTypePortfolio   NotificationType = "portfolio"
	NotificationTypeAllocation  NotificationType = "allocation"

	ChannelEmail    NotificationChannel = "email"
	ChannelPush     NotificationChannel = "push"
	ChannelSMS      NotificationChannel = "sms"
	ChannelInApp    NotificationChannel = "in_app"

	PriorityLow      NotificationPriority = "low"
	PriorityMedium   NotificationPriority = "medium"
	PriorityHigh     NotificationPriority = "high"
	PriorityCritical NotificationPriority = "critical"
)

type Notification struct {
	ID        uuid.UUID            `json:"id" db:"id"`
	UserID    uuid.UUID            `json:"user_id" db:"user_id"`
	Type      NotificationType     `json:"type" db:"type"`
	Channel   NotificationChannel  `json:"channel" db:"channel"`
	Priority  NotificationPriority `json:"priority" db:"priority"`
	Title     string               `json:"title" db:"title"`
	Message   string               `json:"message" db:"message"`
	Data      map[string]interface{} `json:"data,omitempty" db:"data"`
	Read      bool                 `json:"read" db:"read"`
	SentAt    *time.Time           `json:"sent_at,omitempty" db:"sent_at"`
	ReadAt    *time.Time           `json:"read_at,omitempty" db:"read_at"`
	CreatedAt time.Time            `json:"created_at" db:"created_at"`
}

type UserPreference struct {
	ID                    uuid.UUID `json:"id" db:"id"`
	UserID                uuid.UUID `json:"user_id" db:"user_id"`
	EmailNotifications    bool      `json:"email_notifications" db:"email_notifications"`
	PushNotifications     bool      `json:"push_notifications" db:"push_notifications"`
	SMSNotifications      bool      `json:"sms_notifications" db:"sms_notifications"`
	DepositAlerts         bool      `json:"deposit_alerts" db:"deposit_alerts"`
	WithdrawalAlerts      bool      `json:"withdrawal_alerts" db:"withdrawal_alerts"`
	TradeAlerts           bool      `json:"trade_alerts" db:"trade_alerts"`
	SecurityAlerts        bool      `json:"security_alerts" db:"security_alerts"`
	PortfolioUpdates      bool      `json:"portfolio_updates" db:"portfolio_updates"`
	MarketingEmails       bool      `json:"marketing_emails" db:"marketing_emails"`
	UpdatedAt             time.Time `json:"updated_at" db:"updated_at"`
	CreatedAt             time.Time `json:"created_at" db:"created_at"`
}
