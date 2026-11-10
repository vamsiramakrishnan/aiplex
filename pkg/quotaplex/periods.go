package quotaplex

import (
	"errors"
	"fmt"
	"math/big"
	"time"
)

var ErrOutsidePeriod = errors.New("time is outside allocation period")
var ErrUnsupportedPeriod = errors.New("unsupported allocation period")

// Validate accepts only period shapes with a single, explicit interpretation.
// Rolling accounting needs event-expiry history and is intentionally unsupported.
func (period Period) Validate() error {
	switch period.Kind {
	case "fixed":
		if !validAccountingTime(period.Start) || !validAccountingTime(period.End) || !period.Start.Before(period.End) || !period.Anchor.IsZero() || period.Duration != 0 || period.CalendarUnit != "" || period.TimeZone != "" {
			return fmt.Errorf("fixed period needs only ordered start/end boundaries")
		}
	case "anchored":
		if !validAccountingTime(period.Anchor) || period.Duration <= 0 || !period.Start.IsZero() || !period.End.IsZero() || period.CalendarUnit != "" || period.TimeZone != "" {
			return fmt.Errorf("anchored period needs only anchor and positive duration")
		}
		if _, _, err := elapsedWindow(period.Anchor, period.Duration, period.Anchor); err != nil {
			return err
		}
	case "calendar":
		if !period.Start.IsZero() || !period.End.IsZero() || period.Duration != 0 {
			return fmt.Errorf("calendar period cannot also declare fixed or elapsed boundaries")
		}
		if _, _, err := calendarWindow(period.Anchor, period.TimeZone, period.CalendarUnit, period.Anchor); err != nil {
			return err
		}
	default:
		return fmt.Errorf("period %q: %w", period.Kind, ErrUnsupportedPeriod)
	}
	return nil
}

// Resolve pins a half-open allocation window at admission. Settlement retains
// that window even when it occurs after another period has begun.
func (period Period) Resolve(at time.Time) (Window, error) {
	if err := period.Validate(); err != nil {
		return Window{}, err
	}
	if !validAccountingTime(at) {
		return Window{}, fmt.Errorf("valid accounting time required")
	}
	var start, end time.Time
	var err error
	switch period.Kind {
	case "fixed":
		start, end = period.Start, period.End
		if at.Before(start) || !at.Before(end) {
			return Window{}, ErrOutsidePeriod
		}
	case "anchored":
		start, end, err = elapsedWindow(period.Anchor, period.Duration, at)
	case "calendar":
		start, end, err = calendarWindow(period.Anchor, period.TimeZone, period.CalendarUnit, at)
	}
	if err != nil {
		return Window{}, err
	}
	return Window{Start: start.UTC(), End: end.UTC()}, nil
}

func ResolveWindow(period Period, at time.Time) (Window, error) { return period.Resolve(at) }

func validAccountingTime(at time.Time) bool {
	return !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999
}

func elapsedWindow(anchor time.Time, duration time.Duration, at time.Time) (time.Time, time.Time, error) {
	if !validAccountingTime(anchor) || !validAccountingTime(at) || duration <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("valid anchor, time and positive elapsed duration required")
	}
	if at.Before(anchor) {
		return time.Time{}, time.Time{}, ErrOutsidePeriod
	}
	// time.Sub saturates outside time.Duration's approximately 292-year range.
	// Use exact elapsed nanoseconds so long-lived allocation anchors remain valid.
	nanos := func(value time.Time) *big.Int {
		return new(big.Int).Add(new(big.Int).Mul(big.NewInt(value.Unix()), big.NewInt(int64(time.Second))), big.NewInt(int64(value.Nanosecond())))
	}
	a := nanos(anchor)
	d := big.NewInt(int64(duration))
	index := new(big.Int).Quo(new(big.Int).Sub(nanos(at), a), d)
	startNanos := new(big.Int).Add(a, new(big.Int).Mul(index, d))
	endNanos := new(big.Int).Add(startNanos, d)
	fromNanos := func(value *big.Int) (time.Time, error) {
		seconds, remainder := new(big.Int), new(big.Int)
		seconds.QuoRem(value, big.NewInt(int64(time.Second)), remainder)
		if !seconds.IsInt64() {
			return time.Time{}, fmt.Errorf("allocation boundary overflow")
		}
		resolved := time.Unix(seconds.Int64(), remainder.Int64()).UTC()
		if !validAccountingTime(resolved) {
			return time.Time{}, fmt.Errorf("allocation boundary is not representable")
		}
		return resolved, nil
	}
	start, err := fromNanos(startNanos)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := fromNanos(endNanos)
	return start, end, err
}

// localMidnight refuses civil-time gaps and folds instead of silently choosing
// an offset. They are not supported without an explicit boundary-resolution policy.
func localMidnight(year int, month time.Month, day int, location *time.Location) (time.Time, error) {
	resolved := time.Date(year, month, day, 0, 0, 0, 0, location)
	matches := func(value time.Time) bool {
		local := value.In(location)
		return local.Year() == year && local.Month() == month && local.Day() == day && local.Hour() == 0 && local.Minute() == 0 && local.Second() == 0 && local.Nanosecond() == 0
	}
	if !validAccountingTime(resolved) || !matches(resolved) {
		return time.Time{}, fmt.Errorf("calendar boundary is an invalid local midnight")
	}
	_, currentOffset := resolved.Zone()
	seen := map[int]bool{currentOffset: true}
	for hour := -48; hour <= 48; hour += 6 {
		_, offset := resolved.Add(time.Duration(hour) * time.Hour).Zone()
		if seen[offset] {
			continue
		}
		seen[offset] = true
		candidate := resolved.Add(time.Duration(currentOffset-offset) * time.Second)
		if !candidate.Equal(resolved) && matches(candidate) {
			return time.Time{}, fmt.Errorf("calendar boundary is an ambiguous local midnight")
		}
	}
	return resolved, nil
}

func calendarWindow(anchor time.Time, zone, unit string, at time.Time) (time.Time, time.Time, error) {
	if zone == "" || zone == "Local" {
		return time.Time{}, time.Time{}, fmt.Errorf("explicit stable timezone required")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("load calendar timezone: %w", err)
	}
	if !validAccountingTime(anchor) || !validAccountingTime(at) {
		return time.Time{}, time.Time{}, fmt.Errorf("valid calendar anchor and time required")
	}
	localAnchor := anchor.In(location)
	anchorBoundary, err := localMidnight(localAnchor.Year(), localAnchor.Month(), localAnchor.Day(), location)
	if err != nil || !anchorBoundary.Equal(anchor) {
		return time.Time{}, time.Time{}, fmt.Errorf("calendar anchor must be an unambiguous local midnight")
	}
	if at.Before(anchor) {
		return time.Time{}, time.Time{}, ErrOutsidePeriod
	}
	local := at.In(location)
	year, month, day := local.Date()
	var nextYear int
	var nextMonth time.Month
	var nextDay int
	switch unit {
	case "day":
		nextYear, nextMonth, nextDay = time.Date(year, month, day+1, 12, 0, 0, 0, time.UTC).Date()
	case "week":
		delta := (int(local.Weekday()) - int(localAnchor.Weekday()) + 7) % 7
		year, month, day = time.Date(year, month, day-delta, 12, 0, 0, 0, time.UTC).Date()
		nextYear, nextMonth, nextDay = time.Date(year, month, day+7, 12, 0, 0, 0, time.UTC).Date()
	case "month":
		if localAnchor.Day() != 1 {
			return time.Time{}, time.Time{}, fmt.Errorf("monthly calendar anchor must be the first day")
		}
		day = 1
		nextYear, nextMonth, nextDay = time.Date(year, month+1, 1, 12, 0, 0, 0, time.UTC).Date()
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("calendar unit %q: %w", unit, ErrUnsupportedPeriod)
	}
	start, err := localMidnight(year, month, day, location)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := localMidnight(nextYear, nextMonth, nextDay, location)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if at.Before(start) || !at.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("calendar date normalization is unsupported")
	}
	return start.UTC(), end.UTC(), nil
}
