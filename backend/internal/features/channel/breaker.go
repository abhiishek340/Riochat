package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/clock"
)

const (
	probeTTL         = 30 * time.Second
	untilGrace       = 30 * time.Second
	manualOpenFor    = 24 * time.Hour
	genKey           = "cb:generation"
	genTTL           = 30 * 24 * time.Hour
	metaTTL          = 7 * 24 * time.Hour
	backoffCountTTL  = 5 * time.Minute
	keyCounterTTL    = 24 * time.Hour
	maxBreakerWindow = 24 * time.Hour
)

// circuitStore keeps circuit-breaker state, rate-limit back-off and the
// round-robin counters in the shared cache. Operations are serialized inside
// the process; across instances the cache's own atomic primitives are used
// where they exist (probe tokens).
type circuitStore struct {
	kv    cache.KV
	clock clock.Clock

	mu      sync.Mutex
	counter sync.Map // upstream id -> *int64 fallback round-robin counters
}

func newCircuitStore(kv cache.KV, clk clock.Clock) *circuitStore {
	return &circuitStore{kv: kv, clock: clk}
}

func upstreamPrefix(id uint) string { return fmt.Sprintf("cb:u:%d", id) }

// bindingKey is the circuit identity of an upstream model.
func bindingKey(code string) string {
	return "upstream-model-" + strings.ReplaceAll(code, ":", "-")
}

func bindingPrefix(upstreamID uint, code string) string {
	return fmt.Sprintf("cb:u:%d:m:%s", upstreamID, bindingKey(code))
}

func (s *circuitStore) nowMS() int64 { return s.clock.Now().UnixMilli() }

// circuitState is the observed state of one circuit.
type circuitState struct {
	Open         bool
	HalfOpen     bool
	ProbeGranted bool
	UntilSec     int64
}

func (s *circuitStore) generation(ctx context.Context) (int64, error) {
	raw, found, err := s.kv.Get(ctx, genKey)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	n, _ := strconv.ParseInt(string(raw), 10, 64)
	return n, nil
}

func parsePair(raw []byte) (int64, int64) {
	parts := strings.SplitN(string(raw), ":", 2)
	a, _ := strconv.ParseInt(parts[0], 10, 64)
	var b int64
	if len(parts) == 2 {
		b, _ = strconv.ParseInt(parts[1], 10, 64)
	}
	return a, b
}

// readGuarded reads a "<value>:<generation>" entry; entries of an older
// generation (before a global reset) are treated as absent. Entries without a
// generation part (written by another implementation) count as current.
func (s *circuitStore) readGuarded(ctx context.Context, key string, gen int64) (int64, bool, error) {
	raw, found, err := s.kv.Get(ctx, key)
	if err != nil || !found {
		return 0, false, err
	}
	v, g := parsePair(raw)
	if strings.Contains(string(raw), ":") && g != gen {
		return 0, false, nil
	}
	return v, true, nil
}

// check reports the state of a circuit and, for a half-open circuit, tries to
// take the single probe token.
func (s *circuitStore) check(ctx context.Context, prefix string) (circuitState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen, err := s.generation(ctx)
	if err != nil {
		return circuitState{}, err
	}
	now := s.nowMS()
	untilSec, hasUntil, err := s.readGuarded(ctx, prefix+":until", gen)
	if err != nil {
		return circuitState{}, err
	}
	openMS, hasOpen, err := s.readGuarded(ctx, prefix+":open", gen)
	if err != nil {
		return circuitState{}, err
	}
	if hasOpen && openMS > now {
		return circuitState{Open: true, UntilSec: untilSec}, nil
	}
	if !hasUntil {
		return circuitState{}, nil
	}
	untilMS := untilSec * 1000
	if untilMS > now {
		return circuitState{Open: true, UntilSec: untilSec}, nil
	}
	if untilMS+untilGrace.Milliseconds() <= now {
		return circuitState{}, nil // fully closed again
	}
	// Half-open: one caller gets the probe.
	st := circuitState{HalfOpen: true, UntilSec: untilSec}
	probeKey := prefix + ":probe"
	val := []byte(fmt.Sprintf("%d:%d", now+probeTTL.Milliseconds(), gen))
	ok, err := s.kv.SetNX(ctx, probeKey, val, probeTTL)
	if err != nil {
		return circuitState{}, err
	}
	if !ok {
		raw, found, err := s.kv.Get(ctx, probeKey)
		if err != nil {
			return circuitState{}, err
		}
		if found {
			exp, _ := parsePair(raw)
			if exp <= now { // stale token: take it over
				if err := s.kv.Set(ctx, probeKey, val, probeTTL); err != nil {
					return circuitState{}, err
				}
				ok = true
			}
		} else {
			ok, err = s.kv.SetNX(ctx, probeKey, val, probeTTL)
			if err != nil {
				return circuitState{}, err
			}
		}
	}
	st.ProbeGranted = ok
	return st, nil
}

// peek reports whether a circuit is currently open without taking a probe.
func (s *circuitStore) peekOpen(ctx context.Context, prefix string) (bool, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen, err := s.generation(ctx)
	if err != nil {
		return false, 0, err
	}
	now := s.nowMS()
	untilSec, hasUntil, err := s.readGuarded(ctx, prefix+":until", gen)
	if err != nil {
		return false, 0, err
	}
	openMS, hasOpen, err := s.readGuarded(ctx, prefix+":open", gen)
	if err != nil {
		return false, 0, err
	}
	if hasOpen && openMS > now {
		return true, untilSec, nil
	}
	if hasUntil && untilSec*1000 > now {
		return true, untilSec, nil
	}
	return false, untilSec, nil
}

// trip opens a circuit for the duration.
func (s *circuitStore) tripLocked(ctx context.Context, prefix string, duration time.Duration) error {
	return s.writeOpenLocked(ctx, prefix, duration, untilGrace)
}

// writeOpenLocked writes the open marker (TTL = duration) and the until stamp (TTL
// = duration + grace). A zero grace makes the circuit close the moment the
// open period ends, with no half-open probe phase (manual opens).
func (s *circuitStore) writeOpenLocked(ctx context.Context, prefix string, duration, grace time.Duration) error {
	gen, err := s.generation(ctx)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	untilMS := now.Add(duration).UnixMilli()
	untilSec := now.Add(duration).Unix()
	if err := s.kv.Set(ctx, prefix+":open", []byte(fmt.Sprintf("%d:%d", untilMS, gen)), duration); err != nil {
		return err
	}
	if err := s.kv.Set(ctx, prefix+":until", []byte(fmt.Sprintf("%d:%d", untilSec, gen)), duration+grace); err != nil {
		return err
	}
	if gen > 0 {
		_ = s.kv.Set(ctx, genKey, []byte(strconv.FormatInt(gen, 10)), genTTL)
	}
	return nil
}

// openManual opens a circuit for 24 hours.
func (s *circuitStore) openManual(ctx context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeOpenLocked(ctx, prefix, manualOpenFor, 0)
}

// reset clears the open, until, probe and fails keys of one circuit.
func (s *circuitStore) reset(ctx context.Context, prefix string, withMeta bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := []string{prefix + ":open", prefix + ":until", prefix + ":probe", prefix + ":fails"}
	if withMeta {
		keys = append(keys, prefix+":last_error", prefix+":last_failure_at", prefix+":last_success_at")
	}
	return s.kv.Delete(ctx, keys...)
}

// resetAll voids every circuit's state by bumping the generation; metadata and
// back-off entries survive.
func (s *circuitStore) resetAll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.kv.Incr(ctx, genKey, genTTL)
	return err
}

// releaseProbe gives back a probe token granted to a request.
func (s *circuitStore) releaseProbe(ctx context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kv.Delete(ctx, prefix+":probe")
}

type failWindow struct {
	Gen int64   `json:"g"`
	At  []int64 `json:"t"`
}

// consumeProbeLocked removes the probe token and reports whether one was held.
func (s *circuitStore) consumeProbeLocked(ctx context.Context, prefix string, gen int64) (bool, error) {
	raw, found, err := s.kv.GetDel(ctx, prefix+":probe")
	if err != nil || !found {
		return false, err
	}
	exp, g := parsePair(raw)
	return exp > s.nowMS() && g == gen, nil
}

// addFailureLocked records a failure in the sliding window and returns the
// in-window count.
func (s *circuitStore) addFailureLocked(ctx context.Context, prefix string, gen int64, window time.Duration) (int, error) {
	key := prefix + ":fails"
	now := s.nowMS()
	var w failWindow
	if raw, found, err := s.kv.Get(ctx, key); err != nil {
		return 0, err
	} else if found {
		_ = json.Unmarshal(raw, &w)
	}
	if w.Gen != gen {
		w = failWindow{Gen: gen}
	}
	cut := now - window.Milliseconds()
	kept := w.At[:0]
	for _, t := range w.At {
		if t > cut {
			kept = append(kept, t)
		}
	}
	w.At = append(kept, now)
	raw, _ := json.Marshal(w)
	if err := s.kv.Set(ctx, key, raw, window+time.Minute); err != nil {
		return 0, err
	}
	return len(w.At), nil
}

// breakerLevel carries the effective thresholds of one circuit level.
type breakerLevel struct {
	Threshold int
	Window    time.Duration
	Duration  time.Duration
}

// upstreamLevel adds the model-count rule of the upstream level.
type upstreamLevel struct {
	breakerLevel
	ModelThreshold int
	Logic          string
}

// failureOutcome reports what a recorded failure did.
type failureOutcome struct {
	BindingTripped  bool
	UpstreamTripped bool
}

// recordFailure records a circuit failure for a binding and its upstream.
// activeBindings are the circuit prefixes of all active bindings of the
// upstream (used by the model-count rule); the failing binding is always part
// of it.
func (s *circuitStore) recordFailure(ctx context.Context, upPrefix, bindPrefix string, binding breakerLevel, up upstreamLevel, activeBindings []string) (failureOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out failureOutcome
	gen, err := s.generation(ctx)
	if err != nil {
		return out, err
	}

	if bindPrefix != "" {
		probeFailed, err := s.consumeProbeLocked(ctx, bindPrefix, gen)
		if err != nil {
			return out, err
		}
		count, err := s.addFailureLocked(ctx, bindPrefix, gen, clampWindow(binding.Window))
		if err != nil {
			return out, err
		}
		trip := (binding.Threshold > 0 && count >= binding.Threshold) || probeFailed
		if trip && binding.Duration > 0 {
			if err := s.tripLocked(ctx, bindPrefix, binding.Duration); err != nil {
				return out, err
			}
			out.BindingTripped = true
		}
	}

	probeFailed, err := s.consumeProbeLocked(ctx, upPrefix, gen)
	if err != nil {
		return out, err
	}
	count, err := s.addFailureLocked(ctx, upPrefix, gen, clampWindow(up.Window))
	if err != nil {
		return out, err
	}
	failCond := up.Threshold > 0 && count >= up.Threshold
	openCount := 0
	if up.ModelThreshold > 0 {
		list := activeBindings
		if bindPrefix != "" && !containsString(list, bindPrefix) {
			list = append(append([]string(nil), list...), bindPrefix)
		}
		for _, p := range list {
			open, err := s.openLocked(ctx, p, gen)
			if err != nil {
				return out, err
			}
			if open {
				openCount++
			}
		}
	}
	modelCond := up.ModelThreshold > 0 && openCount >= up.ModelThreshold
	trip := probeFailed
	if !trip {
		if up.Logic == "and" {
			var conds []bool
			if up.Threshold > 0 {
				conds = append(conds, failCond)
			}
			if up.ModelThreshold > 0 {
				conds = append(conds, modelCond)
			}
			trip = len(conds) > 0
			for _, c := range conds {
				trip = trip && c
			}
		} else {
			trip = failCond || modelCond
		}
	}
	if trip && up.Duration > 0 {
		if err := s.tripLocked(ctx, upPrefix, up.Duration); err != nil {
			return out, err
		}
		out.UpstreamTripped = true
	}
	return out, nil
}

func clampWindow(w time.Duration) time.Duration {
	if w <= 0 {
		return time.Minute
	}
	if w > maxBreakerWindow {
		return maxBreakerWindow
	}
	return w
}

func (s *circuitStore) openLocked(ctx context.Context, prefix string, gen int64) (bool, error) {
	now := s.nowMS()
	openMS, hasOpen, err := s.readGuarded(ctx, prefix+":open", gen)
	if err != nil {
		return false, err
	}
	if hasOpen && openMS > now {
		return true, nil
	}
	untilSec, hasUntil, err := s.readGuarded(ctx, prefix+":until", gen)
	if err != nil {
		return false, err
	}
	return hasUntil && untilSec*1000 > now, nil
}

// clearOnSuccess clears circuit keys of a scope whose probe succeeded.
func (s *circuitStore) clearScope(ctx context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kv.Delete(ctx, prefix+":open", prefix+":until", prefix+":probe", prefix+":fails")
}

func (s *circuitStore) setMeta(ctx context.Context, prefix, name, value string) error {
	return s.kv.Set(ctx, prefix+":"+name, []byte(value), metaTTL)
}

// ---- rate-limit back-off ----------------------------------------------------

// backoffConfig is rate_limit.defaults.
type backoffConfig struct {
	BaseSec    int     `json:"backoff_base_sec"`
	MaxSec     int     `json:"backoff_max_sec"`
	Multiplier float64 `json:"backoff_multiplier"`
}

var defaultBackoff = backoffConfig{BaseSec: 5, MaxSec: 60, Multiplier: 2}

func (c backoffConfig) normalized() backoffConfig {
	if c.BaseSec <= 0 {
		c.BaseSec = defaultBackoff.BaseSec
	}
	if c.MaxSec <= 0 {
		c.MaxSec = defaultBackoff.MaxSec
	}
	if c.Multiplier <= 1 {
		c.Multiplier = defaultBackoff.Multiplier
	}
	if c.BaseSec > c.MaxSec {
		c.BaseSec = c.MaxSec
	}
	return c
}

func backoffPrefix(upstreamID, routeID uint) string {
	return fmt.Sprintf("rl:u:%d:r:%d", upstreamID, routeID)
}

// recordBackoff notes an upstream 429 for one route and returns the wait.
func (s *circuitStore) recordBackoff(ctx context.Context, upstreamID, routeID uint, cfg backoffConfig, retryAfter time.Duration) (time.Duration, error) {
	cfg = cfg.normalized()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := backoffPrefix(upstreamID, routeID)
	count := int64(0)
	if raw, found, err := s.kv.Get(ctx, p+":backoff_count"); err != nil {
		return 0, err
	} else if found {
		count, _ = strconv.ParseInt(string(raw), 10, 64)
	}
	count++
	if err := s.kv.Set(ctx, p+":backoff_count", []byte(strconv.FormatInt(count, 10)), backoffCountTTL); err != nil {
		return 0, err
	}
	wait := float64(cfg.BaseSec)
	for i := int64(1); i < count && wait < float64(cfg.MaxSec); i++ {
		wait *= cfg.Multiplier
	}
	if wait > float64(cfg.MaxSec) {
		wait = float64(cfg.MaxSec)
	}
	d := time.Duration(wait * float64(time.Second))
	if retryAfter < 0 {
		retryAfter = 0
	}
	if max := time.Duration(cfg.MaxSec) * time.Second; retryAfter > max {
		retryAfter = max
	}
	if retryAfter > d {
		d = retryAfter
	}
	until := s.clock.Now().Add(d).UnixMilli()
	if err := s.kv.Set(ctx, p+":backoff", []byte(strconv.FormatInt(until, 10)), d); err != nil {
		return 0, err
	}
	return d, nil
}

// backoffRemaining returns the remaining wait of a route (0 when none).
func (s *circuitStore) backoffRemaining(ctx context.Context, upstreamID, routeID uint) (time.Duration, error) {
	raw, found, err := s.kv.Get(ctx, backoffPrefix(upstreamID, routeID)+":backoff")
	if err != nil || !found {
		return 0, err
	}
	until, _ := strconv.ParseInt(string(raw), 10, 64)
	rem := time.Duration(until-s.nowMS()) * time.Millisecond
	if rem <= 0 {
		return 0, nil
	}
	return rem, nil
}

func (s *circuitStore) clearBackoff(ctx context.Context, upstreamID, routeID uint) error {
	p := backoffPrefix(upstreamID, routeID)
	return s.kv.Delete(ctx, p+":backoff", p+":backoff_count")
}

// nextKeyCounter increments the shared round-robin counter of an upstream; when
// the store is unavailable a per-process counter is used.
func (s *circuitStore) nextKeyCounter(ctx context.Context, upstreamID uint) int64 {
	n, err := s.kv.Incr(ctx, fmt.Sprintf("llm:u:%d:key_counter", upstreamID), keyCounterTTL)
	if err == nil {
		return n - 1
	}
	v, _ := s.counter.LoadOrStore(upstreamID, new(int64))
	p := v.(*int64)
	s.mu.Lock()
	defer s.mu.Unlock()
	*p++
	return *p - 1
}

func (s *circuitStore) forgetCounter(upstreamID uint) { s.counter.Delete(upstreamID) }
