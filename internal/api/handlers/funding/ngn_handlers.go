package funding

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/pkg/logger"
)

// NGNHandlers exposes Graph NGN named virtual account endpoints.
type NGNHandlers struct {
	graphVAService *funding.GraphVirtualAccountService
	logger         *logger.Logger
}

// NewNGNHandlers creates NGN virtual account handlers.
func NewNGNHandlers(graphVAService *funding.GraphVirtualAccountService, logger *logger.Logger) *NGNHandlers {
	return &NGNHandlers{graphVAService: graphVAService, logger: logger}
}

// provisionNGNRequest is the body for creating an NGN virtual account. BVN and
// id_number are transient — used to create the Graph person and then discarded.
// id_type defaults to "nin" (NIN preferred); voter's card / DL / passport allowed.
type provisionNGNRequest struct {
	BVN              string `json:"bvn" binding:"required"`
	IDType           string `json:"id_type"`
	IDNumber         string `json:"id_number" binding:"required"`
	IDDocumentURL    string `json:"id_document_url"`
	EmploymentStatus string `json:"employment_status"`
	Occupation       string `json:"occupation"`
	SourceOfFunds    string `json:"source_of_funds"`
	PrimaryPurpose   string `json:"primary_purpose"`
}

// ProvisionNGNAccount handles POST /api/v1/funding/ngn/virtual-account
func (h *NGNHandlers) ProvisionNGNAccount(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	var req provisionNGNRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.IDType) == "" {
		req.IDType = "nin" // NIN preferred when no ID type is specified
	}

	va, err := h.graphVAService.ProvisionNGNAccount(c.Request.Context(), &funding.ProvisionNGNAccountRequest{
		UserID:           userUUID,
		BVN:              req.BVN,
		IDType:           req.IDType,
		IDNumber:         req.IDNumber,
		IDDocumentURL:    req.IDDocumentURL,
		EmploymentStatus: req.EmploymentStatus,
		Occupation:       req.Occupation,
		SourceOfFunds:    req.SourceOfFunds,
		PrimaryPurpose:   req.PrimaryPurpose,
	})
	if err != nil {
		if errors.Is(err, funding.ErrNGNRequiresBVNNIN) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bvn_nin_required", "message": err.Error()})
			return
		}
		h.logger.Error("Failed to provision NGN virtual account", "error", err, "user_id", userUUID)
		common.SendInternalError(c, "NGN_ACCOUNT_ERROR", "Failed to provision NGN virtual account")
		return
	}

	common.SendSuccess(c, gin.H{"virtual_account": va})
}

// GetNGNAccount handles GET /api/v1/funding/ngn/virtual-account
func (h *NGNHandlers) GetNGNAccount(c *gin.Context) {
	userUUID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	va, err := h.graphVAService.GetNGNAccount(c.Request.Context(), userUUID)
	if err != nil {
		h.logger.Error("Failed to get NGN virtual account", "error", err, "user_id", userUUID)
		common.SendInternalError(c, "NGN_ACCOUNT_ERROR", "Failed to retrieve NGN virtual account")
		return
	}
	if va == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "No NGN virtual account. Complete verification to get one."})
		return
	}

	common.SendSuccess(c, gin.H{"virtual_account": va})
}
