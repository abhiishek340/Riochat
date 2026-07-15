package channel

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

// breakerDefaults are the global circuit-breaker parameters
// (setting circuit_breaker.defaults).
type breakerDefaults struct {
	Enabled                  bool
	ModelFailureThreshold    int
	ModelDurationMin         int
	ModelWindowMin           int
	UpstreamFailureThreshold int
	UpstreamModelThreshold   int
	UpstreamThresholdLogic   string
	UpstreamDurationMin      int
	UpstreamWindowMin        int
}

var builtinBreakerDefaults = breakerDefaults{
	Enabled: false, ModelFailureThreshold: 5, ModelDurationMin: 15, ModelWindowMin: 3,
	UpstreamFailureThreshold: 20, UpstreamModelThreshold: 3, UpstreamThresholdLogic: "or",
	UpstreamDurationMin: 30, UpstreamWindowMin: 5,
}

const defaultBreakerJSON = `{"enabled":false,"model_failure_threshold":5,"model_duration_min":15,"model_window_min":3,"upstream_failure_threshold":20,"upstream_model_threshold":3,"upstream_threshold_logic":"or","upstream_duration_min":30,"upstream_window_min":5}`

var breakerKeys = []string{
	"enabled", "model_failure_threshold", "model_duration_min", "model_window_min", "upstream_failure_threshold",
	"upstream_model_threshold", "upstream_threshold_logic", "upstream_duration_min", "upstream_window_min",
}

// maxMinutes keeps minute values convertible to seconds without overflow.
const maxMinutes = math.MaxInt32 / 60

// parseBreakerDefaults parses the stored document. A missing or zero number
// keeps the built-in default; a non-empty logic overrides.
func parseBreakerDefaults(raw string) (breakerDefaults, error) {
	out := builtinBreakerDefaults
	dec := json.NewDecoder(bytes.NewReader([]byte(strings.TrimSpace(raw))))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return out, ErrInvalidJSON
	}
	for _, k := range breakerKeys {
		if v, ok := doc[k]; ok && v == nil {
			return out, ErrInvalidJSON
		}
	}
	intField := func(key string, minutes bool, dst *int) error {
		v, ok := doc[key]
		if !ok {
			return nil
		}
		n, ok := v.(json.Number)
		if !ok {
			return ErrInvalidJSON
		}
		i, err := n.Int64()
		if err != nil || i < 0 || i > math.MaxInt32 || (minutes && i > maxMinutes) {
			return ErrInvalidJSON
		}
		if i > 0 {
			*dst = int(i)
		}
		return nil
	}
	if v, ok := doc["enabled"]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return out, ErrInvalidJSON
		}
		out.Enabled = b
	}
	fields := []struct {
		key     string
		minutes bool
		dst     *int
	}{
		{"model_failure_threshold", false, &out.ModelFailureThreshold},
		{"model_duration_min", true, &out.ModelDurationMin},
		{"model_window_min", true, &out.ModelWindowMin},
		{"upstream_failure_threshold", false, &out.UpstreamFailureThreshold},
		{"upstream_model_threshold", false, &out.UpstreamModelThreshold},
		{"upstream_duration_min", true, &out.UpstreamDurationMin},
		{"upstream_window_min", true, &out.UpstreamWindowMin},
	}
	for _, f := range fields {
		if err := intField(f.key, f.minutes, f.dst); err != nil {
			return out, err
		}
	}
	if v, ok := doc["upstream_threshold_logic"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return out, ErrInvalidJSON
		}
		switch strings.TrimSpace(s) {
		case "":
		case "and", "or":
			out.UpstreamThresholdLogic = strings.TrimSpace(s)
		default:
			return out, ErrInvalidJSON
		}
	}
	return out, nil
}

// breakerEnabledIn reads only the enabled flag of a stored document; an
// unusable document counts as disabled (so it can always be repaired).
func breakerEnabledIn(raw string) bool {
	d, err := parseBreakerDefaults(raw)
	return err == nil && d.Enabled
}

// errorClassification is circuit_breaker.error_classification.
type errorClassification struct {
	CircuitErrors   []string `json:"circuit_errors"`
	RateLimitErrors []string `json:"rate_limit_errors"`
	IgnoreErrors    []string `json:"ignore_errors"`
}

var defaultClassification = errorClassification{
	CircuitErrors:   []string{"5xx", "timeout", "connection_error"},
	RateLimitErrors: []string{"429"},
	IgnoreErrors:    []string{"4xx"},
}

// parseClassification reads the stored classification; missing keys, invalid
// JSON and empty lists fall back to the defaults (per list).
func parseClassification(raw string) errorClassification {
	out := defaultClassification
	var doc errorClassification
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &doc) != nil {
		return out
	}
	if len(doc.CircuitErrors) > 0 {
		out.CircuitErrors = doc.CircuitErrors
	}
	if len(doc.RateLimitErrors) > 0 {
		out.RateLimitErrors = doc.RateLimitErrors
	}
	if len(doc.IgnoreErrors) > 0 {
		out.IgnoreErrors = doc.IgnoreErrors
	}
	return out
}

func listHas(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), v) {
			return true
		}
	}
	return false
}

// parseBackoff reads rate_limit.defaults, keeping defaults for non-positive or
// missing fields and for invalid JSON.
func parseBackoff(raw string) backoffConfig {
	cfg := defaultBackoff
	var doc backoffConfig
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &doc) != nil {
		return cfg
	}
	if doc.BaseSec > 0 {
		cfg.BaseSec = doc.BaseSec
	}
	if doc.MaxSec > 0 {
		cfg.MaxSec = doc.MaxSec
	}
	if doc.Multiplier > 0 {
		cfg.Multiplier = doc.Multiplier
	}
	return cfg
}
