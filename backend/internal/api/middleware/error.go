package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"github.com/nulzo/trader/internal/domain"
)

// ErrorHandler converts errors collected via c.Error(...) into a
// standardised JSON response. Handlers that prefer to write responses
// directly can still do so — this middleware only fires when the
// handler chain finished without having set a response and at least
// one error is queued on the context.
//
// The response shape is intentionally compatible with what the rest of
// the app already returns via s.serverError / s.badRequest so callers
// don't need to switch styles in lockstep.
func ErrorHandler(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		last := c.Errors.Last()
		err := last.Err

		// Map well-known domain errors to sensible HTTP statuses.
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, domain.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, domain.ErrValidation):
			status = http.StatusBadRequest
		case errors.Is(err, domain.ErrUnauthorized):
			status = http.StatusUnauthorized
		case errors.Is(err, domain.ErrRiskRejected),
			errors.Is(err, domain.ErrConflict),
			errors.Is(err, domain.ErrBrokerRejected),
			errors.Is(err, domain.ErrInsufficientFund):
			status = http.StatusConflict
		case errors.Is(err, domain.ErrProviderFailure):
			status = http.StatusBadGateway
		}

		if status >= 500 {
			log.Error().Err(err).Str("path", c.Request.URL.Path).Msg("api error")
		} else {
			log.Warn().Err(err).Str("path", c.Request.URL.Path).Int("status", status).Msg("api error")
		}

		c.AbortWithStatusJSON(status, gin.H{"error": err.Error()})
	}
}
