package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/services/security"
)

type MFAHandlers struct {
	mfaService      *security.MFAService
	geoService      *security.GeoSecurityService
	incidentService *security.IncidentResponseService
	logger          *zap.Logger
}

func NewMFAHandlers(
	mfaService *security.MFAService,
	geoService *security.GeoSecurityService,
	incidentService *security.IncidentResponseService,
	logger *zap.Logger,
) *MFAHandlers {
	return &MFAHandlers{
		mfaService:      mfaService,
		geoService:      geoService,
		incidentService: incidentService,
		logger:          logger,
	}
}

// GetMFASettings returns user's MFA configuration
// @Summary Get MFA settings
// @Tags Security
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/security/mfa [get]
func (h *MFAHandlers) GetMFASettings(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	settings, err := h.mfaService.GetMFASettings(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get MFA settings", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"primary_method":    settings.PrimaryMethod,
		"fallback_method":   settings.FallbackMethod,
		"mfa_required":      settings.MFARequired,
		"totp_enabled":      settings.TOTPEnabled,
		"sms_enabled":       settings.PhoneNumber != "",
		"webauthn_enabled":  settings.WebAuthnEnabled,
		"backup_codes_left": settings.BackupCodesLeft,
		"grace_period_ends": settings.GracePeriodEnds,
	})
}

// SetupSMSMFA configures SMS as MFA method
// @Summary Setup SMS MFA
// @Tags Security
// @Security BearerAuth
// @Param request body object{phone_number=string} true "Phone number"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/security/mfa/sms [post]
func (h *MFAHandlers) SetupSMSMFA(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req struct {
		PhoneNumber string `json:"phone_number" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	if err := h.mfaService.SetupSMSMFA(c.Request.Context(), userID, req.PhoneNumber); err != nil {
		h.logger.Error("Failed to setup SMS MFA", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SETUP_FAILED"})
		return
	}

	// Send verification code
	if err := h.mfaService.SendSMSCode(c.Request.Context(), userID); err != nil {
		h.logger.Error("Failed to send SMS code", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "SMS MFA setup initiated. Verification code sent.",
	})
}

// SendMFACode sends an MFA verification code
// @Summary Send MFA code
// @Tags Security
// @Security BearerAuth
// @Param request body object{method=string} true "MFA method"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/security/mfa/send-code [post]
func (h *MFAHandlers) SendMFACode(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req struct {
		Method string `json:"method" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	switch req.Method {
	case "sms":
		if err := h.mfaService.SendSMSCode(c.Request.Context(), userID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "SEND_FAILED", "message": err.Error()})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_METHOD"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Code sent"})
}

// VerifyMFACode verifies an MFA code
// @Summary Verify MFA code
// @Tags Security
// @Security BearerAuth
// @Param request body object{code=string,method=string} true "Verification code"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/security/mfa/verify [post]
func (h *MFAHandlers) VerifyMFACode(c *gin.Context) {
	userID, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req struct {
		Code   string `json:"code" binding:"required"`
		Method string `json:"method" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	result, err := h.mfaService.VerifyAny(c.Request.Context(), userID, req.Code, security.MFAMethod(req.Method))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "VERIFICATION_FAILED"})
		return
	}

	if !result.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_CODE"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"verified": true})
}

// GetBlockedCountries returns list of blocked countries
// @Summary Get blocked countries
// @Tags Security
// @Security BearerAuth
// @Success 200 {array} map[string]string
// @Router /api/v1/admin/security/blocked-countries [get]
func (h *MFAHandlers) GetBlockedCountries(c *gin.Context) {
	countries, err := h.geoService.GetBlockedCountries(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"countries": countries})
}

// BlockCountry adds a country to the blocklist
// @Summary Block country
// @Tags Security
// @Security BearerAuth
// @Param request body object{country_code=string,country_name=string,reason=string} true "Country to block"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/admin/security/blocked-countries [post]
func (h *MFAHandlers) BlockCountry(c *gin.Context) {
	var req struct {
		CountryCode string `json:"country_code" binding:"required,len=2"`
		CountryName string `json:"country_name" binding:"required"`
		Reason      string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	blockedBy := c.GetString("user_email")
	if err := h.geoService.BlockCountry(c.Request.Context(), req.CountryCode, req.CountryName, req.Reason, blockedBy); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "BLOCK_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Country blocked"})
}

// UnblockCountry removes a country from the blocklist
// @Summary Unblock country
// @Tags Security
// @Security BearerAuth
// @Param country_code path string true "Country code"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/admin/security/blocked-countries/{country_code} [delete]
func (h *MFAHandlers) UnblockCountry(c *gin.Context) {
	countryCode := c.Param("country_code")
	if len(countryCode) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_COUNTRY_CODE"})
		return
	}

	if err := h.geoService.UnblockCountry(c.Request.Context(), countryCode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "UNBLOCK_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Country unblocked"})
}

// GetSecurityDashboard returns security metrics
// @Summary Get security dashboard
// @Tags Security
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/admin/security/dashboard [get]
func (h *MFAHandlers) GetSecurityDashboard(c *gin.Context) {
	dashboard, err := h.incidentService.GetSecurityDashboard(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, dashboard)
}

// GetOpenIncidents returns open security incidents
// @Summary Get open incidents
// @Tags Security
// @Security BearerAuth
// @Success 200 {array} security.SecurityIncident
// @Router /api/v1/admin/security/incidents [get]
func (h *MFAHandlers) GetOpenIncidents(c *gin.Context) {
	incidents, err := h.incidentService.GetOpenIncidents(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"incidents": incidents})
}

// GetIncident returns a specific incident
// @Summary Get incident details
// @Tags Security
// @Security BearerAuth
// @Param id path string true "Incident ID"
// @Success 200 {object} security.SecurityIncident
// @Router /api/v1/admin/security/incidents/{id} [get]
func (h *MFAHandlers) GetIncident(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID"})
		return
	}

	incident, err := h.incidentService.GetIncident(c.Request.Context(), incidentID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND"})
		return
	}

	c.JSON(http.StatusOK, incident)
}

// UpdateIncidentStatus updates incident status
// @Summary Update incident status
// @Tags Security
// @Security BearerAuth
// @Param id path string true "Incident ID"
// @Param request body object{status=string,notes=string} true "Status update"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/admin/security/incidents/{id}/status [put]
func (h *MFAHandlers) UpdateIncidentStatus(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID"})
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"`
		Notes  string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}

	if err := h.incidentService.UpdateIncidentStatus(c.Request.Context(), incidentID, security.IncidentStatus(req.Status), req.Notes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "UPDATE_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Status updated"})
}

// ExecutePlaybook runs automated response for an incident
// @Summary Execute incident playbook
// @Tags Security
// @Security BearerAuth
// @Param id path string true "Incident ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/admin/security/incidents/{id}/playbook [post]
func (h *MFAHandlers) ExecutePlaybook(c *gin.Context) {
	incidentID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID"})
		return
	}

	if err := h.incidentService.ExecutePlaybook(c.Request.Context(), incidentID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "PLAYBOOK_FAILED", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Playbook executed"})
}

// GetGeoInfo returns geolocation info for current request
// @Summary Get geo info
// @Tags Security
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/security/geo-info [get]
func (h *MFAHandlers) GetGeoInfo(c *gin.Context) {
	clientIP := c.ClientIP()
	
	var userID uuid.UUID
	if userIDStr := c.GetString("user_id"); userIDStr != "" {
		userID, _ = uuid.Parse(userIDStr)
	}

	result, err := h.geoService.CheckIP(c.Request.Context(), userID, clientIP)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "GEO_CHECK_FAILED"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ip_address":   clientIP,
		"country_code": result.Location.CountryCode,
		"region":       result.Location.Region,
		"city":         result.Location.City,
		"is_vpn":       result.Location.IsVPN,
		"is_proxy":     result.Location.IsProxy,
		"risk_score":   result.RiskScore,
		"risk_factors": result.RiskFactors,
	})
}
