package channel

// Request bodies. Tags use the platform validator ("binding"); checks that need
// a domain error code instead of request.invalid_body are done in the service.

type createUpstreamReq struct {
	Name                 string `json:"name" binding:"required,min=2,max=128"`
	BaseURL              string `json:"baseURL" binding:"required,url,max=512"`
	Compatible           string `json:"compatible" binding:"max=32"`
	ProtocolDefaultsJSON string `json:"protocolDefaultsJSON" binding:"max=10000"`
	APIKeys              string `json:"apiKeys" binding:"required,min=2,max=10000"`
	Status               string `json:"status" binding:"omitempty,oneof=active inactive"`
	ConnectTimeoutMS     int    `json:"connectTimeoutMS"`
	ReadTimeoutMS        int    `json:"readTimeoutMS"`
	StreamIdleTimeoutMS  int    `json:"streamIdleTimeoutMS"`
	CBFailureThreshold   int    `json:"cbFailureThreshold" binding:"gte=0"`
	CBModelThreshold     int    `json:"cbModelThreshold" binding:"gte=0"`
	CBThresholdLogic     string `json:"cbThresholdLogic" binding:"omitempty,oneof=or and"`
	CBDurationMin        int    `json:"cbDurationMin" binding:"gte=0"`
	CBWindowMin          int    `json:"cbWindowMin" binding:"gte=0"`
	HeadersJSON          string `json:"headersJSON" binding:"max=10000"`
}

type updateUpstreamReq struct {
	Name                 *string  `json:"name" binding:"omitempty,min=2,max=128"`
	BaseURL              *string  `json:"baseURL" binding:"omitempty,url,max=512"`
	Compatible           *string  `json:"compatible" binding:"omitempty,max=32"`
	ProtocolDefaultsJSON *string  `json:"protocolDefaultsJSON" binding:"omitempty,max=10000"`
	APIKeys              *string  `json:"apiKeys" binding:"omitempty,min=2,max=10000"`
	AddAPIKeys           *string  `json:"addAPIKeys" binding:"omitempty,min=2,max=10000"`
	DeleteAPIKeyIDs      []string `json:"deleteAPIKeyIDs" binding:"omitempty,dive,min=8,max=128"`
	Status               *string  `json:"status" binding:"omitempty,oneof=active inactive"`
	ConnectTimeoutMS     *int     `json:"connectTimeoutMS"`
	ReadTimeoutMS        *int     `json:"readTimeoutMS"`
	StreamIdleTimeoutMS  *int     `json:"streamIdleTimeoutMS"`
	CBFailureThreshold   *int     `json:"cbFailureThreshold" binding:"omitempty,gte=0"`
	CBModelThreshold     *int     `json:"cbModelThreshold" binding:"omitempty,gte=0"`
	CBThresholdLogic     *string  `json:"cbThresholdLogic" binding:"omitempty,oneof=or and"`
	CBDurationMin        *int     `json:"cbDurationMin" binding:"omitempty,gte=0"`
	CBWindowMin          *int     `json:"cbWindowMin" binding:"omitempty,gte=0"`
	HeadersJSON          *string  `json:"headersJSON" binding:"omitempty,max=10000"`
}

type batchDeleteReq struct {
	IDs []uint `json:"ids" binding:"required,min=1,dive,gt=0"`
}

type upsertBindingReq struct {
	RouteIDs           []uint   `json:"routeIDs" binding:"omitempty,max=2,unique,dive,gt=0"`
	PlatformModelName  string   `json:"platformModelName" binding:"required,min=2,max=128"`
	UpstreamModelName  string   `json:"upstreamModelName" binding:"required,min=1,max=128"`
	Protocols          []string `json:"protocols" binding:"required,max=2,unique,dive,min=1,max=64"`
	KindsJSON          string   `json:"kindsJSON" binding:"max=1000"`
	Status             *string  `json:"status" binding:"omitempty,oneof=active inactive"`
	Priority           *int     `json:"priority" binding:"omitempty,gt=0"`
	Weight             *int     `json:"weight" binding:"omitempty,gt=0"`
	Source             *string  `json:"source" binding:"omitempty,max=64"`
	CBFailureThreshold *int     `json:"cbFailureThreshold" binding:"omitempty,gte=0"`
	CBDurationMin      *int     `json:"cbDurationMin" binding:"omitempty,gte=0"`
	CBWindowMin        *int     `json:"cbWindowMin" binding:"omitempty,gte=0"`
	HeadersJSON        *string  `json:"headersJSON" binding:"omitempty,max=10000"`
}

type importItemReq struct {
	PlatformModelName string   `json:"platformModelName" binding:"required,min=2,max=128"`
	UpstreamModelName string   `json:"upstreamModelName" binding:"required,min=1,max=128"`
	Protocol          string   `json:"protocol" binding:"max=64"`
	Protocols         []string `json:"protocols" binding:"omitempty,max=2,dive,min=1,max=64"`
	KindsJSON         string   `json:"kindsJSON" binding:"max=1000"`
	Status            string   `json:"status" binding:"omitempty,oneof=active inactive"`
	Priority          int      `json:"priority"`
}

type importReq struct {
	Items              []importItemReq `json:"items" binding:"required,min=1,dive"`
	PermissionGroupIDs []uint          `json:"permissionGroupIDs"`
}

type createModelReq struct {
	PlatformModelName  string `json:"platformModelName" binding:"required,min=2,max=128"`
	Vendor             string `json:"vendor" binding:"max=64"`
	DisplayGroupID     uint   `json:"displayGroupID"`
	KindsJSON          string `json:"kindsJSON" binding:"max=1000"`
	Icon               string `json:"icon" binding:"max=2048"`
	CapabilitiesJSON   string `json:"capabilitiesJSON" binding:"max=10000"`
	SystemPrompt       string `json:"systemPrompt" binding:"max=80000"`
	AccessScope        string `json:"accessScope" binding:"max=32"`
	Status             string `json:"status" binding:"omitempty,oneof=active inactive"`
	Description        string `json:"description" binding:"max=10000"`
	CBPolicyMode       string `json:"cbPolicyMode" binding:"omitempty,oneof=default enforced"`
	CBFailureThreshold int    `json:"cbFailureThreshold" binding:"gte=0"`
	CBDurationMin      int    `json:"cbDurationMin" binding:"gte=0"`
	CBWindowMin        int    `json:"cbWindowMin" binding:"gte=0"`
}

type updateModelReq struct {
	PlatformModelName  *string `json:"platformModelName" binding:"omitempty,min=2,max=128"`
	Vendor             *string `json:"vendor" binding:"omitempty,max=64"`
	DisplayGroupID     *uint   `json:"displayGroupID"`
	KindsJSON          *string `json:"kindsJSON" binding:"omitempty,max=1000"`
	Icon               *string `json:"icon" binding:"omitempty,max=2048"`
	CapabilitiesJSON   *string `json:"capabilitiesJSON" binding:"omitempty,max=10000"`
	SystemPrompt       *string `json:"systemPrompt" binding:"omitempty,max=80000"`
	AccessScope        *string `json:"accessScope" binding:"omitempty,max=32"`
	Status             *string `json:"status" binding:"omitempty,oneof=active inactive"`
	Description        *string `json:"description" binding:"omitempty,max=10000"`
	CBPolicyMode       *string `json:"cbPolicyMode" binding:"omitempty,oneof=default enforced"`
	CBFailureThreshold *int    `json:"cbFailureThreshold" binding:"omitempty,gte=0"`
	CBDurationMin      *int    `json:"cbDurationMin" binding:"omitempty,gte=0"`
	CBWindowMin        *int    `json:"cbWindowMin" binding:"omitempty,gte=0"`
}

type setProtocolsReq struct {
	Protocols []string `json:"protocols" binding:"required,min=1,max=2,unique,dive,min=1,max=64"`
	KindsJSON string   `json:"kindsJSON" binding:"required,min=2,max=1000"`
}

type reorderReq struct {
	ModelIDs []uint `json:"modelIDs" binding:"required,min=1"`
}

type assignGroupReq struct {
	ModelIDs       []uint `json:"modelIDs" binding:"required,min=1,max=1000"`
	DisplayGroupID *uint  `json:"displayGroupID" binding:"required"`
}

type bindSourceReq struct {
	UpstreamID         uint    `json:"upstreamID" binding:"required,gt=0"`
	UpstreamModelID    uint    `json:"upstreamModelID" binding:"required,gt=0"`
	Protocol           *string `json:"protocol" binding:"omitempty,max=64"`
	Status             *string `json:"status" binding:"omitempty,oneof=active inactive"`
	Priority           *int    `json:"priority"`
	Weight             *int    `json:"weight"`
	CBFailureThreshold *int    `json:"cbFailureThreshold"`
	CBDurationMin      *int    `json:"cbDurationMin"`
	CBWindowMin        *int    `json:"cbWindowMin"`
}

type updateSourceReq struct {
	Protocol           *string `json:"protocol" binding:"omitempty,max=64"`
	Status             *string `json:"status" binding:"omitempty,oneof=active inactive"`
	Priority           *int    `json:"priority"`
	Weight             *int    `json:"weight"`
	CBFailureThreshold *int    `json:"cbFailureThreshold"`
	CBDurationMin      *int    `json:"cbDurationMin"`
	CBWindowMin        *int    `json:"cbWindowMin"`
}

type createVendorReq struct {
	Key  string `json:"key" binding:"required,max=64"`
	Name string `json:"name" binding:"required,max=64"`
	Icon string `json:"icon" binding:"max=2048"`
}

type updateVendorReq struct {
	Name *string `json:"name" binding:"omitempty,max=64"`
	Icon *string `json:"icon" binding:"omitempty,max=2048"`
}

type createGroupReq struct {
	Name     string `json:"name" binding:"required,max=64"`
	Icon     string `json:"icon" binding:"max=2048"`
	ModelIDs []uint `json:"modelIDs" binding:"omitempty,max=10000"`
}

type updateGroupReq struct {
	Name     *string `json:"name" binding:"omitempty,max=64"`
	Icon     *string `json:"icon" binding:"omitempty,max=2048"`
	ModelIDs *[]uint `json:"modelIDs" binding:"omitempty,max=10000"`
}

type probeReq struct {
	TaskType string `json:"taskType" binding:"omitempty,oneof=chat image_generation image_edit video_generation video_extension"`
}

type resolveCatalogReq struct {
	Name             string   `json:"name" binding:"max=255"`
	Vendor           string   `json:"vendor" binding:"max=128"`
	Protocols        []string `json:"protocols" binding:"omitempty,max=32,dive,max=64"`
	CapabilitiesJSON string   `json:"capabilitiesJSON" binding:"max=10000"`
}

type patchSettingReq struct {
	Value string `json:"value" binding:"required"`
}
