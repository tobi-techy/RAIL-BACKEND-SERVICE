package common

import (
    "net/http"
    "github.com/gin-gonic/gin"
)

// MaxRequestBodySizeMiddleware limits request bodies to 1 MiB.
func MaxRequestBodySizeMiddleware() gin.HandlerFunc {
    const maxBodySize = 1 << 20 // 1 MiB
    return func(c *gin.Context) {
        c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodySize)
        c.Next()
    }
}
