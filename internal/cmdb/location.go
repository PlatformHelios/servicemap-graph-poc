package cmdb

import (
	"context"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // embed zone data so timezone lookups work without an OS tz database
)

// Location CIs store hours of operation as one flat string property per weekday,
// for example hoursMonday = "08:00-17:00" or hoursSunday = "closed". Times are
// local to the location's timezone property (an IANA name such as America/New_York).
// A closing time at or before the opening time means the site closes after midnight.
const (
	HoursClosed       = "closed"
	TimezoneProperty  = "timezone"
	hoursPropertyBase = "hours"
)

var weekdayNames = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// HoursPropertyKey returns the property name that holds the hours for a weekday.
func HoursPropertyKey(day time.Weekday) string {
	return hoursPropertyBase + weekdayNames[day]
}

// HoursPropertyKeys lists the weekday hour property names, Monday first.
func HoursPropertyKeys() []string {
	keys := make([]string, 0, 7)
	for offset := 1; offset <= 7; offset++ {
		keys = append(keys, HoursPropertyKey(time.Weekday(offset%7)))
	}
	return keys
}

type dailyHours struct {
	closed bool
	open   int // minutes from midnight
	close  int
}

func parseClock(value string) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[0]) > 2 || len(parts[1]) != 2 {
		return 0, fmt.Errorf("time %q must use HH:MM", value)
	}
	var hour, minute int
	if _, err := fmt.Sscanf(parts[0]+" "+parts[1], "%d %d", &hour, &minute); err != nil {
		return 0, fmt.Errorf("time %q must use HH:MM", value)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("time %q is out of range", value)
	}
	return hour*60 + minute, nil
}

func parseDailyHours(value string) (dailyHours, error) {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == HoursClosed {
		return dailyHours{closed: true}, nil
	}
	parts := strings.Split(text, "-")
	if len(parts) != 2 {
		return dailyHours{}, fmt.Errorf("hours %q must be HH:MM-HH:MM or %q", value, HoursClosed)
	}
	open, err := parseClock(strings.TrimSpace(parts[0]))
	if err != nil {
		return dailyHours{}, err
	}
	closeAt, err := parseClock(strings.TrimSpace(parts[1]))
	if err != nil {
		return dailyHours{}, err
	}
	return dailyHours{open: open, close: closeAt}, nil
}

func formatDailyHours(hours dailyHours) string {
	if hours.closed {
		return HoursClosed
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", hours.open/60, hours.open%60, hours.close/60, hours.close%60)
}

// validateLocationProperties checks and normalizes hours and timezone properties when present.
func validateLocationProperties(properties map[string]any) error {
	for _, key := range HoursPropertyKeys() {
		value, ok := properties[key]
		if !ok {
			continue
		}
		text, isString := value.(string)
		if !isString {
			return fmt.Errorf("%w: %s must be a string", ErrInvalid, key)
		}
		hours, err := parseDailyHours(text)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalid, key, err)
		}
		properties[key] = formatDailyHours(hours)
	}
	if value, ok := properties[TimezoneProperty]; ok {
		text, isString := value.(string)
		if !isString {
			return fmt.Errorf("%w: %s must be a string", ErrInvalid, TimezoneProperty)
		}
		text = strings.TrimSpace(text)
		if _, err := time.LoadLocation(text); err != nil || text == "" {
			return fmt.Errorf("%w: %s %q is not a recognized IANA time zone", ErrInvalid, TimezoneProperty, text)
		}
		properties[TimezoneProperty] = text
	}
	for key, limit := range map[string]float64{LatitudeProperty: 90, LongitudeProperty: 180} {
		value, ok := properties[key]
		if !ok {
			continue
		}
		number, isNumber := numberValue(value)
		if !isNumber || number < -limit || number > limit {
			return fmt.Errorf("%w: %s must be a number between -%v and %v", ErrInvalid, key, limit, limit)
		}
		properties[key] = number
	}
	return nil
}

// Coordinates for plotting a location. They are derived from the address by the
// configured Geocoder when a location CI is created or its address changes, unless
// the caller supplies them explicitly. geoPrecision records how they were derived.
const (
	LatitudeProperty     = "latitude"
	LongitudeProperty    = "longitude"
	GeoPrecisionProperty = "geoPrecision"
	GeoPrecisionManual   = "manual"
)

// AddressPropertyKeys lists the properties that make up a location's postal address.
func AddressPropertyKeys() []string {
	return []string{"addressLine1", "addressLine2", "city", "state", "postalCode", "country"}
}

type GeoPoint struct {
	Latitude  float64
	Longitude float64
	Precision string // e.g. "address" or "state"
}

// Geocoder resolves a location CI's address properties to coordinates. It returns
// found=false when nothing usable could be resolved; errors are treated the same
// way by the service, which never fails a save because geocoding failed.
type Geocoder interface {
	GeocodeLocation(ctx context.Context, properties map[string]any) (point GeoPoint, found bool, err error)
}

func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func hasAddressChange(properties map[string]any) bool {
	for _, key := range AddressPropertyKeys() {
		if _, ok := properties[key]; ok {
			return true
		}
	}
	return false
}

func hasCoordinates(properties map[string]any) bool {
	_, hasLatitude := properties[LatitudeProperty]
	_, hasLongitude := properties[LongitudeProperty]
	return hasLatitude && hasLongitude
}

// IsLocationOpenAt reports whether a location is open at the given instant using its
// stored hours and timezone. The second result is false when the schedule for that
// day is not recorded, in which case the open state is unknown.
func IsLocationOpenAt(properties map[string]any, at time.Time) (open bool, known bool, err error) {
	zone := time.UTC
	if value, ok := properties[TimezoneProperty].(string); ok && strings.TrimSpace(value) != "" {
		zone, err = time.LoadLocation(strings.TrimSpace(value))
		if err != nil {
			return false, false, fmt.Errorf("%w: %s %q is not a recognized IANA time zone", ErrInvalid, TimezoneProperty, value)
		}
	}
	local := at.In(zone)
	minute := local.Hour()*60 + local.Minute()

	today, todayKnown, err := hoursFor(properties, local.Weekday())
	if err != nil {
		return false, false, err
	}
	if todayKnown && !today.closed {
		if today.close > today.open {
			if minute >= today.open && minute < today.close {
				return true, true, nil
			}
		} else if minute >= today.open {
			return true, true, nil
		}
	}

	// A previous day's schedule that runs past midnight can still cover the current time.
	yesterday, yesterdayKnown, err := hoursFor(properties, (local.Weekday()+6)%7)
	if err != nil {
		return false, false, err
	}
	if yesterdayKnown && !yesterday.closed && yesterday.close <= yesterday.open && minute < yesterday.close {
		return true, true, nil
	}
	return false, todayKnown, nil
}

func hoursFor(properties map[string]any, day time.Weekday) (dailyHours, bool, error) {
	value, ok := properties[HoursPropertyKey(day)].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return dailyHours{}, false, nil
	}
	hours, err := parseDailyHours(value)
	if err != nil {
		return dailyHours{}, false, fmt.Errorf("%w: %s: %v", ErrInvalid, HoursPropertyKey(day), err)
	}
	return hours, true, nil
}
