package cmdb

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalid = errors.New("invalid input")

type NodeKind string

const (
	Identity    NodeKind = "identity"
	JobCode     NodeKind = "job-code"
	Birthright  NodeKind = "birthright"
	Role        NodeKind = "role"
	Entitlement NodeKind = "entitlement"
	Group       NodeKind = "group"
	CI          NodeKind = "ci"
	Incident    NodeKind = "incident"
	Change      NodeKind = "change"
	Event       NodeKind = "event"
	Request     NodeKind = "request" // catalog request that captures a CI before it exists
	// Workflow definitions move a submitted catalog request from group to group.
	// A workflow is an ordered set of steps; a run is one execution of it for a
	// request, and a task is one step of a run waiting on its assignees.
	Workflow     NodeKind = "workflow"
	WorkflowStep NodeKind = "workflow-step"
	WorkflowRun  NodeKind = "workflow-run"
	Task         NodeKind = "task"
)

// RequestType names the catalog form a request was raised from. Each type
// mirrors the properties of the CI it will eventually become.
type RequestType string

const (
	VendorRequest RequestType = "vendor"
	// An access request asks for roles or entitlements an identity does not
	// already hold (exception access). It is raised complete, never drafted,
	// and runs the fixed access workflow: one approval task per requested item
	// to that item's Accountable, then a fulfilment task per approved item.
	AccessRequest RequestType = "access"
)

// RequestState is the lifecycle of a catalog request: drafts can be saved
// incomplete; submission requires what the requester must supply. When an
// enabled workflow exists for the form, submission starts a run and the request
// is in review until the last step completes and the CI is created (fulfilled).
// A reviewer can return the request to the requester, which puts it back in
// draft with their comment attached.
type RequestState string

const (
	RequestDraft     RequestState = "draft"
	RequestSubmitted RequestState = "submitted"
	RequestInReview  RequestState = "in-review"
	RequestFulfilled RequestState = "fulfilled"
	RequestDenied    RequestState = "denied" // every item of an access request was denied; the request retires
)

// StepType is what a workflow step asks of its assignees: a review completes
// once the group has looked the form over (and filled in any fields exposed to
// it); an approval needs a yes from the assignees under its approval rule.
type StepType string

const (
	ReviewStep   StepType = "review"
	ApprovalStep StepType = "approval"
)

// ApprovalRule decides when an approval step assigned to several people is
// satisfied: the first approval, or one from everyone eligible.
type ApprovalRule string

const (
	AnyApprover  ApprovalRule = "any"
	AllApprovers ApprovalRule = "all"
)

// TaskStatus is the state of one step of a workflow run.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskCompleted TaskStatus = "completed"
	TaskApproved  TaskStatus = "approved"
	TaskRejected  TaskStatus = "rejected"
)

// TaskAction is what an assignee does to a pending task.
type TaskAction string

const (
	CompleteTask TaskAction = "complete" // review steps
	ApproveTask  TaskAction = "approve"  // approval steps
	RejectTask   TaskAction = "reject"   // any step: return the request to its requester
)

// RunStatus is the state of a workflow run.
type RunStatus string

const (
	RunActive    RunStatus = "active"
	RunCompleted RunStatus = "completed"
	RunReturned  RunStatus = "returned"
)

type CIType string

const (
	Server        CIType = "server"
	Printer       CIType = "printer"
	DataConnector CIType = "data-connector"
	Application   CIType = "application"
	ServiceCI     CIType = "service"
	Process       CIType = "process"
	Function      CIType = "function"
	Location      CIType = "location"
	Contract      CIType = "contract"
	Vendor        CIType = "vendor"
)

// LegacyCIType maps a retired ciType value to the value that replaces it so
// existing records can be rewritten on startup.
type LegacyCIType struct {
	OldType string
	NewType CIType
}

func LegacyCITypes() []LegacyCIType {
	return []LegacyCIType{
		{OldType: "business-process", NewType: Process},
	}
}

// ContractType classifies contract CIs.
type ContractType string

const (
	BasicContract ContractType = "basic-contract"
	MSA           ContractType = "msa"
	NDA           ContractType = "nda"
)

// HostingModel records whether an application CI is hosted internally or externally.
type HostingModel string

const (
	HostedInternal HostingModel = "internal"
	HostedExternal HostingModel = "external"
)

// Criticality ranks how important a vendor CI is to the business.
type Criticality string

const (
	CriticalVendor        Criticality = "critical"
	ImportantVendor       Criticality = "important"
	BusinessSupportVendor Criticality = "business-support"
)

// CICategory classifies service, process, and function CIs.
type CICategory string

const (
	BusinessCategory   CICategory = "business"
	TechnologyCategory CICategory = "technology"
	SecurityCategory   CICategory = "security"
)

type nodeSpec struct {
	label string
	key   string
}

var nodeSpecs = map[NodeKind]nodeSpec{
	Identity:    {label: "Identity", key: "id"},
	JobCode:     {label: "JobCode", key: "code"},
	Birthright:  {label: "Birthright", key: "id"},
	Role:        {label: "Role", key: "id"},
	Entitlement: {label: "Entitlement", key: "id"},
	Group:       {label: "Group", key: "id"},
	CI:          {label: "CI", key: "id"},
	Incident:    {label: "Incident", key: "id"},
	Change:      {label: "Change", key: "id"},
	Event:       {label: "Event", key: "id"},
	Request:     {label: "Request", key: "id"},
	Workflow:     {label: "Workflow", key: "id"},
	WorkflowStep: {label: "WorkflowStep", key: "id"},
	WorkflowRun:  {label: "WorkflowRun", key: "id"},
	Task:         {label: "Task", key: "id"},
}

type RelationshipKind string

const (
	HasJobCode   RelationshipKind = "has-job-code"
	WorkLocation RelationshipKind = "work-location"
	QualifiesFor RelationshipKind = "qualifies-for"
	Grants       RelationshipKind = "grants"
	Includes     RelationshipKind = "includes"
	Affects      RelationshipKind = "affects"
	Changes      RelationshipKind = "changes"
	AssignedTo   RelationshipKind = "assigned-to" // incident to the identity or group working it
	ObservedOn   RelationshipKind = "observed-on"
	DependsOn    RelationshipKind = "depends-on"
	Hosts        RelationshipKind = "hosts"
	Uses         RelationshipKind = "uses"
	Governs      RelationshipKind = "governs"
	Provides     RelationshipKind = "provides"
	Member       RelationshipKind = "member"
	// Catalog requests are tied to their requester: drafted-request while the
	// form is a draft, replaced by form-submitted when it is submitted.
	DraftedRequest RelationshipKind = "drafted-request"
	FormSubmitted  RelationshipKind = "form-submitted"
	// Workflow definitions: a workflow has ordered steps, each step is assigned
	// to the identities or groups who act on it, and next-step chains them.
	HasStep        RelationshipKind = "has-step"
	NextStep       RelationshipKind = "next-step"
	StepAssignedTo RelationshipKind = "step-assigned-to"
	// Workflow execution: a run is an instance of a workflow for one request;
	// each task belongs to a run, mirrors a step, is assigned to the step's
	// assignees, and records who acted on it. A fulfilled request points at
	// the CI created from it.
	RunFor         RelationshipKind = "run-for"
	InstanceOf     RelationshipKind = "instance-of"
	TaskFor        RelationshipKind = "task-for"
	TaskStep       RelationshipKind = "task-step"
	TaskAssignedTo RelationshipKind = "task-assigned-to"
	ActedBy        RelationshipKind = "acted-by"
	FulfilledBy    RelationshipKind = "fulfilled-by"
	// Access: an application has roles and is entitled by entitlements (a role
	// or entitlement is for one application); a role or entitlement
	// permissions an identity directly (exception access granted outside a
	// birthright). An access request names the identity it is for and the
	// items it asks for; each task of its run decides or provisions one item.
	HasRole    RelationshipKind = "has-role"
	EntitledBy RelationshipKind = "entitled-by"
	Permissions    RelationshipKind = "permissions"
	RequestedFor   RelationshipKind = "requested-for"
	RequestsAccess RelationshipKind = "requests-access"
	TaskItem       RelationshipKind = "task-item"
	// RACI ownership of a CI, job code, birthright, role, or entitlement.
	// Accountable points at one identity; the other three can point at
	// identities or groups.
	Accountable RelationshipKind = "accountable"
	Responsible RelationshipKind = "responsible"
	Consulted   RelationshipKind = "consulted"
	Informed    RelationshipKind = "informed"
)

// Every relationship is stored once, in its forward direction. The inverse
// label is how the same edge reads from the "to" side (e.g. grants / granted-by).
type relationshipSpec struct {
	typeName    string
	inverse     string
	from        NodeKind
	fromKinds   []NodeKind // when set, the from node may be any of these kinds
	to          NodeKind   // primary to kind
	toKinds     []NodeKind // when set, the to node may be any of these kinds
	fromCITypes []CIType   // when set, the from node must be a CI of one of these types
	toCITypes   []CIType   // when set, the to node must be a CI of one of these types
	ciTypeRules []CITypeRule // when set, the (from, to) CI types must satisfy one of these pairings instead
}

// CITypeRule is one allowed pairing of CI types at the two ends of a
// relationship. An empty side means any CI type is accepted at that end.
type CITypeRule struct {
	From []CIType
	To   []CIType
}

type RelationshipDefinition struct {
	TypeName    string
	Inverse     string
	From        NodeKind
	FromKinds   []NodeKind // every kind the from node may have; always contains From
	To          NodeKind
	ToKinds     []NodeKind // every kind the to node may have; always contains To
	FromCITypes []CIType   // union of every rule's from types (for labels); empty = any
	ToCITypes   []CIType   // union of every rule's to types (for labels); empty = any
	CITypeRules []CITypeRule // the pairings that are actually enforced; empty = unrestricted
}

var relationshipSpecs = map[RelationshipKind]relationshipSpec{
	HasJobCode:   {typeName: "HAS_JOB_CODE", inverse: "job-code-for", from: Identity, to: JobCode},
	WorkLocation: {typeName: "WORK_LOCATION", inverse: "work-location-for", from: Identity, to: CI, toCITypes: []CIType{Location}},
	QualifiesFor: {typeName: "QUALIFIES_FOR", inverse: "qualified-by", from: JobCode, to: Birthright},
	Grants:       {typeName: "GRANTS", inverse: "granted-by", from: Birthright, to: Role, toKinds: []NodeKind{Role, Entitlement}},
	Includes:     {typeName: "INCLUDES", inverse: "included-by", from: Role, to: Entitlement},
	Affects:      {typeName: "AFFECTS", inverse: "affected-by", from: Incident, to: CI},
	Changes:      {typeName: "CHANGES", inverse: "changed-by", from: Change, to: CI},
	AssignedTo:   {typeName: "ASSIGNED_TO", inverse: "assignee-of", from: Incident, to: Identity, toKinds: []NodeKind{Identity, Group}},
	ObservedOn:   {typeName: "OBSERVED_ON", inverse: "observed", from: Event, to: CI},
	DependsOn:    {typeName: "DEPENDS_ON", inverse: "depended-on-by", from: CI, to: CI},
	Hosts:        {typeName: "HOSTS", inverse: "hosted-by", from: CI, to: CI},
	Uses:         {typeName: "USES", inverse: "used-by", from: CI, to: CI},
	Governs:      {typeName: "GOVERNS", inverse: "governed-by", from: CI, to: CI, fromCITypes: []CIType{Contract}},
	// Services and functions provide one another; vendors provide the
	// applications, servers, and printers they supply.
	Provides: {typeName: "PROVIDES", inverse: "provided-by", from: CI, to: CI, ciTypeRules: []CITypeRule{
		{From: []CIType{ServiceCI, Function}, To: []CIType{ServiceCI, Function}},
		{From: []CIType{Vendor}, To: []CIType{Application, Server, Printer}},
	}},
	Member:       {typeName: "MEMBER", inverse: "member-of", from: Group, to: Identity},
	DraftedRequest: {typeName: "DRAFTED_REQUEST", inverse: "drafted-request-for", from: Identity, to: Request},
	FormSubmitted:  {typeName: "FORM_SUBMITTED", inverse: "submitted-by", from: Identity, to: Request},
	HasStep:        {typeName: "HAS_STEP", inverse: "step-of", from: Workflow, to: WorkflowStep},
	NextStep:       {typeName: "NEXT_STEP", inverse: "previous-step", from: WorkflowStep, to: WorkflowStep},
	StepAssignedTo: {typeName: "STEP_ASSIGNED_TO", inverse: "assigned-step", from: WorkflowStep, to: Identity, toKinds: []NodeKind{Identity, Group}},
	RunFor:         {typeName: "RUN_FOR", inverse: "has-run", from: WorkflowRun, to: Request},
	InstanceOf:     {typeName: "INSTANCE_OF", inverse: "has-instance", from: WorkflowRun, to: Workflow},
	TaskFor:        {typeName: "TASK_FOR", inverse: "has-task", from: Task, to: WorkflowRun},
	TaskStep:       {typeName: "TASK_STEP", inverse: "step-task", from: Task, to: WorkflowStep},
	TaskAssignedTo: {typeName: "TASK_ASSIGNED_TO", inverse: "assigned-task", from: Task, to: Identity, toKinds: []NodeKind{Identity, Group}},
	ActedBy:        {typeName: "ACTED_BY", inverse: "acted-on", from: Task, to: Identity},
	FulfilledBy:    {typeName: "FULFILLED_BY", inverse: "fulfills", from: Request, to: CI},
	HasRole:    {typeName: "HAS_ROLE", inverse: "role-for", from: CI, fromCITypes: []CIType{Application}, to: Role},
	EntitledBy: {typeName: "ENTITLED_BY", inverse: "entitlement-for", from: CI, fromCITypes: []CIType{Application}, to: Entitlement},
	Permissions:    {typeName: "PERMISSIONS", inverse: "permissioned-by", from: Role, fromKinds: []NodeKind{Role, Entitlement}, to: Identity},
	RequestedFor:   {typeName: "REQUESTED_FOR", inverse: "subject-of", from: Request, to: Identity},
	RequestsAccess: {typeName: "REQUESTS_ACCESS", inverse: "requested-on", from: Request, to: Role, toKinds: []NodeKind{Role, Entitlement}},
	TaskItem:       {typeName: "TASK_ITEM", inverse: "item-task", from: Task, to: Role, toKinds: []NodeKind{Role, Entitlement}},
	Accountable:  {typeName: "ACCOUNTABLE", inverse: "accountable-for", from: CI, fromKinds: raciOwnedKinds, to: Identity},
	Responsible:  {typeName: "RESPONSIBLE", inverse: "responsible-for", from: CI, fromKinds: raciOwnedKinds, to: Identity, toKinds: []NodeKind{Identity, Group}},
	Consulted:    {typeName: "CONSULTED", inverse: "consulted-on", from: CI, fromKinds: raciOwnedKinds, to: Identity, toKinds: []NodeKind{Identity, Group}},
	Informed:     {typeName: "INFORMED", inverse: "informed-of", from: CI, fromKinds: raciOwnedKinds, to: Identity, toKinds: []NodeKind{Identity, Group}},
}

// raciOwnedKinds are the record kinds that carry RACI ownership.
var raciOwnedKinds = []NodeKind{CI, JobCode, Birthright, Role, Entitlement}

// RACIRelationshipKinds lists the ownership relationships every owned record carries, in RACI order.
func RACIRelationshipKinds() []RelationshipKind {
	return []RelationshipKind{Accountable, Responsible, Consulted, Informed}
}

// RACIOwnedKinds lists the node kinds RACI ownership applies to.
func RACIOwnedKinds() []NodeKind {
	return append([]NodeKind(nil), raciOwnedKinds...)
}

// AccessItemKinds are the kinds an identity can be permissioned to directly
// and an access request can ask for.
func AccessItemKinds() []NodeKind {
	return []NodeKind{Role, Entitlement}
}

// LegacyRelationshipTypes maps retired Neo4j relationship types to the current
// type that replaces them and whether the stored direction must be flipped.
type LegacyRelationshipType struct {
	OldType string
	NewType string
	Flip    bool
}

func LegacyRelationshipTypes() []LegacyRelationshipType {
	return []LegacyRelationshipType{
		{OldType: "HOSTED_ON", NewType: "HOSTS", Flip: true},
		{OldType: "USED_BY", NewType: "USES", Flip: true},
	}
}

type Node struct {
	Kind          NodeKind       `json:"kind"`
	ID            string         `json:"id"`
	Properties    map[string]any `json:"properties,omitempty"`
	Status        string         `json:"status"`
	RetiredAt     string         `json:"retiredAt,omitempty"`
	Relationships []Relationship `json:"relationships,omitempty"`
}

type Relationship struct {
	Kind       RelationshipKind `json:"kind"`
	FromID     string           `json:"fromId"`
	ToID       string           `json:"toId"`
	// FromName and ToName are the endpoints' display names, filled in when a
	// relationship is listed alongside a node so people are shown names, not IDs.
	FromName   string         `json:"fromName,omitempty"`
	ToName     string         `json:"toName,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	Status     string         `json:"status"`
	RetiredAt  string         `json:"retiredAt,omitempty"`
}

func ParseNodeKind(value string) (NodeKind, error) {
	kind := NodeKind(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := nodeSpecs[kind]; !ok {
		return "", fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, value)
	}
	return kind, nil
}

func ParseRelationshipKind(value string) (RelationshipKind, error) {
	kind := RelationshipKind(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := relationshipSpecs[kind]; !ok {
		kind = RelationshipKind(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-")))
	}
	if _, ok := relationshipSpecs[kind]; !ok {
		return "", fmt.Errorf("%w: unsupported relationship kind %q", ErrInvalid, value)
	}
	return kind, nil
}

// RetiredTypeSuffix marks the Neo4j relationship type that holds a kind's
// retired edges. Retiring an edge moves it from HAS_JOB_CODE to
// HAS_JOB_CODE_RETIRED (same direction and properties) so live traversals
// never visit retired history: Neo4j skips edges by type, not by property, and
// a busy node would otherwise drag every retired edge through each read.
const RetiredTypeSuffix = "_RETIRED"

// RetiredTypeName returns the relationship type that stores retired edges of a live type.
func RetiredTypeName(typeName string) string {
	return typeName + RetiredTypeSuffix
}

// ParseStoredRelationshipType maps a Neo4j relationship type back to its kind,
// reporting whether the type is the kind's retired twin.
func ParseStoredRelationshipType(typeName string) (RelationshipKind, bool, error) {
	retired := strings.HasSuffix(typeName, RetiredTypeSuffix)
	kind, err := ParseRelationshipKind(strings.TrimSuffix(typeName, RetiredTypeSuffix))
	if err != nil {
		return "", false, err
	}
	return kind, retired, nil
}

// RelationshipTypeNamesFor returns the live relationship types that can touch
// a node kind at either end, so retiring one of its records only has to look
// at those types rather than every type in the model.
func RelationshipTypeNamesFor(kind NodeKind) []string {
	names := make([]string, 0)
	for _, relationshipKind := range RelationshipKinds() {
		definition, err := RelationshipDefinitionFor(relationshipKind)
		if err != nil {
			continue
		}
		touches := false
		for _, candidate := range append(append([]NodeKind(nil), definition.FromKinds...), definition.ToKinds...) {
			if candidate == kind {
				touches = true
				break
			}
		}
		if touches {
			names = append(names, definition.TypeName)
		}
	}
	return names
}

func ParseCIType(value string) (CIType, error) {
	kind := CIType(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", "-")))
	switch kind {
	case Server, Printer, DataConnector, Application, ServiceCI, Process, Function, Location, Contract, Vendor:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: unsupported CI type %q; allowed types are %s", ErrInvalid, value, strings.Join(CITypeNames(), ", "))
	}
}

// RequiresCategory reports whether a CI type carries the business/technology/security category.
func RequiresCategory(ciType CIType) bool {
	switch ciType {
	case ServiceCI, Process, Function:
		return true
	default:
		return false
	}
}

func ParseCICategory(value string) (CICategory, error) {
	category := CICategory(strings.ToLower(strings.TrimSpace(value)))
	switch category {
	case BusinessCategory, TechnologyCategory, SecurityCategory:
		return category, nil
	default:
		return "", fmt.Errorf("%w: unsupported CI category %q; allowed categories are %s", ErrInvalid, value, strings.Join(CICategoryNames(), ", "))
	}
}

// RequiresContractType reports whether a CI type carries the contractType classification.
func RequiresContractType(ciType CIType) bool {
	return ciType == Contract
}

func ParseContractType(value string) (ContractType, error) {
	contractType := ContractType(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", "-")))
	switch contractType {
	case BasicContract, MSA, NDA:
		return contractType, nil
	default:
		return "", fmt.Errorf("%w: unsupported contract type %q; allowed types are %s", ErrInvalid, value, strings.Join(ContractTypeNames(), ", "))
	}
}

func ContractTypeNames() []string {
	return []string{string(BasicContract), string(MSA), string(NDA)}
}

// RequiresHosting reports whether a CI type carries the hosted (internal/external) property.
func RequiresHosting(ciType CIType) bool {
	return ciType == Application
}

func ParseHostingModel(value string) (HostingModel, error) {
	hosting := HostingModel(strings.ToLower(strings.TrimSpace(value)))
	switch hosting {
	case HostedInternal, HostedExternal:
		return hosting, nil
	default:
		return "", fmt.Errorf("%w: unsupported hosted value %q; allowed values are %s", ErrInvalid, value, strings.Join(HostingModelNames(), ", "))
	}
}

func HostingModelNames() []string {
	return []string{string(HostedInternal), string(HostedExternal)}
}

// RequiresCriticality reports whether a CI type carries the criticality ranking.
func RequiresCriticality(ciType CIType) bool {
	return ciType == Vendor
}

func ParseCriticality(value string) (Criticality, error) {
	criticality := Criticality(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", "-")))
	switch criticality {
	case CriticalVendor, ImportantVendor, BusinessSupportVendor:
		return criticality, nil
	default:
		return "", fmt.Errorf("%w: unsupported criticality %q; allowed values are %s", ErrInvalid, value, strings.Join(CriticalityNames(), ", "))
	}
}

func CriticalityNames() []string {
	return []string{string(CriticalVendor), string(ImportantVendor), string(BusinessSupportVendor)}
}

func ParseRequestType(value string) (RequestType, error) {
	requestType := RequestType(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", "-")))
	switch requestType {
	case VendorRequest, AccessRequest:
		return requestType, nil
	default:
		return "", fmt.Errorf("%w: unsupported requestType %q; allowed types are %s", ErrInvalid, value, strings.Join(RequestTypeNames(), ", "))
	}
}

func RequestTypeNames() []string {
	return []string{string(VendorRequest), string(AccessRequest)}
}

func ParseRequestState(value string) (RequestState, error) {
	state := RequestState(strings.ToLower(strings.TrimSpace(value)))
	switch state {
	case RequestDraft, RequestSubmitted, RequestInReview, RequestFulfilled, RequestDenied:
		return state, nil
	default:
		return "", fmt.Errorf("%w: unsupported request state %q; allowed states are %s", ErrInvalid, value, strings.Join(RequestStateNames(), ", "))
	}
}

func RequestStateNames() []string {
	return []string{string(RequestDraft), string(RequestSubmitted), string(RequestInReview), string(RequestFulfilled), string(RequestDenied)}
}

// RequestClosedStates are the states the workflow leaves a request in when it
// is done with it; the request is retired at the same time.
func RequestClosedStates() []RequestState {
	return []RequestState{RequestFulfilled, RequestDenied}
}

// RequestFieldKeys lists every form field a request of this type can carry:
// the properties of the CI it becomes. Workflow steps may expose any of them
// to the group handling the step.
func RequestFieldKeys(requestType RequestType) []string {
	switch requestType {
	case VendorRequest:
		return []string{"name", "description", "criticality", ContactNameProperty, ContactPhoneProperty, ContactEmailProperty}
	default:
		return nil
	}
}

// RequestFulfilmentProperties lists what the target CI requires beyond what the
// requester supplies. An enabled workflow must require each of them at some
// step, otherwise its final step could never create the CI.
func RequestFulfilmentProperties(requestType RequestType) []string {
	switch requestType {
	case VendorRequest:
		return []string{"criticality"}
	default:
		return nil
	}
}

// RequestCIType is the CI type a request of this type is fulfilled as.
func RequestCIType(requestType RequestType) (CIType, bool) {
	switch requestType {
	case VendorRequest:
		return Vendor, true
	default:
		return "", false
	}
}

func ParseStepType(value string) (StepType, error) {
	stepType := StepType(strings.ToLower(strings.TrimSpace(value)))
	switch stepType {
	case ReviewStep, ApprovalStep:
		return stepType, nil
	default:
		return "", fmt.Errorf("%w: unsupported step type %q; allowed types are %s", ErrInvalid, value, strings.Join(StepTypeNames(), ", "))
	}
}

func StepTypeNames() []string {
	return []string{string(ReviewStep), string(ApprovalStep)}
}

func ParseApprovalRule(value string) (ApprovalRule, error) {
	rule := ApprovalRule(strings.ToLower(strings.TrimSpace(value)))
	switch rule {
	case AnyApprover, AllApprovers:
		return rule, nil
	default:
		return "", fmt.Errorf("%w: unsupported approval rule %q; allowed rules are %s", ErrInvalid, value, strings.Join(ApprovalRuleNames(), ", "))
	}
}

func ApprovalRuleNames() []string {
	return []string{string(AnyApprover), string(AllApprovers)}
}

func ParseAssigneeRule(value string) (AssigneeRule, error) {
	rule := AssigneeRule(strings.ToLower(strings.TrimSpace(value)))
	switch rule {
	case "":
		return "", nil
	case ItemAccountable:
		return rule, nil
	default:
		return "", fmt.Errorf("%w: unsupported assignee rule %q; the only rule is %s", ErrInvalid, value, ItemAccountable)
	}
}

func ParseItemKind(value string) (NodeKind, error) {
	kind, err := ParseNodeKind(value)
	if err != nil {
		return "", err
	}
	for _, allowed := range AccessItemKinds() {
		if kind == allowed {
			return kind, nil
		}
	}
	return "", fmt.Errorf("%w: access can only be requested for roles and entitlements, not %s", ErrInvalid, kind)
}

func ParseTaskAction(value string) (TaskAction, error) {
	action := TaskAction(strings.ToLower(strings.TrimSpace(value)))
	switch action {
	case CompleteTask, ApproveTask, RejectTask:
		return action, nil
	default:
		return "", fmt.Errorf("%w: unsupported task action %q; allowed actions are %s", ErrInvalid, value, strings.Join(TaskActionNames(), ", "))
	}
}

func TaskActionNames() []string {
	return []string{string(CompleteTask), string(ApproveTask), string(RejectTask)}
}

// Assignee is an identity or group named on a workflow step or task.
type Assignee struct {
	ID   string   `json:"id"`
	Kind NodeKind `json:"kind"`
	Name string   `json:"name,omitempty"`
}

// WorkflowStepDefinition is one step of a workflow as the admin configures it.
type WorkflowStepDefinition struct {
	ID             string       `json:"id,omitempty"` // assigned by the server; present when updating an existing step
	Order          int          `json:"order"`
	Name           string       `json:"name"`
	StepType       StepType     `json:"stepType"`
	Instructions   string       `json:"instructions,omitempty"`
	ApprovalRule   ApprovalRule `json:"approvalRule,omitempty"` // approval steps only
	EditableFields []string     `json:"editableFields"`         // request fields the assignees may fill in or change
	RequiredFields []string     `json:"requiredFields"`         // editable fields that must be present before the step can complete
	AssigneeIDs      []string     `json:"assigneeIds"`            // identities or groups (input)
	Assignees        []Assignee   `json:"assignees,omitempty"`    // resolved (output)
	AssigneeRule     AssigneeRule `json:"assigneeRule,omitempty"` // when set, assignees are worked out per task instead of named here
	OLAEnabled       bool         `json:"olaEnabled"`
	OLABusinessDays  int          `json:"olaBusinessDays,omitempty"`
}

// AssigneeRule picks a task's assignees at run time. The access workflow's
// approval step is assigned to the Accountable of each requested item.
type AssigneeRule string

const (
	ItemAccountable AssigneeRule = "item-accountable"
)

// WorkflowDefinition is a workflow and its ordered steps.
type WorkflowDefinition struct {
	ID              string                   `json:"id,omitempty"`
	Name            string                   `json:"name"`
	Description     string                   `json:"description,omitempty"`
	RequestType     RequestType              `json:"requestType"`
	Enabled         bool                     `json:"enabled"`
	Status          string                   `json:"status,omitempty"`
	SLAEnabled      bool                     `json:"slaEnabled"`
	SLABusinessDays int                      `json:"slaBusinessDays,omitempty"`
	Steps           []WorkflowStepDefinition `json:"steps"`
}

// TaskActionRecord is one thing an assignee did to a task.
type TaskActionRecord struct {
	ActorID   string     `json:"actorId"`
	ActorName string     `json:"actorName,omitempty"`
	Action    TaskStatus `json:"action"` // completed, approved, or rejected
	Comment   string     `json:"comment,omitempty"`
	ActedAt   string     `json:"actedAt"`
}

// TaskView is a task with everything its assignees need to act on it: the
// step it mirrors, the run and workflow it belongs to, the request under
// review, who may act, and what has been done so far.
type TaskView struct {
	ID            string                 `json:"id"`
	Status        TaskStatus             `json:"status"`
	CreatedAt     string                 `json:"createdAt,omitempty"`
	CompletedAt   string                 `json:"completedAt,omitempty"`
	RetiredAt     string                 `json:"retiredAt,omitempty"` // set once the task record is retired (its run completed, or it was retired by hand)
	Step          WorkflowStepDefinition `json:"step"`
	FinalStep     bool                   `json:"finalStep"` // completing this task fulfils the request
	RunID         string                 `json:"runId"`
	RunStatus     RunStatus              `json:"runStatus"`
	WorkflowID    string                 `json:"workflowId"`
	WorkflowName  string                 `json:"workflowName"`
	Request       Node                   `json:"request"`
	Requester     *Assignee              `json:"requester,omitempty"`
	Assignees     []Assignee             `json:"assignees"`
	EligibleActors []Assignee            `json:"eligibleActors"` // identities who may act: direct assignees plus group members
	Actions       []TaskActionRecord     `json:"actions"`
	// Access requests: who the access is for, the item this task decides or
	// provisions, and every item on the request with where it has got to.
	RequestedFor *Assignee        `json:"requestedFor,omitempty"`
	Item         *AccessItemView  `json:"item,omitempty"`
	Items        []AccessItemView `json:"items,omitempty"`
	SLA          *AgreementClock  `json:"sla,omitempty"`
	OLA          *AgreementClock  `json:"ola,omitempty"`
}

// ItemDecision is where one requested item of an access request has got to.
type ItemDecision string

const (
	ItemPending   ItemDecision = "pending"   // waiting on the Accountable
	ItemApproved  ItemDecision = "approved"  // approved; waiting on fulfilment
	ItemDenied    ItemDecision = "denied"    // the Accountable said no
	ItemFulfilled ItemDecision = "fulfilled" // provisioned: the permissions link exists
)

// AccessItemView is one role or entitlement on an access request.
type AccessItemView struct {
	ID              string       `json:"id"`
	Kind            NodeKind     `json:"kind"`
	Name            string       `json:"name,omitempty"`
	ApplicationID   string       `json:"applicationId,omitempty"`
	ApplicationName string       `json:"applicationName,omitempty"`
	Note            string       `json:"note,omitempty"`
	Decision        ItemDecision `json:"decision,omitempty"`
	DecidedAt       string       `json:"decidedAt,omitempty"`
	DecidedBy       string       `json:"decidedBy,omitempty"`
	FulfilledAt     string       `json:"fulfilledAt,omitempty"`
}

// AccessOption is a role or entitlement an identity may request: it is provided
// by an application, has an Accountable to approve it, and is not already held.
type AccessOption struct {
	ID          string   `json:"id"`
	Kind        NodeKind `json:"kind"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Owner       Assignee `json:"owner"`
}

// AccessApplication groups the requestable items of one application.
type AccessApplication struct {
	ID           string         `json:"id"`
	Name         string         `json:"name,omitempty"`
	Roles        []AccessOption `json:"roles"`
	Entitlements []AccessOption `json:"entitlements"`
}

// AccessHolding is access the identity already has (or has asked for), with how.
type AccessHolding struct {
	ID     string   `json:"id"`
	Kind   NodeKind `json:"kind"`
	Name   string   `json:"name,omitempty"`
	Via    string   `json:"via"`              // birthright, role, direct, or pending
	Source string   `json:"source,omitempty"` // the birthright, role, or request it comes through
}

// AccessOptions is what the Access Request Form shows for one identity.
type AccessOptions struct {
	Identity     Assignee            `json:"identity"`
	Applications []AccessApplication `json:"applications"`
	Held         []AccessHolding     `json:"held"`
}

// AccessAnomaly is an identity that holds the same role or entitlement through
// more than one path: a birthright (job code → qualifies-for → grants, and
// entitlements included by granted roles), a held role that includes the
// entitlement, and/or a direct permissions link. The Birthrights page lists
// these so duplicate paths can be cleaned up.
type AccessAnomaly struct {
	Identity    Assignee   `json:"identity"`
	Item        Assignee   `json:"item"` // kind is role or entitlement
	Birthrights []Assignee `json:"birthrights"`
	Roles       []Assignee `json:"roles"` // roles that include the item when the overlap is via role
	Paths       []string   `json:"paths"` // subset of birthright, role, direct
}

// AccessAnomalyReport is the stored result of the last anomaly check. The
// check walks every identity's access, so it runs on a schedule and reads
// serve this snapshot; ComputedAt is empty until the first run has finished.
type AccessAnomalyReport struct {
	ComputedAt string          `json:"computedAt,omitempty"`
	Anomalies  []AccessAnomaly `json:"anomalies"`
}

// AccessRequestItemInput is one item asked for on the Access Request Form.
type AccessRequestItemInput struct {
	ID   string   `json:"id"`
	Kind NodeKind `json:"kind"`
	Note string   `json:"note,omitempty"`
}

// AccessRequestInput is a submitted Access Request Form.
type AccessRequestInput struct {
	RequestedByID   string                   `json:"requestedById"`
	RequestForID    string                   `json:"requestForId"`
	Items           []AccessRequestItemInput `json:"items"`
	SLAEnabled      bool                     `json:"slaEnabled"`
	SLABusinessDays int                      `json:"slaBusinessDays,omitempty"`
	SLADueAt        string                   `json:"-"`
}

// TaskActionInput is what an assignee sends to act on a task.
type TaskActionInput struct {
	ActorID    string         `json:"actorId"`
	Action     TaskAction     `json:"action"`
	Comment    string         `json:"comment,omitempty"`
	Properties map[string]any `json:"properties,omitempty"` // request field updates, limited to the step's editable fields
}

// TaskFilter narrows a task listing.
type TaskFilter struct {
	ActorID     string // tasks the identity may act on (assigned directly or via a group)
	RequestID   string // tasks raised for a request
	IncludeDone bool   // include tasks that are no longer pending
	Limit       int    // page size; zero returns every matching task
	Offset      int
}

// TaskOutcome is what the store applies once the service has decided the
// effect of an action on a pending task.
type TaskOutcome struct {
	Action       TaskStatus     // what the actor did: completed, approved, or rejected
	Comment      string
	Properties   map[string]any // validated request updates to apply
	Advance      bool           // the task is done; move the run to the next step (or finish it)
	Return       bool           // send the request back to its requester as a draft
	CIProperties map[string]any // when advancing past the final step: create this CI and link it as fulfilled-by
	// Access requests work per item rather than per run: the item this task is
	// about, and whether the action denies it, approves it (raising its
	// fulfilment task), or provisions it (creating the permissions link).
	Item      string
	Deny      bool
	Approve   bool
	Provision bool
}

// Managed request properties set by the workflow when a request is returned.
const (
	ReturnCommentProperty = "returnComment"
	ReturnedAtProperty    = "returnedAt"
)

// RequestRequiredProperties lists the properties a request must carry before it
// can be submitted. The request only exposes what the requester can know: a
// vendor's criticality is decided later, when the CI is created, so it is not
// required here.
func RequestRequiredProperties(requestType RequestType) []string {
	switch requestType {
	case VendorRequest:
		return []string{"name"}
	default:
		return nil
	}
}

// Vendor contact details, carried by vendor CIs and vendor requests alike.
const (
	ContactNameProperty  = "contactName"
	ContactPhoneProperty = "contactPhone"
	ContactEmailProperty = "contactEmail"
)

func ContactProperties() []string {
	return []string{ContactNameProperty, ContactPhoneProperty, ContactEmailProperty}
}

func RequiresGeneratedID(kind NodeKind) bool {
	_, supported := nodeSpecs[kind]
	return supported
}

func GeneratedIDPrefix(kind NodeKind) (string, error) {
	switch kind {
	case Identity:
		return "IDN", nil
	case JobCode:
		return "JOB", nil
	case Birthright:
		return "BIR", nil
	case Role:
		return "ROLE", nil
	case Entitlement:
		return "ENT", nil
	case Group:
		return "GRP", nil
	case CI:
		return "CI", nil
	case Incident:
		return "INC", nil
	case Change:
		return "CHG", nil
	case Event:
		return "EVT", nil
	case Request:
		return "REQ", nil
	case Workflow:
		return "WFL", nil
	case WorkflowStep:
		return "STP", nil
	case WorkflowRun:
		return "RUN", nil
	case Task:
		return "TSK", nil
	default:
		return "", fmt.Errorf("%w: %s IDs are not generated by the application", ErrInvalid, kind)
	}
}

func CITypeNames() []string {
	return []string{string(Server), string(Printer), string(DataConnector), string(Application), string(ServiceCI), string(Process), string(Function), string(Location), string(Contract), string(Vendor)}
}

func CICategoryNames() []string {
	return []string{string(BusinessCategory), string(TechnologyCategory), string(SecurityCategory)}
}

func NodeKinds() []NodeKind {
	return []NodeKind{Identity, JobCode, Birthright, Role, Entitlement, Group, CI, Incident, Change, Event, Request, Workflow, WorkflowStep, WorkflowRun, Task}
}

// WorkflowNodeKinds are the kinds managed through the workflow API rather than
// the generic record forms.
func WorkflowNodeKinds() []NodeKind {
	return []NodeKind{Workflow, WorkflowStep, WorkflowRun, Task}
}

func RelationshipKinds() []RelationshipKind {
	return []RelationshipKind{HasJobCode, WorkLocation, Member, DraftedRequest, FormSubmitted, RequestedFor, RequestsAccess, HasStep, NextStep, StepAssignedTo, RunFor, InstanceOf, TaskFor, TaskStep, TaskItem, TaskAssignedTo, ActedBy, FulfilledBy, QualifiesFor, Grants, Includes, HasRole, EntitledBy, Permissions, Affects, Changes, AssignedTo, ObservedOn, DependsOn, Hosts, Uses, Governs, Provides, Accountable, Responsible, Consulted, Informed}
}

// RelationshipTypeNames returns the Neo4j relationship types in RelationshipKinds order.
func RelationshipTypeNames() []string {
	names := make([]string, 0, len(relationshipSpecs))
	for _, kind := range RelationshipKinds() {
		names = append(names, relationshipSpecs[kind].typeName)
	}
	return names
}

func NodeDefinition(kind NodeKind) (label, key string, err error) {
	spec, ok := nodeSpecs[kind]
	if !ok {
		return "", "", fmt.Errorf("unsupported node kind %q", kind)
	}
	return spec.label, spec.key, nil
}

func RelationshipDefinitionFor(kind RelationshipKind) (RelationshipDefinition, error) {
	spec, ok := relationshipSpecs[kind]
	if !ok {
		return RelationshipDefinition{}, fmt.Errorf("unsupported relationship kind %q", kind)
	}
	toKinds := []NodeKind{spec.to}
	if len(spec.toKinds) > 0 {
		toKinds = append([]NodeKind(nil), spec.toKinds...)
	}
	fromKinds := []NodeKind{spec.from}
	if len(spec.fromKinds) > 0 {
		fromKinds = append([]NodeKind(nil), spec.fromKinds...)
	}
	// A plain from/to type list is just a single pairing rule.
	rules := spec.ciTypeRules
	if len(rules) == 0 && (len(spec.fromCITypes) > 0 || len(spec.toCITypes) > 0) {
		rules = []CITypeRule{{From: spec.fromCITypes, To: spec.toCITypes}}
	}
	definition := RelationshipDefinition{TypeName: spec.typeName, Inverse: spec.inverse, From: spec.from, FromKinds: fromKinds, To: spec.to, ToKinds: toKinds}
	for _, rule := range rules {
		definition.CITypeRules = append(definition.CITypeRules, CITypeRule{From: append([]CIType(nil), rule.From...), To: append([]CIType(nil), rule.To...)})
		definition.FromCITypes = appendCITypes(definition.FromCITypes, rule.From...)
		definition.ToCITypes = appendCITypes(definition.ToCITypes, rule.To...)
	}
	return definition, nil
}

// appendCITypes adds types not already present, preserving order.
func appendCITypes(list []CIType, types ...CIType) []CIType {
	for _, ciType := range types {
		found := false
		for _, existing := range list {
			if existing == ciType {
				found = true
				break
			}
		}
		if !found {
			list = append(list, ciType)
		}
	}
	return list
}

// AllowsCITypes reports whether a from/to CI type pairing satisfies the
// definition's rules. Definitions without rules accept any pairing.
func (d RelationshipDefinition) AllowsCITypes(from, to CIType) bool {
	if len(d.CITypeRules) == 0 {
		return true
	}
	for _, rule := range d.CITypeRules {
		if (len(rule.From) == 0 || containsCIType(rule.From, from)) && (len(rule.To) == 0 || containsCIType(rule.To, to)) {
			return true
		}
	}
	return false
}

func containsCIType(list []CIType, ciType CIType) bool {
	for _, item := range list {
		if item == ciType {
			return true
		}
	}
	return false
}

// CITypeNamesOf renders a CI type list for queries and messages.
func CITypeNamesOf(types []CIType) []string {
	names := make([]string, 0, len(types))
	for _, ciType := range types {
		names = append(names, string(ciType))
	}
	return names
}

// LinkedCIRelationship returns the relationship created when a node of the given
// kind is created with ciIds, and the CI type those CIs must have (empty = any).
func LinkedCIRelationship(kind NodeKind) (RelationshipKind, CIType, bool) {
	switch kind {
	case Identity:
		return WorkLocation, Location, true
	case Incident:
		return Affects, "", true
	case Change:
		return Changes, "", true
	case Event:
		return ObservedOn, "", true
	default:
		return "", "", false
	}
}

// RelationshipLabel returns how a relationship reads from the perspective of
// one of its endpoints: the forward kind when viewed from the "from" node and
// the inverse label when viewed from the "to" node.
func RelationshipLabel(kind RelationshipKind, viewedFromSource bool) string {
	spec, ok := relationshipSpecs[kind]
	if !ok || viewedFromSource {
		return string(kind)
	}
	return spec.inverse
}
