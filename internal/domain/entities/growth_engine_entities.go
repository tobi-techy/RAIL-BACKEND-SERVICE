package entities

import (
	"time"

	"github.com/google/uuid"
)

type GrowthEventName string
type UserLifecycleStage string
type GrowthSegment string
type GrowthCampaignChannel string
type GrowthDeliveryStatus string

const (
	GrowthEventUserSignedUp           GrowthEventName = "user_signed_up"
	GrowthEventAppOpened              GrowthEventName = "app_opened"
	GrowthEventKYCStarted             GrowthEventName = "kyc_started"
	GrowthEventKYCCompleted           GrowthEventName = "kyc_completed"
	GrowthEventDepositStarted         GrowthEventName = "deposit_started"
	GrowthEventDepositCompleted       GrowthEventName = "deposit_completed"
	GrowthEventMiriamUsed             GrowthEventName = "miriam_used"
	GrowthEventAllocationEnabled      GrowthEventName = "allocation_enabled"
	GrowthEventInactive7DaysDetected  GrowthEventName = "inactive_7_days_detected"
	GrowthEventInactive14DaysDetected GrowthEventName = "inactive_14_days_detected"
	GrowthEventReactivated            GrowthEventName = "reactivated"

	StageSignedUp         UserLifecycleStage = "signed_up"
	StageOpenedApp        UserLifecycleStage = "opened_app"
	StageKYCStarted       UserLifecycleStage = "kyc_started"
	StageKYCCompleted     UserLifecycleStage = "kyc_completed"
	StageFirstDepositDone UserLifecycleStage = "first_deposit_done"
	StageActivated        UserLifecycleStage = "activated"
	StageDormant          UserLifecycleStage = "dormant"
	StageChurned          UserLifecycleStage = "churned"

	SegmentActive             GrowthSegment = "active"
	SegmentSignupNoKYC        GrowthSegment = "signup_no_kyc"
	SegmentKYCAbandoned       GrowthSegment = "kyc_abandoned"
	SegmentKYCNoDeposit       GrowthSegment = "kyc_no_deposit"
	SegmentInactive7Days      GrowthSegment = "inactive_7_days"
	SegmentMiriamUserInactive GrowthSegment = "miriam_user_inactive"
	SegmentFullyChurned       GrowthSegment = "fully_churned"

	GrowthChannelEmail          GrowthCampaignChannel = "email"
	GrowthChannelPush           GrowthCampaignChannel = "push"
	GrowthChannelManualWhatsApp GrowthCampaignChannel = "manual_whatsapp"
	GrowthChannelInApp          GrowthCampaignChannel = "in_app"

	GrowthDeliveryQueued    GrowthDeliveryStatus = "queued"
	GrowthDeliverySent      GrowthDeliveryStatus = "sent"
	GrowthDeliveryFailed    GrowthDeliveryStatus = "failed"
	GrowthDeliveryConverted GrowthDeliveryStatus = "converted"
)

type UserEvent struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	UserID    uuid.UUID       `json:"user_id" db:"user_id"`
	EventName GrowthEventName `json:"event_name" db:"event_name"`
	Metadata  map[string]any  `json:"metadata,omitempty" db:"metadata"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
}

type UserLifecycle struct {
	UserID                uuid.UUID          `json:"user_id" db:"user_id"`
	LifecycleStage        UserLifecycleStage `json:"lifecycle_stage" db:"lifecycle_stage"`
	CurrentSegment        GrowthSegment      `json:"current_segment" db:"current_segment"`
	LastActiveAt          *time.Time         `json:"last_active_at,omitempty" db:"last_active_at"`
	KYCStartedAt          *time.Time         `json:"kyc_started_at,omitempty" db:"kyc_started_at"`
	KYCCompletedAt        *time.Time         `json:"kyc_completed_at,omitempty" db:"kyc_completed_at"`
	FirstDepositStartedAt *time.Time         `json:"first_deposit_started_at,omitempty" db:"first_deposit_started_at"`
	FirstDepositAt        *time.Time         `json:"first_deposit_at,omitempty" db:"first_deposit_at"`
	LastDepositAt         *time.Time         `json:"last_deposit_at,omitempty" db:"last_deposit_at"`
	MiriamLastUsedAt      *time.Time         `json:"miriam_last_used_at,omitempty" db:"miriam_last_used_at"`
	AllocationEnabled     bool               `json:"allocation_enabled" db:"allocation_enabled"`
	ReactivationScore     int                `json:"reactivation_score" db:"reactivation_score"`
	SegmentAssignedAt     *time.Time         `json:"segment_assigned_at,omitempty" db:"segment_assigned_at"`
	ReactivatedAt         *time.Time         `json:"reactivated_at,omitempty" db:"reactivated_at"`
	CreatedAt             time.Time          `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at" db:"updated_at"`
}

type GrowthUserSnapshot struct {
	UserID                uuid.UUID
	Email                 string
	Phone                 *string
	FirstName             string
	CreatedAt             time.Time
	LastLoginAt           *time.Time
	KYCStatus             string
	KYCStartedAt          *time.Time
	KYCCompletedAt        *time.Time
	FirstDepositStartedAt *time.Time
	FirstDepositAt        *time.Time
	LastDepositAt         *time.Time
	MiriamLastUsedAt      *time.Time
	LastActiveAt          *time.Time
	AllocationEnabled     bool
	CurrentSegment        GrowthSegment
}

type GrowthCampaign struct {
	ID              uuid.UUID             `json:"id" db:"id"`
	Name            string                `json:"name" db:"name"`
	Segment         GrowthSegment         `json:"segment" db:"segment"`
	Channel         GrowthCampaignChannel `json:"channel" db:"channel"`
	TemplateKey     string                `json:"template_key" db:"template_key"`
	Subject         string                `json:"subject" db:"subject"`
	Body            string                `json:"body" db:"body"`
	CTA             string                `json:"cta" db:"cta"`
	CooldownDays    int                   `json:"cooldown_days" db:"cooldown_days"`
	ConversionEvent GrowthEventName       `json:"conversion_event" db:"conversion_event"`
	FromEmail       string                `json:"from_email" db:"from_email"`
	FromName        string                `json:"from_name" db:"from_name"`
	ReplyTo         string                `json:"reply_to" db:"reply_to"`
	IsActive        bool                  `json:"is_active" db:"is_active"`
	CreatedAt       time.Time             `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at" db:"updated_at"`
}

type CampaignDelivery struct {
	ID          uuid.UUID             `json:"id" db:"id"`
	UserID      uuid.UUID             `json:"user_id" db:"user_id"`
	CampaignID  uuid.UUID             `json:"campaign_id" db:"campaign_id"`
	Segment     GrowthSegment         `json:"segment" db:"segment"`
	Channel     GrowthCampaignChannel `json:"channel" db:"channel"`
	Status      GrowthDeliveryStatus  `json:"status" db:"status"`
	Error       string                `json:"error,omitempty" db:"error"`
	RenderedTo  string                `json:"rendered_to,omitempty" db:"rendered_to"`
	Subject     string                `json:"subject,omitempty" db:"subject"`
	Body        string                `json:"body,omitempty" db:"body"`
	SentAt      *time.Time            `json:"sent_at,omitempty" db:"sent_at"`
	OpenedAt    *time.Time            `json:"opened_at,omitempty" db:"opened_at"`
	ClickedAt   *time.Time            `json:"clicked_at,omitempty" db:"clicked_at"`
	ConvertedAt *time.Time            `json:"converted_at,omitempty" db:"converted_at"`
	CreatedAt   time.Time             `json:"created_at" db:"created_at"`
}

type WhatsAppGrowthLead struct {
	Name             string        `json:"name"`
	Phone            string        `json:"phone"`
	Segment          GrowthSegment `json:"segment"`
	SuggestedMessage string        `json:"suggested_message"`
	LastActionAt     *time.Time    `json:"last_action_at,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
}
