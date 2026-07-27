package mcp

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Limits that bound the runtime settings.
const (
	defaultToolTimeoutSeconds   = 10
	maxToolTimeoutSeconds       = 1800
	defaultMaxConcurrentCalls   = 8
	defaultMaxSelectedPerMsg    = 32
	maxSelectedPerMsg           = 128
	defaultMaxLLMCallsPerRun    = 5
	defaultMaxToolCallsPerRun   = 8
	maxServers                  = 100
	sortStep                    = 100
	maxRequestSelectedToolCount = 128
)

// RuntimeSettings are the settings of namespace "mcp" (see the spec, 9.1).
type RuntimeSettings struct {
	// Enabled is the master switch (mcp_enable).
	Enabled bool
	// ToolTimeoutSeconds is mcp_tool_timeout_seconds.
	ToolTimeoutSeconds int
	// RetryCount is mcp_tool_retry_count.
	RetryCount int
	// MaxConcurrentCalls is mcp_max_concurrent_calls.
	MaxConcurrentCalls int
	// MaxSelectedToolsPerMessage is mcp_max_selected_tools_per_message.
	MaxSelectedToolsPerMessage int
	// MaxLLMCallsPerRun is mcp_max_llm_calls_per_run.
	MaxLLMCallsPerRun int
	// MaxToolCallsPerRun is mcp_max_tool_calls_per_run.
	MaxToolCallsPerRun int
	// ToolPrompt is mcp_tool_prompt; empty selects the built-in guidance.
	ToolPrompt string
}

// DefaultRuntimeSettings returns the documented defaults (feature off).
func DefaultRuntimeSettings() RuntimeSettings {
	return RuntimeSettings{
		Enabled:                    false,
		ToolTimeoutSeconds:         defaultToolTimeoutSeconds,
		RetryCount:                 0,
		MaxConcurrentCalls:         defaultMaxConcurrentCalls,
		MaxSelectedToolsPerMessage: defaultMaxSelectedPerMsg,
		MaxLLMCallsPerRun:          defaultMaxLLMCallsPerRun,
		MaxToolCallsPerRun:         defaultMaxToolCallsPerRun,
	}
}

// normalized applies the runtime fallbacks and caps: a non-positive timeout is
// 10 s and at most 30 minutes, a non-positive concurrency is 8, the selection
// limit is clamped into [1, 128] with 32 for non-positive values.
func (s RuntimeSettings) normalized() RuntimeSettings {
	if s.ToolTimeoutSeconds <= 0 {
		s.ToolTimeoutSeconds = defaultToolTimeoutSeconds
	}
	if s.ToolTimeoutSeconds > maxToolTimeoutSeconds {
		s.ToolTimeoutSeconds = maxToolTimeoutSeconds
	}
	if s.RetryCount < 0 {
		s.RetryCount = 0
	}
	if s.RetryCount > 5 {
		s.RetryCount = 5
	}
	if s.MaxConcurrentCalls <= 0 {
		s.MaxConcurrentCalls = defaultMaxConcurrentCalls
	}
	if s.MaxConcurrentCalls > 64 {
		s.MaxConcurrentCalls = 64
	}
	s.MaxSelectedToolsPerMessage = clampSelection(s.MaxSelectedToolsPerMessage)
	if s.MaxLLMCallsPerRun < 2 {
		s.MaxLLMCallsPerRun = defaultMaxLLMCallsPerRun
	}
	if s.MaxToolCallsPerRun <= 0 {
		s.MaxToolCallsPerRun = defaultMaxToolCallsPerRun
	}
	return s
}

func clampSelection(n int) int {
	if n <= 0 {
		return defaultMaxSelectedPerMsg
	}
	if n > maxSelectedPerMsg {
		return maxSelectedPerMsg
	}
	return n
}

// Settings supplies the runtime settings. The settings area implements it.
type Settings interface {
	MCPSettings(ctx context.Context) RuntimeSettings
}

// BillingModes reports the platform billing mode ("self", "usage", "period").
type BillingModes interface {
	BillingMode(ctx context.Context) (string, error)
}

// ProjectLinks removes project-default tool associations. The association
// table belongs to the conversation area, so the integrator may replace the
// default implementation with a call into that area. tx is the transaction the
// caller is running in; an implementation that writes through it keeps the
// removal atomic with the tool deletion.
type ProjectLinks interface {
	RemoveToolLinks(ctx context.Context, tx *gorm.DB, toolIDs []uint) error
}

// Ports groups the collaborators; nil members get defaults.
type Ports struct {
	Settings     Settings
	BillingModes BillingModes
	ProjectLinks ProjectLinks
	// Client talks to remote servers. Nil selects the HTTP client.
	Client Client
}

// staticSettings serves fixed settings (the default when none are wired).
type staticSettings struct{ s RuntimeSettings }

func (s staticSettings) MCPSettings(context.Context) RuntimeSettings { return s.s }

// sqlProjectLinks deletes rows of chat_conversation_project_mcp_tools directly,
// tolerating a database where the conversation area has not created it.
type sqlProjectLinks struct{}

const projectLinkTable = "chat_conversation_project_mcp_tools"

func (sqlProjectLinks) RemoveToolLinks(ctx context.Context, tx *gorm.DB, toolIDs []uint) error {
	if len(toolIDs) == 0 || !tx.Migrator().HasTable(projectLinkTable) {
		return nil
	}
	for start := 0; start < len(toolIDs); start += 500 {
		end := min(start+500, len(toolIDs))
		if err := tx.WithContext(ctx).Exec("DELETE FROM "+projectLinkTable+" WHERE tool_id IN ?", toolIDs[start:end]).Error; err != nil {
			return err
		}
	}
	return nil
}

// toolTimeout converts the settings timeout to a duration.
func (s RuntimeSettings) toolTimeout() time.Duration {
	return time.Duration(s.normalized().ToolTimeoutSeconds) * time.Second
}
