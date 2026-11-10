package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const maxJSONBody = 10 << 20

func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func decodeJSON(c *gin.Context, v any, max int64) error {
	if max <= 0 {
		max = maxJSONBody
	}
	body := http.MaxBytesReader(c.Writer, c.Request.Body, max)
	data, err := io.ReadAll(body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &decodeError{status: http.StatusRequestEntityTooLarge, code: "", msg: "Request body is too large"}
		}
		return &decodeError{status: http.StatusBadRequest, code: CodeInvalidJSON, msg: "Invalid JSON body."}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return &decodeError{status: http.StatusBadRequest, code: CodeInvalidJSON, msg: "Request body is required."}
	}
	if err := strictUnmarshal(data, v); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			return &decodeError{status: http.StatusBadRequest, code: CodeBadRequest, msg: "Unrecognized key in body -- please review the v2 API documentation for request body changes"}
		}
		return &decodeError{status: http.StatusBadRequest, code: CodeInvalidJSON, msg: "Invalid JSON body."}
	}
	return nil
}

type decodeError struct {
	status int
	code   string
	msg    string
}

func (e *decodeError) Error() string { return e.msg }
