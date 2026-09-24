package confirmation

import (
	"context"
	"fmt"
	"strings"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	svc "github.com/rail-service/rail_service/internal/domain/services/confirmation"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
)

// CardSender delivers the live card after staging. Implemented by the bridge
// dispatcher (SendConfirmationCard). Nil sender = create without delivery
// (caller sends the card itself); creation never moves money either way.
type CardSender func(ctx context.Context, userID uuid.UUID, threadID string, card *platform.ConfirmationCardPayload) error

type Handler struct {
	svc    *svc.Service
	send   CardSender
	logger *zap.Logger
}

func NewHandler(service *svc.Service, send CardSender, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{svc: service, send: send, logger: logger}
}

type createRequest struct {
	Action    string         `json:"action" binding:"required"`
	Payload   map[string]any `json:"payload"`
	Title     string         `json:"title"`
	Subtitle  string         `json:"subtitle"`
	ThreadID  string         `json:"thread_id"`
	TTLSecond int            `json:"ttl_seconds"`
}

func cardPayload(c *entities.Confirmation, url string) *platform.ConfirmationCardPayload {
	expiresIn := ""
	if remain := time.Until(c.ExpiresAt); remain > 0 {
		expiresIn = fmt.Sprintf("%d:%02d", int(remain.Minutes()), int(remain.Seconds())%60)
	}
	layout := entities.PreviewLayoutFor(c, c.State, expiresIn)
	return &platform.ConfirmationCardPayload{
		ActionID: c.ID.String(), Action: string(c.Action), State: string(c.State),
		ConfirmURL: url, Title: c.Title, Subtitle: c.Subtitle,
		Amount: c.Amount, Asset: c.Asset, Destination: c.Destination,
		Fee: c.Fee, RiskLine: c.RiskLine,
		ExpiresAt:  c.ExpiresAt.UTC().Format(time.RFC3339),
		Caption:    layout.Caption,
		Subcaption: layout.Subcaption,
		Image:      layout.Image,
		Summary:    layout.Summary,
	}
}

// Create stages a confirmation (JWT) and sends one live card. It never moves
// money — Face ID success + server accept does.
func (h *Handler) Create(c *gin.Context) {
	uid, ok := userIDOf(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	action := entities.ConfirmationAction(req.Action)
	if !entities.ValidConfirmationAction(action) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown action %q", req.Action)})
		return
	}
	in := svc.CreateInput{
		UserID: uid, Action: action, Payload: req.Payload,
		Title: req.Title, Subtitle: req.Subtitle,
	}
	// Trust boundary: a JWT-authed app caller must never mint a Miriam
	// challenge binding. miriam_confirm_id routes the card through the
	// Miriam settle executor (RouteByMiriamConfirm); it is set server-side
	// for Miriam-originated cards only. Strip it here so app cards always
	// take their direct executor.
	if in.Payload != nil {
		delete(in.Payload, svc.MiriamConfirmPayloadKey)
	}
	if req.TTLSecond > 0 {
		in.TTL = time.Duration(req.TTLSecond) * time.Second
	}
	if req.ThreadID != "" {
		if in.Payload == nil {
			in.Payload = map[string]any{}
		}
		in.Payload["thread_id"] = req.ThreadID
	}
	rec, url, err := h.svc.Create(c.Request.Context(), in)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	card := cardPayload(rec, url)
	if h.send != nil && req.ThreadID != "" {
		if err := h.send(c.Request.Context(), uid, req.ThreadID, card); err != nil {
			h.logger.Warn("confirmation card send failed (record staged, no card in transcript)",
				zap.String("action_id", rec.ID.String()), zap.Error(err))
			c.JSON(http.StatusCreated, gin.H{
				"action_id": rec.ID.String(), "confirm_url": url, "card": card,
				"card_delivered": false, "card_error": err.Error(),
			})
			return
		}
	}
	c.JSON(http.StatusCreated, gin.H{
		"action_id": rec.ID.String(), "confirm_url": url, "card": card,
		"card_delivered": h.send != nil && req.ThreadID != "",
	})
}

// Fetch serves the extension (token-gated, no session): parses url →
// actionId + token, returns the payload or the dead state.
func (h *Handler) Fetch(c *gin.Context) {
	id, ok := uuidOf(c, "id")
	if !ok {
		return
	}
	token := c.Query("t")
	rec, err := h.svc.Fetch(c.Request.Context(), id, token)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "confirmation not found or link invalid"})
		return
	}
	url := reissueURL(rec)
	c.JSON(http.StatusOK, gin.H{"confirmation": publicView(rec), "card": cardPayload(rec, url)})
}

type decisionRequest struct {
	Token           string `json:"t" binding:"required"`
	Biometric       string `json:"biometric"`        // pass|fail|cancel
	DeviceAssertion string `json:"device_assertion"` // opaque, logged only
	// Secure Enclave proof (real extension only). KeyID + Signature over
	// actionId.expiry; EnrollKey is a base64 SPKI for trust-on-first-use
	// enrollment on the first approval from a new device.
	DeviceKeyID string `json:"device_key_id"`
	Signature   string `json:"signature"`
	EnrollKey   string `json:"enroll_device_key"`
}

// Approve runs Face ID success + server accept (token-gated, single-use).
func (h *Handler) Approve(c *gin.Context) {
	id, ok := uuidOf(c, "id")
	if !ok {
		return
	}
	var req decisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Peek the owner from the record: the token is the credential.
	rec, err := h.svc.Fetch(c.Request.Context(), id, req.Token)
	if err != nil || rec == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "confirmation not found or link invalid"})
		return
	}
	out, err := h.svc.ApproveWithDevice(c.Request.Context(), rec.UserID, id, req.Token, req.Biometric, svc.DeviceApproval{
		KeyID:     req.DeviceKeyID,
		Signature: req.Signature,
		EnrollKey: req.EnrollKey,
	})
	if err != nil {
		// Terminal replay is a no-op success; real errors surface failed state.
		if latest, lerr := h.svc.Fetch(c.Request.Context(), id, req.Token); lerr == nil && latest != nil && latest.IsTerminal() {
			c.JSON(http.StatusOK, gin.H{"confirmation": publicView(latest), "card": cardPayload(latest, reissueURL(latest))})
			return
		}
		h.logger.Warn("confirmation approve failed", zap.String("action_id", id.String()), zap.Error(err))
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"confirmation": publicView(out), "card": cardPayload(out, reissueURL(out))})
}

// Reject records a Face ID cancel (token-gated, single-use).
func (h *Handler) Reject(c *gin.Context) {
	id, ok := uuidOf(c, "id")
	if !ok {
		return
	}
	var req decisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rec, err := h.svc.Fetch(c.Request.Context(), id, req.Token)
	if err != nil || rec == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "confirmation not found or link invalid"})
		return
	}
	out, err := h.svc.Reject(c.Request.Context(), rec.UserID, id, req.Token, req.Biometric)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"confirmation": publicView(out), "card": cardPayload(out, reissueURL(out))})
}

type markRequest struct {
	State  string `json:"state" binding:"required"`
	Result string `json:"result"`
}


type otpSendRequest struct {
	Token string `json:"t"`
}

// SendOTP emails a 6-digit code for a pending confirmation (token-gated).
// Hackathon/demo path gated by CONFIRMATION_DEMO_EMAIL_OTP.
func (h *Handler) SendOTP(c *gin.Context) {
	id, ok := uuidOf(c, "id")
	if !ok {
		return
	}
	token := c.Query("t")
	var req otpSendRequest
	_ = c.ShouldBindJSON(&req)
	if token == "" {
		token = req.Token
	}
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token t required"})
		return
	}
	out, err := h.svc.SendEmailOTP(c.Request.Context(), id, token)
	if err != nil {
		h.logger.Warn("confirmation otp send failed", zap.String("action_id", id.String()), zap.Error(err))
		status := http.StatusUnprocessableEntity
		msg := err.Error()
		switch {
		case strings.Contains(msg, "disabled"):
			status = http.StatusForbidden
		case strings.Contains(msg, "not found"), strings.Contains(msg, "invalid"), strings.Contains(msg, "expired") && strings.Contains(msg, "token"):
			status = http.StatusNotFound
		case strings.Contains(msg, "cooldown"):
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"masked_email": out.MaskedEmail,
		"expires_in_seconds": int(out.ExpiresIn.Seconds()),
		"cooldown_seconds":   int(out.Cooldown.Seconds()),
		"message":            "OTP sent if the confirmation is valid",
	})
}

type otpApproveRequest struct {
	Token string `json:"t" binding:"required"`
	Code  string `json:"code" binding:"required"`
}

// ApproveOTP verifies the emailed code and settles the confirmation
// (assurance=email_otp). Token-gated, single-use. Face ID Approve stays
// registered but is rejected while CONFIRMATION_DEMO_EMAIL_OTP=true.
func (h *Handler) ApproveOTP(c *gin.Context) {
	id, ok := uuidOf(c, "id")
	if !ok {
		return
	}
	var req otpApproveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rec, err := h.svc.Fetch(c.Request.Context(), id, req.Token)
	if err != nil || rec == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "confirmation not found or link invalid"})
		return
	}
	out, err := h.svc.ApproveWithEmailOTP(c.Request.Context(), rec.UserID, id, req.Token, req.Code)
	if err != nil {
		if latest, lerr := h.svc.Fetch(c.Request.Context(), id, req.Token); lerr == nil && latest != nil && latest.IsTerminal() {
			c.JSON(http.StatusOK, gin.H{"confirmation": publicView(latest), "card": cardPayload(latest, reissueURL(latest))})
			return
		}
		h.logger.Warn("confirmation otp approve failed", zap.String("action_id", id.String()), zap.Error(err))
		status := http.StatusUnprocessableEntity
		msg := err.Error()
		if strings.Contains(msg, "disabled") {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{"confirmation": publicView(out), "card": cardPayload(out, reissueURL(out))})
}

// Mark applies a terminal state reported by Miriam over the shared secret
// (X-Rail-Service-Key, enforced by middleware at the route): the chat tap won
// the race, so the live card must show the same ending. Legal edges apply and
// trigger the card edit; an already-terminal card returns its current state
// as a 200 no-op, never an error.
func (h *Handler) Mark(c *gin.Context) {
	id, ok := uuidOf(c, "id")
	if !ok {
		return
	}
	var req markRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	to := entities.ConfirmationState(req.State)
	switch to {
	case entities.ConfirmationCompleted,
		entities.ConfirmationRejected,
		entities.ConfirmationFailed,
		entities.ConfirmationExpired:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("mark needs a terminal state, got %q", req.State)})
		return
	}
	out, err := h.svc.MarkExternal(c.Request.Context(), id, to, req.Result)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "confirmation not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"confirmation": publicView(out), "card": cardPayload(out, reissueURL(out))})
}

func publicView(c *entities.Confirmation) gin.H {
	dead := c.IsTerminal()
	return gin.H{
		"action_id": c.ID.String(), "action": string(c.Action), "state": string(c.State),
		"title": c.Title, "subtitle": c.Subtitle, "amount": c.Amount, "asset": c.Asset,
		"destination": c.Destination, "fee": c.Fee, "risk_line": c.RiskLine,
		"expires_at": c.ExpiresAt.UTC().Format(time.RFC3339),
		"result":     c.ResultSummary, "dead": dead,
		"live": !dead, "assurance": c.Assurance,
		"enrolled_key_id": c.EnrolledKeyID,
	}
}

func reissueURL(rec *entities.Confirmation) string {
	// The extension already holds its signed URL; echo a relative dead-state
	// marker so responses never mint fresh tokens.
	if rec == nil {
		return ""
	}
	return "/confirm/" + rec.ID.String()
}

func userIDOf(c *gin.Context) (uuid.UUID, bool) {
	v, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, false
	}
	uid, ok := v.(uuid.UUID)
	return uid, ok && uid != uuid.Nil
}

func uuidOf(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return uuid.Nil, false
	}
	return id, true
}
