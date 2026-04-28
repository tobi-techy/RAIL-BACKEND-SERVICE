package middleware

import (
	"github.com/gin-gonic/gin"
)

const DeviceFingerprintCtxKey = "x_device_fingerprint"

// DeviceFingerprintExtractor extracts X-Device-Fingerprint header and sets it in context.
// This is a lightweight middleware that runs before auth, complementing the existing
// DeviceVerification middleware which requires an authenticated user.
func DeviceFingerprintExtractor() gin.HandlerFunc {
	return func(c *gin.Context) {
		fingerprint := c.GetHeader("X-Device-Fingerprint")
		if fingerprint != "" {
			c.Set(DeviceFingerprintCtxKey, fingerprint)
		}
		c.Next()
	}
}

// GetExtractedFingerprint retrieves the device fingerprint from context
func GetExtractedFingerprint(c *gin.Context) string {
	fp, _ := c.Get(DeviceFingerprintCtxKey)
	if s, ok := fp.(string); ok {
		return s
	}
	return ""
}
