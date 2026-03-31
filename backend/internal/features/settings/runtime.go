package settings

import (
	"strconv"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/config"
)

// Runtime is the in-process projection of the settings that other modules read
// on every request. A snapshot is immutable once published; the service swaps
// whole snapshots atomically. Module-read namespaces (billing, circuit) are not
// part of it.
type Runtime struct {
	Auth             AuthRuntime
	Chat             ChatRuntime
	KnowledgeBase    KnowledgeBaseRuntime
	PersonalProvider PersonalProviderRuntime
	Desktop          DesktopRuntime
	Storage          StorageRuntime
	File             FileRuntime
	Extract          ExtractRuntime
	MCP              MCPRuntime
}

// AuthRuntime mirrors namespace "auth" (login_default_next_path excluded).
type AuthRuntime struct {
	TokenTTLHours                   int
	RefreshTokenTTLHours            int
	LoginMaxFailures                int
	LoginLockMinutes                int
	RateLimitEnabled                bool
	RateLimitRPM                    int
	PublicAuthRateLimitRPM          int
	UsernameLoginEnabled            bool
	EmailLoginEnabled               bool
	ThirdPartyLoginEnabled          bool
	EmailRegistrationEnabled        bool
	EmailVerificationEnabled        bool
	PasswordResetEnabled            bool
	SMTPHost                        string
	SMTPPort                        int
	SMTPUsername                    string
	SMTPPassword                    string
	SMTPFrom                        string
	EmailRegistrationAllowedDomains string
	EmailRegistrationBlockPlusAlias bool
	AutoLinkVerifiedEmail           bool
	TurnstileRegistrationEnabled    bool
	TurnstileSiteKey                string
	TurnstileSecretKey              string
}

// ChatRuntime mirrors namespace "chat".
type ChatRuntime struct {
	MaxContextMessages              int
	ContextMaxTurns                 int
	UIComponentsEnabled             bool
	ContextCompactEnabled           bool
	ContextWindowFallbackTokens     int
	ContextCompactTriggerPercent    int
	ContextCompactPreserveRecent    int
	ConversationDefaultModel        string
	ConversationTaskModel           string
	ConversationTitlePrompt         string
	ConversationLabelsPrompt        string
	DefaultSystemPrompt             string
	SkillsPrompt                    string
	ModelOptionPolicyMode           string
	ModelOptionAllowedPaths         string
	ModelOptionDeniedPaths          string
	RAGEnabled                      bool
	RAGMinSimilarity                float64
	RAGTokenBudget                  int
	RAGFetchMultiplier              int
	RAGWaitReadyMS                  int
	RAGQueryHistoryTurns            int
	RAGRetrievalCacheTTLSeconds     int
	RAGHybridEnabled                bool
	ContextCompactHighlightsPerRole int
	ContextCompactSnippetChars      int
	CompactLLMEnabled               bool
	CompactTaskModel                string
	CompactAsyncEnabled             bool
	CompactMaxFailures              int
	CompactSystemPrompt             string
	CompactLightPrompt              string
	ContextTokenBudgetEnabled       bool
	MessageEmbeddingEnabled         bool
	SemanticContextEnabled          bool
	ProcessTraceEnabled             bool
	ProcessTracePersistInflight     bool
	ContextArtifactRetentionDays    int
}

// KnowledgeBaseRuntime mirrors namespace "knowledgebase".
type KnowledgeBaseRuntime struct {
	Enabled bool
}

// PersonalProviderRuntime mirrors namespace "personal_provider".
type PersonalProviderRuntime struct {
	Enabled      bool
	MaxPerUser   int
	BlockedHosts string
}

// DesktopRuntime mirrors namespace "desktop".
type DesktopRuntime struct {
	DownloadEnabled bool
	DownloadURL     string
}

// StorageRuntime mirrors namespace "storage".
type StorageRuntime struct {
	UserStorageQuotaBytes int64
	MaxUploadFileBytes    int64
	MaxMessageFiles       int
}

// FileRuntime mirrors namespace "file". Zero means unlimited for the optional
// limits.
type FileRuntime struct {
	ImageMaxDimension         int
	FullContextLimitEnabled   bool
	FullContextMaxBytes       int64
	FullContextMaxTokens      int
	ImageMaxBytes             int64
	DocMaxBytes               int64
	FullContextPDFMaxPages    int
	AllowedMimeTypes          string
	EmbeddingEnabled          bool
	EmbeddingHost             string
	EmbeddingProtocol         string
	EmbeddingKey              string
	EmbeddingTimeoutSeconds   int
	EmbeddingOutputDimensions int
	EmbeddingDimensionsPolicy string
	EmbeddingNormalize        bool
	EmbeddingModelSignature   string
	EmbedTriggerOnUpload      bool
	EmbedChunkSizeTokens      int
	EmbedChunkOverlapTokens   int
	EmbedBatchSize            int
	RAGTopK                   int
	RAGModel                  string
}

// ExtractRuntime mirrors namespace "extract".
type ExtractRuntime struct {
	Engine                  string
	OCREngine               string
	ImageOCREnabled         bool
	PDFOCRFallbackEnabled   bool
	TikaSource              string
	TikaBaseURL             string
	TikaTimeoutSeconds      int
	TikaAuthToken           string
	DoclingBaseURL          string
	DoclingAuthToken        string
	DoclingTimeoutSeconds   int
	TesseractBaseURL        string
	TesseractAuthToken      string
	TesseractTimeoutSeconds int
	RapidOCRSource          string
	RapidOCRBaseURL         string
	RapidOCRAuthToken       string
	RapidOCRTimeoutSeconds  int
	PaddleBaseURL           string
	PaddleAuthToken         string
	PaddleTimeoutSeconds    int
	TencentSecretID         string
	TencentSecretKey        string
	TencentRegion           string
	TencentEndpoint         string
	TencentTimeoutSeconds   int
	AliyunAccessKeyID       string
	AliyunAccessKeySecret   string
	AliyunRegion            string
	AliyunEndpoint          string
	AliyunTimeoutSeconds    int
	MinerUSource            string
	MinerUBaseURL           string
	MinerUFileTypes         string
	MinerUAuthToken         string
	MinerUTimeoutSeconds    int
	MistralBaseURL          string
	MistralAuthToken        string
	MistralModel            string
	MistralTimeoutSeconds   int
	LLMOCRBaseURL           string
	LLMOCRModel             string
	LLMOCRAuthToken         string
	LLMOCRTimeoutSeconds    int
	LLMOCRPrompt            string
}

// MCPRuntime mirrors namespace "mcp".
type MCPRuntime struct {
	Enable                     bool
	ToolTimeoutSeconds         int
	ToolRetryCount             int
	MaxConcurrentCalls         int
	MaxSelectedToolsPerMessage int
	MaxLLMCallsPerRun          int
	MaxToolCallsPerRun         int
	ToolPrompt                 string
}

// AccessTokenTTL is the access token lifetime.
func (a AuthRuntime) AccessTokenTTL() time.Duration {
	return time.Duration(a.TokenTTLHours) * time.Hour
}

// RefreshTokenTTL is the refresh token lifetime.
func (a AuthRuntime) RefreshTokenTTL() time.Duration {
	return time.Duration(a.RefreshTokenTTLHours) * time.Hour
}

// DesktopDownloadTarget returns the download entry shown to users: enabled and
// the URL only when the entry is switched on and the URL is valid.
func (d DesktopRuntime) DesktopDownloadTarget() (bool, string) {
	url := strings.TrimSpace(d.DownloadURL)
	if !d.DownloadEnabled || !ValidDesktopDownloadURL(url) {
		return false, ""
	}
	return true, url
}

// setters used by the registry: each key points at one field of Runtime.

// applyValue parses v onto the field target points at. A value that fails to
// parse leaves the field untouched. emptyZero makes an empty numeric value mean
// zero (unlimited).
func applyValue(target any, v string, emptyZero bool) {
	switch p := target.(type) {
	case *string:
		*p = strings.TrimSpace(v)
	case *bool:
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			*p = b
		}
	case *int:
		if n, ok := parseNumber(v, emptyZero); ok {
			*p = int(n)
		}
	case *int64:
		if n, ok := parseNumber(v, emptyZero); ok {
			*p = n
		}
	case *float64:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			*p = f
		}
	}
}

func parseNumber(v string, emptyZero bool) (int64, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, emptyZero
	}
	return parseInt(v)
}

// normalize applies the cross-field rules every published snapshot must obey.
func (r *Runtime) normalize() {
	a := &r.Auth
	if !a.EmailLoginEnabled {
		a.EmailRegistrationEnabled = false
	}
	if !a.EmailRegistrationEnabled {
		a.TurnstileRegistrationEnabled = false
	}
	if !a.EmailVerificationEnabled || (!a.UsernameLoginEnabled && !a.EmailLoginEnabled) {
		a.PasswordResetEnabled = false
	}
	if a.TokenTTLHours <= 0 {
		a.TokenTTLHours = 24
	}
	if a.RefreshTokenTTLHours <= 0 {
		a.RefreshTokenTTLHours = 720
	}

	f := &r.File
	if !f.EmbeddingEnabled || strings.TrimSpace(f.EmbeddingHost) == "" || strings.TrimSpace(f.RAGModel) == "" {
		r.Chat.RAGEnabled = false
		r.Chat.MessageEmbeddingEnabled = false
		r.Chat.SemanticContextEnabled = false
	}
	if !r.Chat.MessageEmbeddingEnabled {
		r.Chat.SemanticContextEnabled = false
	}

	c := &r.Chat
	switch c.ModelOptionPolicyMode {
	case "allowlist", "denylist", "disabled":
	default:
		c.ModelOptionPolicyMode = "allowlist"
	}
	if strings.TrimSpace(c.ModelOptionAllowedPaths) == "" {
		c.ModelOptionAllowedPaths = defaultAllowedPathsJSON
	}
	if strings.TrimSpace(c.ModelOptionDeniedPaths) == "" {
		c.ModelOptionDeniedPaths = defaultDeniedPathsJSON
	}
	if c.ContextWindowFallbackTokens < 16384 || c.ContextWindowFallbackTokens > 16000000 {
		c.ContextWindowFallbackTokens = 128000
	}
	if p := c.ContextCompactTriggerPercent; p != 0 && (p < 10 || p > 95) {
		c.ContextCompactTriggerPercent = 80
	}

	m := &r.MCP
	switch {
	case m.MaxSelectedToolsPerMessage <= 0:
		m.MaxSelectedToolsPerMessage = 32
	case m.MaxSelectedToolsPerMessage > 128:
		m.MaxSelectedToolsPerMessage = 128
	}
	switch {
	case m.ToolTimeoutSeconds <= 0:
		m.ToolTimeoutSeconds = 10
	case m.ToolTimeoutSeconds > 1800:
		m.ToolTimeoutSeconds = 1800
	}

	if !ValidDesktopDownloadURL(r.Desktop.DownloadURL) {
		r.Desktop.DownloadURL = defaultDesktopURL
	} else {
		r.Desktop.DownloadURL = strings.TrimSpace(r.Desktop.DownloadURL)
	}

	if !f.FullContextLimitEnabled {
		f.FullContextMaxBytes = 0
		f.FullContextMaxTokens = 0
		f.FullContextPDFMaxPages = 0
	}
}

const defaultDesktopURL = "https://github.com/riochat/riochat/releases/latest"

// projectAuth writes the settings the platform itself consumes into the
// configuration snapshot.
func (r *Runtime) projectAuth(c *config.Config) {
	c.Dynamic.Auth.AccessTokenTTL = r.Auth.AccessTokenTTL()
	c.Dynamic.Auth.RefreshTokenTTL = r.Auth.RefreshTokenTTL()
	c.Dynamic.Auth.RateLimitEnabled = r.Auth.RateLimitEnabled
	c.Dynamic.Auth.RateLimitAuthRPM = r.Auth.RateLimitRPM
	c.Dynamic.Auth.RateLimitPublicRPM = r.Auth.PublicAuthRateLimitRPM
}
