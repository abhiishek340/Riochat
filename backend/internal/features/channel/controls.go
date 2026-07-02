package channel

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	controlIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
	controlIconRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
)

// forbiddenControlRoots are request fields owned by the system; a control may
// not write below any of them.
var forbiddenControlRoots = []string{
	"model", "messages", "input", "instructions", "prompt", "system", "systemInstruction", "system_instruction",
	"headers", "api_key", "apiKey", "base_url", "baseURL", "stream", "stream_options", "previous_response_id",
	"prompt_cache_key", "prompt_cache_options", "prompt_cache_breakpoint", "prompt_cache_retention",
	"tools", "tool_choice", "functions", "function_call", "contents",
}

func forbiddenControlPath(path string) bool {
	for _, root := range forbiddenControlRoots {
		if path == root || strings.HasPrefix(path, root+".") {
			return true
		}
	}
	return false
}

// ControlOption is one choice of a select control.
type ControlOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`

	patch   map[string]any
	patches map[string]map[string]any
}

// Control is a user-facing model control as served to clients. Parameter
// patches stay internal.
type Control struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Type        string          `json:"type"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Icon        string          `json:"icon"`
	Placement   string          `json:"placement"`
	Default     *string         `json:"default"`
	Locked      bool            `json:"locked"`
	Options     []ControlOption `json:"options"`
	Min         *float64        `json:"min"`
	Max         *float64        `json:"max"`
	Step        *float64        `json:"step"`
	Integer     bool            `json:"integer"`
	Protocols   []string        `json:"protocols"`

	hidden bool
	path   string
	on     ControlOption
	off    ControlOption
}

// Patch returns the request-option patch that selecting a value applies on a
// protocol (the protocol-specific patch wins over the general one).
func (c *Control) Patch(value, protocol string) map[string]any {
	pk := protocolKey(protocol)
	pick := func(o ControlOption) map[string]any {
		if p, ok := o.patches[pk]; ok {
			return p
		}
		if p, ok := o.patches[protocol]; ok {
			return p
		}
		return o.patch
	}
	switch c.Type {
	case "select":
		for _, o := range c.Options {
			if o.Value == value {
				return pick(o)
			}
		}
	case "toggle":
		if value == "on" {
			return pick(c.on)
		}
		return pick(c.off)
	case "number":
		if c.path == "" {
			return nil
		}
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil
		}
		return nestedPatch(c.path, f)
	}
	return nil
}

func nestedPatch(path string, v any) map[string]any {
	segs := strings.Split(path, ".")
	root := map[string]any{}
	cur := root
	for i, s := range segs {
		if i == len(segs)-1 {
			cur[s] = v
			break
		}
		next := map[string]any{}
		cur[s] = next
		cur = next
	}
	return root
}

// NormalizeValue normalizes a client-supplied value for the control; invalid
// values fall back to the default ("" when there is none).
func (c *Control) NormalizeValue(raw any) string {
	def := ""
	if c.Default != nil {
		def = *c.Default
	}
	switch c.Type {
	case "select":
		s, ok := raw.(string)
		if !ok {
			return def
		}
		s = strings.TrimSpace(s)
		if c.Kind == "reasoning" {
			s = strings.ToLower(s)
		}
		for _, o := range c.Options {
			if o.Value == s {
				return s
			}
		}
		return def
	case "toggle":
		switch t := raw.(type) {
		case bool:
			if t {
				return "on"
			}
			return "off"
		case string:
			switch strings.ToLower(strings.TrimSpace(t)) {
			case "on", "true":
				return "on"
			case "off", "false":
				return "off"
			}
		}
		return def
	case "number":
		var f float64
		switch t := raw.(type) {
		case float64:
			f = t
		case int:
			f = float64(t)
		case json.Number:
			v, err := t.Float64()
			if err != nil {
				return def
			}
			f = v
		case string:
			v, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			if err != nil {
				return def
			}
			f = v
		default:
			return def
		}
		if !c.numberOK(f) {
			return def
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return def
}

func (c *Control) numberOK(f float64) bool {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	if c.Min != nil && f < *c.Min {
		return false
	}
	if c.Max != nil && f > *c.Max {
		return false
	}
	if c.Integer && f != math.Trunc(f) {
		return false
	}
	if c.Step != nil && *c.Step > 0 {
		base := 0.0
		if c.Min != nil {
			base = *c.Min
		}
		q := (f - base) / *c.Step
		if math.Abs(q-math.Round(q)) > 1e-9 {
			return false
		}
	}
	return true
}

// ---- parsing ---------------------------------------------------------------

func floatOf(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

func boolOf(v any) bool { b, _ := v.(bool); return b }

func ctrlErr(forbidden bool) error {
	if forbidden {
		return ErrControlForbidden
	}
	return ErrControlsInvalid
}

// checkPatch validates a patch object: it must be a JSON object whose top-level
// keys do not start with a system-owned root.
func checkPatch(v any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, ErrControlsInvalid
	}
	if err := checkPatchKeys(m, ""); err != nil {
		return nil, err
	}
	return m, nil
}

func checkPatchKeys(m map[string]any, prefix string) error {
	for k := range m {
		if prefix == "" && forbiddenControlPath(k) {
			return ErrControlForbidden
		}
		if k == "" {
			return ErrControlsInvalid
		}
	}
	return nil
}

func parseOptionBody(v any, withValue bool) (ControlOption, error) {
	var o ControlOption
	m, ok := v.(map[string]any)
	if !ok {
		return o, ErrControlsInvalid
	}
	if withValue {
		val, ok := m["value"].(string)
		if !ok || strings.TrimSpace(val) == "" || runeLen(val) > 64 {
			return o, ErrControlsInvalid
		}
		o.Value = strings.TrimSpace(val)
	}
	o.Label = strOf(m["label"])
	o.Description = strOf(m["description"])
	if runeLen(o.Label) > 64 || runeLen(o.Description) > 240 {
		return o, ErrControlsInvalid
	}
	var err error
	if o.patch, err = checkPatch(m["patch"]); err != nil {
		return o, err
	}
	if raw, has := m["patches"]; has && raw != nil {
		pm, ok := raw.(map[string]any)
		if !ok {
			return o, ErrControlsInvalid
		}
		o.patches = map[string]map[string]any{}
		for k, pv := range pm {
			p, err := checkPatch(pv)
			if err != nil {
				return o, err
			}
			o.patches[k] = p
		}
	}
	return o, nil
}

// parseControl validates one explicit control entry.
func parseControl(v any) (*Control, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, ErrControlsInvalid
	}
	c := &Control{Kind: "generic", Placement: "menu", Protocols: []string{}}
	if k, has := m["kind"]; has {
		ks, _ := k.(string)
		ks = lower(ks)
		if ks != "generic" && ks != "reasoning" {
			return nil, ErrControlsInvalid
		}
		c.Kind = ks
	}
	id := strOf(m["id"])
	if c.Kind == "reasoning" {
		id = "reasoning"
		c.Placement = "toolbar"
	}
	if !controlIDRe.MatchString(id) {
		return nil, ErrControlsInvalid
	}
	c.ID = id
	c.Type = lower(strOf(m["type"]))
	if c.Kind == "reasoning" {
		c.Type = "select"
	}
	if c.Type != "select" && c.Type != "toggle" && c.Type != "number" {
		return nil, ErrControlsInvalid
	}
	c.Label, c.Description = strOf(m["label"]), strOf(m["description"])
	if runeLen(c.Label) > 64 || runeLen(c.Description) > 240 {
		return nil, ErrControlsInvalid
	}
	if icon := strOf(m["icon"]); icon != "" {
		if !controlIconRe.MatchString(icon) {
			return nil, ErrControlsInvalid
		}
		c.Icon = icon
	}
	if p, has := m["placement"]; has {
		ps := lower(strOf(p))
		if ps != "toolbar" && ps != "menu" {
			return nil, ErrControlsInvalid
		}
		c.Placement = ps
	}
	if raw, has := m["protocols"]; has && raw != nil {
		arr, ok := raw.([]any)
		if !ok {
			return nil, ErrControlsInvalid
		}
		for _, e := range arr {
			s, ok := e.(string)
			if !ok || strings.TrimSpace(s) == "" || len(s) > 64 {
				return nil, ErrControlsInvalid
			}
			c.Protocols = append(c.Protocols, protocolKey(s))
		}
	}
	c.Locked, c.hidden = boolOf(m["locked"]), boolOf(m["hidden"])

	var defStr *string
	if d, has := m["default"]; has && d != nil {
		switch t := d.(type) {
		case string:
			defStr = ptr(strings.TrimSpace(t))
		case bool:
			if t {
				defStr = ptr("on")
			} else {
				defStr = ptr("off")
			}
		case json.Number:
			defStr = ptr(t.String())
		default:
			return nil, ErrControlsInvalid
		}
	}

	switch c.Type {
	case "select":
		rawOpts, has := m["options"]
		if c.Kind == "reasoning" && !has {
			c.Default = defStr
			return c, nil // presentation override of the generated control
		}
		arr, ok := rawOpts.([]any)
		if !ok || len(arr) < 1 || len(arr) > 32 {
			return nil, ErrControlsInvalid
		}
		seen := map[string]bool{}
		for _, e := range arr {
			o, err := parseOptionBody(e, true)
			if err != nil {
				return nil, err
			}
			if seen[o.Value] {
				return nil, ErrControlsInvalid
			}
			seen[o.Value] = true
			if c.Kind == "reasoning" {
				o.Value = strings.ToLower(o.Value)
				if o.Value != "auto" && !isCanonicalLevel(o.Value) {
					return nil, ErrControlsInvalid
				}
			}
			c.Options = append(c.Options, o)
		}
		if defStr != nil && !seen[*defStr] {
			return nil, ErrControlsInvalid
		}
		c.Default = defStr
	case "toggle":
		var err error
		if c.on, err = parseOptionBody(orEmpty(m["on"]), false); err != nil {
			return nil, err
		}
		if c.off, err = parseOptionBody(orEmpty(m["off"]), false); err != nil {
			return nil, err
		}
		c.on.Value, c.off.Value = "on", "off"
		c.Options = []ControlOption{c.on, c.off}
		if defStr == nil {
			defStr = ptr("off")
		}
		if *defStr != "on" && *defStr != "off" {
			return nil, ErrControlsInvalid
		}
		c.Default = defStr
	case "number":
		path := strOf(m["path"])
		if path == "" {
			return nil, ErrControlsInvalid
		}
		for _, seg := range strings.Split(path, ".") {
			if seg == "" {
				return nil, ErrControlsInvalid
			}
		}
		if forbiddenControlPath(path) {
			return nil, ErrControlForbidden
		}
		c.path = path
		if x, has := m["min"]; has && x != nil {
			f, ok := floatOf(x)
			if !ok {
				return nil, ErrControlsInvalid
			}
			c.Min = &f
		}
		if x, has := m["max"]; has && x != nil {
			f, ok := floatOf(x)
			if !ok {
				return nil, ErrControlsInvalid
			}
			c.Max = &f
		}
		if x, has := m["step"]; has && x != nil {
			f, ok := floatOf(x)
			if !ok || f <= 0 {
				return nil, ErrControlsInvalid
			}
			c.Step = &f
		}
		c.Integer = boolOf(m["integer"])
		if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
			return nil, ErrControlsInvalid
		}
		if defStr != nil {
			f, err := strconv.ParseFloat(*defStr, 64)
			if err != nil || !c.numberOK(f) {
				return nil, ErrControlsInvalid
			}
			defStr = ptr(strconv.FormatFloat(f, 'f', -1, 64))
		}
		c.Default = defStr
	}
	return c, nil
}

func orEmpty(v any) any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

// parseControls validates the `controls` array of a capabilities document.
func parseControls(caps capsMap) ([]*Control, error) {
	raw, has := caps["controls"]
	if !has || raw == nil {
		return nil, nil
	}
	arr, ok := raw.([]any)
	if !ok || len(arr) > 32 {
		return nil, ErrControlsInvalid
	}
	seen := map[string]bool{}
	var out []*Control
	for _, e := range arr {
		c, err := parseControl(e)
		if err != nil {
			return nil, err
		}
		if seen[c.ID] {
			return nil, ErrControlsInvalid
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, nil
}

func validateControlsDeclaration(caps capsMap) error {
	_, err := parseControls(caps)
	return err
}

// ---- effective controls ------------------------------------------------------
