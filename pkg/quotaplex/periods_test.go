package quotaplex

import (
	"errors"
	"testing"
	"time"
)

func primitiveTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestFixedAndUnequalAnchoredPeriodsHaveExactHalfOpenBoundaries(t *testing.T) {
	start := primitiveTime(t, "2026-11-01T04:00:00Z")
	fixed := Period{Kind: "fixed", Start: start, End: start.Add(9 * 24 * time.Hour)}
	window, err := fixed.Resolve(start)
	if err != nil || !window.Start.Equal(start) || !window.End.Equal(fixed.End) {
		t.Fatalf("inclusive start rejected: %+v %v", window, err)
	}
	for _, at := range []time.Time{start.Add(-time.Nanosecond), fixed.End} {
		if _, err := fixed.Resolve(at); !errors.Is(err, ErrOutsidePeriod) {
			t.Fatalf("outside fixed time accepted: %s %v", at, err)
		}
	}
	for _, duration := range []time.Duration{17 * time.Hour, 11 * 24 * time.Hour} {
		period := Period{Kind: "anchored", Anchor: start, Duration: duration}
		window, err := period.Resolve(start.Add(3 * duration))
		if err != nil || !window.Start.Equal(start.Add(3*duration)) || !window.End.Equal(start.Add(4*duration)) {
			t.Fatalf("unequal recurring duration boundary wrong: %+v %v", window, err)
		}
	}
	// More than time.Duration's maximum elapsed range cannot silently saturate.
	anchor := primitiveTime(t, "1500-01-01T00:00:00Z")
	at := primitiveTime(t, "2026-11-09T00:00:00Z")
	period := Period{Kind: "anchored", Anchor: anchor, Duration: 17 * time.Hour}
	window, err = period.Resolve(at)
	index := (at.Unix() - anchor.Unix()) / int64(17*time.Hour/time.Second)
	wantStart := time.Unix(anchor.Unix()+index*int64(17*time.Hour/time.Second), 0)
	if err != nil || !window.Start.Equal(wantStart) || window.End.Sub(window.Start) != 17*time.Hour {
		t.Fatalf("long elapsed anchor saturated: %+v expected %s error=%v", window, wantStart, err)
	}
}

func TestCalendarPeriodsPreserveCivilBoundariesAcrossDST(t *testing.T) {
	zone, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2026, 1, 1, 0, 0, 0, 0, zone)
	daily := Period{Kind: "calendar", Anchor: anchor, CalendarUnit: "day", TimeZone: zone.String()}
	for _, test := range []struct {
		month time.Month
		day   int
		hours time.Duration
	}{{time.April, 5, 25}, {time.October, 4, 23}} {
		at := time.Date(2026, test.month, test.day, 12, 0, 0, 0, zone)
		window, err := daily.Resolve(at)
		if err != nil || window.End.Sub(window.Start) != test.hours*time.Hour || window.Start.In(zone).Hour() != 0 || window.End.In(zone).Hour() != 0 {
			t.Fatalf("civil day lost DST semantics: %+v %v", window, err)
		}
		next, err := daily.Resolve(window.End)
		if err != nil || !next.Start.Equal(window.End) {
			t.Fatalf("calendar boundary overlaps or leaves a gap: %+v %v", next, err)
		}
	}
	weekly := Period{Kind: "calendar", Anchor: time.Date(2026, 1, 5, 0, 0, 0, 0, zone), CalendarUnit: "week", TimeZone: zone.String()}
	week, err := weekly.Resolve(time.Date(2026, 4, 5, 12, 0, 0, 0, zone))
	if err != nil || week.Start.In(zone).Weekday() != time.Monday || week.End.Sub(week.Start) != 169*time.Hour {
		t.Fatalf("calendar week used a fixed 168-hour duration: %+v %v", week, err)
	}
	monthly := Period{Kind: "calendar", Anchor: anchor, CalendarUnit: "month", TimeZone: zone.String()}
	month, err := monthly.Resolve(time.Date(2026, 2, 28, 23, 0, 0, 0, zone))
	if err != nil || month.Start.In(zone).Day() != 1 || month.End.In(zone).Month() != time.March || month.End.Sub(month.Start) != 28*24*time.Hour {
		t.Fatalf("calendar month normalized to a trailing duration: %+v %v", month, err)
	}
}

func TestInvalidAndAmbiguousCalendarConfigurationFailsExplicitly(t *testing.T) {
	at := primitiveTime(t, "2026-11-09T00:00:00Z")
	for _, period := range []Period{
		{Kind: "rolling", Duration: time.Hour},
		{Kind: "fixed", Start: at, End: at.Add(time.Hour), Duration: time.Hour},
		{Kind: "anchored", Anchor: at, Duration: -time.Hour},
		{Kind: "calendar", Anchor: at.Add(time.Hour), CalendarUnit: "day", TimeZone: "UTC"},
		{Kind: "calendar", Anchor: at, CalendarUnit: "day", TimeZone: "Local"},
		{Kind: "calendar", Anchor: at, CalendarUnit: "month", TimeZone: "UTC"},
		{Kind: "calendar", Anchor: at, CalendarUnit: "year", TimeZone: "UTC"},
	} {
		if _, err := period.Resolve(at); err == nil {
			t.Fatalf("unsupported/conflicting period accepted: %+v", period)
		}
	}
	if _, err := (Period{Kind: "rolling", Duration: time.Hour}).Resolve(at); !errors.Is(err, ErrUnsupportedPeriod) {
		t.Fatalf("rolling semantics were not explicitly unsupported: %v", err)
	}
	for _, test := range []struct {
		zone  string
		year  int
		month time.Month
		day   int
	}{{"America/Sao_Paulo", 2018, time.November, 4}, {"Pacific/Apia", 2011, time.December, 30}, {"America/Havana", 2026, time.November, 1}} {
		zone, err := time.LoadLocation(test.zone)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := localMidnight(test.year, test.month, test.day, zone); err == nil {
			t.Fatalf("gap/fold boundary silently normalized: %+v", test)
		}
	}
	apia, err := time.LoadLocation("Pacific/Apia")
	if err != nil {
		t.Fatal(err)
	}
	dayBeforeSkippedDate := Period{Kind: "calendar", Anchor: time.Date(2011, 12, 1, 0, 0, 0, 0, apia), CalendarUnit: "day", TimeZone: apia.String()}
	if _, err := dayBeforeSkippedDate.Resolve(time.Date(2011, 12, 29, 12, 0, 0, 0, apia)); err == nil {
		t.Fatal("calendar date arithmetic silently merged a skipped day into the prior window")
	}
}
