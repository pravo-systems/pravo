package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// ParseAge validates a refresh age and returns the maximum age; never is true
// for "never". "always" and "" return zero.
func ParseAge(p string) (maxAge time.Duration, never bool, err error) {
	switch strings.TrimSpace(p) {
	case "", "always":
		return 0, false, nil
	case "never":
		return 0, true, nil
	}
	s := strings.TrimSpace(p)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, false, fmt.Errorf("invalid refresh age %q", p)
		}
		return time.Duration(n) * 24 * time.Hour, false, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false, fmt.Errorf("invalid refresh age %q: want \"always\", \"never\" or a duration such as \"30m\", \"6h\", \"7d\"", p)
	}
	return d, false, nil
}

// parsedAge is a refresh age that was parsed.
type parsedAge struct {
	max   time.Duration
	never bool
	err   error
}

// parseAge is ParseAge with the result kept: an estate has a few distinct
// ages and many records.
func (e *Engine) parseAge(text string) (time.Duration, bool, error) {
	e.mu.Lock()
	a, ok := e.ages[text]
	e.mu.Unlock()
	if !ok {
		a.max, a.never, a.err = ParseAge(text)
		e.mu.Lock()
		if e.ages == nil {
			e.ages = map[string]parsedAge{}
		}
		e.ages[text] = a
		e.mu.Unlock()
	}
	return a.max, a.never, a.err
}

// refreshDue decides whether sr must be read given its refresh age. A mode
// that reads by age reads a record that is older than that; whether the plan
// looks at the record at all is decided by the caller.
func (e *Engine) refreshDue(sr *state.Resource, age string) (bool, error) {
	switch e.Opts.Refresh {
	case RefreshNone:
		return false, nil
	case RefreshAll:
		return true, nil
	}
	return e.pastAge(sr, age)
}

// pastAge reports whether the record is older than the refresh age, which is
// the default one when age is empty.
func (e *Engine) pastAge(sr *state.Resource, age string) (bool, error) {
	if age == "" {
		age = e.Opts.DefaultRefresh
	}
	maxAge, never, err := e.parseAge(age)
	if err != nil {
		return false, err
	}
	if never {
		return false, nil
	}
	if maxAge == 0 || sr.RefreshedAt == nil {
		return true, nil
	}
	return olderThan(*sr.RefreshedAt, e.clock(), maxAge), nil
}

// clock returns the time that records are aged against.
func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// olderThan reports whether a record that was read at the given time is past
// the refresh age at now. A record exactly as old as the age is within it.
func olderThan(read, now time.Time, age time.Duration) bool {
	return now.Sub(read) > age
}

// shortAge writes a duration in its largest whole unit: 12s, 45m, 3h, 5d.
func shortAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}
