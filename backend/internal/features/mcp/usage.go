package mcp

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"
)

// UsageItem is a count of billable calls of one tool at one price.
type UsageItem struct {
	ServerID     uint   `json:"serverID"`
	ServerName   string `json:"serverName"`
	ToolName     string `json:"toolName"`
	Count        int64  `json:"callCount"`
	PriceNanoUSD int64  `json:"priceNanousd"`
}

type usageKey struct {
	server uint
	name   string
	tool   string
	price  int64
}

// AggregateUsage merges items by server id, case-insensitive trimmed server
// name, case-insensitive trimmed tool name and price (so different prices never
// merge), keeps first-seen order and drops zero counts. An empty result is nil.
func AggregateUsage(items []UsageItem) []UsageItem {
	var out []UsageItem
	index := map[usageKey]int{}
	for _, it := range items {
		if it.Count <= 0 {
			continue
		}
		k := usageKey{it.ServerID, strings.ToLower(strings.TrimSpace(it.ServerName)), strings.ToLower(strings.TrimSpace(it.ToolName)), it.PriceNanoUSD}
		if i, ok := index[k]; ok {
			out[i].Count += it.Count
			continue
		}
		index[k] = len(out)
		it.ServerName = strings.TrimSpace(it.ServerName)
		it.ToolName = strings.TrimSpace(it.ToolName)
		out = append(out, it)
	}
	return out
}

// ServiceLine is one billable service line for the billing area.
type ServiceLine struct {
	ServiceCode      string
	ServiceName      string
	PricingMode      string
	Calls            int64
	UnitPriceNanoUSD int64
	BilledNanoUSD    int64
	RateMultiplier   float64
}

// BillingPlan is what a run's MCP usage turns into.
type BillingPlan struct {
	// Lines are the charged lines (empty in billing mode "self").
	Lines []ServiceLine
	// TotalNanoUSD is the sum of billed amounts.
	TotalNanoUSD int64
	// Snapshot is the aggregated usage, stored with the usage record even when
	// nothing is charged.
	Snapshot []UsageItem
}

// SnapshotJSON encodes the snapshot ("[]" when there was no usage).
func (p BillingPlan) SnapshotJSON() string {
	if len(p.Snapshot) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(p.Snapshot)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func saturatingMul(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

func saturatingAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func orUnknown(s string) string {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return "unknown"
}

// PlanBilling converts usage into service lines. Items with a zero count or a
// zero price are not charged; in billing mode "self" nothing is charged. Free
// model exemptions deliberately do not apply: the caller must settle these
// lines even for free models.
func PlanBilling(usage []UsageItem, billingMode string) BillingPlan {
	plan := BillingPlan{Snapshot: AggregateUsage(usage)}
	if billingMode == "self" {
		return plan
	}
	for _, it := range plan.Snapshot {
		if it.Count <= 0 || it.PriceNanoUSD <= 0 {
			continue
		}
		name := ""
		switch srv, tool := strings.TrimSpace(it.ServerName), strings.TrimSpace(it.ToolName); {
		case srv != "" && tool != "":
			name = srv + " / " + tool
		case srv != "":
			name = srv
		case tool != "":
			name = tool
		default:
			name = "unknown"
		}
		line := ServiceLine{
			ServiceCode:      "mcp_tool." + orUnknown(it.ServerName) + "." + orUnknown(it.ToolName),
			ServiceName:      name,
			PricingMode:      "call",
			Calls:            it.Count,
			UnitPriceNanoUSD: it.PriceNanoUSD,
			BilledNanoUSD:    saturatingMul(it.Count, it.PriceNanoUSD),
			RateMultiplier:   1,
		}
		plan.Lines = append(plan.Lines, line)
		plan.TotalNanoUSD = saturatingAdd(plan.TotalNanoUSD, line.BilledNanoUSD)
	}
	return plan
}

// ---- model-facing output hygiene ----

const (
	omittedBinary    = "[binary data omitted]"
	opaqueStringMin  = 2048
	dataURLOmitFloor = 256
)

func looksOpaque(s string) bool {
	if strings.HasPrefix(s, "data:") && strings.Contains(s[:min(len(s), 128)], ";base64,") && len(s) > dataURLOmitFloor {
		return true
	}
	if len(s) < opaqueStringMin {
		return false
	}
	good := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=' || c == '-' || c == '_' {
			good++
		}
	}
	return good*100 >= len(s)*99
}

func sanitizeValue(v any) any {
	switch x := v.(type) {
	case string:
		if looksOpaque(x) {
			return omittedBinary
		}
	case []any:
		for i := range x {
			x[i] = sanitizeValue(x[i])
		}
	case map[string]any:
		typ, _ := x["type"].(string)
		binaryItem := typ == "image" || typ == "audio"
		for k, e := range x {
			if s, ok := e.(string); ok && (k == "blob" || (binaryItem && k == "data")) && len(s) > 64 {
				x[k] = omittedBinary
				continue
			}
			x[k] = sanitizeValue(e)
		}
	}
	return v
}

// SanitizeToolOutput removes opaque payloads (base64 blobs, data URLs, image and
// audio content) from a tool result before the model sees it.
func SanitizeToolOutput(text string) string {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		if looksOpaque(strings.TrimSpace(text)) {
			return omittedBinary
		}
		return text
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sanitizeValue(v)); err != nil {
		return text
	}
	return strings.TrimSpace(buf.String())
}

// TrimHeadTail shortens text to about max runes by keeping its beginning and
// its end.
func TrimHeadTail(text string, max int) string { return trimHeadTail(text, max) }

func trimHeadTail(text string, max int) string {
	if max <= 0 || utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	head := max / 2
	tail := max - head
	return string(runes[:head]) + "\n...[trimmed]...\n" + string(runes[len(runes)-tail:])
}

// ---- guidance ----

const defaultGuidance = `# tool_use

You can call external tools. Follow these rules.

- Follow each tool's declared input schema exactly; do not invent parameters.
- Use a tool only when the answer needs external, real-time or private data, or when the user explicitly asks for it. Answer from your own knowledge otherwise.
- Be economical: make the fewest calls that settle the question and do not repeat a call whose result you already have.
- If a call fails, do not retry it with identical arguments. Change the input or continue without it.
- When tools fail or return too little, say plainly which information is missing instead of guessing.
- Do not show raw tool output or internal details such as tool names, identifiers or errors unless the user asks for them.`

func guidanceText(custom string) string {
	if c := strings.TrimSpace(custom); c != "" {
		return c
	}
	return defaultGuidance
}
