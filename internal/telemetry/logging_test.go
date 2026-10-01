package telemetry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewLoggerWritesJSONWithoutOTLPEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_LOGS_EXPORTER", "")
	var output bytes.Buffer
	logger, shutdown, err := NewLogger(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("request handled", "http.response.status_code", 200)
	if err := shutdown(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"msg":"request handled"`) || !strings.Contains(output.String(), `"http.response.status_code":200`) {
		t.Fatalf("unexpected JSON log output: %s", output.String())
	}
}

func TestNewLoggerExportsOTLPLogs(t *testing.T) {
	received := make(chan int, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read OTLP request body: %v", err)
		}
		received <- len(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", collector.URL+"/v1/logs")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")

	var output bytes.Buffer
	logger, shutdown, err := NewLogger(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("export test", "test.record", true)
	if err := shutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case bodySize := <-received:
		if bodySize == 0 {
			t.Fatal("collector received an empty OTLP payload")
		}
	default:
		t.Fatal("collector did not receive an OTLP log request")
	}
}
