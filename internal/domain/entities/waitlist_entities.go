package entities

import (
	"time"

	"github.com/google/uuid"
)

type WaitlistStatus string

const (
	WaitlistStatusWaitlist  WaitlistStatus = "waitlist"
	WaitlistStatusInvited   WaitlistStatus = "invited"
	WaitlistStatusConverted WaitlistStatus = "converted"
)

type WaitlistUser struct {
	ID              uuid.UUID      `json:"id" db:"id"`
	Email           string         `json:"email" db:"email"`
	FullName        string         `json:"full_name" db:"full_name"`
	Status          WaitlistStatus `json:"status" db:"status"`
	ReferralCode    string         `json:"referral_code" db:"referral_code"`
	ReferredBy      *uuid.UUID     `json:"referred_by,omitempty" db:"referred_by"`
	Position        int            `json:"position" db:"position"`
	Source          string         `json:"source" db:"source"`
	ConvertedUserID *uuid.UUID     `json:"converted_user_id,omitempty" db:"converted_user_id"`
	InvitedAt       *time.Time     `json:"invited_at,omitempty" db:"invited_at"`
	ConvertedAt     *time.Time     `json:"converted_at,omitempty" db:"converted_at"`
	CreatedAt       time.Time      `json:"created_at" db:"created_at"`
}
