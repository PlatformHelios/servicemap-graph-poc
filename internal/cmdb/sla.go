package cmdb

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const holidayDateLayout = "2006-01-02"

// Holiday is a calendar day the platform treats as closed for SLA and OLA
// business-day counting. Dates are YYYY-MM-DD in the platform's business calendar.
type Holiday struct {
	Date string `json:"date"`
	Name string `json:"name,omitempty"`
}

// AgreementClock is the SLA or OLA progress for a request or task, counted in
// Monday–Friday business days and skipping marked holidays.
type AgreementClock struct {
	Kind           string  `json:"kind"` // sla or ola
	Enabled        bool    `json:"enabled"`
	BusinessDays   int     `json:"businessDays,omitempty"`
	StartedAt      string  `json:"startedAt,omitempty"`
	DueAt          string  `json:"dueAt,omitempty"`   // last included business day, YYYY-MM-DD
	ElapsedDays    int     `json:"elapsedDays"`
	RemainingDays  int     `json:"remainingDays"`
	Progress       float64 `json:"progress"` // 0–1 of business time used, in 8-hour steps; may exceed 1 when overdue
	Status         string  `json:"status"`   // none, on-track, due-today, overdue, met, missed
	Overdue        bool    `json:"overdue"`
}

const (
	ClockNone     = "none"
	ClockOnTrack  = "on-track"
	ClockDueToday = "due-today"
	ClockOverdue  = "overdue"
	ClockMet      = "met"
	ClockMissed   = "missed"
)

// ParseHolidayDate accepts YYYY-MM-DD and rejects anything else.
func ParseHolidayDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := time.ParseInLocation(holidayDateLayout, value, time.UTC)
	if err != nil || parsed.Format(holidayDateLayout) != value {
		return "", fmt.Errorf("%w: holiday date must be YYYY-MM-DD", ErrInvalid)
	}
	return value, nil
}

func normalizeHolidays(holidays []Holiday) ([]Holiday, error) {
	seen := map[string]bool{}
	clean := make([]Holiday, 0, len(holidays))
	for _, holiday := range holidays {
		date, err := ParseHolidayDate(holiday.Date)
		if err != nil {
			return nil, err
		}
		if seen[date] {
			continue
		}
		seen[date] = true
		clean = append(clean, Holiday{Date: date, Name: strings.TrimSpace(holiday.Name)})
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i].Date < clean[j].Date })
	return clean, nil
}

func holidaySet(holidays []Holiday) map[string]bool {
	set := make(map[string]bool, len(holidays))
	for _, holiday := range holidays {
		set[holiday.Date] = true
	}
	return set
}

func calendarDay(value time.Time) time.Time {
	return time.Date(value.UTC().Year(), value.UTC().Month(), value.UTC().Day(), 0, 0, 0, 0, time.UTC)
}

func isBusinessDay(day time.Time, closed map[string]bool) bool {
	weekday := day.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	return !closed[day.Format(holidayDateLayout)]
}

// AddBusinessDays returns the calendar date that is `days` business days from
// start, counting start's calendar day as day 1 when it is a business day.
// Weekends and holidays are skipped. Used as the SLA/OLA due date.
func AddBusinessDays(start time.Time, days int, holidays []Holiday) time.Time {
	if days <= 0 {
		return calendarDay(start)
	}
	closed := holidaySet(holidays)
	day := calendarDay(start)
	counted := 0
	for {
		if isBusinessDay(day, closed) {
			counted++
			if counted >= days {
				return day
			}
		}
		day = day.AddDate(0, 0, 1)
	}
}

// BusinessDaysElapsed counts business days from start's calendar day through
// asOf's calendar day (inclusive). Used for day-count labels on the meter.
func BusinessDaysElapsed(start, asOf time.Time, holidays []Holiday) int {
	closed := holidaySet(holidays)
	from := calendarDay(start)
	until := calendarDay(asOf)
	if until.Before(from) {
		return 0
	}
	elapsed := 0
	for !from.After(until) {
		if isBusinessDay(from, closed) {
			elapsed++
		}
		from = from.AddDate(0, 0, 1)
	}
	return elapsed
}

// progressIncrement is the resolution at which an agreement meter advances:
// three 8-hour steps per SLA/OLA business day.
const progressIncrement = 8 * time.Hour
const stepsPerBusinessDay = int((24 * time.Hour) / progressIncrement) // 3

// BusinessProgress measures business time between start and asOf in business-day
// units, advancing one third of a day for every full 8 hours that fall on a
// Monday–Friday non-holiday. Tickets opened on a weekend stay at 0 until
// weekday hours accumulate. The result may exceed the window when overdue.
func BusinessProgress(start, asOf time.Time, holidays []Holiday) float64 {
	start = start.UTC()
	asOf = asOf.UTC()
	if !asOf.After(start) {
		return 0
	}
	closed := holidaySet(holidays)
	var business time.Duration
	day := calendarDay(start)
	last := calendarDay(asOf)
	for !day.After(last) {
		if isBusinessDay(day, closed) {
			segStart := start
			if segStart.Before(day) {
				segStart = day
			}
			segEnd := asOf
			dayEnd := day.Add(24 * time.Hour)
			if segEnd.After(dayEnd) {
				segEnd = dayEnd
			}
			if segEnd.After(segStart) {
				business += segEnd.Sub(segStart)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	steps := int(business / progressIncrement)
	return float64(steps) / float64(stepsPerBusinessDay)
}

func parseTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), true
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), true
	}
	return time.Time{}, false
}

func intProperty(properties map[string]any, key string) (int, bool) {
	value, ok := properties[key]
	if !ok || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func boolProperty(properties map[string]any, key string) bool {
	value, ok := properties[key]
	if !ok {
		return false
	}
	flag, _ := value.(bool)
	return flag
}

func agreementClock(kind string, enabled bool, days int, startedAt, finishedAt string, now time.Time, holidays []Holiday) AgreementClock {
	clock := AgreementClock{Kind: kind, Enabled: enabled, BusinessDays: days, StartedAt: startedAt, Status: ClockNone}
	if !enabled || days <= 0 {
		return clock
	}
	start, ok := parseTimestamp(startedAt)
	if !ok {
		return clock
	}
	due := AddBusinessDays(start, days, holidays)
	clock.DueAt = due.Format(holidayDateLayout)
	end := now
	finished := false
	if done, ok := parseTimestamp(finishedAt); ok {
		end = done
		finished = true
	}
	elapsed := BusinessDaysElapsed(start, end, holidays)
	clock.ElapsedDays = elapsed
	remaining := days - elapsed
	if remaining < 0 {
		remaining = 0
	}
	clock.RemainingDays = remaining
	// The day counts above drive the labels; the meter fills in 8-hour steps.
	progressDays := BusinessProgress(start, end, holidays)
	clock.Progress = progressDays / float64(days)
	overdue := calendarDay(end).After(due) || progressDays > float64(days)
	clock.Overdue = overdue
	// Yellow in the final 8-hour chunk of the window (last third of the last day).
	totalSteps := days * stepsPerBusinessDay
	consumedSteps := int(progressDays*float64(stepsPerBusinessDay) + 1e-9)
	switch {
	case finished && overdue:
		clock.Status = ClockMissed
	case finished:
		clock.Status = ClockMet
		// A met SLA shows a full green ring regardless of how early it finished.
		clock.Progress = 1
	case overdue:
		clock.Status = ClockOverdue
		if clock.Progress < 1 {
			clock.Progress = 1
		}
	case consumedSteps >= totalSteps-1:
		clock.Status = ClockDueToday
	default:
		clock.Status = ClockOnTrack
	}
	return clock
}

func slaClockForRequest(request Node, now time.Time, holidays []Holiday) *AgreementClock {
	if !boolProperty(request.Properties, "slaEnabled") {
		clock := agreementClock("sla", false, 0, "", "", now, holidays)
		return &clock
	}
	days, _ := intProperty(request.Properties, "slaBusinessDays")
	started, _ := request.Properties["submittedAt"].(string)
	finished, _ := request.Properties["fulfilledAt"].(string)
	clock := agreementClock("sla", true, days, started, finished, now, holidays)
	return &clock
}

func olaClockForTask(task TaskView, now time.Time, holidays []Holiday) *AgreementClock {
	days := task.Step.OLABusinessDays
	enabled := task.Step.OLAEnabled && days > 0
	finished := task.CompletedAt
	if finished == "" {
		finished = task.RetiredAt
	}
	clock := agreementClock("ola", enabled, days, task.CreatedAt, finished, now, holidays)
	return &clock
}

func parseBusinessDays(value any, field string) (int, error) {
	if value == nil {
		return 0, nil
	}
	var days int
	switch typed := value.(type) {
	case int:
		days = typed
	case int64:
		days = int(typed)
	case float64:
		if typed != float64(int(typed)) {
			return 0, fmt.Errorf("%w: %s must be a whole number of business days", ErrInvalid, field)
		}
		days = int(typed)
	default:
		return 0, fmt.Errorf("%w: %s must be a whole number of business days", ErrInvalid, field)
	}
	if days < 0 {
		return 0, fmt.Errorf("%w: %s cannot be negative", ErrInvalid, field)
	}
	if days > 365 {
		return 0, fmt.Errorf("%w: %s cannot exceed 365 business days", ErrInvalid, field)
	}
	return days, nil
}
