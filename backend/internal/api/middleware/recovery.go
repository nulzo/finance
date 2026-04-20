package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// Recovery is a panic-to-500 middleware that logs the panic through
// our structured logger (instead of the default stderr dump). The
// stack trace is attached as a field so log backends preserve it.
func Recovery(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().
					Interface("panic", rec).
					Str("path", c.Request.URL.Path).
					Str("method", c.Request.Method).
					Bytes("stack", debug.Stack()).
					Msg("panic recovered")
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
		}()
		c.Next()
	}
}
