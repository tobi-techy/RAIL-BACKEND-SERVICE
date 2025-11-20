package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/stack-service/stack_service/pkg/version"
)

func VersionHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, version.Get())
	}
}
