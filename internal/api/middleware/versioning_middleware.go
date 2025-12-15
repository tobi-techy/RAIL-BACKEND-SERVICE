package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// APIVersionMiddleware enforces API versioning
func APIVersionMiddleware(supportedVersions []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		version := c.GetHeader("API-Version")
		if version == "" {
			// Extract from path if not in header
			path := c.Request.URL.Path
			if strings.HasPrefix(path, "/api/") {
				parts := strings.Split(path, "/")
				if len(parts) > 2 {
					version = parts[2]
				}
			}
		}

		if version == "" {
			version = "v1" // Default version
		}

		// Validate version
		valid := false
		for _, v := range supportedVersions {
			if v == version {
				valid = true
				break
			}
		}

		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported API version"})
			c.Abort()
			return
		}

		c.Set("api_version", version)
		c.Next()
	}
}
