package httpapi

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/config"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/graph"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/telemetry"
)

//go:embed static/*
var dashboard embed.FS

type handler struct {
	service *cmdb.Service
}

type nodeRequest struct {
	ID         string         `json:"id"`
	Properties map[string]any `json:"properties"`
}

type propertiesRequest struct {
	Properties map[string]any `json:"properties"`
}

type relationshipRequest struct {
	FromID     string         `json:"fromId"`
	ToID       string         `json:"toId"`
	Properties map[string]any `json:"properties"`
}

type relationshipMetadata struct {
	Kind string        `json:"kind"`
	From cmdb.NodeKind `json:"from"`
	To   cmdb.NodeKind `json:"to"`
}

func NewHandler(service *cmdb.Service) http.Handler {
	return NewHandlerWithLogger(service, slog.Default())
}

func NewHandlerWithLogger(service *cmdb.Service, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	api := &handler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", api.health)
	mux.HandleFunc("GET /api/meta", api.metadata)
	mux.HandleFunc("GET /api/nodes/{kind}", api.listNodes)
	mux.HandleFunc("POST /api/nodes/{kind}", api.createNode)
	mux.HandleFunc("GET /api/nodes/{kind}/{id}", api.getNode)
	mux.HandleFunc("PATCH /api/nodes/{kind}/{id}", api.updateNode)
	mux.HandleFunc("DELETE /api/nodes/{kind}/{id}", api.retireNode)
	mux.HandleFunc("GET /api/relationships/{kind}", api.listRelationships)
	mux.HandleFunc("POST /api/relationships/{kind}", api.createRelationship)
	mux.HandleFunc("GET /api/relationships/{kind}/{fromId}/{toId}", api.getRelationship)
	mux.HandleFunc("PATCH /api/relationships/{kind}/{fromId}/{toId}", api.updateRelationship)
	mux.HandleFunc("DELETE /api/relationships/{kind}/{fromId}/{toId}", api.retireRelationship)

	static, err := fs.Sub(dashboard, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	return securityHeaders(requestLogging(logger, mux))
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	if _, err := h.service.ListNodes(r.Context(), cmdb.Identity, false); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Neo4j is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) metadata(w http.ResponseWriter, _ *http.Request) {
	relationships := make([]relationshipMetadata, 0, len(cmdb.RelationshipKinds()))
	for _, kind := range cmdb.RelationshipKinds() {
		definition, err := cmdb.RelationshipDefinitionFor(kind)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		relationships = append(relationships, relationshipMetadata{Kind: string(kind), From: definition.From, To: definition.To})
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodeKinds": cmdb.NodeKinds(), "relationships": relationships})
}

func (h *handler) listNodes(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseNodeKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	includeRetired, err := parseIncludeRetired(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nodes, err := h.service.ListNodes(r.Context(), kind, includeRetired)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (h *handler) createNode(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseNodeKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	request, err := decodeJSON[nodeRequest](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	node, err := h.service.CreateNode(r.Context(), kind, request.ID, request.Properties)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, node)
}

func (h *handler) getNode(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseNodeKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	includeRetired, err := parseIncludeRetired(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	node, err := h.service.GetNode(r.Context(), kind, r.PathValue("id"), includeRetired)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (h *handler) updateNode(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseNodeKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	request, err := decodeJSON[propertiesRequest](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	node, err := h.service.UpdateNode(r.Context(), kind, r.PathValue("id"), request.Properties)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (h *handler) retireNode(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseNodeKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	node, err := h.service.RetireNode(r.Context(), kind, r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (h *handler) listRelationships(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseRelationshipKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	includeRetired, err := parseIncludeRetired(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	relationships, err := h.service.ListRelationships(r.Context(), kind, includeRetired)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relationships)
}

func (h *handler) createRelationship(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseRelationshipKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	request, err := decodeJSON[relationshipRequest](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	relationship, err := h.service.CreateRelationship(r.Context(), kind, request.FromID, request.ToID, request.Properties)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, relationship)
}

func (h *handler) getRelationship(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseRelationshipKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	includeRetired, err := parseIncludeRetired(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	relationship, err := h.service.GetRelationship(r.Context(), kind, r.PathValue("fromId"), r.PathValue("toId"), includeRetired)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relationship)
}

func (h *handler) updateRelationship(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseRelationshipKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	request, err := decodeJSON[propertiesRequest](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	relationship, err := h.service.UpdateRelationship(r.Context(), kind, r.PathValue("fromId"), r.PathValue("toId"), request.Properties)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relationship)
}

func (h *handler) retireRelationship(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseRelationshipKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	relationship, err := h.service.RetireRelationship(r.Context(), kind, r.PathValue("fromId"), r.PathValue("toId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, relationship)
}

func parseIncludeRetired(r *http.Request) (bool, error) {
	value := r.URL.Query().Get("includeRetired")
	if value == "" {
		return false, nil
	}
	includeRetired, err := strconv.ParseBool(value)
	if err != nil {
		return false, errors.New("includeRetired must be true or false")
	}
	return includeRetired, nil
}

func decodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var value T
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("invalid JSON body: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return value, errors.New("request body must contain a single JSON value")
	}
	return value, nil
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, cmdb.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, cmdb.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "request failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Default().Error("write HTTP response failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

const maxLoggedRequestBody = 8 * 1024

func (w *responseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(body)
	w.bytes += written
	return written, err
}

func requestBodyForLog(r *http.Request) (string, bool) {
	if (r.Method != http.MethodPost && r.Method != http.MethodPatch) || r.Body == nil || r.Body == http.NoBody {
		return "", false
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxLoggedRequestBody+1))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
	if err != nil {
		return "[omitted: request body could not be read]", true
	}
	if len(body) > maxLoggedRequestBody {
		return "[omitted: request body exceeds 8192 bytes]", true
	}

	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return "[omitted: request body is not valid JSON]", true
	}
	redacted, err := json.Marshal(redactRequestValue(value))
	if err != nil {
		return "[omitted: request body could not be encoded]", true
	}
	return string(redacted), true
}

func redactRequestValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(value))
		for key, item := range value {
			if sensitiveRequestField(key) {
				redacted[key] = "[REDACTED]"
			} else {
				redacted[key] = redactRequestValue(item)
			}
		}
		return redacted
	case []any:
		redacted := make([]any, len(value))
		for index, item := range value {
			redacted[index] = redactRequestValue(item)
		}
		return redacted
	default:
		return value
	}
}

func sensitiveRequestField(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(key))
	for _, fragment := range []string{"password", "passwd", "secret", "token", "authorization", "credential", "apikey", "privatekey", "cookie"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return normalized == "auth"
}

func requestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestBody, hasRequestBody := requestBodyForLog(r)
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		attributes := []slog.Attr{
			slog.String("http.request.method", r.Method),
			slog.String("http.route", route),
			slog.Int("http.response.status_code", status),
			slog.Float64("http.server.duration_ms", float64(time.Since(started))/float64(time.Millisecond)),
			slog.Int("http.response.body.size", recorder.bytes),
		}
		if hasRequestBody {
			attributes = append(attributes, slog.String("http.request.body", requestBody))
		}
		logger.LogAttrs(r.Context(), level, "http request", attributes...)
	})
}

func Run(ctx context.Context, args []string) error {
	defaultAddress := strings.TrimSpace(os.Getenv("HTTP_ADDR"))
	if defaultAddress == "" {
		defaultAddress = "127.0.0.1:8080"
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := flags.String("addr", defaultAddress, "HTTP listen address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected serve argument %q", flags.Arg(0))
	}
	logger, shutdownTelemetry, err := telemetry.NewLogger(ctx, os.Stdout)
	if err != nil {
		return fmt.Errorf("initialize OpenTelemetry logging: %w", err)
	}
	slog.SetDefault(logger)
	defer func() {
		if err := shutdownTelemetry(); err != nil {
			fmt.Fprintf(os.Stderr, "shutdown OpenTelemetry logging: %v\n", err)
		}
	}()

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	store, err := graph.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close(ctx)

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *address, err)
	}
	server := &http.Server{Addr: *address, Handler: NewHandlerWithLogger(cmdb.NewService(store), logger), ReadHeaderTimeout: 5 * time.Second}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	fmt.Printf("Dashboard listening at http://%s\n", *address)

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
