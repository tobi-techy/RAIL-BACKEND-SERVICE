// Package vault exposes the Premium Global Dollar Retirement Vault over HTTP.
//
// Handlers stay thin: resolve the caller, delegate to the deterministic vault
// service, translate verdicts into status codes. Every string a user can see
// here is plain money language — no chain, token, wallet or provider word ever
// reaches the client.
package vault

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	vaultsvc "github.com/rail-service/rail_service/internal/domain/services/vault"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Handlers serves the retirement vault API.
type Handlers struct {
	service *vaultsvc.Service
	log     *logger.Logger
}

// NewHandlers builds the vault handlers, or nil when the vault is not configured.
func NewHandlers(service *vaultsvc.Service, log *logger.Logger) *Handlers {
	if service == nil {
		return nil
	}
	return &Handlers{service: service, log: log}
}

// GetVault returns the plan: principal, growth, unlock date and the fee that
// applies if the user dips into growth today.
// GET /vault
func (h *Handlers) GetVault(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	view, err := h.service.GetView(c.Request.Context(), userID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, view)
}

// CreateVault opens a retirement plan and enrolls the user.
// POST /vault
func (h *Handlers) CreateVault(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	var req entities.VaultCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondError(c, http.StatusBadRequest, "VAULT_INVALID_REQUEST", "We couldn't read that request.", nil)
		return
	}
	response, err := h.service.CreateVault(c.Request.Context(), userID, &req)
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// UpdateVault changes the automatic-saving rule or the retirement age.
// PATCH /vault
func (h *Handlers) UpdateVault(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	var req entities.VaultUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondError(c, http.StatusBadRequest, "VAULT_INVALID_REQUEST", "We couldn't read that request.", nil)
		return
	}
	view, err := h.service.UpdateVault(c.Request.Context(), userID, &req)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, view)
}

// ListStrategies returns the plans a user can choose from. Only tiers the
// bootstrap resolved to real strategies are listed; unconfigured tiers are
// absent, never offered-then-refused.
// GET /vault/strategies
func (h *Handlers) ListStrategies(c *gin.Context) {
	options, err := h.service.ListStrategyOptions(c.Request.Context())
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"plans": options})
}

// Activity returns the contribution history.
// GET /vault/activity
func (h *Handlers) Activity(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	limit := common.ParseIntParam(c, "limit", 50)
	entries, err := h.service.Activity(c.Request.Context(), userID, limit)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"activity": entries})
}

// PreviewWithdrawal shows the split and the fee before anything moves.
// POST /vault/withdraw/preview
func (h *Handlers) PreviewWithdrawal(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	var req entities.VaultWithdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondError(c, http.StatusBadRequest, "VAULT_INVALID_REQUEST", "We couldn't read that request.", nil)
		return
	}
	plan, err := h.service.PreviewWithdrawal(c.Request.Context(), userID, req.AmountUSD)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, plan)
}

// Withdraw takes money out of the plan. The handler proves the session is
// interactive; a messaging agent can never trigger this.
// POST /vault/withdraw
func (h *Handlers) Withdraw(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	var req entities.VaultWithdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.RespondError(c, http.StatusBadRequest, "VAULT_INVALID_REQUEST", "We couldn't read that request.", nil)
		return
	}
	stepUpVerified := !h.isAgent(c)
	result, err := h.service.Withdraw(c.Request.Context(), userID, &req, stepUpVerified)
	if err != nil {
		h.respond(c, err, result)
		return
	}
	common.RespondSuccess(c, result)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (h *Handlers) user(c *gin.Context) (uuid.UUID, bool) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return uuid.Nil, false
	}
	return userID, true
}

func (h *Handlers) isAgent(c *gin.Context) bool {
	if value, exists := c.Get("is_agent"); exists {
		if isAgent, ok := value.(bool); ok {
			return isAgent
		}
	}
	return false
}

func (h *Handlers) respondAction(c *gin.Context, status entities.InvestmentActionStatus, payload any) {
	switch status {
	case entities.InvestmentActionAwaitingConfirmation:
		c.JSON(http.StatusAccepted, payload)
	case entities.InvestmentActionRejected:
		c.JSON(http.StatusUnprocessableEntity, payload)
	default:
		common.RespondSuccess(c, payload)
	}
}

// respond maps vault errors onto HTTP answers, with the human reason attached.
func (h *Handlers) respond(c *gin.Context, err error, payload any) {
	switch {
	case errors.Is(err, vaultsvc.ErrDisabled):
		h.respondError(c, http.StatusServiceUnavailable, "VAULT_DISABLED", "Retirement plans aren't available yet.", payload)
	case errors.Is(err, vaultsvc.ErrNotFound):
		h.respondError(c, http.StatusNotFound, "VAULT_NOT_FOUND", "You don't have a retirement plan yet.", payload)
	case errors.Is(err, vaultsvc.ErrAlreadyExists):
		h.respondError(c, http.StatusConflict, "VAULT_ALREADY_EXISTS", "You already have a retirement plan.", payload)
	case errors.Is(err, vaultsvc.ErrStrategyUnavailable):
		h.respondError(c, http.StatusServiceUnavailable, "VAULT_PLAN_UNAVAILABLE", "That retirement plan isn't available yet.", payload)
	case errors.Is(err, vaultsvc.ErrStepUpRequired):
		h.respondError(c, http.StatusForbidden, "VAULT_STEP_UP_REQUIRED", "Confirm in the app to take money out.", payload)
	case errors.Is(err, vaultsvc.ErrAuthorizationInvalid):
		h.respondError(c, http.StatusForbidden, "VAULT_NOT_AUTHORIZED", "We couldn't authorise that withdrawal from your plan.", payload)
	case errors.Is(err, vaultsvc.ErrValidation):
		h.respondError(c, http.StatusUnprocessableEntity, "VAULT_VALIDATION_FAILED", humanMessage(err), payload)
	case errors.Is(err, vaultsvc.ErrUnlockDateUnavailable):
		h.respondError(c, http.StatusUnprocessableEntity, "VAULT_UNLOCK_UNKNOWN",
			"We need your date of birth to confirm when your plan unlocks.", payload)
	case errors.Is(err, vaultsvc.ErrInsufficientValue):
		h.respondError(c, http.StatusUnprocessableEntity, "VAULT_INSUFFICIENT_VALUE",
			"That's more than your plan is worth right now.", payload)
	case errors.Is(err, vaultsvc.ErrInvalidAmount):
		h.respondError(c, http.StatusUnprocessableEntity, "VAULT_INVALID_AMOUNT",
			"Enter an amount greater than zero.", payload)
	case errors.Is(err, vaultsvc.ErrLotBasisMismatch), errors.Is(err, vaultsvc.ErrInvalidPolicy):
		h.respondError(c, http.StatusUnprocessableEntity, "VAULT_UNAVAILABLE",
			"We couldn't reconcile your plan, so nothing was withdrawn.", payload)
	default:
		if h.log != nil {
			h.log.Error("retirement vault request failed", "error", err)
		}
		h.respondError(c, http.StatusInternalServerError, "VAULT_ERROR", "We couldn't complete that.", payload)
	}
}

func (h *Handlers) respondError(c *gin.Context, status int, code, message string, payload any) {
	if payload != nil {
		c.JSON(status, gin.H{"code": code, "message": message, "result": payload})
		return
	}
	common.RespondError(c, status, code, message, nil)
}

// humanMessage strips the internal error prefix so the user sees a sentence,
// not a Go error string.
func humanMessage(err error) string {
	message := err.Error()
	if idx := lastColonSpace(message); idx >= 0 {
		return message[idx+2:]
	}
	return "We couldn't complete that request."
}

func lastColonSpace(value string) int {
	for i := len(value) - 2; i >= 0; i-- {
		if value[i] == ':' && value[i+1] == ' ' {
			return i
		}
	}
	return -1
}
