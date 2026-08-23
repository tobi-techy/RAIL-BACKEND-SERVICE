package platform

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

type PlatformHandler struct {
	linkingService *platform.LinkingService
	// bridgeAddress is the platform address users text the link code to (e.g. the
	// bridge's iMessage handle). Empty if not configured.
	bridgeAddress string
	logger        *zap.Logger
}

func NewPlatformHandlerWithLogger(ls *platform.LinkingService, bridgeAddress string, logger *zap.Logger) *PlatformHandler {
	return &PlatformHandler{linkingService: ls, bridgeAddress: bridgeAddress, logger: logger}
}

type initiateLinkRequest struct {
	Platform string `json:"platform" binding:"required"`
}

type initiateLinkResponse struct {
	// DeepLink opens a pre-filled message to the bridge address containing the
	// token. Empty when no bridge address is configured — the client should then
	// instruct the user to text the token manually.
	DeepLink         string `json:"deep_link,omitempty"`
	BridgeAddress    string `json:"bridge_address,omitempty"`
	Token            string `json:"token"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

func (h *PlatformHandler) InitiateLink(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req initiateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	platformType := entities.Platform(req.Platform)
	if platformType != entities.PlatformIMessage &&
		platformType != entities.PlatformWhatsApp &&
		platformType != entities.PlatformTelegram {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported platform"})
		return
	}

	result, err := h.linkingService.InitiateHandshake(c.Request.Context(), userID.(uuid.UUID), platformType)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	// Deep link opens a message TO the bridge address with the token pre-filled;
	// the user simply hits send and the bridge captures their real sender id.
	// Platform-aware: iMessage/sms, WhatsApp wa.me, Telegram share (MVP: iMessage primary).
	var deepLink string
	if h.bridgeAddress != "" {
		body := url.QueryEscape(result.Token)
		switch platformType {
		case entities.PlatformWhatsApp:
			// bridgeAddress should be E.164 without + for wa.me
			deepLink = "https://wa.me/" + url.PathEscape(h.bridgeAddress) + "?text=" + body
		case entities.PlatformTelegram:
			// bridgeAddress as @bot username without @
			deepLink = "https://t.me/" + url.PathEscape(h.bridgeAddress) + "?start=" + body
		default:
			deepLink = "sms:" + url.QueryEscape(h.bridgeAddress) + "&body=" + body
		}
	}

	c.JSON(http.StatusOK, initiateLinkResponse{
		DeepLink:         deepLink,
		BridgeAddress:    h.bridgeAddress,
		Token:            result.Token,
		ExpiresInSeconds: h.linkingService.TokenTTLSeconds(),
	})
}

var ErrPlatformNotFound = errors.New("platform identity not found")

func (h *PlatformHandler) Unlink(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	platformType := entities.Platform(c.Param("platform"))
	if platformType != entities.PlatformIMessage &&
		platformType != entities.PlatformWhatsApp &&
		platformType != entities.PlatformTelegram {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported platform"})
		return
	}

	if err := h.linkingService.Unlink(c.Request.Context(), userID.(uuid.UUID), platformType); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "platform not linked"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "unlinked"})
}

func (h *PlatformHandler) ListLinked(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	identities, err := h.linkingService.GetLinkedIdentities(c.Request.Context(), userID.(uuid.UUID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, identities)
}

// HandleInbound receives an inbound message from the bridge via HTTP POST
// and feeds it to the processor. HMAC-authenticated via middleware.
func (h *PlatformHandler) HandleInbound(processor *platform.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		if processor == nil {
			if h.logger != nil {
				h.logger.Error("platform processor not configured for inbound handler")
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "processor not configured"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 5*1024*1024))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
			return
		}
		if err := processor.Process(c.Request.Context(), body); err != nil {
			if h.logger != nil {
				h.logger.Warn("platform inbound processing failed", zap.Error(err))
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// HandleAction receives an action postback (confirm/cancel) from the bridge
// via HTTP POST and feeds it to the processor. HMAC-authenticated via middleware.
func (h *PlatformHandler) HandleAction(processor *platform.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		if processor == nil {
			if h.logger != nil {
				h.logger.Error("platform processor not configured for action handler")
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "processor not configured"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1024*1024))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
			return
		}
		if err := processor.ProcessAction(c.Request.Context(), body); err != nil {
			if h.logger != nil {
				h.logger.Warn("platform action processing failed", zap.Error(err))
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}
