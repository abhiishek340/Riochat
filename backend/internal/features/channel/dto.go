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
