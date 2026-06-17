package middleware

import (
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// circleIPRanges contains Circle's published webhook source IP ranges.
// Source: https://developers.circle.com/developer/docs/webhook-notifications
// Update this list when Circle publishes new ranges.
var circleIPRanges = []string{
	// Circle production IP ranges
	"54.187.174.169/32",
	"54.187.205.235/32",
	"54.187.216.72/32",
	"54.241.91.89/32",
	"54.241.91.90/32",
	"54.241.91.91/32",
}

// CircleIPAllowlist returns a Gin middleware that restricts the /webhooks/circle
// endpoint to requests originating from Circle's official IP ranges.
// This is the primary security control for Circle v2 webhooks, which do not
// use shared-secret HMAC signatures.
//
// In non-production environments the check is skipped to allow local testing.
func CircleIPAllowlist(environment string, logger *zap.Logger) gin.HandlerFunc {
	// Parse CIDRs once at startup.
	nets := make([]*net.IPNet, 0, len(circleIPRanges))
	for _, cidr := range circleIPRanges {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			logger.Error("Invalid Circle IP range in allowlist — skipping",
				zap.String("cidr", cidr),
				zap.Error(err))
			continue
		}
		nets = append(nets, ipNet)
	}

	return func(c *gin.Context) {
		// Only enforce in production; allow all IPs in dev/staging for local testing.
		if environment != "production" {
			c.Next()
			return
		}

		clientIP := net.ParseIP(c.ClientIP())
		if clientIP == nil {
			logger.Warn("Circle webhook: could not parse client IP",
				zap.String("raw_ip", c.ClientIP()))
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "IP_NOT_ALLOWED",
				"message": "Request origin not authorized",
			})
			return
		}

		for _, ipNet := range nets {
			if ipNet.Contains(clientIP) {
				c.Next()
				return
			}
		}

		logger.Warn("Circle webhook request from non-allowlisted IP — rejected",
			zap.String("client_ip", clientIP.String()))
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "IP_NOT_ALLOWED",
			"message": "Request origin not authorized",
		})
	}
}
