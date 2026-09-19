package middleware

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// UserEntityReader exposes the minimum user lookup needed for capability checks.
type UserEntityReader interface {
	GetUserEntityByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
}

func extractUserID(c *gin.Context) (uuid.UUID, error) {
	userIDValue, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, fmt.Errorf("user_id not found in context")
	}

	switch v := userIDValue.(type) {
	case uuid.UUID:
		return v, nil
	case string:
		return uuid.Parse(v)
	default:
		return uuid.Nil, fmt.Errorf("unsupported user_id type %T", userIDValue)
	}
}

// RequireCryptoCapability allows crypto transfers for both KYC'd users and non-KYC
// users with Circle wallets. Non-KYC users get limited transfer amounts (enforced
// by the limits service, not this middleware).
func RequireCryptoCapability(userReader UserEntityReader, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := extractUserID(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": "UNAUTHORIZED", "message": "Authentication required"})
			return
		}

		user, err := userReader.GetUserEntityByID(c.Request.Context(), userID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": "USER_LOOKUP_ERROR", "message": "Unable to verify account"})
			return
		}

		tier := entities.EffectiveKYCTier(user.KYCTier, user.KYCStatus)
		if tier == entities.KYCTierUnverified {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "ACCOUNT_SETUP_REQUIRED",
				"message": "Complete account setup to access crypto transfers",
			})
			return
		}

		// Set tier in context for downstream handlers/limits
		c.Set("kyc_tier", string(tier))
		c.Next()
	}
}

// RequireBridgeCapability enforces Bridge KYC eligibility for Bridge-dependent features.
func RequireBridgeCapability(userReader UserEntityReader, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := extractUserID(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			})
			return
		}

		user, err := userReader.GetUserEntityByID(c.Request.Context(), userID)
		if err != nil {
			log.Error("Failed to load user for Bridge capability check",
				zap.Error(err),
				zap.String("user_id", userID.String()),
				zap.String("request_id", c.GetString("request_id")))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    "KYC_STATUS_ERROR",
				"message": "Unable to verify Bridge capability at this time",
			})
			return
		}

		bridgeActive := user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "active"
		legacyApproved := user.KYCStatus == "approved"
		if !bridgeActive && !legacyApproved {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "BRIDGE_KYC_REQUIRED",
				"message": "Bridge identity verification is required to access this feature",
			})
			return
		}

		c.Next()
	}
}

// RequireAlpacaCapability enforces Alpaca-related KYC eligibility for investing features.
func RequireAlpacaCapability(userReader UserEntityReader, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := extractUserID(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			})
			return
		}

		user, err := userReader.GetUserEntityByID(c.Request.Context(), userID)
		if err != nil {
			log.Error("Failed to load user for Alpaca capability check",
				zap.Error(err),
				zap.String("user_id", userID.String()),
				zap.String("request_id", c.GetString("request_id")))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    "KYC_STATUS_ERROR",
				"message": "Unable to verify investing eligibility at this time",
			})
			return
		}

		if user.KYCStatus != "approved" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "ALPACA_KYC_REQUIRED",
				"message": "Complete identity verification to access investing features",
			})
			return
		}

		c.Next()
	}
}

// RequireActiveAccount is the identity floor for an authenticated feature: the
// caller must resolve to a real, active user row. It deliberately does NOT check
// KYC tier — feature-specific compliance (KYC for fiat rails, cards, brokerage)
// is enforced by that feature's own gate, not here.
func RequireActiveAccount(userReader UserEntityReader, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := extractUserID(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			})
			return
		}

		user, err := userReader.GetUserEntityByID(c.Request.Context(), userID)
		if err != nil {
			log.Error("Failed to load user for account check",
				zap.Error(err),
				zap.String("user_id", userID.String()),
				zap.String("request_id", c.GetString("request_id")))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    "ACCOUNT_LOOKUP_ERROR",
				"message": "Unable to verify your account at this time",
			})
			return
		}
		if user == nil || user.ID == uuid.Nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "ACCOUNT_SETUP_REQUIRED",
				"message": "Finish setting up your account to continue",
			})
			return
		}

		// Record the tier for downstream limits/pricing without gating on it.
		tier := entities.EffectiveKYCTier(user.KYCTier, user.KYCStatus)
		c.Set("kyc_tier", string(tier))
		c.Next()
	}
}

// RequireTokenizedInvestingCapability gates Glider strategy investing (create a
// strategy, enroll funds, place strategy orders, rebalance).
//
// This used to require advanced (Tier 3) verification and 403'd everyone else.
// That was wrong for this rail: the provider (Glider) holds and executes the
// assets on its own regulated infrastructure, so a brand-new, unverified user is
// allowed to start and fund a strategy. The gate now only requires a resolvable,
// active account, which also turns a missing/inactive user into a clear 403
// instead of a confusing downstream service error.
//
// KYC is still enforced everywhere it is legally required: USD fiat virtual
// accounts and cards (RequireBridgeCapability), brokerage investing
// (RequireAlpacaCapability), fiat withdrawals/ramps and P2P transfers.
func RequireTokenizedInvestingCapability(userReader UserEntityReader, log *zap.Logger) gin.HandlerFunc {
	return RequireActiveAccount(userReader, log)
}
