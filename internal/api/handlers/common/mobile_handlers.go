package common

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// MobileHandlers handles mobile-optimized API endpoints
type MobileHandlers struct {
	stationService    *station.Service
	allocationService *allocation.Service
	investingService  *investing.Service
	userRepo          repositories.UserRepository
	cardRepo          CardRepository
	logger            *zap.Logger
}

const (
	mobileRequestTimeout = 4 * time.Second
	batchMaxConcurrent   = 4
)

// CardRepository interface for card status checks
type CardRepository interface {
	GetActiveVirtualCard(ctx context.Context, userID uuid.UUID) (*entities.BridgeCard, error)
}

// NewMobileHandlers creates a new mobile handlers instance
func NewMobileHandlers(
	stationService *station.Service,
	allocationService *allocation.Service,
	investingService *investing.Service,
	userRepo repositories.UserRepository,
	cardRepo CardRepository,
	logger *zap.Logger,
) *MobileHandlers {
	return &MobileHandlers{
		stationService:    stationService,
		allocationService: allocationService,
		investingService:  investingService,
		userRepo:          userRepo,
		cardRepo:          cardRepo,
		logger:            logger,
	}
}

// MobileHomeResponse is a compact response for the mobile home screen
type MobileHomeResponse struct {
	// Core balances (minimal payload)
	TotalBalance  string `json:"total_balance"`
	SpendBalance  string `json:"spend_balance"`
	InvestBalance string `json:"invest_balance"`
	BrokerCash    string `json:"broker_cash"`
	Currency      string `json:"currency"`

	// Status indicators
	SystemStatus string `json:"system_status"` // active, allocating, paused
	KYCVerified  bool   `json:"kyc_verified"`
	HasCard      bool   `json:"has_card"`

	// Sync metadata for offline support
	LastSyncAt  string `json:"last_sync_at"`
	SyncVersion int64  `json:"sync_version"`

	// Optional: recent activity count (not full list)
	PendingActions int `json:"pending_actions"`
}

// GetMobileHome handles GET /mobile/home
// @Summary Get mobile home screen data
// @Description Returns optimized payload for mobile home screen with all essential data in one call
// @Tags mobile
// @Produce json
// @Success 200 {object} MobileHomeResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/mobile/home [get]
func (h *MobileHandlers) GetMobileHome(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), mobileRequestTimeout)
	defer cancel()

	userID, err := h.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	var (
		balances   *entities.AllocationBalances
		balanceErr error
		user       *entities.User
		hasCard    bool
	)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		balances, balanceErr = h.allocationService.GetBalancesLite(ctx, userID)
	}()

	go func() {
		defer wg.Done()
		user, _ = h.userRepo.GetUserEntityByID(ctx, userID)
	}()

	go func() {
		defer wg.Done()
		if h.cardRepo != nil {
			if card, _ := h.cardRepo.GetActiveVirtualCard(ctx, userID); card != nil {
				hasCard = true
			}
		}
	}()

	wg.Wait()

	if balanceErr != nil {
		h.logger.Error("Failed to get balances", zap.Error(balanceErr), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BALANCE_ERROR",
			Message: "Failed to retrieve balances",
		})
		return
	}

	kycVerified := user != nil && user.KYCStatus == "approved"

	systemStatus := "active"
	if !balances.ModeActive {
		systemStatus = "paused"
	}

	response := MobileHomeResponse{
		TotalBalance:   balances.TotalBalance.StringFixed(2),
		SpendBalance:   balances.SpendingBalance.StringFixed(2),
		InvestBalance:  balances.InvestBalance.StringFixed(2),
		BrokerCash:     balances.FiatExposure.StringFixed(2),
		Currency:       "USD",
		SystemStatus:   systemStatus,
		KYCVerified:    kycVerified,
		HasCard:        hasCard,
		LastSyncAt:     time.Now().UTC().Format(time.RFC3339),
		SyncVersion:    time.Now().Unix(),
		PendingActions: 0,
	}

	c.JSON(http.StatusOK, response)
}

// BatchRequest represents a batch API request
type BatchRequest struct {
	Requests []BatchRequestItem `json:"requests" validate:"required,min=1,max=10"`
}

// BatchRequestItem represents a single request in a batch
type BatchRequestItem struct {
	ID     string `json:"id" validate:"required"`
	Method string `json:"method" validate:"required,oneof=GET POST"`
	Path   string `json:"path" validate:"required"`
}

// BatchResponse represents a batch API response
type BatchResponse struct {
	Responses []BatchResponseItem `json:"responses"`
}

// BatchResponseItem represents a single response in a batch
type BatchResponseItem struct {
	ID     string      `json:"id"`
	Status int         `json:"status"`
	Data   interface{} `json:"data,omitempty"`
	Error  *string     `json:"error,omitempty"`
}

// BatchExecute handles POST /mobile/batch
// @Summary Execute batch API requests
// @Description Execute multiple API requests in a single call to reduce round trips
// @Tags mobile
// @Accept json
// @Produce json
// @Param request body BatchRequest true "Batch request"
// @Success 200 {object} BatchResponse
// @Failure 400 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/mobile/batch [post]
func (h *MobileHandlers) BatchExecute(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), mobileRequestTimeout)
	defer cancel()

	userID, err := h.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	var req BatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid batch request",
		})
		return
	}

	responses := make([]BatchResponseItem, len(req.Requests))
	sem := make(chan struct{}, batchMaxConcurrent)
	var wg sync.WaitGroup

	for i, item := range req.Requests {
		i, item := i, item
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			responses[i] = h.executeBatchItem(ctx, userID, item)
		}()
	}

	wg.Wait()

	c.JSON(http.StatusOK, BatchResponse{Responses: responses})
}

// executeBatchItem executes a single batch request item
func (h *MobileHandlers) executeBatchItem(ctx context.Context, userID uuid.UUID, item BatchRequestItem) BatchResponseItem {
	response := BatchResponseItem{ID: item.ID}

	switch item.Path {
	case "/balances":
		balances, err := h.allocationService.GetBalancesLite(ctx, userID)
		if err != nil {
			errMsg := "Failed to get balances"
			response.Status = 500
			response.Error = &errMsg
		} else {
			response.Status = 200
			response.Data = map[string]string{
				"total":       balances.TotalBalance.StringFixed(2),
				"spend":       balances.SpendingBalance.StringFixed(2),
				"invest":      balances.InvestBalance.StringFixed(2),
				"broker_cash": balances.FiatExposure.StringFixed(2),
			}
		}

	case "/portfolio":
		portfolio, err := h.investingService.GetPortfolioOverview(ctx, userID)
		if err != nil {
			errMsg := "Failed to get portfolio"
			response.Status = 500
			response.Error = &errMsg
		} else {
			response.Status = 200
			response.Data = portfolio
		}

	case "/profile":
		user, err := h.userRepo.GetUserEntityByID(ctx, userID)
		if err != nil {
			errMsg := "Failed to get profile"
			response.Status = 500
			response.Error = &errMsg
		} else {
			response.Status = 200
			response.Data = map[string]interface{}{
				"email":      user.Email,
				"kyc_status": user.KYCStatus,
				"onboarding": user.OnboardingStatus,
			}
		}

	default:
		errMsg := "Unknown path"
		response.Status = 404
		response.Error = &errMsg
	}

	return response
}

// SyncRequest represents a sync request for offline support
type SyncRequest struct {
	LastSyncVersion int64    `json:"last_sync_version"`
	RequestedData   []string `json:"requested_data"` // balances, portfolio, transactions
}

// SyncResponse represents sync data for offline support
type SyncResponse struct {
	SyncVersion int64                  `json:"sync_version"`
	HasChanges  bool                   `json:"has_changes"`
	Data        map[string]interface{} `json:"data,omitempty"`
}

// Sync handles POST /mobile/sync
// @Summary Sync data for offline support
// @Description Returns changed data since last sync for offline caching
// @Tags mobile
// @Accept json
// @Produce json
// @Param request body SyncRequest true "Sync request"
// @Success 200 {object} SyncResponse
// @Failure 400 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/mobile/sync [post]
func (h *MobileHandlers) Sync(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), mobileRequestTimeout)
	defer cancel()

	userID, err := h.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	var req SyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid sync request",
		})
		return
	}

	currentVersion := time.Now().Unix()
	data := make(map[string]interface{})

	// Always return data if requested (simplified - in production, track actual changes)
	hasChanges := req.LastSyncVersion < currentVersion

	if hasChanges {
		var (
			wg sync.WaitGroup
			mu sync.Mutex
		)

		for _, dataType := range req.RequestedData {
			dataType := dataType
			wg.Add(1)
			go func() {
				defer wg.Done()
				switch dataType {
				case "balances":
					if balances, err := h.allocationService.GetBalancesLite(ctx, userID); err == nil {
						payload := map[string]string{
							"total":       balances.TotalBalance.StringFixed(2),
							"spend":       balances.SpendingBalance.StringFixed(2),
							"invest":      balances.InvestBalance.StringFixed(2),
							"broker_cash": balances.FiatExposure.StringFixed(2),
						}
						mu.Lock()
						data["balances"] = payload
						mu.Unlock()
					}
				case "portfolio":
					if portfolio, err := h.investingService.GetPortfolioOverview(ctx, userID); err == nil {
						mu.Lock()
						data["portfolio"] = portfolio
						mu.Unlock()
					}
				case "profile":
					if user, err := h.userRepo.GetUserEntityByID(ctx, userID); err == nil {
						payload := map[string]interface{}{
							"email":      user.Email,
							"kyc_status": user.KYCStatus,
						}
						mu.Lock()
						data["profile"] = payload
						mu.Unlock()
					}
				}
			}()
		}

		wg.Wait()
	}

	c.JSON(http.StatusOK, SyncResponse{
		SyncVersion: currentVersion,
		HasChanges:  hasChanges,
		Data:        data,
	})
}

// getUserID extracts user ID from context
func (h *MobileHandlers) GetUserID(c *gin.Context) (uuid.UUID, error) {
	userIDStr, exists := c.Get("userID")
	if !exists {
		return uuid.Nil, fmt.Errorf("user not authenticated")
	}
	userID, ok := userIDStr.(uuid.UUID)
	if !ok {
		return uuid.Nil, fmt.Errorf("invalid user ID")
	}
	return userID, nil
}
