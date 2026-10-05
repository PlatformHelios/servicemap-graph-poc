package cmdb

import (
	"testing"
	"time"
)

func TestAddBusinessDaysSkipsWeekendsAndHolidays(t *testing.T) {
	// Friday 3 Oct 2025 counts as day 1; with Monday 6 Oct a holiday, +3 lands on Wednesday 8 Oct.
	start := time.Date(2025, 10, 3, 15, 0, 0, 0, time.UTC)
	holidays := []Holiday{{Date: "2025-10-06", Name: "Observed"}}
	due := AddBusinessDays(start, 1, holidays)
	if got := due.Format(holidayDateLayout); got != "2025-10-03" {
		t.Fatalf("AddBusinessDays Friday+1 = %s, want 2025-10-03", got)
	}
	due = AddBusinessDays(start, 3, holidays)
	if got := due.Format(holidayDateLayout); got != "2025-10-08" {
		t.Fatalf("AddBusinessDays Friday+3 with Monday holiday = %s, want 2025-10-08", got)
	}
}

func TestBusinessDaysElapsed(t *testing.T) {
	start := time.Date(2025, 10, 3, 9, 0, 0, 0, time.UTC) // Friday
	holidays := []Holiday{{Date: "2025-10-06"}}
	if got := BusinessDaysElapsed(start, start, holidays); got != 1 {
		t.Fatalf("elapsed same day = %d, want 1", got)
	}
	monday := time.Date(2025, 10, 6, 12, 0, 0, 0, time.UTC)
	if got := BusinessDaysElapsed(start, monday, holidays); got != 1 {
		t.Fatalf("elapsed through holiday Monday = %d, want 1 (Friday only)", got)
	}
	tuesday := time.Date(2025, 10, 7, 8, 0, 0, 0, time.UTC)
	if got := BusinessDaysElapsed(start, tuesday, holidays); got != 2 {
		t.Fatalf("elapsed through Tuesday = %d, want 2", got)
	}
}

func TestBusinessProgressAdvancesInEightHourSteps(t *testing.T) {
	sunday := time.Date(2026, 10, 4, 13, 46, 26, 0, time.UTC)
	if got := BusinessProgress(sunday, sunday.Add(11*time.Hour), nil); got != 0 {
		t.Fatalf("weekend hours should not advance SLA: got %v", got)
	}
	start := time.Date(2025, 10, 3, 9, 0, 0, 0, time.UTC) // Friday
	holidays := []Holiday{{Date: "2025-10-06"}}
	cases := []struct {
		name string
		asOf time.Time
		want float64
	}{
		{"same moment", start, 0},
		{"+7h59 Friday", time.Date(2025, 10, 3, 16, 59, 0, 0, time.UTC), 0},
		{"+8h Friday", time.Date(2025, 10, 3, 17, 0, 0, 0, time.UTC), 1.0 / 3},
		{"Saturday", time.Date(2025, 10, 4, 20, 0, 0, 0, time.UTC), 1.0 / 3},
		{"holiday Monday", time.Date(2025, 10, 6, 17, 0, 0, 0, time.UTC), 1.0 / 3},
		{"Tuesday 08:00", time.Date(2025, 10, 7, 8, 0, 0, 0, time.UTC), 2.0 / 3},
		{"Tuesday 16:00", time.Date(2025, 10, 7, 16, 0, 0, 0, time.UTC), 1},
	}
	for _, tc := range cases {
		if got := BusinessProgress(start, tc.asOf, holidays); got != tc.want {
			t.Fatalf("%s: progress = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAgreementClockStatuses(t *testing.T) {
	start := "2025-10-03T09:00:00Z" // Friday
	holidays := []Holiday{{Date: "2025-10-06"}}
	onTrack := agreementClock("sla", true, 3, start, "", time.Date(2025, 10, 3, 17, 0, 0, 0, time.UTC), holidays)
	if onTrack.Status != ClockOnTrack || onTrack.Progress != (1.0/3)/3 || onTrack.DueAt != "2025-10-08" {
		t.Fatalf("on-track clock = %+v", onTrack)
	}
	// Final 8-hour chunk of a 1-day SLA (16h into a 24h window).
	dueSoon := agreementClock("ola", true, 1, "2025-10-03T00:00:00Z", "", time.Date(2025, 10, 3, 16, 0, 0, 0, time.UTC), holidays)
	if dueSoon.Status != ClockDueToday || dueSoon.Progress != 2.0/3 {
		t.Fatalf("final-chunk clock = %+v", dueSoon)
	}
	overdue := agreementClock("ola", true, 1, start, "", time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC), holidays)
	if overdue.Status != ClockOverdue || !overdue.Overdue || overdue.Progress < 1 {
		t.Fatalf("overdue clock = %+v", overdue)
	}
	met := agreementClock("ola", true, 3, start, "2025-10-03T17:00:00Z", time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC), holidays)
	if met.Status != ClockMet || met.Progress != 1 {
		t.Fatalf("met clock = %+v", met)
	}
	missed := agreementClock("sla", true, 1, start, "2025-10-07T09:00:00Z", time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC), holidays)
	if missed.Status != ClockMissed {
		t.Fatalf("missed clock = %+v", missed)
	}
	none := agreementClock("sla", false, 0, start, "", time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC), holidays)
	if none.Status != ClockNone {
		t.Fatalf("disabled clock = %+v", none)
	}
}

func TestParseHolidayDate(t *testing.T) {
	if _, err := ParseHolidayDate("2025-13-01"); err == nil {
		t.Fatal("accepted invalid month")
	}
	if _, err := ParseHolidayDate("Oct 3 2025"); err == nil {
		t.Fatal("accepted free text")
	}
	got, err := ParseHolidayDate(" 2025-10-03 ")
	if err != nil || got != "2025-10-03" {
		t.Fatalf("ParseHolidayDate = %q %v", got, err)
	}
}
