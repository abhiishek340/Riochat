// Package httpx implements the uniform JSON response contract of the API:
// success and failure envelopes, typed-error translation, request-body binding
// with structured validation errors, and small request helpers.
package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// Envelope is the body of every JSON response under /api/v1.
type Envelope struct {
	ErrorMsg  string `json:"errorMsg"`
	ErrorCode string `json:"errorCode,omitempty"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	Data      any    `json:"data"`
}

// Page is the data payload of a paged response.
type Page[T any] struct {
	Total   int64 `json:"total"`
	Results []T   `json:"results"`
}

// OK writes a 200 success envelope.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{Data: data})
}

// Created writes a 201 success envelope.
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Envelope{Data: data})
}

// Paged writes a 200 envelope whose data is {total, results}. A nil results
// slice is rendered as an empty array, never null.
func Paged[T any](c *gin.Context, total int64, results []T) {
	if results == nil {
		results = []T{}
	}
	OK(c, Page[T]{Total: total, Results: results})
}

// BuildFailure builds the failure envelope for a status and error. It is also
// used for terminal error events on streaming responses.
func BuildFailure(status int, err error, requestID string) Envelope {
	code, msg, details := apperr.Resolve(status, err)
	return Envelope{ErrorMsg: msg, ErrorCode: code, Details: details, RequestID: requestID}
}

// Fail aborts the request with a failure envelope. The public code and message
// come from the first registered typed error in err's chain, otherwise from
// the status's generic contract; raw error text is attached to the request
// state for the access log and error log only.
func Fail(c *gin.Context, status int, err error) {
	state := reqctx.FromRequest(c.Request)
	env := BuildFailure(status, err, state.RequestID())
	state.RecordFailure(env.ErrorCode, err, apperr.Diagnostic(err))
	c.AbortWithStatusJSON(status, env)
}

// FailErr aborts using the status hint carried by a typed error (500 when the
// error is untyped or carries no hint).
func FailErr(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if te, ok := apperr.Find(err); ok && te.Status != 0 {
		status = te.Status
	}
	Fail(c, status, err)
}

// InvalidQuery aborts with 400 request.invalid_query, "invalid <name>". The
// name is chosen by the server and never echoes user input.
func InvalidQuery(c *gin.Context, name string) {
	Fail(c, http.StatusBadRequest, apperr.ErrInvalidQuery.WithMessage("invalid "+name))
}

// InvalidID aborts with 400 request.invalid_id.
func InvalidID(c *gin.Context) { Fail(c, http.StatusBadRequest, apperr.ErrInvalidID) }

// NotFound aborts with the generic 404 envelope.
func NotFound(c *gin.Context) { Fail(c, http.StatusNotFound, apperr.ErrNotFound) }
