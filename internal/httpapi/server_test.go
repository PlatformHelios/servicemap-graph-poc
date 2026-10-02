package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestDashboardServedAtRoot(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", response.Code, http.StatusOK)
	}
	page := response.Body.String()
	if !strings.Contains(page, `id="root"`) {
		t.Fatalf("dashboard HTML does not contain the React root")
	}
	asset := regexp.MustCompile(`src="/(assets/[^\"]+\.js)"`).FindStringSubmatch(page)
	if len(asset) != 2 {
		t.Fatalf("dashboard HTML does not reference a built JavaScript asset: %s", page)
	}
	assetRequest := httptest.NewRequest(http.MethodGet, "/"+asset[1], nil)
	assetResponse := httptest.NewRecorder()
	handler.ServeHTTP(assetResponse, assetRequest)
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("GET /%s status = %d, want %d", asset[1], assetResponse.Code, http.StatusOK)
	}
	if !strings.Contains(assetResponse.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("JavaScript asset content type = %q", assetResponse.Header().Get("Content-Type"))
	}
}

func TestMetadataEndpoint(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/meta status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "has-job-code") || !strings.Contains(response.Body.String(), "entitlement") || !strings.Contains(response.Body.String(), "data-connector") || !strings.Contains(response.Body.String(), "application") || !strings.Contains(response.Body.String(), "incident") || !strings.Contains(response.Body.String(), "depends-on") {
		t.Fatalf("metadata response omitted graph kinds: %s", response.Body.String())
	}
}

func TestOpenAPISpecEndpoint(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/openapi.yaml", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/openapi.yaml status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "openapi: 3.1.0") || !strings.Contains(response.Body.String(), "/api/relationships/{kind}") || !strings.Contains(response.Body.String(), "enum: [server, printer, data-connector, application]") || !strings.Contains(response.Body.String(), "depends-on") {
		t.Fatalf("OpenAPI spec is incomplete")
	}
}

func TestSwaggerDocsRoute(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/docs.html" {
		t.Fatalf("GET /api/docs response = %d %q", response.Code, response.Header().Get("Location"))
	}

	docsRequest := httptest.NewRequest(http.MethodGet, "/docs.html", nil)
	docsResponse := httptest.NewRecorder()
	handler.ServeHTTP(docsResponse, docsRequest)
	if docsResponse.Code != http.StatusOK || !strings.Contains(docsResponse.Body.String(), "/swagger-ui/swagger-ui.css") {
		t.Fatalf("Swagger UI document response = %d", docsResponse.Code)
	}
}

func TestUnknownNodeKindIsBadRequest(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/unknown", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("GET unknown node kind status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestCreateIncidentRequiresCIIDs(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodPost, "/api/nodes/incident", strings.NewReader(`{"properties":{"name":"Unavailable"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "at least one CI") {
		t.Fatalf("POST incident without CI response = %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/nodes/incident", strings.NewReader(`{"id":"INC-1001","ciIds":["ci-1"]}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "assigned automatically") {
		t.Fatalf("POST incident with caller-supplied ID response = %d %s", response.Code, response.Body.String())
	}
}

func TestCreateNodesRejectCallerSuppliedID(t *testing.T) {
	handler := NewHandler(nil)
	for _, kind := range []string{"identity", "job-code", "birthright", "role", "entitlement", "ci", "incident", "change", "event"} {
		request := httptest.NewRequest(http.MethodPost, "/api/nodes/"+kind, strings.NewReader(`{"id":"custom-id","properties":{"ciType":"server"}}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "assigned automatically") {
			t.Errorf("POST %s with caller-supplied ID response = %d %s", kind, response.Code, response.Body.String())
		}
	}
}

func TestRequestLoggingUsesRouteTemplate(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := NewHandlerWithLogger(nil, logger)
	request := httptest.NewRequest(http.MethodGet, "/api/nodes/private-person", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("GET invalid node kind status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	for _, expected := range []string{"http request", "GET /api/nodes/{kind}", "http.response.status_code", "400"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("request log %q missing %q", output.String(), expected)
		}
	}
	if strings.Contains(output.String(), "private-person") {
		t.Fatalf("request log contains raw path value: %s", output.String())
	}
}

func TestRequestLoggingIncludesRedactedPostBodyAndRestoresIt(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	body := `{"id":"e0003","properties":{"department":"Platform Engineering","password":"secret-value","api_key":"key-value"}}`
	var received string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body in handler: %v", err)
		}
		received = string(requestBody)
		w.WriteHeader(http.StatusCreated)
	})
	handler := requestLogging(logger, next)
	request := httptest.NewRequest(http.MethodPost, "/api/nodes/identity", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if received != body {
		t.Fatalf("handler received body %q, want original %q", received, body)
	}
	for _, expected := range []string{"Platform Engineering", "[REDACTED]"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("request log %q does not contain %q", output.String(), expected)
		}
	}
	for _, secret := range []string{"secret-value", "key-value"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("request log leaked %q: %s", secret, output.String())
		}
	}
}

func TestRequestLoggingOmitsOversizedBody(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	body := `{"value":"` + strings.Repeat("x", maxLoggedRequestBody) + `"}`
	var received string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body in handler: %v", err)
		}
		received = string(requestBody)
	})
	handler := requestLogging(logger, next)
	request := httptest.NewRequest(http.MethodPatch, "/api/nodes/identity/e0003", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if received != body {
		t.Fatal("handler did not receive the complete oversized body")
	}
	if !strings.Contains(output.String(), "request body exceeds 8192 bytes") || strings.Contains(output.String(), strings.Repeat("x", 32)) {
		t.Fatalf("oversized request body was not safely omitted: %s", output.String())
	}
}
