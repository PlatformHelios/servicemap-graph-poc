package cmdb

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeGeocoder struct {
	calls int
	point GeoPoint
	found bool
}

func (f *fakeGeocoder) GeocodeLocation(context.Context, map[string]any) (GeoPoint, bool, error) {
	f.calls++
	return f.point, f.found, nil
}

// recordingStore captures the properties handed to the store and serves one existing node.
type recordingStore struct {
	Store
	existing map[string]any
	saved    map[string]any
}

func (r *recordingStore) CreateGeneratedNode(_ context.Context, kind NodeKind, properties map[string]any, _ []string) (*Node, error) {
	r.saved = properties
	return &Node{Kind: kind, ID: "CI-000001", Properties: properties, Status: "active"}, nil
}

func (r *recordingStore) GetNode(_ context.Context, kind NodeKind, id string, _ bool) (*Node, error) {
	return &Node{Kind: kind, ID: id, Properties: r.existing, Status: "active"}, nil
}

func (r *recordingStore) UpdateNode(_ context.Context, kind NodeKind, id string, properties map[string]any) (*Node, error) {
	r.saved = properties
	return &Node{Kind: kind, ID: id, Properties: properties, Status: "active"}, nil
}

func TestLocationCreateIsGeocoded(t *testing.T) {
	store := &recordingStore{}
	geocoder := &fakeGeocoder{point: GeoPoint{Latitude: 32.78, Longitude: -96.8, Precision: "address"}, found: true}
	service := NewService(store).WithGeocoder(geocoder)
	if _, err := service.CreateGeneratedNode(context.Background(), CI, map[string]any{"ciType": "location", "name": "Dallas", "state": "TX"}, nil); err != nil {
		t.Fatal(err)
	}
	if geocoder.calls != 1 || store.saved[LatitudeProperty] != 32.78 || store.saved[LongitudeProperty] != -96.8 || store.saved[GeoPrecisionProperty] != "address" {
		t.Fatalf("location was not geocoded: calls=%d saved=%#v", geocoder.calls, store.saved)
	}

	// Explicit coordinates win and are marked manual; non-location CIs are never geocoded.
	geocoder.calls = 0
	if _, err := service.CreateGeneratedNode(context.Background(), CI, map[string]any{"ciType": "location", "name": "x", "latitude": 10, "longitude": 20.5}, nil); err != nil {
		t.Fatal(err)
	}
	if geocoder.calls != 0 || store.saved[LatitudeProperty] != float64(10) || store.saved[GeoPrecisionProperty] != GeoPrecisionManual {
		t.Fatalf("manual coordinates mishandled: calls=%d saved=%#v", geocoder.calls, store.saved)
	}
	if _, err := service.CreateGeneratedNode(context.Background(), CI, map[string]any{"ciType": "server", "name": "srv"}, nil); err != nil || geocoder.calls != 0 {
		t.Fatalf("server CI should not be geocoded: err=%v calls=%d", err, geocoder.calls)
	}
	if _, err := service.CreateGeneratedNode(context.Background(), CI, map[string]any{"ciType": "location", "latitude": 95}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("latitude out of range error = %v, want ErrInvalid", err)
	}
}

func TestLocationAddressUpdateIsRegeocoded(t *testing.T) {
	store := &recordingStore{existing: map[string]any{"ciType": "location", "name": "Dallas", "state": "TX", "city": "Dallas"}}
	geocoder := &fakeGeocoder{point: GeoPoint{Latitude: 30.26, Longitude: -97.74, Precision: "address"}, found: true}
	service := NewService(store).WithGeocoder(geocoder)
	if _, err := service.UpdateNode(context.Background(), CI, "CI-000001", map[string]any{"city": "Austin"}); err != nil {
		t.Fatal(err)
	}
	if geocoder.calls != 1 || store.saved[LatitudeProperty] != 30.26 {
		t.Fatalf("address change was not re-geocoded: calls=%d saved=%#v", geocoder.calls, store.saved)
	}
	geocoder.calls = 0
	if _, err := service.UpdateNode(context.Background(), CI, "CI-000001", map[string]any{"description": "no address change"}); err != nil || geocoder.calls != 0 {
		t.Fatalf("non-address update should not geocode: err=%v calls=%d", err, geocoder.calls)
	}
	// Without a geocoder, saves still succeed and coordinates are left alone.
	plain := NewService(&recordingStore{existing: store.existing})
	if _, err := plain.UpdateNode(context.Background(), CI, "CI-000001", map[string]any{"city": "Waco"}); err != nil {
		t.Fatal(err)
	}
}

func TestHoursPropertyKeys(t *testing.T) {
	keys := HoursPropertyKeys()
	if len(keys) != 7 || keys[0] != "hoursMonday" || keys[6] != "hoursSunday" {
		t.Fatalf("unexpected hour keys: %v", keys)
	}
}

func TestValidateLocationPropertiesNormalizes(t *testing.T) {
	properties := map[string]any{"ciType": "location", "hoursMonday": " 8:00 - 17:00 ", "hoursSunday": "Closed", "timezone": " America/New_York "}
	if err := validateCIProperties(CI, properties, true); err != nil {
		t.Fatal(err)
	}
	if properties["hoursMonday"] != "08:00-17:00" || properties["hoursSunday"] != "closed" || properties["timezone"] != "America/New_York" {
		t.Fatalf("properties were not normalized: %#v", properties)
	}
}

func TestValidateLocationPropertiesRejectsBadValues(t *testing.T) {
	for name, properties := range map[string]map[string]any{
		"bad range":    {"ciType": "location", "hoursMonday": "9-5"},
		"bad hour":     {"ciType": "location", "hoursMonday": "25:00-17:00"},
		"bad minute":   {"ciType": "location", "hoursMonday": "08:60-17:00"},
		"not a string": {"ciType": "location", "hoursMonday": 9},
		"bad zone":     {"ciType": "location", "timezone": "Mars/Olympus"},
		"empty zone":   {"ciType": "location", "timezone": " "},
	} {
		if err := validateCIProperties(CI, properties, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestIsLocationOpenAt(t *testing.T) {
	properties := map[string]any{
		"timezone":     "America/New_York",
		"hoursMonday":  "08:00-17:00",
		"hoursFriday":  "22:00-02:00",
		"hoursSunday":  "closed",
		"hoursTuesday": "00:00-00:00",
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		at    time.Time
		open  bool
		known bool
	}{
		{"monday during hours", time.Date(2026, 10, 5, 9, 30, 0, 0, zone), true, true},
		{"monday before opening", time.Date(2026, 10, 5, 7, 59, 0, 0, zone), false, true},
		{"monday at closing", time.Date(2026, 10, 5, 17, 0, 0, 0, zone), false, true},
		{"monday evaluated from UTC", time.Date(2026, 10, 5, 13, 30, 0, 0, time.UTC), true, true},
		{"tuesday open all day", time.Date(2026, 10, 6, 23, 59, 0, 0, zone), true, true},
		{"friday late night", time.Date(2026, 10, 9, 23, 30, 0, 0, zone), true, true},
		{"saturday after midnight carries friday", time.Date(2026, 10, 10, 1, 30, 0, 0, zone), true, true},
		{"saturday after friday closes", time.Date(2026, 10, 10, 2, 0, 0, 0, zone), false, false},
		{"sunday closed", time.Date(2026, 10, 11, 12, 0, 0, 0, zone), false, true},
		{"wednesday not recorded", time.Date(2026, 10, 7, 12, 0, 0, 0, zone), false, false},
	}
	for _, tc := range cases {
		open, known, err := IsLocationOpenAt(properties, tc.at)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if open != tc.open || known != tc.known {
			t.Errorf("%s: open=%v known=%v, want open=%v known=%v", tc.name, open, known, tc.open, tc.known)
		}
	}
	if _, _, err := IsLocationOpenAt(map[string]any{"timezone": "Nowhere/Nope"}, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad timezone error = %v, want ErrInvalid", err)
	}
}
