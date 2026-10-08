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

	"github.com/PlatformHelios/servicemap-graph-poc/internal/automation"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/config"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/geocode"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/graph"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/telemetry"
)

//go:embed openapi.yaml static/*
var dashboard embed.FS

type handler struct {
	service *cmdb.Service
}

type nodeRequest struct {
	ID         string         `json:"id,omitempty"`
	Properties map[string]any `json:"properties"`
	CIIDs      []string       `json:"ciIds,omitempty"`
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
	Kind        string          `json:"kind"`
	Inverse     string          `json:"inverse"`
	From        cmdb.NodeKind   `json:"from"`
	To          cmdb.NodeKind   `json:"to"`
	FromCITypes []string        `json:"fromCiTypes,omitempty"`
	ToCITypes   []string        `json:"toCiTypes,omitempty"`
	FromKinds   []cmdb.NodeKind `json:"fromKinds,omitempty"`   // present when the from node may be more than one kind
	ToKinds     []cmdb.NodeKind `json:"toKinds,omitempty"`     // present when the to node may be more than one kind
	CITypeRules []ciTypeRule    `json:"ciTypeRules,omitempty"` // present when the allowed CI types depend on the pairing
}

type ciTypeRule struct {
	From []string `json:"from"`
	To   []string `json:"to"`
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
	mux.HandleFunc("GET /api/openapi.yaml", api.openapi)
	mux.HandleFunc("GET /api/docs", api.docs)
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
	// Catalog workflows: definitions built in the Workflow Creator, and the tasks
	// their runs raise for assignees once a request is submitted.
	mux.HandleFunc("GET /api/workflows", api.listWorkflows)
	mux.HandleFunc("POST /api/workflows", api.createWorkflow)
	mux.HandleFunc("GET /api/workflows/{id}", api.getWorkflow)
	mux.HandleFunc("PUT /api/workflows/{id}", api.updateWorkflow)
	mux.HandleFunc("DELETE /api/workflows/{id}", api.retireWorkflow)
	mux.HandleFunc("GET /api/tasks", api.listTasks)
	mux.HandleFunc("GET /api/tasks/{id}", api.getTask)
	mux.HandleFunc("POST /api/tasks/{id}/actions", api.actOnTask)
	// Access requests: what an identity may ask for, raising a request, and
	// birthright vs direct permission overlaps.
	mux.HandleFunc("GET /api/access-options", api.accessOptions)
	mux.HandleFunc("GET /api/access-anomalies", api.accessAnomalies)
	mux.HandleFunc("POST /api/access-anomalies/refresh", api.refreshAccessAnomalies)
	mux.HandleFunc("POST /api/access-requests", api.createAccessRequest)
	mux.HandleFunc("GET /api/holidays", api.listHolidays)
	mux.HandleFunc("PUT /api/holidays", api.saveHolidays)
	mux.HandleFunc("GET /api/form-sla", api.formSLA)
	mux.HandleFunc("GET /api/capabilities", api.capabilities)
	// Service Catalog: items published by the teams that automate them, and
	// requests raised from them.
	mux.HandleFunc("GET /api/catalog-items", api.listCatalogItems)
	mux.HandleFunc("POST /api/catalog-items", api.publishCatalogItem)
	mux.HandleFunc("GET /api/catalog-items/{id}", api.getCatalogItem)
	mux.HandleFunc("GET /api/catalog-items/{id}/options/{field}", api.catalogFieldOptions)
	mux.HandleFunc("POST /api/catalog-requests", api.createCatalogRequest)
	mux.HandleFunc("GET /api/catalog-analysis", api.analyzeCatalogItems)
	mux.HandleFunc("POST /api/catalog-analysis", api.analyzeManifest)
	mux.HandleFunc("GET /api/catalog-analysis/{id}", api.analyzeCatalogItem)

	static, err := fs.Sub(dashboard, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	return securityHeaders(requestLogging(logger, mux))
}

func (h *handler) openapi(w http.ResponseWriter, _ *http.Request) {
	spec, err := dashboard.ReadFile("openapi.yaml")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "OpenAPI spec is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(spec); err != nil {
		slog.Default().Error("write OpenAPI spec failed", "error", err)
	}
}

func (h *handler) docs(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/docs.html", http.StatusFound)
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	if _, _, err := h.service.ListNodes(r.Context(), cmdb.Identity, cmdb.ListOptions{Limit: 1}); err != nil {
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
		item := relationshipMetadata{Kind: string(kind), Inverse: definition.Inverse, From: definition.From, To: definition.To, FromCITypes: cmdb.CITypeNamesOf(definition.FromCITypes), ToCITypes: cmdb.CITypeNamesOf(definition.ToCITypes)}
		if len(definition.FromKinds) > 1 {
			item.FromKinds = definition.FromKinds
		}
		if len(definition.ToKinds) > 1 {
			item.ToKinds = definition.ToKinds
		}
		if len(definition.CITypeRules) > 1 {
			for _, rule := range definition.CITypeRules {
				item.CITypeRules = append(item.CITypeRules, ciTypeRule{From: cmdb.CITypeNamesOf(rule.From), To: cmdb.CITypeNamesOf(rule.To)})
			}
		}
		relationships = append(relationships, item)
	}
	requestFields := make(map[string][]string, len(cmdb.WorkflowFormTypes()))
	fulfilmentFields := make(map[string][]string, len(cmdb.WorkflowFormTypes()))
	for _, name := range cmdb.WorkflowFormTypes() {
		requestFields[name] = cmdb.RequestFieldKeys(cmdb.RequestType(name))
		fulfilmentFields[name] = cmdb.RequestFulfilmentProperties(cmdb.RequestType(name))
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodeKinds": cmdb.NodeKinds(), "ciTypes": cmdb.CITypeNames(), "ciCategories": cmdb.CICategoryNames(), "contractTypes": cmdb.ContractTypeNames(), "hostingModels": cmdb.HostingModelNames(), "criticalities": cmdb.CriticalityNames(), "requestTypes": cmdb.WorkflowFormTypes(), "requestStates": cmdb.RequestStateNames(), "requestFields": requestFields, "fulfilmentFields": fulfilmentFields, "stepTypes": cmdb.StepTypeNames(), "approvalRules": cmdb.ApprovalRuleNames(), "taskActions": cmdb.TaskActionNames(), "raciKinds": cmdb.RACIOwnedKinds(), "accessItemKinds": cmdb.AccessItemKinds(), "accessWorkflowTemplate": cmdb.AccessWorkflowTemplate(), "relationships": relationships})
}

func (h *handler) listWorkflows(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireCapability(w, r, cmdb.CapWorkflowCreatorUse); !ok {
		return
	}
	includeRetired, err := parseIncludeRetired(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	workflows, err := h.service.ListWorkflows(r.Context(), includeRetired)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workflows)
}

func (h *handler) createWorkflow(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireCapability(w, r, cmdb.CapWorkflowCreatorUse); !ok {
		return
	}
	definition, err := decodeJSON[cmdb.WorkflowDefinition](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(definition.ID) != "" {
		writeError(w, http.StatusBadRequest, "id is assigned automatically; use PUT /api/workflows/{id} to update a workflow")
		return
	}
	workflow, err := h.service.SaveWorkflow(r.Context(), "", definition)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, workflow)
}

func (h *handler) getWorkflow(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireCapability(w, r, cmdb.CapWorkflowCreatorUse); !ok {
		return
	}
	workflow, err := h.service.GetWorkflow(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workflow)
}

func (h *handler) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireCapability(w, r, cmdb.CapWorkflowCreatorUse); !ok {
		return
	}
	definition, err := decodeJSON[cmdb.WorkflowDefinition](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(definition.ID) != "" && definition.ID != id {
		writeError(w, http.StatusBadRequest, "workflow id in the body does not match the path")
		return
	}
	workflow, err := h.service.SaveWorkflow(r.Context(), id, definition)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workflow)
}

func (h *handler) retireWorkflow(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireCapability(w, r, cmdb.CapWorkflowCreatorUse); !ok {
		return
	}
	workflow, err := h.service.RetireWorkflow(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workflow)
}

func (h *handler) listTasks(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	filter := cmdb.TaskFilter{ActorID: r.URL.Query().Get("actorId"), RequestID: r.URL.Query().Get("requestId")}
	// Own queue (Home) and the trail of one's own request are always allowed;
	// listing all tasks needs the Workflow Tasks entitlement.
	ownQueue := !access.SuperAdmin && filter.ActorID != "" && filter.ActorID == access.ActorID && filter.RequestID == ""
	ownRequest := !access.SuperAdmin && filter.ActorID == "" && filter.RequestID != "" && h.raisedBy(r, filter.RequestID, access.ActorID)
	if !ownQueue && !ownRequest && !access.Has(cmdb.CapWorkflowTasksRead) && !access.SuperAdmin {
		writeServiceError(w, fmt.Errorf("%w: missing capability %s", cmdb.ErrForbidden, cmdb.CapWorkflowTasksRead))
		return
	}
	if value := r.URL.Query().Get("includeDone"); value != "" {
		includeDone, err := strconv.ParseBool(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, "includeDone must be true or false")
			return
		}
		filter.IncludeDone = includeDone
	}
	limit, offset, err := parsePage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter.Limit, filter.Offset = limit, offset
	tasks, total, err := h.service.ListTasks(r.Context(), filter)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, tasks)
}

func (h *handler) getTask(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	task, err := h.service.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if !access.SuperAdmin && !access.Has(cmdb.CapWorkflowTasksRead) && !taskAssignedTo(access.ActorID, task) {
		writeServiceError(w, fmt.Errorf("%w: missing capability %s", cmdb.ErrForbidden, cmdb.CapWorkflowTasksRead))
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *handler) actOnTask(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	input, err := decodeJSON[cmdb.TaskActionInput](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if access.SuperAdmin && strings.TrimSpace(input.ActorID) == "" {
		input.ActorID = cmdb.PlatformSuperAdminID
	}
	if !access.SuperAdmin && strings.TrimSpace(input.ActorID) == cmdb.PlatformSuperAdminID {
		writeServiceError(w, fmt.Errorf("%w: only the platform super admin may act as the platform super admin", cmdb.ErrForbidden))
		return
	}
	// Assignees may act from Home without the Workflow Tasks page entitlement.
	if !access.SuperAdmin && !access.Has(cmdb.CapWorkflowTasksRead) {
		if strings.TrimSpace(input.ActorID) == "" {
			input.ActorID = access.ActorID
		}
		if input.ActorID != access.ActorID {
			writeServiceError(w, fmt.Errorf("%w: missing capability %s", cmdb.ErrForbidden, cmdb.CapWorkflowTasksRead))
			return
		}
	}
	task, err := h.service.ActOnTask(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *handler) capabilities(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, access)
}

func (h *handler) accessOptions(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	identityID := strings.TrimSpace(r.URL.Query().Get("identityId"))
	if !access.SuperAdmin && access.ActorID != identityID && !access.Has(cmdb.CapAccessRequestUse) {
		writeServiceError(w, fmt.Errorf("%w: access options for another identity require %s", cmdb.ErrForbidden, cmdb.CapAccessRequestUse))
		return
	}
	options, err := h.service.AccessOptions(r.Context(), identityID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

func (h *handler) accessAnomalies(w http.ResponseWriter, r *http.Request) {
	// Birthrights page and Map badges both need this; Map readers hold CapMapRead.
	if _, ok := h.requireAnyCapability(w, r, cmdb.CapBirthrightRead, cmdb.CapMapRead); !ok {
		return
	}
	report, err := h.service.AccessAnomalies(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// refreshAccessAnomalies reruns the whole-graph anomaly check now rather than
// waiting for the schedule. The dashboard only offers it in development.
func (h *handler) refreshAccessAnomalies(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAnyCapability(w, r, cmdb.CapBirthrightRead, cmdb.CapMapRead); !ok {
		return
	}
	report, err := h.service.RefreshAccessAnomalies(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *handler) createAccessRequest(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireCapability(w, r, cmdb.CapAccessRequestUse); !ok {
		return
	}
	input, err := decodeJSON[cmdb.AccessRequestInput](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	request, err := h.service.CreateAccessRequest(r.Context(), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, request)
}

func (h *handler) formSLA(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAnyCapability(w, r, cmdb.CapVendorRequestUse, cmdb.CapAccessRequestUse, cmdb.CapWorkflowCreatorUse); !ok {
		return
	}
	requestType, err := cmdb.ParseRequestType(r.URL.Query().Get("requestType"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled, days, err := h.service.FormSLA(r.Context(), requestType)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requestType": requestType, "enabled": enabled, "businessDays": days})
}

func (h *handler) listHolidays(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if !access.SuperAdmin {
		writeServiceError(w, cmdb.ErrForbidden)
		return
	}
	holidays, err := h.service.ListHolidays(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if holidays == nil {
		holidays = []cmdb.Holiday{}
	}
	writeJSON(w, http.StatusOK, holidays)
}

func (h *handler) saveHolidays(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if !access.SuperAdmin {
		writeServiceError(w, cmdb.ErrForbidden)
		return
	}
	body, err := decodeJSON[struct {
		Holidays []cmdb.Holiday `json:"holidays"`
	}](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	holidays, err := h.service.SaveHolidays(r.Context(), body.Holidays)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, holidays)
}

func (h *handler) listNodes(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseNodeKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if !h.authorizeNodeList(w, access, kind) {
		return
	}
	options, err := parseListOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Without identities-read, an identity may only list itself (session / Home).
	// Map readers need the full identity set for graph exploration.
	if kind == cmdb.Identity && !access.SuperAdmin && !access.Has(cmdb.CapIdentityRead) && !access.Has(cmdb.CapMapRead) {
		self, err := h.service.GetNode(r.Context(), kind, access.ActorID, options.IncludeRetired)
		if err != nil && !errors.Is(err, cmdb.ErrNotFound) {
			writeServiceError(w, err)
			return
		}
		nodes := []cmdb.Node{}
		if self != nil {
			nodes = append(nodes, *self)
		}
		writeTotals(w, cmdb.ListTotals{Total: len(nodes), Active: len(nodes)})
		writeJSON(w, http.StatusOK, nodes)
		return
	}
	nodes, totals, err := h.service.ListNodes(r.Context(), kind, options)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeTotals(w, totals)
	writeJSON(w, http.StatusOK, nodes)
}

// writeTotals reports how many records a list matched before paging.
func writeTotals(w http.ResponseWriter, totals cmdb.ListTotals) {
	w.Header().Set("X-Total-Count", strconv.Itoa(totals.Total))
	w.Header().Set("X-Active-Count", strconv.Itoa(totals.Active))
}

func (h *handler) createNode(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseNodeKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	request, err := decodeJSON[nodeRequest](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(request.ID) != "" {
		writeError(w, http.StatusBadRequest, "id is assigned automatically for all node records")
		return
	}
	if !h.authorizeNodeWrite(w, access, kind, "create", request.Properties) {
		return
	}
	node, err := h.service.CreateGeneratedNode(r.Context(), kind, request.Properties, request.CIIDs)
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
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !h.authorizeNodeGet(w, access, kind, id) {
		return
	}
	includeRetired, err := parseIncludeRetired(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	node, err := h.service.GetNode(r.Context(), kind, id, includeRetired)
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
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	request, err := decodeJSON[propertiesRequest](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	properties := request.Properties
	if kind == cmdb.CI {
		existing, err := h.service.GetNode(r.Context(), kind, r.PathValue("id"), true)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if properties == nil {
			properties = map[string]any{}
		}
		if _, ok := properties["ciType"]; !ok {
			properties["ciType"] = existing.Properties["ciType"]
		}
	}
	if !h.authorizeNodeWrite(w, access, kind, "update", properties) {
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
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	properties := map[string]any{}
	if kind == cmdb.CI {
		existing, err := h.service.GetNode(r.Context(), kind, r.PathValue("id"), true)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		properties["ciType"] = existing.Properties["ciType"]
	}
	if !h.authorizeNodeWrite(w, access, kind, "retire", properties) {
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
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if !relationshipReadAllowed(access) {
		writeServiceError(w, fmt.Errorf("%w: listing relationships requires a platform tool entitlement", cmdb.ErrForbidden))
		return
	}
	options, err := parseListOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	relationships, totals, err := h.service.ListRelationships(r.Context(), kind, options)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeTotals(w, totals)
	writeJSON(w, http.StatusOK, relationships)
}

func (h *handler) createRelationship(w http.ResponseWriter, r *http.Request) {
	kind, err := cmdb.ParseRelationshipKind(r.PathValue("kind"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if err := access.RequireAny(cmdb.RelationshipMutateCapabilities(kind)...); err != nil {
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
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if !relationshipReadAllowed(access) {
		writeServiceError(w, fmt.Errorf("%w: reading relationships requires a platform tool entitlement", cmdb.ErrForbidden))
		return
	}
	_ = kind
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
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if err := access.RequireAny(cmdb.RelationshipMutateCapabilities(kind)...); err != nil {
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
	access, ok := h.requireActor(w, r)
	if !ok {
		return
	}
	if err := access.RequireAny(cmdb.RelationshipMutateCapabilities(kind)...); err != nil {
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

// maxPageSize caps one page so a client cannot ask for the whole graph in a
// single paged request; omit limit to read everything (pickers, the map).
const maxPageSize = 500

// parsePage reads limit/offset; a missing limit means "no paging".
func parsePage(r *http.Request) (limit, offset int, err error) {
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > maxPageSize {
			return 0, 0, fmt.Errorf("limit must be a whole number from 1 to %d", maxPageSize)
		}
	}
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset must be a whole number of 0 or more")
		}
	}
	return limit, offset, nil
}

// parseListOptions reads the list query parameters shared by node and
// relationship lists: includeRetired, limit, offset, q, and involvedId.
func parseListOptions(r *http.Request) (cmdb.ListOptions, error) {
	includeRetired, err := parseIncludeRetired(r)
	if err != nil {
		return cmdb.ListOptions{}, err
	}
	limit, offset, err := parsePage(r)
	if err != nil {
		return cmdb.ListOptions{}, err
	}
	return cmdb.ListOptions{IncludeRetired: includeRetired, Limit: limit, Offset: offset, Query: r.URL.Query().Get("q"), InvolvedID: r.URL.Query().Get("involvedId"), RequestType: cmdb.RequestType(strings.TrimSpace(r.URL.Query().Get("requestType")))}, nil
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
	case errors.Is(err, cmdb.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, cmdb.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, cmdb.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		// The cause stays in the server log; clients only learn the request failed.
		slog.Default().Error("request failed", "error", err)
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

// accessAnomalyInterval reads ACCESS_ANOMALY_INTERVAL (a Go duration such as
// 15m or 1h; default 15m). "0" or "off" disables the schedule, leaving the
// snapshot to POST /api/access-anomalies/refresh.
func accessAnomalyInterval() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("ACCESS_ANOMALY_INTERVAL"))
	switch value {
	case "":
		return 15 * time.Minute, nil
	case "0", "off", "none":
		return 0, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval < time.Minute {
		return 0, fmt.Errorf("ACCESS_ANOMALY_INTERVAL must be a duration of at least 1m (for example 15m or 1h), or off")
	}
	return interval, nil
}

// scheduleAccessAnomalies refreshes the anomaly snapshot once at startup and
// then on the interval, so reads never run the whole-graph check themselves.
func scheduleAccessAnomalies(ctx context.Context, service *cmdb.Service, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		return
	}
	refresh := func() {
		started := time.Now()
		report, err := service.RefreshAccessAnomalies(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.ErrorContext(ctx, "access anomaly check failed", "error", err)
			}
			return
		}
		logger.InfoContext(ctx, "access anomaly check completed", "anomalies", len(report.Anomalies), "duration", time.Since(started).String())
	}
	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
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

	service := cmdb.NewService(store).WithGeocoder(geocode.NewCensus(nil))
	// Constraints, indexes, and one-time data migrations run before the first
	// request so every read can rely on them.
	if err := service.EnsureConstraints(ctx); err != nil {
		return fmt.Errorf("prepare Neo4j schema: %w", err)
	}

	// With TEMPORAL_ADDRESS set, approved catalog requests start their team's
	// workflow and this process runs the platform's fulfilment worker.
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	if temporalConfig := automation.ConfigFromEnv(); temporalConfig.Address != "" {
		engine, err := automation.Connect(temporalConfig, logger)
		if err != nil {
			return err
		}
		defer engine.Close()
		service.WithAutomation(engine).WithInsight(engine)
		go engine.RunWorker(workerCtx, service, logger)
		fmt.Printf("Catalog automation via Temporal at %s (namespace %s, task queue %s)\n", temporalConfig.Address, temporalConfig.Namespace, temporalConfig.TaskQueue)
	}

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *address, err)
	}
	server := &http.Server{Addr: *address, Handler: NewHandlerWithLogger(service, logger), ReadHeaderTimeout: 5 * time.Second}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	fmt.Printf("Dashboard listening at http://%s\n", *address)

	anomalyInterval, err := accessAnomalyInterval()
	if err != nil {
		return err
	}
	scheduleCtx, stopSchedule := context.WithCancel(ctx)
	defer stopSchedule()
	go scheduleAccessAnomalies(scheduleCtx, service, anomalyInterval, logger)

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
