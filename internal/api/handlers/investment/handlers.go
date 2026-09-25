// Package investment exposes the Glider-backed investment engine over HTTP.
//
// These endpoints are the Agent API: the Python MIRIAM agent calls them through
// its Go client, and the mobile app calls the same endpoints for confirmations
// and step-up actions. Handlers stay thin: they resolve the caller, delegate to
// the deterministic service, and translate the service's verdicts into status
// codes. No business rule lives here.
package investment

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	investmentsvc "github.com/rail-service/rail_service/internal/domain/services/investment"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// Handlers serves the investment Agent API.
type Handlers struct {
	service *investmentsvc.Service
	log     *logger.Logger
}

// NewHandlers builds the investment handlers. It returns nil when the service is
// not configured so route registration can skip the feature entirely.
func NewHandlers(service *investmentsvc.Service, log *logger.Logger) *Handlers {
	if service == nil {
		return nil
	}
	return &Handlers{service: service, log: log}
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// GetLimits answers "what may this user do?" without attempting anything.
// GET /investments/limits
func (h *Handlers) GetLimits(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	response, err := h.service.GetLimitsResponse(c.Request.Context(), userID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, response)
}

// GetPortfolio returns balances, positions and enrollment summaries.
// GET /investments/portfolio
func (h *Handlers) GetPortfolio(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	summary, err := h.service.GetPortfolio(c.Request.Context(), userID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, summary)
}

// GetPositions returns the normalized holdings only.
// GET /investments/positions
func (h *Handlers) GetPositions(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	positions, err := h.service.GetPositions(c.Request.Context(), userID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"positions": positions})
}

// GetOwner returns the caller's Solana owner account id (CAIP-10) for
// user-signed enrollment. Read-only; an account without a Solana wallet gets
// an explicit error, never an invented address.
// GET /investments/owner
func (h *Handlers) GetOwner(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	account, err := h.service.GetOwnerAccount(c.Request.Context(), userID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"owner_account_id": account})
}

// ListAssets searches the supported asset catalog.
// GET /investments/assets
func (h *Handlers) ListAssets(c *gin.Context) {
	query := c.Query("query")
	limit := common.ParseIntParam(c, "limit", 25)
	assets, err := h.service.ListAssets(c.Request.Context(), query, limit)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"assets": assets})
}

// GetAsset resolves one asset by id, CAIP-19 or symbol.
// GET /investments/assets/:id
func (h *Handlers) GetAsset(c *gin.Context) {
	asset, err := h.service.GetAsset(c.Request.Context(), c.Param("id"), c.Query("caip19"), c.Query("symbol"))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, asset)
}

// ListStrategies returns the user's strategies.
// GET /investments/strategies
func (h *Handlers) ListStrategies(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategies, err := h.service.ListStrategies(c.Request.Context(), userID, c.Query("status"))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"strategies": strategies})
}

// ListRailStrategies returns the Rail-curated strategies (e.g. the Rail Stock
// Sleeve) every verified user may enroll into. These have no owning user, so
// they never appear in ListStrategies; without this endpoint a seeded Rail
// strategy would be enrollable by id but undiscoverable.
// GET /investments/strategies/rail
func (h *Handlers) ListRailStrategies(c *gin.Context) {
	strategies, err := h.service.ListRailStrategies(c.Request.Context())
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"strategies": strategies})
}

// GetStrategy returns a strategy with its version history.
// GET /investments/strategies/:id
func (h *Handlers) GetStrategy(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	strategy, versions, err := h.service.GetStrategy(c.Request.Context(), userID, strategyID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"strategy": strategy, "versions": versions})
}

// GetExecution returns one auditable action.
// GET /investments/executions/:id
func (h *Handlers) GetExecution(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	executionID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	execution, err := h.service.GetExecution(c.Request.Context(), userID, executionID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, execution)
}

// ListExecutions returns a user's auditable actions.
// GET /investments/executions
func (h *Handlers) ListExecutions(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	executions, err := h.service.ListExecutions(c.Request.Context(), userID, c.Query("status"), common.ParseIntParam(c, "limit", 25))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"executions": executions})
}

// ListAuditEvents returns the investment event trail.
// GET /investments/audit
func (h *Handlers) ListAuditEvents(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	events, err := h.service.ListAuditEvents(c.Request.Context(), userID, common.ParseIntParam(c, "limit", 50))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"events": events})
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

// ListInvestors returns mirrorable public strategies with provenance labels.
// GET /investments/investors
func (h *Handlers) ListInvestors(c *gin.Context) {
	response, err := h.service.ListInvestors(c.Request.Context(), c.Query("collection"), c.Query("cursor"), common.ParseIntParam(c, "limit", 25))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, response)
}

// GetInvestor returns one public strategy.
// GET /investments/investors/:id
func (h *Handlers) GetInvestor(c *gin.Context) {
	investor, err := h.service.GetInvestor(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, investor)
}

// GetInvestorActivity returns only activity we can actually verify.
// GET /investments/investors/:id/activity
func (h *Handlers) GetInvestorActivity(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	response, err := h.service.GetInvestorActivity(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, response)
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

// CreateStrategy validates and (after confirmation) creates a strategy.
// POST /investments/strategies
func (h *Handlers) CreateStrategy(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	req := &entities.InvestmentCreateStrategyRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	response, err := h.service.CreateStrategy(c.Request.Context(), userID, req, h.actor(c))
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// PublishStrategyVersion appends a new immutable allocation version.
// POST /investments/strategies/:id/versions
func (h *Handlers) PublishStrategyVersion(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	req := &entities.InvestmentUpdateStrategyRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	response, err := h.service.PublishStrategyVersion(c.Request.Context(), userID, strategyID, req, h.actor(c))
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// PreviewRebalance calculates drift and proposed trades without changing anything.
// GET /investments/strategies/:id/preview
func (h *Handlers) PreviewRebalance(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	var amount *decimal.Decimal
	if raw := strings.TrimSpace(c.Query("amount_usd")); raw != "" {
		parsed, err := decimal.NewFromString(raw)
		if err != nil {
			common.RespondBadRequest(c, "amount_usd must be a decimal string")
			return
		}
		amount = &parsed
	}
	preview, err := h.service.PreviewRebalance(c.Request.Context(), userID, strategyID, amount)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, preview)
}

// PauseStrategy stops provider automation for a portfolio.
// POST /investments/strategies/:id/pause
func (h *Handlers) PauseStrategy(c *gin.Context) {
	h.lifecycle(c, h.service.PauseStrategy)
}

// ResumeStrategy restarts provider automation.
// POST /investments/strategies/:id/resume
func (h *Handlers) ResumeStrategy(c *gin.Context) {
	h.lifecycle(c, h.service.ResumeStrategy)
}

// TriggerRebalance manually asks the provider to converge the portfolio.
// POST /investments/strategies/:id/rebalance
func (h *Handlers) TriggerRebalance(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	execution, err := h.service.TriggerRebalance(c.Request.Context(), userID, strategyID, "manual", h.actor(c))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, execution)
}

// Enroll enrolls the user into a strategy and funds the new portfolio.
// POST /investments/enroll
func (h *Handlers) Enroll(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	req := &entities.InvestmentEnrollRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	response, err := h.service.Enroll(c.Request.Context(), userID, req, h.actor(c))
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// EnrollPrepare runs Glider stage 1 for a user-held Solana wallet (Model B).
// It returns the base64 transaction to sign plus the confirmation the
// allocate card binds to. This is confirmation 1 of 2: the token staged here
// is not valid for complete. POST /investments/enroll/prepare
func (h *Handlers) EnrollPrepare(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	req := &investmentsvc.UserEnrollPrepareRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	response, err := h.service.PrepareUserEnrollment(c.Request.Context(), userID, req, h.actor(c))
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// Contribute adds USDC to a portfolio the user is already enrolled in.
// POST /investments/contributions
func (h *Handlers) Contribute(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	req := &investmentsvc.UserContributeRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	response, err := h.service.Contribute(c.Request.Context(), userID, req, h.actor(c))
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// EnrollComplete submits the wallet-signed transaction (stage 2, idempotent
// on flowId), persists the enrollment, starts automation, and funds when an
// amount was bound. This is confirmation 2 of 2: it needs its own token bound
// to flowId + amount + strategyId. POST /investments/enroll/complete
func (h *Handlers) EnrollComplete(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	req := &investmentsvc.UserEnrollCompleteRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	response, err := h.service.CompleteUserEnrollment(c.Request.Context(), userID, req, h.actor(c))
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// PlaceOrder changes one asset's target weight.
// POST /investments/orders
func (h *Handlers) PlaceOrder(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	req := &entities.InvestmentOrderRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	response, err := h.service.PlaceOrder(c.Request.Context(), userID, req, h.actor(c))
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// SetAllocation replaces the whole target allocation.
// POST /investments/allocations
func (h *Handlers) SetAllocation(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	req := &entities.InvestmentMultiOrderRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	response, err := h.service.PlaceMultiOrder(c.Request.Context(), userID, req, h.actor(c))
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// Withdraw takes money out of a portfolio.
//
// Money only leaves through an interactive session with a verified step-up; the
// route is registered behind RequireInteractiveSession, so an agent token can
// never reach here, and the service re-checks the policy verdict.
// POST /investments/withdrawals
func (h *Handlers) Withdraw(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	req := &entities.InvestmentWithdrawalRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.ConfirmationToken = h.confirmationToken(c, req.ConfirmationToken)

	// The middleware proves the session is interactive; the step-up proof is
	// asserted by the client after the app unlocks. Agent callers are rejected
	// upstream, so this flag is only true for a real in-app confirmation.
	stepUpVerified := !h.isAgent(c)

	response, err := h.service.Withdraw(c.Request.Context(), userID, req, stepUpVerified, entities.InvestmentActorUser)
	if err != nil {
		h.respond(c, err, response)
		return
	}
	h.respondAction(c, response.Status, response)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (h *Handlers) lifecycle(c *gin.Context, action func(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentEnrollment, error)) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	enrollment, err := action(c.Request.Context(), userID, strategyID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, enrollment)
}

func (h *Handlers) user(c *gin.Context) (uuid.UUID, bool) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return uuid.Nil, false
	}
	return userID, true
}

// actor labels who initiated an action. Agent tokens are the Python MIRIAM
// agent acting on the user's behalf; everything else is the user themselves.
func (h *Handlers) actor(c *gin.Context) entities.InvestmentActor {
	if h.isAgent(c) {
		return entities.InvestmentActorMiriam
	}
	return entities.InvestmentActorUser
}

func (h *Handlers) isAgent(c *gin.Context) bool {
	if value, exists := c.Get("is_agent"); exists {
		if isAgent, ok := value.(bool); ok {
			return isAgent
		}
	}
	return false
}

// confirmationToken reads the confirmation from the body or the dedicated
// header, so a caller can replay a confirmed action either way.
func (h *Handlers) confirmationToken(c *gin.Context, bodyValue string) string {
	if strings.TrimSpace(bodyValue) != "" {
		return bodyValue
	}
	return strings.TrimSpace(c.GetHeader(entities.InvestmentConfirmationHeader))
}

// respondAction maps the service's coarse action status onto a status code.
func (h *Handlers) respondAction(c *gin.Context, status entities.InvestmentActionStatus, payload any) {
	switch status {
	case entities.InvestmentActionAwaitingConfirmation:
		// 202: the action is valid but staged, and the caller must confirm the
		// exact payload before it will run.
		c.JSON(http.StatusAccepted, payload)
	case entities.InvestmentActionRejected:
		c.JSON(http.StatusUnprocessableEntity, payload)
	default:
		common.RespondSuccess(c, payload)
	}
}

// respond translates domain errors into HTTP responses. A payload is included
// whenever it carries the reason (policy verdict, validation report) so the
// agent can explain the outcome instead of inventing one.
func (h *Handlers) respond(c *gin.Context, err error, payload any) {
	switch {
	case errors.Is(err, investmentsvc.ErrDisabled):
		h.respondError(c, http.StatusServiceUnavailable, "INVESTMENT_DISABLED", "Investing is not enabled yet", payload)
	case errors.Is(err, investmentsvc.ErrNotFound):
		h.respondError(c, http.StatusNotFound, "INVESTMENT_NOT_FOUND", "Not found", payload)
	case errors.Is(err, investmentsvc.ErrValidationFailed):
		h.respondError(c, http.StatusUnprocessableEntity, "INVESTMENT_VALIDATION_FAILED", err.Error(), payload)
	case errors.Is(err, investmentsvc.ErrPolicyBlocked):
		h.respondError(c, http.StatusForbidden, "INVESTMENT_NOT_ALLOWED", err.Error(), payload)
	case errors.Is(err, investmentsvc.ErrStepUpRequired):
		h.respondError(c, http.StatusForbidden, "INVESTMENT_STEP_UP_REQUIRED", err.Error(), payload)
	case errors.Is(err, investmentsvc.ErrConfirmationInvalid):
		h.respondError(c, http.StatusBadRequest, "INVESTMENT_CONFIRMATION_INVALID", err.Error(), payload)
	case errors.Is(err, investmentsvc.ErrProviderCooldown):
		// The provider rate-limits rebalances; 429 is the honest answer so the
		// caller retries later instead of hammering the venue.
		h.respondError(c, http.StatusTooManyRequests, "INVESTMENT_PROVIDER_COOLDOWN", err.Error(), payload)
	case errors.Is(err, investmentsvc.ErrProviderConflict):
		h.respondError(c, http.StatusConflict, "INVESTMENT_PROVIDER_CONFLICT", err.Error(), payload)
	case errors.Is(err, investmentsvc.ErrUnsupported):
		h.respondError(c, http.StatusNotImplemented, "INVESTMENT_NOT_SUPPORTED", err.Error(), payload)
	default:
		if h.log != nil {
			h.log.Error("investment request failed", "error", err)
		}
		h.respondError(c, http.StatusInternalServerError, "INVESTMENT_ERROR", "Unable to complete the investment request", payload)
	}
}

func (h *Handlers) respondError(c *gin.Context, status int, code, message string, payload any) {
	if payload != nil {
		c.JSON(status, gin.H{"code": code, "message": message, "result": payload})
		return
	}
	common.RespondError(c, status, code, message, nil)
}
