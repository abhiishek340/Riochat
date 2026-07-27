package mcp

import (
	"errors"
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Error codes are part of the client contract: the web client keeps localized
// messages keyed by them.
var (
	ErrInvalidServerID    = apperr.Define("mcp.server.invalid_id", "invalid mcp server id")
	ErrInvalidToolID      = apperr.Define("mcp.tool.invalid_id", "invalid mcp tool id")
	ErrInvalidServerName  = apperr.Define("mcp.invalid_server_name", "invalid mcp server name")
	ErrInvalidServerURL   = apperr.Define("mcp.invalid_server_base_url", "invalid mcp server base url")
	ErrInvalidServerState = apperr.Define("mcp.invalid_server_status", "invalid mcp server status")
	ErrInvalidHeaders     = apperr.Define("mcp.invalid_server_headers", "invalid mcp server headers json")
	ErrInvalidToolStatus  = apperr.Define("mcp.invalid_tool_status", "invalid mcp tool status")
	ErrInvalidToolName    = apperr.Define("mcp.invalid_tool_name", "invalid mcp tool display name")
	ErrInvalidToolDesc    = apperr.Define("mcp.invalid_tool_description", "invalid mcp tool description")
	ErrInvalidAttachment  = apperr.Define("mcp.invalid_attachment_configuration", "invalid MCP tool attachment configuration")
	ErrInvalidSelection   = apperr.Define("mcp.invalid_tool_selection", "invalid mcp tool selection")
	ErrInvalidToolPrice   = apperr.Define("request.invalid_mcp_tool_price", "invalid mcp tool price")
	ErrServerLimit        = apperr.Define("mcp.server_limit_exceeded", "mcp server limit exceeded")
	ErrClientUnavailable  = apperr.Define("mcp.client_unavailable", "mcp client unavailable")
	ErrImageProcessing    = apperr.Define("mcp.image_processing_failed", "image processing tool failed")
	ErrTooManyTools       = apperr.Define("message.too_many_selected_tools", "too many selected tools")
	ErrMultipleProcessors = apperr.Define("message.multiple_image_processors", "select only one image attachment processor")
	ErrInvalidOverwrite   = apperr.Define("request.invalid_overwrite_customized_metadata", "invalid overwrite_customized_metadata")
)

// errAttachmentConfig is ErrInvalidAttachment carrying the lowercase internal
// log text the contract asks for; the public text is unchanged.
func errAttachmentConfig() error {
	return ErrInvalidAttachment.WithCause(errors.New("invalid mcp tool attachment configuration"))
}

// clientErrors are the typed errors answered with HTTP 400.
var clientErrors = []*apperr.Error{
	ErrInvalidServerID, ErrInvalidToolID, ErrInvalidServerName, ErrInvalidServerURL,
	ErrInvalidServerState, ErrInvalidHeaders, ErrInvalidToolStatus, ErrInvalidToolName,
	ErrInvalidToolDesc, ErrInvalidAttachment, ErrInvalidSelection, ErrInvalidToolPrice,
	ErrServerLimit, ErrTooManyTools, ErrMultipleProcessors, ErrInvalidOverwrite,
}

// statusFor maps a service error to its HTTP status. Everything that is not a
// known client error is a 500 whose detail stays in the logs.
func statusFor(err error) int {
	for _, e := range clientErrors {
		if errors.Is(err, e) {
			return http.StatusBadRequest
		}
	}
	if errors.Is(err, ErrImageProcessing) {
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}
