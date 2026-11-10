package v2

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	CodeBadRequest          = "BAD_REQUEST"
	CodeInvalidJSON         = "BAD_REQUEST_INVALID_JSON"
	CodeUnauthorized        = "UNAUTHORIZED"
	CodeNotFound            = "NOT_FOUND"
	CodeTimeout             = "SCRAPE_TIMEOUT"
	CodeContentTooLarge     = "SCRAPE_CONTENT_TOO_LARGE"
	CodeUnsupportedFile     = "UNSUPPORTED_FILE_TYPE"
	CodeUnsupportedOptions  = "PARSE_UNSUPPORTED_OPTIONS"
	CodeIdempotencyReused   = "IDEMPOTENCY_KEY_REUSED"
	CodeProviderUnavailable = "PROVIDER_NOT_CONFIGURED"
	CodeNotImplemented      = "NOT_IMPLEMENTED"
	CodeUnknown             = "UNKNOWN_ERROR"
)

func writeError(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, ErrorResponse{Success: false, Code: code, Error: msg})
}

func writeBadRequest(c *gin.Context, msg string) {
	writeError(c, http.StatusBadRequest, CodeBadRequest, msg)
}

func writeNotFound(c *gin.Context, msg string) {
	writeError(c, http.StatusNotFound, CodeNotFound, msg)
}

func writeProviderUnavailable(c *gin.Context, msg string) {
	writeError(c, http.StatusNotImplemented, CodeProviderUnavailable, msg)
}

func writeDecodeError(c *gin.Context, err error) {
	var de *decodeError
	if errors.As(err, &de) {
		if de.status == http.StatusRequestEntityTooLarge {
			c.AbortWithStatusJSON(de.status, ErrorResponse{Success: false, Error: de.msg})
			return
		}
		writeError(c, de.status, de.code, de.msg)
		return
	}
	writeError(c, http.StatusBadRequest, CodeInvalidJSON, "Invalid JSON body.")
}
