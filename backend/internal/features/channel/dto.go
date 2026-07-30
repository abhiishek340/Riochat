package channel

import "time"

// UpstreamView is the admin view of an upstream (4.13.1).
type UpstreamView struct {
	ID                   uint         `json:"id"`
	Name                 string       `json:"name"`
	BaseURL              string       `json:"baseURL"`
	Compatible           string       `json:"compatible"`
	ProtocolDefaultsJSON string       `json:"protocolDefaultsJSON"`
	APIKeysMasked        string       `json:"apiKeysMasked"`
	APIKeyItems          []APIKeyItem `json:"apiKeyItems"`
	Status               string       `json:"status"`
	ConnectTimeoutMS     int          `json:"connectTimeoutMS"`
	ReadTimeoutMS        int          `json:"readTimeoutMS"`
	StreamIdleTimeoutMS  int          `json:"streamIdleTimeoutMS"`
	CBFailureThreshold   int          `json:"cbFailureThreshold"`
	CBModelThreshold     int          `json:"cbModelThreshold"`
	CBThresholdLogic     string       `json:"cbThresholdLogic"`
	CBDurationMin        int          `json:"cbDurationMin"`
	CBWindowMin          int          `json:"cbWindowMin"`
	HeadersJSON          string       `json:"headersJSON"`
	ModelsCount          int64        `json:"modelsCount"`
	ActiveModelsCount    int64        `json:"activeModelsCount"`
	CircuitOpen          bool         `json:"circuitOpen"`
	CircuitUntil         string       `json:"circuitUntil"`
	CreatedAt            time.Time    `json:"createdAt"`
	UpdatedAt            time.Time    `json:"updatedAt"`
}

// UpstreamModelRow is one row of the upstream model listing (4.13.2).
type UpstreamModelRow struct {
	ID                     uint      `json:"id"`
	RouteID                uint      `json:"routeID"`
	UpstreamID             uint      `json:"upstreamID"`
	BindingCode            string    `json:"bindingCode"`
	PlatformModelID        uint      `json:"platformModelID"`
	PlatformModelName      string    `json:"platformModelName"`
	ModelVendor            string    `json:"modelVendor"`
	ModelKindsJSON         string    `json:"modelKindsJSON"`
	ModelIcon              string    `json:"modelIcon"`
	UpstreamModelName      string    `json:"upstreamModelName"`
	UpstreamModelVendor    string    `json:"upstreamModelVendor"`
	UpstreamModelIcon      string    `json:"upstreamModelIcon"`
	UpstreamModelKindsJSON string    `json:"upstreamModelKindsJSON"`
	SuggestedProtocol      string    `json:"suggestedProtocol"`
	Protocol               string    `json:"protocol"`
	UpstreamModelStatus    string    `json:"upstreamModelStatus"`
	RouteStatus            string    `json:"routeStatus"`
	Priority               int       `json:"priority"`
	Weight                 int       `json:"weight"`
	Source                 string    `json:"source"`
	CBFailureThreshold     int       `json:"cbFailureThreshold"`
	CBDurationMin          int       `json:"cbDurationMin"`
	CBWindowMin            int       `json:"cbWindowMin"`
	HeadersJSON            string    `json:"headersJSON"`
	CircuitOpen            bool      `json:"circuitOpen"`
	CircuitUntil           string    `json:"circuitUntil"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

// ModelView is the admin view of a platform model (4.13.3).
type ModelView struct {
	ID                 uint      `json:"id"`
	PlatformModelName  string    `json:"platformModelName"`
	Vendor             string    `json:"vendor"`
	VendorName         string    `json:"vendorName"`
	VendorIcon         string    `json:"vendorIcon"`
	DisplayGroupID     *uint     `json:"displayGroupID"`
	DisplayGroupName   string    `json:"displayGroupName"`
	DisplayGroupIcon   string    `json:"displayGroupIcon"`
	KindsJSON          string    `json:"kindsJSON"`
	Icon               string    `json:"icon"`
	CapabilitiesJSON   string    `json:"capabilitiesJSON"`
	ContextWindow      int       `json:"contextWindow"`
	SystemPrompt       string    `json:"systemPrompt"`
	AccessScope        string    `json:"accessScope"`
	Status             string    `json:"status"`
	Description        string    `json:"description"`
	CBPolicyMode       string    `json:"cbPolicyMode"`
	CBFailureThreshold int       `json:"cbFailureThreshold"`
	CBDurationMin      int       `json:"cbDurationMin"`
	CBWindowMin        int       `json:"cbWindowMin"`
	SortOrder          int       `json:"sortOrder"`
	SourceCount        int64     `json:"sourceCount"`
	ActiveSourceCount  int64     `json:"activeSourceCount"`
	ProtocolsJSON      string    `json:"protocolsJSON"`
	UpstreamNamesJSON  string    `json:"upstreamNamesJSON"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// SourceView is one route of a platform model (4.13.7).
type SourceView struct {
	ID                     uint      `json:"id"`
	UpstreamID             uint      `json:"upstreamID"`
	UpstreamName           string    `json:"upstreamName"`
	UpstreamStatus         string    `json:"upstreamStatus"`
	BaseURL                string    `json:"baseURL"`
	BindingCode            string    `json:"bindingCode"`
	UpstreamModelName      string    `json:"upstreamModelName"`
	UpstreamModelKindsJSON string    `json:"upstreamModelKindsJSON"`
	UpstreamModelVendor    string    `json:"upstreamModelVendor"`
	UpstreamModelIcon      string    `json:"upstreamModelIcon"`
	SuggestedProtocol      string    `json:"suggestedProtocol"`
	UpstreamModelStatus    string    `json:"upstreamModelStatus"`
	Protocol               string    `json:"protocol"`
	Status                 string    `json:"status"`
	Priority               int       `json:"priority"`
	Weight                 int       `json:"weight"`
	Source                 string    `json:"source"`
	CBFailureThreshold     int       `json:"cbFailureThreshold"`
	CBDurationMin          int       `json:"cbDurationMin"`
	CBWindowMin            int       `json:"cbWindowMin"`
	HeadersJSON            string    `json:"headersJSON"`
	CircuitOpen            bool      `json:"circuitOpen"`
	CircuitUntil           string    `json:"circuitUntil"`
	CircuitScope           string    `json:"circuitScope"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

// VendorView is a model vendor (4.13.9).
type VendorView struct {
	ID        uint      `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon"`
	BuiltIn   bool      `json:"builtIn"`
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// GroupView is a display group (4.13.9).
type GroupView struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon"`
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// IconAssetView is a library icon.
type IconAssetView struct {
	Ref         string    `json:"ref"`
	PublicID    string    `json:"publicID"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	CreatedAt   time.Time `json:"createdAt"`
}

// IconUploadResult is the result of an icon upload.
type IconUploadResult struct {
	Ref         string `json:"ref"`
	PublicID    string `json:"publicID"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Reused      bool   `json:"reused"`
}

// SettingView is an llm setting row.
type SettingView struct {
	ID          uint      `json:"id"`
	Key         string    `json:"key"`
	Value       string    `json:"value"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// BatchItem is one result of a batch delete.
type BatchItem struct {
	ID     uint   `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// BatchResult is the response of a batch delete.
type BatchResult struct {
	Total         int         `json:"total"`
	SuccessCount  int         `json:"successCount"`
	NotFoundCount int         `json:"notFoundCount"`
	FailedCount   int         `json:"failedCount"`
	Results       []BatchItem `json:"results"`
}

// PublicReasoning is the reasoning capability served to users.
type PublicReasoning struct {
	Levels      []string `json:"levels"`
	Default     string   `json:"default"`
	ControlPath *string  `json:"controlPath"`
	Source      string   `json:"source"`
	Locked      bool     `json:"locked"`
}

// PublicModel is a model as listed for users (4.13.8).
type PublicModel struct {
	PlatformModelName     string           `json:"platformModelName"`
	Vendor                string           `json:"vendor"`
	VendorName            string           `json:"vendorName"`
	VendorIcon            string           `json:"vendorIcon"`
	DisplayGroupID        *uint            `json:"displayGroupID"`
	DisplayGroupName      string           `json:"displayGroupName"`
	DisplayGroupIcon      string           `json:"displayGroupIcon"`
	KindsJSON             string           `json:"kindsJSON"`
	Icon                  string           `json:"icon"`
	ProtocolsJSON         string           `json:"protocolsJSON"`
	CapabilitiesJSON      string           `json:"capabilitiesJSON"`
	Description           string           `json:"description"`
	SortOrder             int              `json:"sortOrder"`
	Pricing               *Pricing         `json:"pricing"`
	Reasoning             *PublicReasoning `json:"reasoning"`
	Controls              []Control        `json:"controls"`
	InputModalities       []string         `json:"inputModalities"`
	InputModalitiesSource *string          `json:"inputModalitiesSource"`
	OutputModalities      []string         `json:"outputModalities"`
	ContextWindow         *int             `json:"contextWindow"`
	Source                string           `json:"source"`
	ProviderName          string           `json:"providerName"`
	ProviderIcon          string           `json:"providerIcon"`
	ProviderHost          string           `json:"providerHost"`

	// modelID and groupsKey are used by internal filtering only.
	modelID uint
}

// ModelID is the id of the platform model behind a public entry (0 for personal models).
func (m PublicModel) ModelID() uint { return m.modelID }

// ProbeResult is the result of a connectivity probe (4.13.11).
type ProbeResult struct {
	Success            bool        `json:"success"`
	Status             string      `json:"status"`
	ErrorCode          string      `json:"errorCode,omitempty"`
	ErrorMessage       string      `json:"errorMessage,omitempty"`
	LatencyMS          int64       `json:"latencyMS"`
	Protocol           string      `json:"protocol"`
	Endpoint           string      `json:"endpoint"`
	PlatformModelID    uint        `json:"platformModelID"`
	PlatformModelName  string      `json:"platformModelName"`
	UpstreamID         uint        `json:"upstreamID"`
	UpstreamName       string      `json:"upstreamName"`
	UpstreamModelID    uint        `json:"upstreamModelID"`
	UpstreamModelName  string      `json:"upstreamModelName"`
	RouteID            uint        `json:"routeID"`
	BindingCode        string      `json:"bindingCode"`
	UpstreamStatusCode int         `json:"upstreamStatusCode,omitempty"`
	Debug              *ProbeDebug `json:"debug,omitempty"`
}

// ProbeAllResult is the result of probing every suitable route of a model.
type ProbeAllResult struct {
	TotalCount       int           `json:"totalCount"`
	SuccessCount     int           `json:"successCount"`
	FailedCount      int           `json:"failedCount"`
	UnsupportedCount int           `json:"unsupportedCount"`
	Results          []ProbeResult `json:"results"`
}
