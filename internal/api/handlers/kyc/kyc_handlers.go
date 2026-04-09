package kyc

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/kyc"
	"github.com/rail-service/rail_service/pkg/logger"
)

type Handler struct {
	kycService *kyc.Service
	logger     *logger.Logger
}

const maxKYCSubmitBodyBytes = 30 * 1024 * 1024

func NewHandler(kycService *kyc.Service, log *logger.Logger) *Handler {
	return &Handler{
		kycService: kycService,
		logger:     log,
	}
}

// SubmitKYC handles POST /api/v1/kyc/submit
// @Summary Submit KYC information
// @Description Submit tax ID, identity documents, and regulatory disclosures for KYC verification
// @Tags KYC
// @Accept json
// @Produce json
// @Param request body entities.KYCSubmitRequest true "KYC submission data"
// @Success 200 {object} entities.KYCSubmitResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 429 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Security BearerAuth
// @Router /kyc/submit [post]
func (h *Handler) SubmitKYC(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxKYCSubmitBodyBytes)

	// Parse request
	var req entities.KYCSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid KYC request",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		if strings.Contains(strings.ToLower(err.Error()), "request body too large") {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": "KYC payload too large",
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request format",
		})
		return
	}

	// Set context fields
	req.UserID = userID
	req.IPAddress = c.ClientIP()

	// Submit KYC
	response, err := h.kycService.SubmitKYC(ctx, &req)
	if err != nil {
		h.logger.Error("KYC submission failed",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)

		// Handle specific errors
		switch err {
		case kyc.ErrInvalidSSN:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid SSN format"})
		case kyc.ErrInvalidITIN:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ITIN format"})
		case kyc.ErrUnsupportedTaxIDType:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported tax_id_type for issuing_country"})
		case kyc.ErrInvalidIssuingCountry:
			c.JSON(http.StatusBadRequest, gin.H{"error": "issuing_country must be a valid ISO alpha-3 code"})
		case kyc.ErrMissingTaxID:
			c.JSON(http.StatusBadRequest, gin.H{"error": "tax_id is required"})
		case kyc.ErrMissingTaxIDType:
			c.JSON(http.StatusBadRequest, gin.H{"error": "tax_id_type is required"})
		case kyc.ErrMissingDocumentFront:
			c.JSON(http.StatusBadRequest, gin.H{"error": "id_document_front is required"})
		case kyc.ErrInvalidImage:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image format. Use data:image/jpeg;base64,... or data:image/png;base64,..."})
		case kyc.ErrImageTooLarge:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Image exceeds 10MB limit"})
		case kyc.ErrKYCAlreadyApproved:
			c.JSON(http.StatusBadRequest, gin.H{"error": "KYC already approved"})
		case kyc.ErrNoBridgeCustomer:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Complete signup first"})
		default:
			var incompleteProfileErr *kyc.IncompleteProfileError
			if errors.As(err, &incompleteProfileErr) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":          "Complete your profile details before KYC submission",
					"missing_fields": incompleteProfileErr.MissingFields,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit KYC"})
		}
		return
	}

	c.JSON(http.StatusOK, response)
}

// CreateSumsubSession handles POST /api/v1/kyc/sumsub/session.
func (h *Handler) CreateSumsubSession(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req entities.KYCSumsubSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid Sumsub session request",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	response, err := h.kycService.StartSumsubSession(ctx, userID, &req)
	if err != nil {
		h.logger.Error("Failed to create Sumsub KYC session",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		switch err {
		case kyc.ErrInvalidSSN:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid SSN format"})
		case kyc.ErrInvalidITIN:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ITIN format"})
		case kyc.ErrUnsupportedTaxIDType:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported tax_id_type for issuing_country"})
		case kyc.ErrKYCAlreadyApproved:
			c.JSON(http.StatusBadRequest, gin.H{"error": "KYC already approved"})
		case kyc.ErrNoBridgeCustomer:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Complete signup first"})
		case kyc.ErrSumsubNotConfigured:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "KYC provider not configured"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create verification session"})
		}
		return
	}

	c.JSON(http.StatusOK, response)
}

// HandleSumsubWebhook handles POST /api/v1/kyc/sumsub/webhook.
func (h *Handler) HandleSumsubWebhook(c *gin.Context) {
	ctx := c.Request.Context()

	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook payload"})
		return
	}

	digest := c.GetHeader("x-payload-digest")
	digestAlg := c.GetHeader("x-payload-digest-alg")
	if err := h.kycService.VerifySumsubWebhookSignature(rawBody, digest, digestAlg); err != nil {
		h.logger.Warn("Invalid Sumsub webhook signature", zap.Error(err))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	var payload entities.SumsubWebhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		h.logger.Warn("Invalid Sumsub webhook JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook payload"})
		return
	}

	enqueued, err := h.kycService.EnqueueSumsubWebhook(ctx, &payload, rawBody)
	if err != nil {
		h.logger.Error("Failed to enqueue Sumsub webhook", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enqueue webhook"})
		return
	}
	if !enqueued {
		c.JSON(http.StatusOK, gin.H{"status": "duplicate_ignored"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}

// GetKYCStatus handles GET /api/v1/kyc/status
// @Summary Get KYC status
// @Description Get current KYC verification status and capabilities
// @Tags KYC
// @Produce json
// @Success 200 {object} entities.KYCStatusResponse
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Security BearerAuth
// @Router /kyc/status [get]
func (h *Handler) GetKYCStatus(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	status, err := h.kycService.GetKYCStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get KYC status",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get KYC status"})
		return
	}

	c.JSON(http.StatusOK, status)
}

// RefreshSumsubToken handles GET /api/v1/kyc/sumsub/token.
// Issues a fresh WebSDK access token for the user's existing applicant.
func (h *Handler) RefreshSumsubToken(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	resp, err := h.kycService.RefreshSumsubToken(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to refresh Sumsub token",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to refresh verification token"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// CreateDiditSession handles POST /api/v1/kyc/didit/session.
func (h *Handler) CreateDiditSession(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req entities.KYCDigitSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid Didit session request", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
		return
	}

	response, err := h.kycService.StartDiditSession(ctx, userID, &req)
	if err != nil {
		h.logger.Error("Failed to create Didit KYC session",
			zap.Error(err), zap.String("user_id", userID.String()))
		switch {
		case errors.Is(err, kyc.ErrUnsupportedTaxIDType):
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported tax_id_type for issuing_country"})
		case errors.Is(err, kyc.ErrKYCAlreadyApproved):
			c.JSON(http.StatusBadRequest, gin.H{"error": "KYC already approved"})
		case errors.Is(err, kyc.ErrNoBridgeCustomer):
			c.JSON(http.StatusBadRequest, gin.H{"error": "Complete signup first"})
		case errors.Is(err, kyc.ErrDiditNotConfigured):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "KYC provider not configured"})
		default:
			var profileErr *kyc.IncompleteProfileError
			if errors.As(err, &profileErr) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":          "Profile incomplete - complete all fields before starting KYC",
					"missing_fields": profileErr.MissingFields,
				})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create verification session"})
			}
		}
		return
	}

	c.JSON(http.StatusOK, response)
}

// HandleDiditWebhook handles POST /api/v1/kyc/didit/webhook.
func (h *Handler) HandleDiditWebhook(c *gin.Context) {
	ctx := c.Request.Context()

	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook payload"})
		return
	}

	sig := c.GetHeader("X-Signature-V2")
	ts := c.GetHeader("X-Timestamp")
	if err := h.kycService.VerifyDiditWebhookSignature(rawBody, sig, ts); err != nil {
		h.logger.Warn("Invalid Didit webhook signature", zap.Error(err))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	var payload entities.DiditWebhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		h.logger.Warn("Invalid Didit webhook JSON", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook payload"})
		return
	}

	enqueued, err := h.kycService.EnqueueDiditWebhook(ctx, &payload, rawBody)
	if err != nil {
		h.logger.Error("Failed to enqueue Didit webhook", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enqueue webhook"})
		return
	}
	if !enqueued {
		c.JSON(http.StatusOK, gin.H{"status": "duplicate_ignored"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}

// ResyncBridge re-triggers Bridge KYC sync for a user with missing tax ID.
// POST /admin/kyc/resync-bridge  {"user_id":"...","tax_id":"...","tax_id_type":"...","issuing_country":"..."}
func (h *Handler) ResyncBridge(c *gin.Context) {
	var req struct {
		UserID         string `json:"user_id" binding:"required"`
		TaxID          string `json:"tax_id" binding:"required"`
		TaxIDType      string `json:"tax_id_type" binding:"required"`
		IssuingCountry string `json:"issuing_country" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	if err := h.kycService.ResyncBridge(c.Request.Context(), userID, req.TaxID, req.TaxIDType, req.IssuingCountry); err != nil {
		h.logger.Error("ResyncBridge failed", "user_id", req.UserID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// RepairBridgeGovID fetches the Didit session decision and pushes the gov ID to Bridge.
// POST /admin/kyc/repair-bridge-govid  {"user_id":"..."}
func (h *Handler) RepairBridgeGovID(c *gin.Context) {
	var req struct {
		UserID string `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	if err := h.kycService.RepairBridgeGovID(c.Request.Context(), userID); err != nil {
		h.logger.Error("RepairBridgeGovID failed", "user_id", req.UserID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
