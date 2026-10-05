package geocode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCensusMatchUsesAddressCoordinates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("street") != "1600 Pennsylvania Ave NW" || r.URL.Query().Get("state") != "DC" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"result":{"addressMatches":[{"coordinates":{"x":-77.03653,"y":38.89767}}]}}`))
	}))
	defer server.Close()

	geocoder := &Census{Client: server.Client(), BaseURL: server.URL}
	point, found, err := geocoder.GeocodeLocation(context.Background(), map[string]any{
		"addressLine1": "1600 Pennsylvania Ave NW", "city": "Washington", "state": "DC", "postalCode": "20500",
	})
	if err != nil || !found {
		t.Fatalf("GeocodeLocation() = found %v, err %v", found, err)
	}
	if point.Precision != PrecisionAddress || point.Latitude != 38.89767 || point.Longitude != -77.03653 {
		t.Fatalf("unexpected point %+v", point)
	}
}

func TestCensusFallsBackToStateCentroid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"addressMatches":[]}}`))
	}))
	defer server.Close()

	geocoder := &Census{Client: server.Client(), BaseURL: server.URL}
	point, found, err := geocoder.GeocodeLocation(context.Background(), map[string]any{"addressLine1": "1 Nowhere Rd", "city": "Dallas", "state": "TX"})
	if err != nil || !found {
		t.Fatalf("fallback: found %v, err %v", found, err)
	}
	expected, _ := StateCentroid("tx")
	if point != expected || point.Precision != PrecisionState {
		t.Fatalf("fallback point = %+v, want %+v", point, expected)
	}
}

func TestCensusWithoutStreetSkipsNetwork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()

	geocoder := &Census{Client: server.Client(), BaseURL: server.URL}
	point, found, err := geocoder.GeocodeLocation(context.Background(), map[string]any{"state": "HI"})
	if err != nil || !found || point.Precision != PrecisionState || called {
		t.Fatalf("state-only lookup: point %+v found %v err %v called %v", point, found, err, called)
	}
	if _, found, _ := geocoder.GeocodeLocation(context.Background(), map[string]any{"city": "Springfield"}); found {
		t.Fatal("lookup without a street or state should not be found")
	}
}

func TestCensusUnreachableFallsBack(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer server.Close()

	geocoder := &Census{Client: server.Client(), BaseURL: server.URL}
	point, found, err := geocoder.GeocodeLocation(context.Background(), map[string]any{"addressLine1": "5 Main St", "postalCode": "02108", "state": "MA"})
	if err != nil || !found || point.Precision != PrecisionState {
		t.Fatalf("unreachable geocoder should fall back to the state: %+v %v %v", point, found, err)
	}
	if _, found, err := geocoder.GeocodeLocation(context.Background(), map[string]any{"addressLine1": "5 Main St", "postalCode": "02108"}); found || err == nil {
		t.Fatal("unreachable geocoder with no state should report the error")
	}
}
