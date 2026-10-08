package cmdb

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Platform Super Admin id used until Entra SSO replaces the mocked sign-in.
// That actor has every capability regardless of entitlements or groups.
const PlatformSuperAdminID = "platform-super-admin"

// PlatformSuperAdminName names the super admin's identity record, which the
// store keeps in place so its actions link to an identity like anyone's.
const PlatformSuperAdminName = "Platform Super Admin"

// ErrForbidden is returned when the actor lacks a required platform capability.
var ErrForbidden = errors.New("forbidden")

// Capability is one action the Anthos UI/API may allow.
type Capability string

const (
	CapIdentityRead         Capability = "identity:read"
	CapIdentityCreate       Capability = "identity:create"
	CapIdentityUpdate       Capability = "identity:update"
	CapIdentityRetire       Capability = "identity:retire"
	CapJobCodeRead          Capability = "job-code:read"
	CapJobCodeCreate        Capability = "job-code:create"
	CapJobCodeUpdate        Capability = "job-code:update"
	CapJobCodeRetire        Capability = "job-code:retire"
	CapBirthrightRead       Capability = "birthright:read"
	CapBirthrightCreate     Capability = "birthright:create"
	CapBirthrightUpdate     Capability = "birthright:update"
	CapBirthrightRetire     Capability = "birthright:retire"
	CapRoleRead             Capability = "role:read"
	CapRoleCreate           Capability = "role:create"
	CapRoleUpdate           Capability = "role:update"
	CapRoleRetire           Capability = "role:retire"
	CapEntitlementRead      Capability = "entitlement:read"
	CapEntitlementCreate    Capability = "entitlement:create"
	CapEntitlementUpdate    Capability = "entitlement:update"
	CapEntitlementRetire    Capability = "entitlement:retire"
	CapGroupRead            Capability = "group:read"
	CapGroupCreate          Capability = "group:create"
	CapGroupUpdate          Capability = "group:update"
	CapGroupRetire          Capability = "group:retire"
	CapIncidentRead         Capability = "incident:read"
	CapIncidentCreate       Capability = "incident:create"
	CapIncidentUpdate       Capability = "incident:update"
	CapIncidentRetire       Capability = "incident:retire"
	CapChangeRead           Capability = "change:read"
	CapChangeCreate         Capability = "change:create"
	CapChangeUpdate         Capability = "change:update"
	CapChangeRetire         Capability = "change:retire"
	CapEventRead            Capability = "event:read"
	CapEventCreate          Capability = "event:create"
	CapEventUpdate          Capability = "event:update"
	CapEventRetire          Capability = "event:retire"
	CapRelationshipRead     Capability = "relationship:read"
	CapRelationshipCreate   Capability = "relationship:create"
	CapRelationshipUpdate   Capability = "relationship:update"
	CapRelationshipRetire   Capability = "relationship:retire"
	CapConfigItemRead       Capability = "config-item:read"
	CapConfigItemCreate     Capability = "config-item:create"
	CapConfigItemUpdate     Capability = "config-item:update"
	CapConfigItemRetire     Capability = "config-item:retire"
	CapLocationMapRead      Capability = "location-map:read"
	CapLocationMapCreate    Capability = "location-map:create"
	CapLocationMapUpdate    Capability = "location-map:update"
	CapLocationMapRetire    Capability = "location-map:retire"
	CapMapRead              Capability = "map:read"
	CapVendorRequestUse     Capability = "vendor-request:use"
	CapAccessRequestUse     Capability = "access-request:use"
	CapWorkflowCreatorUse   Capability = "workflow-creator:use"
	CapWorkflowTasksRead    Capability = "workflow-tasks:read"
	CapWorkflowRunsRead     Capability = "workflow-runs:read"
	// Service Catalog: raising requests from the items an actor can see, and
	// publishing items (what a team's CI pipeline holds).
	CapCatalogUse     Capability = "catalog:use"
	CapCatalogPublish Capability = "catalog:publish"
)

// ActorAccess is the resolved platform access for a signed-in actor.
type ActorAccess struct {
	ActorID      string       `json:"actorId"`
	SuperAdmin   bool         `json:"superAdmin"`
	Entitlements []string     `json:"entitlements"`
	Capabilities []Capability `json:"capabilities"`
	capability   map[Capability]bool
}

// Has reports whether the actor holds the capability (super admin always true).
func (a *ActorAccess) Has(capability Capability) bool {
	if a == nil {
		return false
	}
	if a.SuperAdmin {
		return true
	}
	return a.capability[capability]
}

// HasAny is true when the actor holds at least one of the capabilities.
func (a *ActorAccess) HasAny(capabilities ...Capability) bool {
	for _, capability := range capabilities {
		if a.Has(capability) {
			return true
		}
	}
	return false
}

// Require returns ErrForbidden when the actor lacks the capability.
func (a *ActorAccess) Require(capability Capability) error {
	if a.Has(capability) {
		return nil
	}
	return fmt.Errorf("%w: missing capability %s", ErrForbidden, capability)
}

// RequireAny returns ErrForbidden when the actor holds none of the capabilities.
func (a *ActorAccess) RequireAny(capabilities ...Capability) error {
	if a.HasAny(capabilities...) {
		return nil
	}
	parts := make([]string, len(capabilities))
	for i, capability := range capabilities {
		parts[i] = string(capability)
	}
	return fmt.Errorf("%w: missing one of %s", ErrForbidden, strings.Join(parts, ", "))
}

// entitlementCapabilities maps Anthos platform entitlement names to capabilities.
var entitlementCapabilities = map[string][]Capability{
	"IAA-identities-read":        {CapIdentityRead},
	"IAA-identities-admin":       {CapIdentityRead, CapIdentityCreate, CapIdentityUpdate, CapIdentityRetire},
	"IAA-jobcodes-read":          {CapJobCodeRead},
	"IAA-jobcodes-admin":         {CapJobCodeRead, CapJobCodeCreate, CapJobCodeUpdate, CapJobCodeRetire},
	"IAA-birthrights-read":       {CapBirthrightRead},
	"IAA-birthrights-admin":      {CapBirthrightRead, CapBirthrightCreate, CapBirthrightUpdate, CapBirthrightRetire},
	"IAA-roles-read":             {CapRoleRead},
	"IAA-roles-admin":            {CapRoleRead, CapRoleCreate, CapRoleUpdate, CapRoleRetire},
	"IAA-entitlements-read":      {CapEntitlementRead},
	"IAA-entitlements-admin":     {CapEntitlementRead, CapEntitlementCreate, CapEntitlementUpdate, CapEntitlementRetire},
	"IAA-groups-read":            {CapGroupRead},
	"IAA-groups-admin":           {CapGroupRead, CapGroupCreate, CapGroupUpdate, CapGroupRetire},
	"CAT-incidents-read":         {CapIncidentRead},
	"CAT-incidents-create":       {CapIncidentRead, CapIncidentCreate},
	"CAT-incidents-admin":        {CapIncidentRead, CapIncidentCreate, CapIncidentUpdate, CapIncidentRetire},
	"CAT-changes-read":           {CapChangeRead},
	"CAT-changes-admin":          {CapChangeRead, CapChangeCreate, CapChangeUpdate, CapChangeRetire},
	"CAT-events-read":            {CapEventRead},
	"CAT-events-admin":           {CapEventRead, CapEventCreate, CapEventUpdate, CapEventRetire},
	"CMDB-relationships-read":    {CapRelationshipRead},
	"CMDB-relationships-admin":   {CapRelationshipRead, CapRelationshipCreate, CapRelationshipUpdate, CapRelationshipRetire},
	"CMDB-configitems-read":      {CapConfigItemRead},
	"CMDB-configitems-admin":     {CapConfigItemRead, CapConfigItemCreate, CapConfigItemUpdate, CapConfigItemRetire},
	"CMDB-map-read":              {CapMapRead},
	"LOC-locationmaps-read":      {CapLocationMapRead},
	"LOC-locationmaps-admin":     {CapLocationMapRead, CapLocationMapCreate, CapLocationMapUpdate, CapLocationMapRetire},
	"CREQ-vendorrequestform-use": {CapVendorRequestUse},
	"CREQ-accessrequestform-use": {CapAccessRequestUse},
	"CREQ-workflowcreator-use":   {CapWorkflowCreatorUse},
	"CREQ-workflowtasks-read":    {CapWorkflowTasksRead},
	"CREQ-workflowruns-read":     {CapWorkflowRunsRead},
	"CREQ-catalog-use":           {CapCatalogUse},
	"CREQ-catalog-publish":       {CapCatalogUse, CapCatalogPublish},
}

// AccessFromEntitlements builds ActorAccess from held entitlement names.
func AccessFromEntitlements(actorID string, entitlementNames []string) *ActorAccess {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" || actorID == PlatformSuperAdminID {
		return SuperAdminAccess()
	}
	seen := map[Capability]bool{}
	names := uniqueSorted(entitlementNames)
	for _, name := range names {
		for _, capability := range entitlementCapabilities[name] {
			seen[capability] = true
		}
	}
	capabilities := make([]Capability, 0, len(seen))
	for capability := range seen {
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i] < capabilities[j] })
	return &ActorAccess{
		ActorID:      actorID,
		SuperAdmin:   false,
		Entitlements: names,
		Capabilities: capabilities,
		capability:   seen,
	}
}

// SuperAdminAccess returns full platform access for the development super admin.
func SuperAdminAccess() *ActorAccess {
	return &ActorAccess{
		ActorID:      PlatformSuperAdminID,
		SuperAdmin:   true,
		Entitlements: []string{},
		Capabilities: []Capability{},
		capability:   map[Capability]bool{},
	}
}

// NodeReadCapability is the read capability for a list/get of that node kind.
func NodeReadCapability(kind NodeKind) (Capability, bool) {
	switch kind {
	case Identity:
		return CapIdentityRead, true
	case JobCode:
		return CapJobCodeRead, true
	case Birthright:
		return CapBirthrightRead, true
	case Role:
		return CapRoleRead, true
	case Entitlement:
		return CapEntitlementRead, true
	case Group:
		return CapGroupRead, true
	case Incident:
		return CapIncidentRead, true
	case Change:
		return CapChangeRead, true
	case Event:
		return CapEventRead, true
	case CI:
		return CapConfigItemRead, true
	case Request:
		return CapVendorRequestUse, true
	case WorkflowRun:
		return CapWorkflowRunsRead, true
	case Workflow, WorkflowStep, Task:
		return CapWorkflowCreatorUse, true
	case CatalogItem:
		return CapCatalogUse, true
	default:
		return "", false
	}
}

// NodeWriteCapability is the capability for create/update/retire of a node kind.
// action is "create", "update", or "retire".
func NodeWriteCapability(kind NodeKind, action string) (Capability, bool) {
	switch kind {
	case Identity:
		switch action {
		case "create":
			return CapIdentityCreate, true
		case "update":
			return CapIdentityUpdate, true
		case "retire":
			return CapIdentityRetire, true
		}
	case JobCode:
		switch action {
		case "create":
			return CapJobCodeCreate, true
		case "update":
			return CapJobCodeUpdate, true
		case "retire":
			return CapJobCodeRetire, true
		}
	case Birthright:
		switch action {
		case "create":
			return CapBirthrightCreate, true
		case "update":
			return CapBirthrightUpdate, true
		case "retire":
			return CapBirthrightRetire, true
		}
	case Role:
		switch action {
		case "create":
			return CapRoleCreate, true
		case "update":
			return CapRoleUpdate, true
		case "retire":
			return CapRoleRetire, true
		}
	case Entitlement:
		switch action {
		case "create":
			return CapEntitlementCreate, true
		case "update":
			return CapEntitlementUpdate, true
		case "retire":
			return CapEntitlementRetire, true
		}
	case Group:
		switch action {
		case "create":
			return CapGroupCreate, true
		case "update":
			return CapGroupUpdate, true
		case "retire":
			return CapGroupRetire, true
		}
	case Incident:
		switch action {
		case "create":
			return CapIncidentCreate, true
		case "update":
			return CapIncidentUpdate, true
		case "retire":
			return CapIncidentRetire, true
		}
	case Change:
		switch action {
		case "create":
			return CapChangeCreate, true
		case "update":
			return CapChangeUpdate, true
		case "retire":
			return CapChangeRetire, true
		}
	case Event:
		switch action {
		case "create":
			return CapEventCreate, true
		case "update":
			return CapEventUpdate, true
		case "retire":
			return CapEventRetire, true
		}
	case CI:
		switch action {
		case "create":
			return CapConfigItemCreate, true
		case "update":
			return CapConfigItemUpdate, true
		case "retire":
			return CapConfigItemRetire, true
		}
	case Request:
		return CapVendorRequestUse, true
	case Workflow, WorkflowStep:
		return CapWorkflowCreatorUse, true
	case WorkflowRun:
		return CapWorkflowRunsRead, false // runs are not created through node write
	case Task:
		return CapWorkflowTasksRead, false
	}
	return "", false
}

// RelationshipMutateCapabilities are capabilities that may create/update/retire
// a relationship of this kind. Holding any one is enough. Read of relationships
// is broader (see RelationshipReadAllowed).
func RelationshipMutateCapabilities(kind RelationshipKind) []Capability {
	switch kind {
	case HasJobCode, WorkLocation:
		return []Capability{CapIdentityUpdate}
	case Member:
		return []Capability{CapGroupUpdate}
	case QualifiesFor:
		return []Capability{CapJobCodeUpdate}
	case Grants:
		return []Capability{CapBirthrightUpdate}
	case Includes:
		return []Capability{CapRoleUpdate}
	case HasRole:
		return []Capability{CapRoleUpdate, CapConfigItemUpdate}
	case EntitledBy:
		return []Capability{CapEntitlementUpdate, CapConfigItemUpdate}
	case Permissions:
		return []Capability{CapIdentityUpdate, CapRoleUpdate, CapEntitlementUpdate}
	case Affects, AssignedTo:
		return []Capability{CapIncidentUpdate, CapIncidentCreate}
	case Changes:
		return []Capability{CapChangeUpdate}
	case ObservedOn:
		return []Capability{CapEventUpdate}
	case DependsOn, Hosts, Uses, Governs, Provides:
		return []Capability{CapRelationshipCreate, CapRelationshipUpdate, CapRelationshipRetire}
	case Accountable, Responsible, Consulted, Informed:
		return []Capability{
			CapIdentityUpdate, CapJobCodeUpdate, CapBirthrightUpdate, CapRoleUpdate, CapEntitlementUpdate,
			CapConfigItemUpdate, CapLocationMapUpdate,
		}
	case DraftedRequest, FormSubmitted:
		return []Capability{CapVendorRequestUse, CapAccessRequestUse}
	case RequestedFor, RequestsAccess:
		return []Capability{CapAccessRequestUse}
	case HasStep, NextStep, StepAssignedTo:
		return []Capability{CapWorkflowCreatorUse}
	case RunFor, InstanceOf, TaskFor, TaskStep, TaskItem, TaskAssignedTo, ActedBy, FulfilledBy:
		return []Capability{CapWorkflowCreatorUse, CapWorkflowTasksRead, CapAccessRequestUse, CapVendorRequestUse}
	case ForCatalogItem, CatalogOwnedBy, VisibleTo, ApprovedThrough, References:
		return []Capability{CapCatalogPublish}
	default:
		return []Capability{CapRelationshipCreate}
	}
}

// NodeKindResource is the UI resource key for a node kind (for clients).
func CapabilityNames() []string {
	names := make([]string, 0, len(entitlementCapabilities))
	for name := range entitlementCapabilities {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
