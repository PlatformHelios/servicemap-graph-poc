package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
)

// Service Catalog endpoints: teams publish items from CI; people raise
// requests from the items offered to them.

func (h *handler) listCatalogItems(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireCapability(w, r, cmdb.CapCatalogUse)
	if !ok {
		return
	}
	items, err := h.service.ListCatalogItems(r.Context(), access.ActorID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *handler) getCatalogItem(w http.ResponseWriter, r *http.Request) {
	if item, ok := h.visibleCatalogItem(w, r); ok {
		writeJSON(w, http.StatusOK, item)
	}
}

// visibleCatalogItem loads the item named in the path, provided the actor may
// see it.
func (h *handler) visibleCatalogItem(w http.ResponseWriter, r *http.Request) (*cmdb.CatalogItemView, bool) {
	access, ok := h.requireCapability(w, r, cmdb.CapCatalogUse)
	if !ok {
		return nil, false
	}
	item, err := h.service.GetCatalogItem(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return nil, false
	}
	visible, err := h.service.CatalogItemVisibleTo(r.Context(), item, access.ActorID)
	if err != nil {
		writeServiceError(w, err)
		return nil, false
	}
	if !visible {
		writeServiceError(w, fmt.Errorf("%w: %s is not offered to %s", cmdb.ErrForbidden, item.ID, access.ActorID))
		return nil, false
	}
	return item, true
}

// catalogFieldOptions lists what a picker field on an item's form offers.
func (h *handler) catalogFieldOptions(w http.ResponseWriter, r *http.Request) {
	item, ok := h.visibleCatalogItem(w, r)
	if !ok {
		return
	}
	options, err := h.service.CatalogFieldOptions(r.Context(), item, r.PathValue("field"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

// publishCatalogItem is what a team's CI pipeline calls with the manifest it
// generated from code. The caller must belong to the item's owner group.
func (h *handler) publishCatalogItem(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireCapability(w, r, cmdb.CapCatalogPublish)
	if !ok {
		return
	}
	manifest, err := decodeJSON[cmdb.CatalogManifest](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item, err := h.service.PublishCatalogItem(r.Context(), access.ActorID, manifest)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *handler) createCatalogRequest(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireCapability(w, r, cmdb.CapCatalogUse)
	if !ok {
		return
	}
	input, err := decodeJSON[cmdb.CatalogRequestInput](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// People raise catalog requests for themselves; only the super admin may
	// name another requester, and raises it for itself when it names nobody.
	if access.SuperAdmin && strings.TrimSpace(input.RequestedByID) == "" {
		input.RequestedByID = cmdb.PlatformSuperAdminID
	}
	if !access.SuperAdmin {
		if strings.TrimSpace(input.RequestedByID) == "" {
			input.RequestedByID = access.ActorID
		}
		if input.RequestedByID != access.ActorID {
			writeServiceError(w, fmt.Errorf("%w: catalog requests are raised by the signed-in identity", cmdb.ErrForbidden))
			return
		}
	}
	request, err := h.service.CreateCatalogRequest(r.Context(), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, request)
}

// Workflow Analyzer: the overview of every item the caller can see, one item
// in full, and a draft manifest checked before it is published.

func (h *handler) analyzeCatalogItems(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireCapability(w, r, cmdb.CapCatalogUse)
	if !ok {
		return
	}
	summaries, err := h.service.AnalyzeCatalogItems(r.Context(), access.ActorID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (h *handler) analyzeCatalogItem(w http.ResponseWriter, r *http.Request) {
	item, ok := h.visibleCatalogItem(w, r)
	if !ok {
		return
	}
	analysis, err := h.service.AnalyzeCatalogItem(r.Context(), item.ID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}

func (h *handler) analyzeManifest(w http.ResponseWriter, r *http.Request) {
	access, ok := h.requireCapability(w, r, cmdb.CapCatalogPublish)
	if !ok {
		return
	}
	manifest, err := decodeJSON[cmdb.CatalogManifest](w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	analysis, err := h.service.AnalyzeManifest(r.Context(), access.ActorID, manifest)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}
