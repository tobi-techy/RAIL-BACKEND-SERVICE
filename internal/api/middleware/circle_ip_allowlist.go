package middleware

import (
	"net"
	"net/http"
	"strings"

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

		// Behind reverse proxies (Atlasflow/Cloudflare), c.ClientIP() returns the
		// proxy's internal IP. Check forwarding headers for the real origin IP.
		candidates := []string{c.ClientIP()}
		if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
			for _, part := range strings.Split(xff, ",") {
				candidates = append(candidates, strings.TrimSpace(part))
			}
		}
		if realIP := c.GetHeader("X-Real-IP"); realIP != "" {
			candidates = append(candidates, strings.TrimSpace(realIP))
		}
		if cfIP := c.GetHeader("CF-Connecting-IP"); cfIP != "" {
			candidates = append(candidates, strings.TrimSpace(cfIP))
		}

		for _, raw := range candidates {
			ip := net.ParseIP(raw)
			if ip == nil {
				continue
			}
			for _, ipNet := range nets {
				if ipNet.Contains(ip) {
					c.Next()
					return
				}
			}
		}

		logger.Warn("Circle webhook request from non-allowlisted IP — rejected",
			zap.String("client_ip", c.ClientIP()),
			zap.String("x_forwarded_for", c.GetHeader("X-Forwarded-For")),
			zap.String("cf_connecting_ip", c.GetHeader("CF-Connecting-IP")))
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "IP_NOT_ALLOWED",
			"message": "Request origin not authorized",
		})
	}
}
