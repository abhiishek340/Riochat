package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// MCPToolUsage is one MCP tool's usage within a run.
type MCPToolUsage struct {
	ServerID     uint   `json:"server_id"`
	ServerName   string `json:"server_name"`
	ToolName     string `json:"tool_name"`
	CallCount    int64  `json:"call_count"`
	PriceNanousd int64  `json:"price_nanousd"`
}

// ServiceItemInput is an auxiliary charge inside one ledger row: a helper model
// call (title, labels, compaction), a native tool or an MCP tool.
type ServiceItemInput struct {
	Code               string
	Name               string
	PlatformModelName  string
	ProviderProtocol   string
	InputTokens        int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	CacheWrite5mTokens int64
	CacheWrite1hTokens int64
	OutputTokens       int64
	ReasoningTokens    int64
	CallCount          int64
	DurationSeconds    int64
	CacheTimeout       string
	RequestSpeed       string
	UsageSpeed         string
	RequestServiceTier string
	UsageServiceTier   string
	// Personal items are recorded at zero cost.
	Personal bool
	// FixedCall prices the item as CallNanousd per call without a price lookup
	// (native tools, MCP tools).
	FixedCall      bool
	CallNanousd    int64
	DurationBilled bool
}

// LedgerInput is everything the chat pipeline reports about one billable run.
type LedgerInput struct {
	UserID            uint
	RefNo             string
	Mode              string // the authorization's mode; empty = current mode
	ConversationID    uint
	PlatformModelName string
	RoutedBindingCode string
	UpstreamModelName string
	UpstreamName      string
	ModelVendor       string
	ModelIcon         string
	ProviderProtocol  string

	// ServiceOnly marks internal rows (title/label/compaction) and personal
	// model runs: no model token cost, only service items bill.
	ServiceOnly bool
	Personal    bool

	BillingAt time.Time

	InputTokens        int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	CacheWrite5mTokens int64
	CacheWrite1hTokens int64
	OutputTokens       int64
	ReasoningTokens    int64
	CallCount          int64
	DurationSeconds    int64
	DurationBillable   bool
	LatencyMS          int64

	CacheTimeout       string
	RequestSpeed       string
	UsageSpeed         string
	RequestServiceTier string
	UsageServiceTier   string

	UpstreamUsage       json.RawMessage
	ServerSideToolUsage map[string]int64
	MCPTools            []MCPToolUsage
	ServiceItems        []ServiceItemInput

	MediaType       string
	InputImageCount int
	UsageSource     string
	BilledReason    string
}

// LedgerDraft is a built, not yet persisted ledger row with its snapshot map.
type LedgerDraft struct {
	Row      UsageLedger
	Snapshot map[string]any
	// ServiceOnly echoes the classification used for the row.
	ServiceOnly bool
}

// priceFor loads the price row of a platform model (nil when none exists).
func (s *Service) priceFor(ctx context.Context, name string) (*priceSpec, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var row ModelPrice
	err := s.db.WithContext(ctx).Where("platform_model_name = ?", name).First(&row).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	sp, err := specFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("model price %q: %w", name, err)
	}
	return &sp, nil
}

// groupRate asks the permission area for the model's rate percent (100 when no
// resolver is wired, the user is unknown or the resolver fails).
func (s *Service) groupRate(ctx context.Context, userID uint, model string, extraGroup uint) int {
	if s.ports.Rates == nil || userID == 0 {
		return 100
	}
	p, err := s.ports.Rates.RatePercent(ctx, userID, model, extraGroup)
	if err != nil {
		s.log.Warn("billing_rate_resolver_failed")
		return 100
	}
	return p
}

// rawUsageIsContainer reports whether raw is valid JSON holding an object or an
// array (the only shapes recorded as upstream usage).
func rawUsageIsContainer(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || (t[0] != '{' && t[0] != '[') {
		return false
	}
	return json.Valid(t)
}

func nonNilMap[T any](m map[string]T) map[string]T {
	if m == nil {
		return map[string]T{}
	}
	return m
}

func legacyInternalItem(items []ServiceItemInput) bool {
	if len(items) == 0 {
		return false
	}
	switch items[0].Code {
	case "title", "labels", "compact":
		return true
	}
	return false
}

func (m multiplier) ratioFloat() float64 { return m.Total.float() }

func putCostSnapshot(snap map[string]any, mc modelCost, mult multiplier) {
	snap["rate_multiplier"] = mult.ratioFloat()
	snap["pricing_mode"] = mc.Spec.Mode
	snap["currency"] = mc.Spec.Currency
	snap["input_nanousd_per_m_tokens"] = mc.Rates.Input
	snap["cache_read_nanousd_per_m_tokens"] = mc.Rates.CacheRead
	snap["cache_write_nanousd_per_m_tokens"] = mc.CacheRate
	snap["output_nanousd_per_m_tokens"] = mc.Rates.Output
	snap["call_nanousd_per_call"] = mc.Rates.Call
	snap["duration_nanousd_per_second"] = mc.Rates.Duration
	snap["base_input_nanousd_per_m_tokens"] = mc.Base.Input
	snap["base_cache_read_nanousd_per_m_tokens"] = mc.Base.CacheRead
	snap["base_cache_write_nanousd_per_m_tokens"] = mc.Base.CacheWrite
	snap["base_output_nanousd_per_m_tokens"] = mc.Base.Output
	snap["base_call_nanousd_per_call"] = mc.Base.Call
	snap["base_duration_nanousd_per_second"] = mc.Base.Duration
	snap["base_cache_write_5m_nanousd_per_m_tokens"] = mc.Base.CacheWrite5m
	snap["base_cache_write_1h_nanousd_per_m_tokens"] = mc.Base.CacheWrite1h
	snap["cache_write_5m_nanousd_per_m_tokens"] = mc.Rates.CacheWrite5m
	snap["cache_write_1h_nanousd_per_m_tokens"] = mc.Rates.CacheWrite1h
	snap["cache_write_5m_tokens"] = mc.Split.T5m
	snap["cache_write_1h_tokens"] = mc.Split.T1h
	snap["tiered_pricing_json"] = mc.Spec.TieredJSON
	if mc.Tiered {
		snap["tiered_from_tokens"] = mc.TierFrom
		if mc.TierUpTo != nil {
			snap["tiered_up_to_tokens"] = *mc.TierUpTo
		} else {
			snap["tiered_up_to_tokens"] = nil
		}
	} else {
		snap["tiered_from_tokens"] = int64(0)
		snap["tiered_up_to_tokens"] = nil
	}
	snap["schedule_pricing_json"] = mc.Spec.ScheduleJSON
	snap["schedule_period_name"] = mult.ScheduleName
	snap["schedule_rate_percent"] = mult.SchedulePercent
	snap["input_billed_nanousd"] = mc.Input
	snap["cache_read_billed_nanousd"] = mc.CacheRead
	snap["cache_write_billed_nanousd"] = mc.CacheW
	snap["cache_write_5m_billed_nanousd"] = mc.CacheW5m
	snap["cache_write_1h_billed_nanousd"] = mc.CacheW1h
	snap["output_billed_nanousd"] = mc.Output
	snap["call_billed_nanousd"] = mc.Call
	snap["duration_billed_nanousd"] = mc.Duration
}

func zeroCost(mc *modelCost) {
	mc.Input, mc.CacheRead, mc.CacheW, mc.CacheW5m, mc.CacheW1h, mc.Output, mc.Call, mc.Duration, mc.Total = 0, 0, 0, 0, 0, 0, 0, 0, 0
}

// itemResult is a priced service item.
type itemResult struct {
	Snapshot map[string]any
	Billed   int64
}

// extraGroupOf returns the permission group of the user's current plan (0
// when none).
func (s *Service) extraGroupOf(ctx context.Context, userID uint) uint {
	if userID == 0 {
		return 0
	}
	snap, err := s.currentSubscriptionSnapshot(ctx, userID)
	if err != nil || snap == nil || snap.PermissionGroupID == nil {
		return 0
	}
	return *snap.PermissionGroupID
}

// priceServiceItem prices one service item at time at.
func (s *Service) priceServiceItem(ctx context.Context, userID uint, mode string, at time.Time, extraGroup uint, it ServiceItemInput) (itemResult, error) {
	calls := it.CallCount
	if calls <= 0 {
		calls = 1
	}
	snap := map[string]any{
		"service_code": it.Code, "service_name": it.Name, "platform_model_name": it.PlatformModelName,
		"provider_protocol": it.ProviderProtocol, "cache_timeout": cacheBucket(it.CacheTimeout),
		"request_speed": it.RequestSpeed, "usage_speed": it.UsageSpeed,
		"request_service_tier": it.RequestServiceTier, "usage_service_tier": it.UsageServiceTier,
		"input_tokens": it.InputTokens, "cache_read_tokens": it.CacheReadTokens, "cache_write_tokens": it.CacheWriteTokens,
		"output_tokens": it.OutputTokens, "reasoning_tokens": it.ReasoningTokens, "call_count": calls, "duration_seconds": it.DurationSeconds,
		"fast_mode": false, "rate_multiplier": 1.0, "schedule_period_name": "", "schedule_rate_percent": 100,
		"pricing_mode": PricingToken, "billed_nanousd": int64(0),
		"billing_speed": "", "billing_service_tier": "",
	}
	res := itemResult{Snapshot: snap}
	if it.FixedCall {
		snap["pricing_mode"] = PricingCall
		snap["call_nanousd_per_call"] = it.CallNanousd
		if mode != ModeSelf && it.CallNanousd > 0 {
			res.Billed = unitAmount(calls, it.CallNanousd)
		}
		snap["call_billed_nanousd"] = res.Billed
		snap["billed_nanousd"] = res.Billed
		return res, nil
	}
	if it.Personal || personalModelRef(it.PlatformModelName) || mode == ModeSelf {
		return res, nil
	}
	spec, err := s.priceFor(ctx, it.PlatformModelName)
	if err != nil {
		return res, err
	}
	if spec == nil {
		return res, ErrPricingRequired
	}
	if spec.IsFree {
		snap["pricing_mode"] = spec.Mode
		return res, nil
	}
	speed := billingSpeed(it.ProviderProtocol, it.UsageSpeed, it.RequestSpeed)
	tier := billingTier(it.ProviderProtocol, it.UsageServiceTier)
	period := spec.scheduleAt(s.now(), s.loc) // helper calls are scheduled at the current time
	mult := composeMultiplier(baseRatio(it.ProviderProtocol, speed, tier, it.PlatformModelName, ""), s.groupRate(ctx, userID, it.PlatformModelName, extraGroup), period)
	mc, err := priceUsage(*spec, usageCounts{Input: it.InputTokens, CacheRead: it.CacheReadTokens, CacheWrite: it.CacheWriteTokens,
		CacheWrite5m: it.CacheWrite5mTokens, CacheWrite1h: it.CacheWrite1hTokens, Output: it.OutputTokens, Reasoning: it.ReasoningTokens,
		Calls: calls, Seconds: it.DurationSeconds}, it.ProviderProtocol, it.CacheTimeout, mult.Total)
	if err != nil {
		return res, err
	}
	putCostSnapshot(snap, mc, mult)
	snap["billing_speed"], snap["billing_service_tier"] = speed, tier
	snap["fast_mode"] = speed == "fast"
	snap["billed_nanousd"] = mc.Total
	res.Billed = mc.Total
	return res, nil
}

// BuildLedger prices one run and returns the ledger row to settle. It fails
// with ErrPricingRequired when a priced component has no price (the caller then
// moves the reservation to reconciliation).
func (s *Service) BuildLedger(ctx context.Context, in LedgerInput) (*LedgerDraft, error) {
	rs, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	mode := in.Mode
	if mode == "" {
		mode = rs.Mode
	}
	at := in.BillingAt
	if at.IsZero() {
		at = s.now()
	}
	at = at.UTC()
	protocol := strings.TrimSpace(in.ProviderProtocol)

	personal := in.Personal || personalModelRef(in.PlatformModelName)
	serviceOnly := in.ServiceOnly || personal || legacyInternalItem(in.ServiceItems)

	var spec *priceSpec
	if !personal && !(in.ServiceOnly && strings.TrimSpace(in.PlatformModelName) == "") {
		spec, err = s.priceFor(ctx, in.PlatformModelName)
		if err != nil {
			return nil, err
		}
	}
	if mode != ModeSelf && !serviceOnly && spec == nil {
		return nil, ErrPricingRequired
	}
	free := spec != nil && spec.IsFree

	if spec != nil && !serviceOnly && !free && mode != ModeSelf && spec.Mode == PricingDuration &&
		(!in.DurationBillable || in.DurationSeconds <= 0) {
		return nil, ErrPricingRequired
	}

	extraGroup := uint(0)
	if mode != ModeSelf {
		extraGroup = s.extraGroupOf(ctx, in.UserID)
	}
	speed := billingSpeed(protocol, in.UsageSpeed, in.RequestSpeed)
	tier := billingTier(protocol, in.UsageServiceTier)
	baseR := baseRatio(protocol, speed, tier, in.PlatformModelName, in.UpstreamModelName)
	groupPct := 100
	var period *SchedulePeriod
	if mode != ModeSelf && spec != nil && !serviceOnly {
		groupPct = s.groupRate(ctx, in.UserID, in.PlatformModelName, extraGroup)
		period = spec.scheduleAt(at, s.loc)
	}
	mult := composeMultiplier(baseR, groupPct, period)

	snap := map[string]any{}
	var mc modelCost
	modelBilled := int64(0)
	currency := CurrencyUSD
	if spec != nil && !serviceOnly {
		mc, err = priceUsage(*spec, usageCounts{
			Input: in.InputTokens, CacheRead: in.CacheReadTokens, CacheWrite: in.CacheWriteTokens,
			CacheWrite5m: in.CacheWrite5mTokens, CacheWrite1h: in.CacheWrite1hTokens, Output: in.OutputTokens,
			Reasoning: in.ReasoningTokens, Calls: maxI64(in.CallCount, 1), Seconds: in.DurationSeconds,
		}, protocol, in.CacheTimeout, mult.Total)
		if err != nil {
			return nil, err
		}
		if spec.Currency != "" {
			currency = spec.Currency
		}
		if mode == ModeSelf || free {
			zeroCost(&mc)
		}
		modelBilled = mc.Total
		putCostSnapshot(snap, mc, mult)
	} else {
		mc = modelCost{Spec: priceSpec{Mode: PricingToken, Currency: CurrencyUSD, TieredJSON: "{}", ScheduleJSON: "{}"}}
		putCostSnapshot(snap, mc, mult)
	}

	// Service items: helper calls, then native tools, then MCP tools.
	var items []map[string]any
	itemTotal := int64(0)
	addItem := func(r itemResult) {
		items = append(items, r.Snapshot)
		itemTotal = satAdd(itemTotal, r.Billed)
	}
	for _, it := range in.ServiceItems {
		r, err := s.priceServiceItem(ctx, in.UserID, mode, at, extraGroup, it)
		if err != nil {
			return nil, err
		}
		addItem(r)
	}

	nativeBilled := int64(0)
	toolUsage := map[string]int64{}
	for name, n := range in.ServerSideToolUsage {
		if strings.TrimSpace(name) != "" && n > 0 {
			toolUsage[strings.TrimSpace(name)] = n
		}
	}
	if mode != ModeSelf && !serviceOnly && !free && rs.NativeToolBillingEnabled && len(toolUsage) > 0 {
		names := sortedKeys(toolUsage)
		for _, name := range names {
			e, ok := nativeEntryFor(protocol, name)
			if !ok {
				continue
			}
			price := effectiveNativePrice(e, rs.NativeOverrides)
			if price <= 0 {
				continue
			}
			r, _ := s.priceServiceItem(ctx, in.UserID, mode, at, extraGroup, ServiceItemInput{
				Code: "native_tool." + e.Key, Name: e.Label,
				ProviderProtocol: protocol, CallCount: toolUsage[name], FixedCall: true, CallNanousd: price,
			})
			nativeBilled = satAdd(nativeBilled, r.Billed)
			addItem(r)
		}
	}

	mcpBilled := int64(0)
	var mcpSnap []map[string]any
	for _, t := range in.MCPTools {
		if t.CallCount <= 0 {
			continue
		}
		server := strings.TrimSpace(t.ServerName)
		if server == "" && t.ServerID != 0 {
			server = fmt.Sprint(t.ServerID)
		}
		if server == "" {
			server = "unknown"
		}
		tool := strings.TrimSpace(t.ToolName)
		if tool == "" {
			tool = "unknown"
		}
		mcpSnap = append(mcpSnap, map[string]any{
			"server_id": t.ServerID, "server_name": t.ServerName, "tool_name": t.ToolName,
			"call_count": t.CallCount, "price_nanousd": t.PriceNanousd,
		})
		if mode == ModeSelf || t.PriceNanousd <= 0 {
			continue
		}
		r, _ := s.priceServiceItem(ctx, in.UserID, mode, at, extraGroup, ServiceItemInput{
			Code: "mcp_tool." + server + "." + tool, Name: server + " / " + tool,
			CallCount: t.CallCount, FixedCall: true, CallNanousd: t.PriceNanousd,
		})
		mcpBilled = satAdd(mcpBilled, r.Billed)
		addItem(r)
	}

	total := satAdd(modelBilled, itemTotal)
	isFree := total == 0 && (mode == ModeSelf || free || serviceOnly)

	callCount := maxI64(in.CallCount, 1)
	seconds := int64(0)
	if in.DurationBillable {
		seconds = clampNonNeg(in.DurationSeconds)
	}
	protoSplit := mc.Split
	cw5, cw1h := protoSplit.T5m, protoSplit.T1h
	if !protoSplit.Split {
		cw5, cw1h = 0, 0
	}

	snap["platform_model_name"] = in.PlatformModelName
	snap["routed_binding_code"] = in.RoutedBindingCode
	snap["model_vendor"] = in.ModelVendor
	snap["model_icon"] = in.ModelIcon
	snap["provider_protocol"] = protocol
	snap["upstream_name"] = in.UpstreamName
	snap["upstream_model_name"] = in.UpstreamModelName
	snap["cache_timeout"] = cacheBucket(in.CacheTimeout)
	m5, m1 := CacheWriteMultipliers(func() string {
		if spec != nil {
			return spec.Basis
		}
		return ""
	}())
	snap["cache_write_5m_multiplier"] = m5
	snap["cache_write_1h_multiplier"] = m1
	snap["request_speed"] = in.RequestSpeed
	snap["usage_speed"] = in.UsageSpeed
	snap["billing_speed"] = speed
	snap["request_service_tier"] = in.RequestServiceTier
	snap["usage_service_tier"] = in.UsageServiceTier
	snap["billing_service_tier"] = tier
	snap["fast_mode"] = speed == "fast"
	snap["billing_mode"] = mode
	snap["duration_billable"] = in.DurationBillable
	snap["service_only"] = serviceOnly
	snap["is_free_model"] = isFree
	if rawUsageIsContainer(in.UpstreamUsage) {
		var raw any
		_ = json.Unmarshal(in.UpstreamUsage, &raw)
		snap["upstream_usage"] = raw
	} else {
		snap["upstream_usage"] = map[string]any{}
	}
	snap["server_side_tool_usage"] = nonNilMap(toolUsage)
	snap["native_tool_billing_enabled"] = rs.NativeToolBillingEnabled
	snap["native_tool_pricing_source"] = nativePricingSource(rs.NativeOverrides)
	snap["native_tool_billed_nanousd"] = nativeBilled
	if mcpSnap == nil {
		mcpSnap = []map[string]any{}
	}
	snap["mcp_tool_usage"] = mcpSnap
	snap["mcp_tool_billed_nanousd"] = mcpBilled
	snap["base_service_billed_nanousd"] = itemTotal - nativeBilled - mcpBilled
	if items == nil {
		items = []map[string]any{}
	}
	snap["service_items"] = items
	if in.MediaType != "" {
		snap["media_type"] = in.MediaType
		snap["input_image_count"] = in.InputImageCount
		snap["usage_source"] = in.UsageSource
	}
	if in.BilledReason != "" {
		snap["billed_reason"] = in.BilledReason
	}

	row := UsageLedger{
		UserID: in.UserID, RefNo: strings.TrimSpace(in.RefNo), ConversationID: in.ConversationID,
		ProviderProtocol: protocol, UpstreamName: in.UpstreamName, PlatformModelName: in.PlatformModelName,
		RoutedBindingCode: in.RoutedBindingCode, UpstreamModelName: in.UpstreamModelName, IsFreeModel: isFree,
		BillingAt: at, UsageDate: DateOf(at.In(s.loc)),
		InputTokens: clampNonNeg(in.InputTokens), CacheReadTokens: clampNonNeg(in.CacheReadTokens),
		CacheWriteTokens: clampNonNeg(mc.Split.Total), CacheWrite5mTokens: cw5, CacheWrite1hTokens: cw1h,
		OutputTokens: clampNonNeg(in.OutputTokens), ReasoningTokens: clampNonNeg(in.ReasoningTokens),
		CallCount: callCount, DurationSeconds: seconds, LatencyMS: clampNonNeg(in.LatencyMS),
		UsageSpeed: speed, ServiceTier: tier, BilledCurrency: currency, BilledNanousd: total,
	}
	if !mc.Split.Split {
		row.CacheWriteTokens = clampNonNeg(in.CacheWriteTokens)
	}
	return &LedgerDraft{Row: row, Snapshot: snap, ServiceOnly: serviceOnly}, nil
}

// insertLedger finalizes the snapshot and inserts the row.
func insertLedger(tx *gorm.DB, d *LedgerDraft) error {
	b, err := json.Marshal(d.Snapshot)
	if err != nil {
		return err
	}
	d.Row.PricingSnapshotJSON = string(b)
	return database.Translate(tx.Create(&d.Row).Error)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// small maps; insertion sort keeps this dependency free
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
