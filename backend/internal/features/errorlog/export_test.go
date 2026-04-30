package errorlog

import "github.com/gin-gonic/gin"

// ListHandlerForTest exposes the handler with a nil service for tests.
func ListHandlerForTest(_ any) gin.HandlerFunc { return listHandler(nil) }
