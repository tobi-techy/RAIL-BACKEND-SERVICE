package entities

import (
	"time"

	"github.com/google/uuid"
)

// CreateWalletSetRequest represents request to create a wallet set
type CreateWalletSetRequest struct {
	Name              string `json:"name" validate:"required"`
	CircleWalletSetID string `json:"circle_wallet_set_id,omitempty"`
}

// WalletSetsListResponse represents response for wallet sets listing
type WalletSetsListResponse struct {
	Items []WalletSet `json:"items"`
	Count int         `json:"count"`
}

// WalletSetDetailResponse represents detailed wallet set response
type WalletSetDetailResponse struct {
	WalletSet WalletSet       `json:"wallet_set"`
	Wallets   []ManagedWallet `json:"wallets,omitempty"`
	Stats     WalletSetStats  `json:"stats,omitempty"`
}

// WalletSetStats represents statistics for a wallet set
type WalletSetStats struct {
	TotalWallets    int64 `json:"total_wallets"`
	LiveWallets     int64 `json:"live_wallets"`
	CreatingWallets int64 `json:"creating_wallets"`
	FailedWallets   int64 `json:"failed_wallets"`
}

// AdminWalletsListResponse represents response for admin wallets listing
type AdminWalletsListResponse struct {
	Items []ManagedWallet `json:"items"`
	Count int             `json:"count"`
}

// AdminRole represents the role assigned to privileged users
type AdminRole string

const (
	AdminRoleUser       AdminRole = "user"
	AdminRoleAdmin      AdminRole = "admin"
	AdminRoleSuperAdmin AdminRole = "super_admin"
)

// IsValid checks whether the role is one of the supported admin roles
func (r AdminRole) IsValid() bool {
	switch r {
	case AdminRoleAdmin, AdminRoleSuperAdmin:
		return true
	default:
		return false
	}
}

// CreateAdminRequest captures the payload required to create a new admin user
type CreateAdminRequest struct {
	Email     string     `json:"email" binding:"required,email"`
	Password  string     `json:"password" binding:"required,min=8"`
	FirstName *string    `json:"firstName,omitempty"`
	LastName  *string    `json:"lastName,omitempty"`
	Phone     *string    `json:"phone,omitempty"`
	Role      *AdminRole `json:"role,omitempty"`
}

// AdminUserResponse represents an admin user's information returned to clients
type AdminUserResponse struct {
	ID               uuid.UUID        `json:"id"`
	Email            string           `json:"email"`
	Role             AdminRole        `json:"role"`
	IsActive         bool             `json:"isActive"`
	OnboardingStatus OnboardingStatus `json:"onboardingStatus"`
	KYCStatus        string           `json:"kycStatus"`
	LastLoginAt      *time.Time       `json:"lastLoginAt,omitempty"`
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
}

// AdminSession contains auth tokens issued to newly created admins
type AdminSession struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// AdminCreationResponse packages admin details with the bootstrapped session
type AdminCreationResponse struct {
	AdminUserResponse
	AdminSession AdminSession `json:"adminSession"`
}

// UpdateUserStatusRequest represents the payload to activate or suspend a user
type UpdateUserStatusRequest struct {
	IsActive bool `json:"isActive"`
}

// AdminTransaction represents a transaction surfaced in admin endpoints
type AdminTransaction struct {
	ID        uuid.UUID              `json:"id"`
	UserID    uuid.UUID              `json:"userId"`
	Type      string                 `json:"type"`
	Amount    string                 `json:"amount"`
	Status    string                 `json:"status"`
	CreatedAt time.Time              `json:"createdAt"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// SystemAnalytics aggregates platform-level metrics for admin dashboards
type SystemAnalytics struct {
	TotalUsers      int64     `json:"totalUsers"`
	ActiveUsers     int64     `json:"activeUsers"`
	TotalAdmins     int64     `json:"totalAdmins"`
	TotalDeposits   string    `json:"totalDeposits"`
	PendingDeposits int64     `json:"pendingDeposits"`
	TotalWallets    int64     `json:"totalWallets"`
	GeneratedAt     time.Time `json:"generatedAt"`
}

// CuratedBasketRequest captures the payload to create or update curated baskets
type CuratedBasketRequest struct {
	Name        string            `json:"name" binding:"required"`
	Description string            `json:"description" binding:"required"`
	RiskLevel   RiskLevel         `json:"riskLevel" binding:"required"`
	Composition []BasketComponent `json:"composition" binding:"required,dive"`
}
