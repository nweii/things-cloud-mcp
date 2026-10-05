// Tests cover configured calendar dates, UTC wire anchors, and recurrence defaults.
package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
)

// Things stores date-only fields (sr, tir, dd) as UTC midnight of the calendar
// date, but "today" is the user's calendar date, not the server's UTC date.

func pinClock(t *testing.T, now time.Time, tz *time.Location) {
	t.Helper()
	old := calendarSettings.Swap(&dateCalendar{now: func() time.Time { return now.UTC() }, zone: tz})
	t.Cleanup(func() { calendarSettings.Store(old) })
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func calendarDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func anytimeTaskOn(date time.Time) *thingscloud.Task {
	return &thingscloud.Task{Schedule: thingscloud.TaskScheduleAnytime, ScheduledDate: &date}
}

// Between 00:00 and 02:00 CEST the server's UTC clock is still on the previous
// day. Today's tasks must already count as today.
func TestTodayFollowsUserCalendarEastOfUTC(t *testing.T) {
	berlin := mustLocation(t, "Europe/Berlin")
	pinClock(t, time.Date(2026, 9, 14, 0, 30, 0, 0, berlin), berlin) // 13 Sep 22:30 UTC

	if got, want := todayMidnightUTC(), calendarDate(2026, 9, 14).Unix(); got != want {
		t.Errorf("todayMidnightUTC = %s, want %s", time.Unix(got, 0).UTC(), time.Unix(want, 0).UTC())
	}
	if !isToday(calendarDate(2026, 9, 14)) || isToday(calendarDate(2026, 9, 13)) {
		t.Error("for a Berlin user at 00:30 on 14 Sep, 14 Sep is today and 13 Sep is not")
	}
	if !isScheduledForTodayOrPast(anytimeTaskOn(calendarDate(2026, 9, 14))) {
		t.Error("a task dated 14 Sep must be in Today")
	}
}

// West of UTC the error flips: at 21:00 in New York, UTC is already on the next
// day. Converting a stored UTC-midnight date into the user's zone lands on the
// previous evening, and tomorrow's tasks leak into Today.
func TestTodayFollowsUserCalendarWestOfUTC(t *testing.T) {
	newYork := mustLocation(t, "America/New_York")
	pinClock(t, time.Date(2026, 9, 13, 21, 0, 0, 0, newYork), newYork) // 14 Sep 01:00 UTC

	if got, want := todayMidnightUTC(), calendarDate(2026, 9, 13).Unix(); got != want {
		t.Errorf("todayMidnightUTC = %s, want %s", time.Unix(got, 0).UTC(), time.Unix(want, 0).UTC())
	}
	if !isToday(calendarDate(2026, 9, 13)) {
		t.Error("13 Sep must be today for a New York user at 21:00 on 13 Sep")
	}
	if isScheduledForTodayOrPast(anytimeTaskOn(calendarDate(2026, 9, 14))) {
		t.Error("a task dated 14 Sep must not be in Today on 13 Sep")
	}
	if !isScheduledForTodayOrPast(anytimeTaskOn(calendarDate(2026, 9, 13))) {
		t.Error("a task dated 13 Sep must be in Today on 13 Sep")
	}
	for _, schedule := range []string{"today", "tonight"} {
		payload := newTaskCreatePayload("Evening task", map[string]string{"schedule": schedule}, 0)
		want := calendarDate(2026, 9, 13).Unix()
		if payload.Sr == nil || payload.Tir == nil || *payload.Sr != want || *payload.Tir != want {
			t.Errorf("%s schedule must anchor 13 Sep at UTC midnight", schedule)
		}
	}
}

// The upcoming window includes the last date in the user's lookahead period.
func TestOverviewUpcomingWindowFollowsUserCalendar(t *testing.T) {
	newYork := mustLocation(t, "America/New_York")
	pinClock(t, time.Date(2026, 9, 14, 21, 30, 0, 0, newYork), newYork)

	fc := newFakeCloud("test@example.com", makeTaskItem("task-in-a-week",
		withTitle("Due in seven days"),
		withSchedule(thingscloud.TaskScheduleSomeday),
		withScheduledDate(calendarDate(2026, 9, 21)),
	), makeTaskItem("task-tomorrow",
		withTitle("Tomorrow"),
		withSchedule(thingscloud.TaskScheduleSomeday),
		withScheduledDate(calendarDate(2026, 9, 15)),
	), makeTaskItem("task-outside-window",
		withTitle("Outside window"),
		withSchedule(thingscloud.TaskScheduleSomeday),
		withScheduledDate(calendarDate(2026, 9, 22)),
	))
	defer fc.Close()
	tmcp := newTestThingsMCP(t, fc)

	result, err := tmcp.handleOverview(context.Background(), makeReq(map[string]any{"lookahead_days": 7}))
	if err != nil {
		t.Fatal(err)
	}
	assertNotError(t, result)
	got := resultJSON[struct {
		UpcomingTasks []TaskOutput `json:"upcomingTasks"`
	}](t, result)
	if len(got.UpcomingTasks) != 2 {
		t.Fatalf("upcoming at 21:30 EDT on 14 Sep with lookahead 7 = %d tasks, want 15 Sep and 21 Sep", len(got.UpcomingTasks))
	}
	for _, task := range got.UpcomingTasks {
		if task.Title != "Tomorrow" && task.Title != "Due in seven days" {
			t.Errorf("unexpected upcoming task: %s", task.Title)
		}
	}
}

func TestTodayNewYorkDSTTransitions(t *testing.T) {
	newYork := mustLocation(t, "America/New_York")
	cases := []struct {
		instant string
		want    time.Time
	}{
		{"2026-03-08T06:59:59Z", calendarDate(2026, 3, 8)},
		{"2026-03-08T07:00:00Z", calendarDate(2026, 3, 8)},
		{"2026-03-09T03:59:59Z", calendarDate(2026, 3, 8)},
		{"2026-03-09T04:00:00Z", calendarDate(2026, 3, 9)},
		{"2026-11-01T05:59:59Z", calendarDate(2026, 11, 1)},
		{"2026-11-01T06:00:00Z", calendarDate(2026, 11, 1)},
		{"2026-11-02T04:59:59Z", calendarDate(2026, 11, 1)},
		{"2026-11-02T05:00:00Z", calendarDate(2026, 11, 2)},
	}
	for _, tc := range cases {
		t.Run(tc.instant, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.instant)
			if err != nil {
				t.Fatal(err)
			}
			pinClock(t, now, newYork)
			if got := userToday(); !got.Equal(tc.want) {
				t.Fatalf("userToday = %s, want %s", got, tc.want)
			}
			if !isScheduledForTodayOrPast(anytimeTaskOn(tc.want)) || isScheduledForTodayOrPast(anytimeTaskOn(tc.want.AddDate(0, 0, 1))) {
				t.Fatal("Today filter included tomorrow or excluded today across DST")
			}
		})
	}
}

// Weekly recurrence without a schedule uses the user's current weekday.
func TestWeeklyRecurrenceWithoutScheduleUsesUsersWeekday(t *testing.T) {
	newYork := mustLocation(t, "America/New_York")
	pinClock(t, time.Date(2026, 9, 14, 21, 30, 0, 0, newYork), newYork) // Monday, Tuesday in UTC

	fc := newFakeCloud("test@example.com")
	defer fc.Close()
	tmcp := newTestThingsMCP(t, fc)
	result, err := tmcp.handleCreateTask(context.Background(), makeReq(map[string]any{
		"title":      "weekly review",
		"recurrence": "weekly",
	}))
	if err != nil {
		t.Fatal(err)
	}
	assertNotError(t, result)

	commits := fc.getCommitLog()
	if len(commits) != 1 {
		t.Fatalf("commit count = %d, want 1", len(commits))
	}
	var body map[string]struct {
		Payload struct {
			ScheduledDate *int64 `json:"sr"`
			InitialDate   *int64 `json:"icsd"`
			Rr            *struct {
				Of []struct {
					Wd *int `json:"wd"`
				} `json:"of"`
			} `json:"rr"`
		} `json:"p"`
	}
	if err := json.Unmarshal(commits[0], &body); err != nil {
		t.Fatal(err)
	}
	for _, item := range body {
		if rr := item.Payload.Rr; rr != nil {
			if len(rr.Of) != 1 || rr.Of[0].Wd == nil || *rr.Of[0].Wd != int(time.Monday) {
				t.Fatalf("weekly rule = %s, want wd=%d (Monday)", commits[0], time.Monday)
			}
			if item.Payload.InitialDate == nil || *item.Payload.InitialDate != calendarDate(2026, 9, 14).Unix() {
				t.Error("recurrence initial date must use the user's Monday")
			}
			if item.Payload.ScheduledDate == nil || *item.Payload.ScheduledDate != calendarDate(2026, 9, 21).Unix() {
				t.Error("recurrence template must schedule the following Monday")
			}
			return
		}
	}
	t.Fatalf("no recurrence template in commit %s", commits[0])
}

// The zone comes from deployment config. MCP_TIMEZONE wins over TZ, which a
// base image may set; a typo must degrade to UTC instead of blocking startup.
func TestLoadUserTimeZone(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"MCP_TIMEZONE wins", map[string]string{"MCP_TIMEZONE": "Europe/Berlin", "TZ": "America/New_York"}, "Europe/Berlin"},
		{"TZ fallback", map[string]string{"TZ": "America/New_York"}, "America/New_York"},
		{"invalid MCP_TIMEZONE falls through to TZ", map[string]string{"MCP_TIMEZONE": "Mars/Olympus", "TZ": "Asia/Tokyo"}, "Asia/Tokyo"},
		{"invalid only is UTC", map[string]string{"MCP_TIMEZONE": "Mars/Olympus"}, "UTC"},
		{"unset is UTC", map[string]string{}, "UTC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := loadUserTimeZone(func(key string) string { return tc.env[key] }); got.String() != tc.want {
				t.Errorf("loadUserTimeZone = %s, want %s", got, tc.want)
			}
		})
	}
}
