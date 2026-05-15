package entities

import (
	"time"

	"github.com/google/uuid"
)

type GrowthMailCampaign string

const (
	GrowthMailFirstDeposit  GrowthMailCampaign = "first_deposit"
	GrowthMailFirstSplit    GrowthMailCampaign = "first_split"
	GrowthMailWeeklyExplore GrowthMailCampaign = "weekly_explore"
	GrowthMailStatusSent                       = "sent"
	GrowthMailStatusFailed                     = "failed"
)

type GrowthMailCandidate struct {
	UserID       uuid.UUID
	Email        string
	FirstName    string
	KYCStatus    string
	CreatedAt    time.Time
	LastLoginAt  *time.Time
	DepositCount int
}

func (c GrowthMailCandidate) HasDeposit() bool {
	return c.DepositCount > 0
}

type GrowthMailEvent struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	CampaignKey string
	Campaign    GrowthMailCampaign
	Subject     string
	Status      string
	Error       string
	SentAt      *time.Time
	CreatedAt   time.Time
}
