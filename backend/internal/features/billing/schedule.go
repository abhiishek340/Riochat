package billing

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	minutesPerDay  = 24 * 60
	minutesPerWeek = 7 * minutesPerDay
	maxPeriods     = 32
)

// SchedulePeriod is one time-of-day rate window.
type SchedulePeriod struct {
	Name        string `json:"name"`
	Weekdays    []int  `json:"weekdays"`
	Start       string `json:"start"`
	End         string `json:"end"`
	RatePercent int    `json:"ratePercent"`
}

// ScheduleConfig is the parsed schedulePricingJSON.
type ScheduleConfig struct {
	Periods []SchedulePeriod `json:"periods"`
}

func parseClock(s string) (int, error) {
	s = strings.TrimSpace(s)
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) == 0 || len(h) > 2 || len(m) != 2 {
		return 0, errors.New("time must be HH:MM")
	}
	var hh, mm int
	if _, err := fmt.Sscanf(h, "%d", &hh); err != nil {
		return 0, err
	}
	if _, err := fmt.Sscanf(m, "%d", &mm); err != nil {
		return 0, err
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, errors.New("time out of range")
	}
	return hh*60 + mm, nil
}

func fmtClock(min int) string { return fmt.Sprintf("%02d:%02d", min/60, min%60) }

// span returns the first minute-of-week of the period on weekday d and its
// length in minutes. A period whose end is at or before its start crosses
// midnight and belongs to the weekday it starts on.
func (p SchedulePeriod) span(d int) (start, length int) {
	s, _ := parseClock(p.Start)
	e, _ := parseClock(p.End)
	start = d*minutesPerDay + s
	if e > s {
		return start, e - s
	}
	return start, minutesPerDay - s + e
}

// normalizeSchedule validates a schedule and returns it in canonical form
// (trimmed names, sorted weekdays, zero-padded times).
func normalizeSchedule(c *ScheduleConfig) (*ScheduleConfig, error) {
	if c == nil || len(c.Periods) == 0 {
		return nil, nil
	}
	if len(c.Periods) > maxPeriods {
		return nil, errors.New("too many schedule periods")
	}
	out := &ScheduleConfig{}
	var occupied [minutesPerWeek]bool
	for _, p := range c.Periods {
		name := strings.TrimSpace(p.Name)
		if name == "" || utf8.RuneCountInString(name) > 32 {
			return nil, errors.New("invalid schedule period name")
		}
		if p.RatePercent < 1 || p.RatePercent > 10000 {
			return nil, errors.New("invalid schedule rate percent")
		}
		if len(p.Weekdays) == 0 {
			return nil, errors.New("schedule period needs weekdays")
		}
		days := append([]int(nil), p.Weekdays...)
		sort.Ints(days)
		for i, d := range days {
			if d < 0 || d > 6 || (i > 0 && days[i-1] == d) {
				return nil, errors.New("invalid schedule weekdays")
			}
		}
		s, err := parseClock(p.Start)
		if err != nil {
			return nil, err
		}
		e, err := parseClock(p.End)
		if err != nil {
			return nil, err
		}
		if s == e {
			return nil, errors.New("schedule start equals end")
		}
		np := SchedulePeriod{Name: name, Weekdays: days, Start: fmtClock(s), End: fmtClock(e), RatePercent: p.RatePercent}
		for _, d := range days {
			start, length := np.span(d)
			for i := 0; i < length; i++ {
				idx := (start + i) % minutesPerWeek
				if occupied[idx] {
					return nil, errors.New("schedule periods overlap")
				}
				occupied[idx] = true
			}
		}
		out.Periods = append(out.Periods, np)
	}
	return out, nil
}

// parseScheduleJSON parses stored or submitted schedule text. Blank text, "{}"
// and an empty list mean "disabled" and return a nil config.
func parseScheduleJSON(text string) (*ScheduleConfig, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "{}" || text == "null" {
		return nil, nil
	}
	var c ScheduleConfig
	if err := json.Unmarshal([]byte(text), &c); err != nil {
		return nil, err
	}
	return normalizeSchedule(&c)
}

// marshalSchedule renders the canonical stored text ("{}" when disabled).
func marshalSchedule(c *ScheduleConfig) string {
	if c == nil || len(c.Periods) == 0 {
		return "{}"
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// match returns the period containing t (interpreted in t's own location), or
// nil. Periods never overlap, so at most one matches.
func (c *ScheduleConfig) match(t time.Time) *SchedulePeriod {
	if c == nil {
		return nil
	}
	idx := int(t.Weekday())*minutesPerDay + t.Hour()*60 + t.Minute()
	for i := range c.Periods {
		p := &c.Periods[i]
		for _, d := range p.Weekdays {
			start, length := p.span(d)
			if (idx-start+minutesPerWeek)%minutesPerWeek < length {
				return p
			}
		}
	}
	return nil
}
