// Calendar helpers interpret Things date-only values using the configured user's
// calendar date while preserving their UTC-midnight wire representation.
package main

import (
	"log"
	"sync/atomic"
	"time"
	_ "time/tzdata" // Supply IANA zones in minimal container images.
)

// Immutable calendar settings are published atomically so readers and test
// clocks never race. A nil setting uses the real clock and UTC.
type dateCalendar struct {
	now  func() time.Time
	zone *time.Location
}

var calendarSettings atomic.Pointer[dateCalendar]

func configureDateCalendar(zone *time.Location) {
	calendarSettings.Store(&dateCalendar{now: time.Now, zone: zone})
}

// loadUserTimeZone resolves MCP_TIMEZONE, then TZ, then UTC. An invalid zone
// logs a configuration warning and falls through to the next option.
func loadUserTimeZone(getenv func(string) string) *time.Location {
	for _, key := range []string{"MCP_TIMEZONE", "TZ"} {
		name := getenv(key)
		if name == "" {
			continue
		}
		zone, err := time.LoadLocation(name)
		if err != nil {
			log.Printf("Invalid %s=%q, ignoring: %v", key, name, err)
			continue
		}
		log.Printf("Date interpretation timezone: %s (from %s)", name, key)
		return zone
	}
	return time.UTC
}

// userToday anchors the user's calendar date at UTC midnight. Stored schedule
// and deadline values must not be converted into the user's zone.
func userToday() time.Time {
	now := time.Now().UTC()
	if settings := calendarSettings.Load(); settings != nil {
		now = settings.now().In(settings.zone)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func todayMidnightUTC() int64 {
	return userToday().Unix()
}

func userTodayEnd() time.Time {
	return userToday().AddDate(0, 0, 1).Add(-time.Nanosecond)
}

// isToday compares the UTC calendar day of a stored date-only value with the
// user's calendar date. UTC-midnight values move back a day west of UTC if
// converted into local time.
func isToday(date time.Time) bool {
	stored, today := date.UTC(), userToday()
	return stored.Year() == today.Year() && stored.Month() == today.Month() && stored.Day() == today.Day()
}
