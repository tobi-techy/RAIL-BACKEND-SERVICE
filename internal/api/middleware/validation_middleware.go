package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

var validate = validator.New()

// ValidateRequest validates request body against struct tags
func ValidateRequest(obj interface{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := c.ShouldBindJSON(obj); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format", "details": err.Error()})
			c.Abort()
			return
		}

		if err := validate.Struct(obj); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Validation failed", "details": err.Error()})
			c.Abort()
			return
		}

		c.Set("validated_request", obj)
		c.Next()
	}
}

// PaginationMiddleware adds pagination parameters to context
func PaginationMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		page := c.DefaultQuery("page", "1")
		limit := c.DefaultQuery("limit", "20")
		
		c.Set("page", page)
		c.Set("limit", limit)
		c.Next()
	}
}
