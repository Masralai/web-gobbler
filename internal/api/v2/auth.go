package v2

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func (h *Handler) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.Config.APIKey == "" {
			c.Next()
			return
		}
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(c, http.StatusUnauthorized, CodeUnauthorized, "Unauthorized")
			return
		}
		got := strings.TrimPrefix(header, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(h.Config.APIKey)) != 1 {
			writeError(c, http.StatusUnauthorized, CodeUnauthorized, "Unauthorized")
			return
		}
		c.Next()
	}
}
