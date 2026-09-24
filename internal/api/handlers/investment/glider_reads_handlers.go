package investment

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ---------------------------------------------------------------------------
// Real Glider reads + metadata (all provider-backed, no local estimates).
// ---------------------------------------------------------------------------

// GetProviderStrategyVersions returns live version history, newest first.
// GET /investments/strategies/:id/provider-versions
func (h *Handlers) GetProviderStrategyVersions(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	versions, next, err := h.service.GetProviderStrategyVersions(c.Request.Context(), userID, strategyID, c.Query("cursor"), common.ParseIntParam(c, "limit", 50))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"versions": versions, "next_cursor": next})
}

// GetStrategyPerformance returns the provider TWR template curve.
// GET /investments/strategies/:id/performance
func (h *Handlers) GetStrategyPerformance(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	perf, err := h.service.GetStrategyPerformance(c.Request.Context(), userID, strategyID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, perf)
}

// GetStrategySchedule returns the configured rebalance cadence.
// GET /investments/strategies/:id/schedule
func (h *Handlers) GetStrategySchedule(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	sched, err := h.service.GetStrategySchedule(c.Request.Context(), userID, strategyID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	if sched == nil {
		common.RespondSuccess(c, gin.H{"schedule": nil, "note": "no cadence on record"})
		return
	}
	common.RespondSuccess(c, gin.H{"schedule": sched})
}

// GetStrategyPreferences returns stored swap overrides.
// GET /investments/strategies/:id/preferences
func (h *Handlers) GetStrategyPreferences(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	prefs, err := h.service.GetStrategyPreferences(c.Request.Context(), userID, strategyID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, prefs)
}

// GetStrategyFees returns the per-strategy fee override.
// GET /investments/strategies/:id/fees
func (h *Handlers) GetStrategyFees(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	fees, err := h.service.GetStrategyFees(c.Request.Context(), userID, strategyID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, fees)
}

// PatchStrategyMetadata patches display fields only.
// PATCH /investments/strategies/:id
func (h *Handlers) PatchStrategyMetadata(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	strategyID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	var patch entities.GliderStrategyPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	updated, err := h.service.PatchStrategyMetadata(c.Request.Context(), userID, strategyID, patch)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, updated)
}

// GetEnrollmentPerformance returns the daily money curve for one enrollment.
// GET /investments/enrollments/:id/performance?returnMethod=MWR|TWR
func (h *Handlers) GetEnrollmentPerformance(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	enrollmentID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	perf, err := h.service.GetEnrollmentPerformance(c.Request.Context(), userID, enrollmentID, c.Query("returnMethod"))
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, perf)
}

// GetEnrollmentSectorExposure returns equity exposure by canonical sector.
// GET /investments/enrollments/:id/sector-exposure
func (h *Handlers) GetEnrollmentSectorExposure(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	enrollmentID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	exposure, err := h.service.GetEnrollmentSectorExposure(c.Request.Context(), userID, enrollmentID)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, exposure)
}

// GetAllocationBreakdown aggregates holdings into Glider buckets.
// POST /investments/breakdown
func (h *Handlers) GetAllocationBreakdown(c *gin.Context) {
	var in entities.GliderBreakdownInput
	if err := c.ShouldBindJSON(&in); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	out, err := h.service.GetAllocationBreakdown(c.Request.Context(), in)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	c.Data(http.StatusOK, "application/json", out)
}

// RenameEnrollment renames a portfolio display name.
// PATCH /investments/enrollments/:id
func (h *Handlers) RenameEnrollment(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	enrollmentID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	var body struct {
		PortfolioName string `json:"portfolioName"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	patched, err := h.service.RenameEnrollment(c.Request.Context(), userID, enrollmentID, body.PortfolioName)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, patched)
}

// PrepareChainActivation returns the owner-signable message for new chains.
// POST /investments/enrollments/:id/chains/signature (EVM only on Glider).
func (h *Handlers) PrepareChainActivation(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		return
	}
	enrollmentID, ok := common.ParsePathUUID(c, "id")
	if !ok {
		return
	}
	var body entities.GliderChainActivationInput
	if err := c.ShouldBindJSON(&body); err != nil {
		common.RespondBadRequest(c, "Invalid request: "+err.Error())
		return
	}
	msg, err := h.service.PrepareChainActivation(c.Request.Context(), userID, enrollmentID, body.ChainIDs)
	if err != nil {
		h.respond(c, err, nil)
		return
	}
	common.RespondSuccess(c, gin.H{"message": msg})
}
