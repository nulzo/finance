package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Auth requires Authorization: Bearer <token> on every request. The
// expected token is resolved lazily via tokenFn so callers can change
// it at runtime (tests, config reloads) without rebuilding the router.
// When tokenFn returns "" the middleware is a no-op.
//
// Pass Static("") to disable auth unconditionally.
func Auth(tokenFn func() string) gin.HandlerFunc {
	if tokenFn == nil {
		tokenFn = Static("")
	}
	return func(c *gin.Context) {
		expected := tokenFn()
		if expected == "" {
			c.Next()
			return
		}
		got := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if got != expected {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// Static is a convenience that returns a constant tokenFn. Useful when
// the token is fixed at startup and you don't need hot-reload.
func Static(token string) func() string { return func() string { return token } }
