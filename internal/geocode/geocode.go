// Package geocode resolves US postal addresses on location CIs to coordinates so
// sites can be plotted on the dashboard's location map.
//
// Addresses are first sent to the US Census Bureau geocoder, which is free, needs
// no API key, and covers the United States (the only country location CIs
// support). When the Census service cannot match the address, or is unreachable,
// the site falls back to the centroid of its state so it still appears on the map
// at reduced precision.
package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
)

const (
	// PrecisionAddress means the Census geocoder matched the street address.
	PrecisionAddress = "address"
	// PrecisionState means only the state was known, so the state centroid is used.
	PrecisionState = "state"

	defaultCensusURL = "https://geocoding.geo.census.gov/geocoder/locations/address"
	defaultTimeout   = 6 * time.Second
)

// Census geocodes through the US Census Bureau and falls back to state centroids.
type Census struct {
	Client  *http.Client
	BaseURL string
}

// NewCensus returns a geocoder with sensible defaults. Pass nil to use a client
// with a short timeout so a slow geocoder never stalls a save for long.
func NewCensus(client *http.Client) *Census {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Census{Client: client, BaseURL: defaultCensusURL}
}

var _ cmdb.Geocoder = (*Census)(nil)

// GeocodeLocation implements cmdb.Geocoder.
func (c *Census) GeocodeLocation(ctx context.Context, properties map[string]any) (cmdb.GeoPoint, bool, error) {
	street := stringProperty(properties, "addressLine1")
	city := stringProperty(properties, "city")
	state := stringProperty(properties, "state")
	zip := stringProperty(properties, "postalCode")

	var lookupErr error
	if street != "" && (zip != "" || (city != "" && state != "")) {
		point, found, err := c.lookup(ctx, street, city, state, zip)
		if found {
			return point, true, nil
		}
		lookupErr = err
	}
	if point, ok := StateCentroid(state); ok {
		return point, true, nil
	}
	if lookupErr != nil {
		return cmdb.GeoPoint{}, false, lookupErr
	}
	return cmdb.GeoPoint{}, false, nil
}

type censusResponse struct {
	Result struct {
		AddressMatches []struct {
			Coordinates struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"coordinates"`
		} `json:"addressMatches"`
	} `json:"result"`
}

func (c *Census) lookup(ctx context.Context, street, city, state, zip string) (cmdb.GeoPoint, bool, error) {
	query := url.Values{"street": {street}, "benchmark": {"Public_AR_Current"}, "format": {"json"}}
	if city != "" {
		query.Set("city", city)
	}
	if state != "" {
		query.Set("state", state)
	}
	if zip != "" {
		query.Set("zip", zip)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"?"+query.Encode(), nil)
	if err != nil {
		return cmdb.GeoPoint{}, false, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.Client.Do(request)
	if err != nil {
		return cmdb.GeoPoint{}, false, fmt.Errorf("census geocoder: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return cmdb.GeoPoint{}, false, fmt.Errorf("census geocoder: unexpected status %d", response.StatusCode)
	}
	var payload censusResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return cmdb.GeoPoint{}, false, fmt.Errorf("census geocoder: decode response: %w", err)
	}
	if len(payload.Result.AddressMatches) == 0 {
		return cmdb.GeoPoint{}, false, errors.New("census geocoder: no address match")
	}
	match := payload.Result.AddressMatches[0].Coordinates
	return cmdb.GeoPoint{Latitude: match.Y, Longitude: match.X, Precision: PrecisionAddress}, true, nil
}

func stringProperty(properties map[string]any, key string) string {
	value, _ := properties[key].(string)
	return strings.TrimSpace(value)
}

// stateCentroids holds approximate geographic centers (latitude, longitude) for the
// 50 states plus DC, used when a street address cannot be resolved.
var stateCentroids = map[string][2]float64{
	"AL": {32.806671, -86.791130}, "AK": {61.370716, -152.404419}, "AZ": {33.729759, -111.431221}, "AR": {34.969704, -92.373123},
	"CA": {36.116203, -119.681564}, "CO": {39.059811, -105.311104}, "CT": {41.597782, -72.755371}, "DE": {39.318523, -75.507141},
	"DC": {38.897438, -77.026817}, "FL": {27.766279, -81.686783}, "GA": {33.040619, -83.643074}, "HI": {21.094318, -157.498337},
	"ID": {44.240459, -114.478828}, "IL": {40.349457, -88.986137}, "IN": {39.849426, -86.258278}, "IA": {42.011539, -93.210526},
	"KS": {38.526600, -96.726486}, "KY": {37.668140, -84.670067}, "LA": {31.169546, -91.867805}, "ME": {44.693947, -69.381927},
	"MD": {39.063946, -76.802101}, "MA": {42.230171, -71.530106}, "MI": {43.326618, -84.536095}, "MN": {45.694454, -93.900192},
	"MS": {32.741646, -89.678696}, "MO": {38.456085, -92.288368}, "MT": {46.921925, -110.454353}, "NE": {41.125370, -98.268082},
	"NV": {38.313515, -117.055374}, "NH": {43.452492, -71.563896}, "NJ": {40.298904, -74.521011}, "NM": {34.840515, -106.248482},
	"NY": {42.165726, -74.948051}, "NC": {35.630066, -79.806419}, "ND": {47.528912, -99.784012}, "OH": {40.388783, -82.764915},
	"OK": {35.565342, -96.928917}, "OR": {44.572021, -122.070938}, "PA": {40.590752, -77.209755}, "RI": {41.680893, -71.511780},
	"SC": {33.856892, -80.945007}, "SD": {44.299782, -99.438828}, "TN": {35.747845, -86.692345}, "TX": {31.054487, -97.563461},
	"UT": {40.150032, -111.862434}, "VT": {44.045876, -72.710686}, "VA": {37.769337, -78.169968}, "WA": {47.400902, -121.490494},
	"WV": {38.491226, -80.954453}, "WI": {44.268543, -89.616508}, "WY": {42.755966, -107.302490},
}

// StateCentroid returns the approximate center of a US state given its two-letter code.
func StateCentroid(state string) (cmdb.GeoPoint, bool) {
	centroid, ok := stateCentroids[strings.ToUpper(strings.TrimSpace(state))]
	if !ok {
		return cmdb.GeoPoint{}, false
	}
	return cmdb.GeoPoint{Latitude: centroid[0], Longitude: centroid[1], Precision: PrecisionState}, true
}
