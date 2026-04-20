// Package middleware collects Gin middlewares used by the HTTP server.
// Each middleware lives in its own file so the set is easy to scan
// and mirrors the layout of the sibling model-router-api project.
package middleware

import "github.com/gin-gonic/gin"

// CORS allows cross-origin requests. Defaults are permissive so the
// trader API can be consumed from any dashboard origin. Tighten via a
// reverse proxy in production when needed.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Set("Access-Control-Allow-Headers",
			"Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		h.Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, PATCH, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
