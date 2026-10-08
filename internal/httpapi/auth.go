package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
)

// ActorHeader is the mocked sign-in identity (or platform-super-admin) until Entra SSO.
const ActorHeader = "X-Actor-Id"

func (h *handler) actorAccess(r *http.Request) (*cmdb.ActorAccess, error) {
	return h.service.ActorAccess(r.Context(), strings.TrimSpace(r.Header.Get(ActorHeader)))
}

func (h *handler) requireActor(w http.ResponseWriter, r *http.Request) (*cmdb.ActorAccess, bool) {
	access, err := h.actorAccess(r)
	if err != nil {
		writeServiceError(w, err)
		return nil, false
	}
	return access, true
}

func (h *handler) requireCapability(w http.ResponseWriter, r *http.Request, capability cmdb.Capability) (*cmdb.ActorAccess, bool) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return nil, false
	}
	if err := access.Require(capability); err != nil {
		writeServiceError(w, err)
		return nil, false
	}
	return access, true
}

func (h *handler) requireAnyCapability(w http.ResponseWriter, r *http.Request, capabilities ...cmdb.Capability) (*cmdb.ActorAccess, bool) {
	access, ok := h.requireActor(w, r)
	if !ok {
		return nil, false
	}
	if err := access.RequireAny(capabilities...); err != nil {
		writeServiceError(w, err)
		return nil, false
	}
	return access, true
}

// ciWriteAllowed is true when the actor may create/update/retire a CI. Location
// CIs may also be managed through the Location Maps entitlements.
func ciWriteAllowed(access *cmdb.ActorAccess, action string, ciType string) bool {
	var configCap, locationCap cmdb.Capability
	switch action {
	case "create":
		configCap, locationCap = cmdb.CapConfigItemCreate, cmdb.CapLocationMapCreate
	case "update":
		configCap, locationCap = cmdb.CapConfigItemUpdate, cmdb.CapLocationMapUpdate
	case "retire":
		configCap, locationCap = cmdb.CapConfigItemRetire, cmdb.CapLocationMapRetire
	default:
		return false
	}
	if access.Has(configCap) {
		return true
	}
	return ciType == string(cmdb.Location) && access.Has(locationCap)
}

func ciReadAllowed(access *cmdb.ActorAccess) bool {
	return access.HasAny(cmdb.CapConfigItemRead, cmdb.CapLocationMapRead, cmdb.CapMapRead)
}

// relationshipReadAllowed lets form users with any tool entitlement load edges,
// and Map readers explore the full relationship set. The Relationships page is
// still gated in the UI by CapRelationshipRead.
func relationshipReadAllowed(access *cmdb.ActorAccess) bool {
	if access.SuperAdmin || access.Has(cmdb.CapMapRead) || access.Has(cmdb.CapRelationshipRead) {
		return true
	}
	return len(access.Capabilities) > 0 || len(access.Entitlements) > 0
}

// raisedBy reports whether the identity drafted or submitted the request, so
// requesters can follow their own request's progress.
func (h *handler) raisedBy(r *http.Request, requestID, actorID string) bool {
	if actorID == "" {
		return false
	}
	request, err := h.service.GetNode(r.Context(), cmdb.Request, requestID, true)
	if err != nil {
		return false
	}
	for _, relationship := range request.Relationships {
		if (relationship.Kind == cmdb.FormSubmitted || relationship.Kind == cmdb.DraftedRequest) && relationship.FromID == actorID && relationship.ToID == requestID {
			return true
		}
	}
	return false
}

func taskAssignedTo(actorID string, task *cmdb.TaskView) bool {
	if task == nil || actorID == "" {
		return false
	}
	for _, assignee := range task.Assignees {
		if assignee.ID == actorID {
			return true
		}
	}
	return false
}

func (h *handler) authorizeNodeList(w http.ResponseWriter, access *cmdb.ActorAccess, kind cmdb.NodeKind) bool {
	// The Map tool loads every node kind for graph exploration.
	if access.Has(cmdb.CapMapRead) {
		return true
	}
	switch kind {
	case cmdb.Identity:
		// Self-only listing is handled after the query when read is missing.
		return true
	case cmdb.CI:
		if ciReadAllowed(access) {
			return true
		}
		writeServiceError(w, fmt.Errorf("%w: missing capability %s", cmdb.ErrForbidden, cmdb.CapConfigItemRead))
		return false
	case cmdb.Request:
		if access.HasAny(cmdb.CapVendorRequestUse, cmdb.CapAccessRequestUse) || access.SuperAdmin {
			return true
		}
		// Home lists the actor's own open requests without a catalog entitlement.
		return true
	case cmdb.WorkflowRun:
		if err := access.Require(cmdb.CapWorkflowRunsRead); err != nil {
			writeServiceError(w, err)
			return false
		}
		return true
	default:
		capability, ok := cmdb.NodeReadCapability(kind)
		if !ok {
			writeServiceError(w, fmt.Errorf("%w: unsupported node kind for access control", cmdb.ErrForbidden))
			return false
		}
		if err := access.Require(capability); err != nil {
			writeServiceError(w, err)
			return false
		}
		return true
	}
}

func (h *handler) authorizeNodeGet(w http.ResponseWriter, access *cmdb.ActorAccess, kind cmdb.NodeKind, id string) bool {
	if access.Has(cmdb.CapMapRead) {
		return true
	}
	if kind == cmdb.Identity && (access.SuperAdmin || access.ActorID == id || access.Has(cmdb.CapIdentityRead)) {
		return true
	}
	return h.authorizeNodeList(w, access, kind)
}

func (h *handler) authorizeNodeWrite(w http.ResponseWriter, access *cmdb.ActorAccess, kind cmdb.NodeKind, action string, properties map[string]any) bool {
	if kind == cmdb.CI {
		ciType, _ := properties["ciType"].(string)
		if !ciWriteAllowed(access, action, ciType) {
			writeServiceError(w, fmt.Errorf("%w: missing capability to %s configuration items", cmdb.ErrForbidden, action))
			return false
		}
		return true
	}
	if kind == cmdb.Request {
		if err := access.Require(cmdb.CapVendorRequestUse); err != nil {
			writeServiceError(w, err)
			return false
		}
		return true
	}
	capability, ok := cmdb.NodeWriteCapability(kind, action)
	if !ok {
		writeServiceError(w, fmt.Errorf("%w: %s is not allowed for %s through this API", cmdb.ErrForbidden, action, kind))
		return false
	}
	if err := access.Require(capability); err != nil {
		writeServiceError(w, err)
		return false
	}
	return true
}
