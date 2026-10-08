package cmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Service Catalog. Teams automate their processes as Temporal workflows they
// own as code, and publish a catalog item for each one: the form (an input
// JSON Schema generated from the workflow's input type), who may see it, the
// approvals it needs, and the workflow that fulfils it. The platform renders
// the form, enforces visibility, runs the approvals through the ordinary task
// engine (a workflow generated from the item), then hands the request to the
// team's workflow and records what it returned.

// CatalogManifest is what a team publishes, normally generated from code by
// the catalog SDK in CI. Owner, visibility, and approval assignees name groups
// or identities by id or by exact name.
type CatalogManifest struct {
	Name            string            `json:"name"`    // stable slug, e.g. server-request
	Version         string            `json:"version"` // major.minor.patch; every change needs a higher version
	Title           string            `json:"title"`
	Description     string            `json:"description,omitempty"`
	Owner           string            `json:"owner"`      // the group that owns the item and its workflow
	Visibility      []string          `json:"visibility"` // groups whose members may raise it (the owner always can)
	Approvals       []CatalogApproval `json:"approvals,omitempty"`
	SLABusinessDays int               `json:"slaBusinessDays,omitempty"`
	Inputs          map[string]any    `json:"inputs"`            // JSON Schema for the form
	Outputs         map[string]any    `json:"outputs,omitempty"` // JSON Schema for what the workflow returns
	Target          CatalogTarget     `json:"target"`
	Source          string            `json:"source,omitempty"` // where it was published from (repo@commit), for audit
}

// CatalogApproval is one approval the platform collects before the team's
// workflow starts. Approvals run in order; each is an approval step assigned
// to the named groups or identities.
type CatalogApproval struct {
	Name         string       `json:"name"`
	Assignees    []string     `json:"assignees"`
	Rule         ApprovalRule `json:"rule,omitempty"` // any (default) or all
	Instructions string       `json:"instructions,omitempty"`
}

// CatalogTarget is the Temporal workflow that fulfils the item.
type CatalogTarget struct {
	TaskQueue    string `json:"taskQueue"`
	WorkflowType string `json:"workflowType"`
}

// CatalogItemView is a published catalog item as the API returns it.
type CatalogItemView struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Title           string            `json:"title"`
	Description     string            `json:"description,omitempty"`
	Owner           Assignee          `json:"owner"`
	Visibility      []Assignee        `json:"visibility"`
	Approvals       []CatalogApproval `json:"approvals"`
	SLABusinessDays int               `json:"slaBusinessDays,omitempty"`
	Inputs          map[string]any    `json:"inputs"`
	Outputs         map[string]any    `json:"outputs,omitempty"`
	Target          CatalogTarget     `json:"target"`
	WorkflowID      string            `json:"workflowId,omitempty"` // the generated approval workflow, when there are approvals
	Source          string            `json:"source,omitempty"`
	PublishedAt     string            `json:"publishedAt,omitempty"`
	PublishedBy     string            `json:"publishedBy,omitempty"`
	Status          string            `json:"status"`
}

// CatalogRequestInput raises a request from a catalog item.
type CatalogRequestInput struct {
	CatalogItemID string         `json:"catalogItemId"`
	RequestedByID string         `json:"requestedById"`
	Inputs        map[string]any `json:"inputs"`
}

// CatalogRequestRecord is a validated catalog request ready to be written.
type CatalogRequestRecord struct {
	Item            CatalogItemView
	RequestedByID   string
	InputsJSON      string
	ReferencedCIs   []string
	SLABusinessDays int
	SLADueAt        string
	WorkflowID      string // approval workflow to start a run of; empty when the item needs no approval
}

// CatalogFulfilment is what the platform hands to the automation engine once
// a request is approved: the team's workflow and the validated form inputs.
type CatalogFulfilment struct {
	RequestID    string         `json:"requestId"`
	CatalogItem  string         `json:"catalogItem"`
	Version      string         `json:"version"`
	TaskQueue    string         `json:"taskQueue"`
	WorkflowType string         `json:"workflowType"`
	RequestedBy  string         `json:"requestedBy"`
	Inputs       map[string]any `json:"inputs"`
}

// CatalogOutcome is how the team's workflow ended.
type CatalogOutcome struct {
	Succeeded bool           `json:"succeeded"`
	Outputs   map[string]any `json:"outputs,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// CatalogAutomation starts the team's workflow for an approved request and
// returns the id it runs under. The Temporal engine implements it.
type CatalogAutomation interface {
	StartCatalogFulfilment(context.Context, CatalogFulfilment) (string, error)
}

// Request properties managed by the catalog.
const (
	CatalogItemProperty         = "catalogItem"
	CatalogItemVersionProperty  = "catalogItemVersion"
	CatalogTaskQueueProperty    = "catalogTaskQueue"
	CatalogWorkflowTypeProperty = "catalogWorkflowType"
	CatalogInputsProperty       = "inputs"
	CatalogOutputsProperty      = "outputs"
	AutomationIDProperty        = "automationWorkflowId"
	FailureReasonProperty       = "failureReason"
	// PortalSourceKeyword marks a form field whose value is a CMDB record, as
	// "cmdb:<ciType>": the form offers a picker, the server checks the record
	// exists, and the request links to it with references.
	PortalSourceKeyword = "x-portal-source"
)

var (
	catalogNamePattern   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	catalogTargetPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-/]+$`)
	catalogFieldPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

const maxCatalogFields = 50

// WithAutomation lets approved catalog requests start their team's workflow.
func (s *Service) WithAutomation(automation CatalogAutomation) *Service {
	s.automation = automation
	return s
}

// PublishCatalogItem validates a manifest against the platform's policy and
// stores it as the current version of its item. Republishing the same version
// with the same content is a no-op, so CI can publish on every build; any
// change needs a higher version. A new version gets its own approval
// workflow, so requests already in review finish on the version they started.
func (s *Service) PublishCatalogItem(ctx context.Context, publisherID string, manifest CatalogManifest) (*CatalogItemView, error) {
	clean, err := normalizeCatalogManifest(manifest)
	if err != nil {
		return nil, err
	}
	owner, err := s.resolveOne(ctx, clean.Owner, "owner", true)
	if err != nil {
		return nil, err
	}
	if err := s.requirePublisher(ctx, publisherID, owner); err != nil {
		return nil, err
	}
	visibility := []Assignee{}
	for _, reference := range clean.Visibility {
		group, err := s.resolveOne(ctx, reference, "visibility", true)
		if err != nil {
			return nil, err
		}
		if group.ID != owner.ID && !containsAssignee(visibility, group.ID) {
			visibility = append(visibility, group)
		}
	}
	if len(visibility) > 0 && len(clean.Approvals) == 0 {
		return nil, fmt.Errorf("%w: %s is visible outside %s, so it needs at least one approval before the workflow runs", ErrInvalid, clean.Name, owner.Name)
	}
	steps := make([]WorkflowStepDefinition, 0, len(clean.Approvals))
	for index, approval := range clean.Approvals {
		assignees := make([]string, 0, len(approval.Assignees))
		for _, reference := range approval.Assignees {
			assignee, err := s.resolveOne(ctx, reference, fmt.Sprintf("approval %d (%s)", index+1, approval.Name), false)
			if err != nil {
				return nil, err
			}
			assignees = append(assignees, assignee.ID)
		}
		clean.Approvals[index].Assignees = assignees
		steps = append(steps, WorkflowStepDefinition{Order: index + 1, Name: approval.Name, StepType: ApprovalStep, ApprovalRule: approval.Rule, Instructions: approval.Instructions, AssigneeIDs: assignees, EditableFields: []string{}, RequiredFields: []string{}})
	}
	clean.Owner = owner.ID
	clean.Visibility = assigneeIDs(visibility)
	manifestJSON, err := json.Marshal(clean)
	if err != nil {
		return nil, fmt.Errorf("encode catalog manifest: %w", err)
	}

	current, err := s.store.GetCatalogItemByName(ctx, clean.Name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if current != nil {
		switch compareVersions(clean.Version, current.Version) {
		case 0:
			stored, err := s.store.CatalogManifest(ctx, current.ID)
			if err != nil {
				return nil, err
			}
			same, err := sameManifestContent(stored, clean)
			if err != nil {
				return nil, err
			}
			if same {
				return current, nil
			}
			return nil, fmt.Errorf("%w: %s %s is already published with different content; publish it as a higher version", ErrInvalid, clean.Name, clean.Version)
		case -1:
			return nil, fmt.Errorf("%w: %s is at version %s; publish a higher version than that, not %s", ErrInvalid, clean.Name, current.Version, clean.Version)
		}
	}

	// Approvals become a workflow of the ordinary engine, one per published
	// version. The previous version's workflow is disabled, not retired, so its
	// runs keep their steps and finish as they started.
	workflowID := ""
	if len(steps) > 0 {
		definition := WorkflowDefinition{Name: clean.Title + " approvals (v" + clean.Version + ")", Description: "Generated from catalog item " + clean.Name + "; republish the item to change it.", RequestType: CatalogRequest, Enabled: true, SLAEnabled: clean.SLABusinessDays > 0, SLABusinessDays: clean.SLABusinessDays, Steps: steps}
		validated, err := validateWorkflowDefinition(definition)
		if err != nil {
			return nil, err
		}
		saved, err := s.store.SaveWorkflow(ctx, "", *validated)
		if err != nil {
			return nil, err
		}
		workflowID = saved.ID
	}
	if current != nil && current.WorkflowID != "" {
		if err := s.disableWorkflow(ctx, current.WorkflowID); err != nil {
			return nil, err
		}
	}
	return s.store.SaveCatalogItem(ctx, clean, string(manifestJSON), workflowID, publisherID)
}

// disableWorkflow stops a superseded approval workflow from starting new runs.
func (s *Service) disableWorkflow(ctx context.Context, id string) error {
	workflow, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if !workflow.Enabled || workflow.Status == "retired" {
		return nil
	}
	workflow.Enabled = false
	_, err = s.store.SaveWorkflow(ctx, id, *workflow)
	return err
}

// requirePublisher holds a publisher to the item's owning team: the actor must
// be a member of the owner group. The platform super admin may publish anything.
func (s *Service) requirePublisher(ctx context.Context, publisherID string, owner Assignee) error {
	publisherID = strings.TrimSpace(publisherID)
	if publisherID == "" || publisherID == PlatformSuperAdminID {
		return nil
	}
	groups, err := s.store.IdentityGroups(ctx, publisherID)
	if err != nil {
		return err
	}
	if !containsString(groups, owner.ID) {
		return fmt.Errorf("%w: %s may only publish catalog items owned by its own groups, and is not a member of %s", ErrForbidden, publisherID, owner.Name)
	}
	return nil
}

// resolveOne finds the group (or, when groupsOnly is false, the group or
// identity) a manifest names by id or exact name.
func (s *Service) resolveOne(ctx context.Context, reference, field string, groupsOnly bool) (Assignee, error) {
	matches, err := s.store.ResolveAssignees(ctx, reference)
	if err != nil {
		return Assignee{}, err
	}
	kept := make([]Assignee, 0, len(matches))
	for _, match := range matches {
		if !groupsOnly || match.Kind == Group {
			kept = append(kept, match)
		}
	}
	what := "group or identity"
	if groupsOnly {
		what = "group"
	}
	switch len(kept) {
	case 1:
		return kept[0], nil
	case 0:
		return Assignee{}, fmt.Errorf("%w: %s names %q, which is not an active %s", ErrInvalid, field, reference, what)
	default:
		return Assignee{}, fmt.Errorf("%w: %s names %q, which matches more than one %s; use its id", ErrInvalid, field, reference, what)
	}
}

// normalizeCatalogManifest checks the shape of a manifest before anything is
// looked up: names, version, target, approvals, and the form schema.
func normalizeCatalogManifest(manifest CatalogManifest) (CatalogManifest, error) {
	clean := manifest
	clean.Name = strings.TrimSpace(manifest.Name)
	clean.Version = strings.TrimSpace(manifest.Version)
	clean.Title = strings.TrimSpace(manifest.Title)
	clean.Description = strings.TrimSpace(manifest.Description)
	clean.Owner = strings.TrimSpace(manifest.Owner)
	clean.Source = strings.TrimSpace(manifest.Source)
	clean.Target = CatalogTarget{TaskQueue: strings.TrimSpace(manifest.Target.TaskQueue), WorkflowType: strings.TrimSpace(manifest.Target.WorkflowType)}
	clean.Visibility = uniqueTrimmed(manifest.Visibility)
	if !catalogNamePattern.MatchString(clean.Name) {
		return clean, fmt.Errorf("%w: name must be a lowercase slug such as server-request, not %q", ErrInvalid, manifest.Name)
	}
	if _, ok := parseVersion(clean.Version); !ok {
		return clean, fmt.Errorf("%w: version must be major.minor.patch, such as 1.0.0, not %q", ErrInvalid, manifest.Version)
	}
	if clean.Title == "" {
		return clean, fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if clean.Owner == "" {
		return clean, fmt.Errorf("%w: owner is required: name the group that owns this item", ErrInvalid)
	}
	if !catalogTargetPattern.MatchString(clean.Target.TaskQueue) || !catalogTargetPattern.MatchString(clean.Target.WorkflowType) {
		return clean, fmt.Errorf("%w: target needs the taskQueue and workflowType of the Temporal workflow that fulfils the item", ErrInvalid)
	}
	if clean.SLABusinessDays < 0 {
		return clean, fmt.Errorf("%w: slaBusinessDays cannot be negative", ErrInvalid)
	}
	clean.Approvals = make([]CatalogApproval, 0, len(manifest.Approvals))
	for index, approval := range manifest.Approvals {
		cleanApproval := CatalogApproval{Name: strings.TrimSpace(approval.Name), Instructions: strings.TrimSpace(approval.Instructions), Assignees: uniqueTrimmed(approval.Assignees), Rule: AnyApprover}
		if cleanApproval.Name == "" {
			return clean, fmt.Errorf("%w: approval %d needs a name", ErrInvalid, index+1)
		}
		if len(cleanApproval.Assignees) == 0 {
			return clean, fmt.Errorf("%w: approval %d (%s) must name at least one group or identity", ErrInvalid, index+1, cleanApproval.Name)
		}
		if strings.TrimSpace(string(approval.Rule)) != "" {
			rule, err := ParseApprovalRule(string(approval.Rule))
			if err != nil {
				return clean, fmt.Errorf("approval %d (%s): %w", index+1, cleanApproval.Name, err)
			}
			cleanApproval.Rule = rule
		}
		clean.Approvals = append(clean.Approvals, cleanApproval)
	}
	if err := validateInputSchema(clean.Inputs); err != nil {
		return clean, err
	}
	if clean.Outputs != nil {
		if _, err := compileSchema(clean.Outputs); err != nil {
			return clean, fmt.Errorf("%w: outputs is not a valid JSON Schema: %v", ErrInvalid, err)
		}
	}
	return clean, nil
}

// validateInputSchema holds the form schema to what the portal can render: an
// object of named scalar fields, with CMDB pickers on string fields.
func validateInputSchema(schema map[string]any) error {
	if schema == nil {
		return fmt.Errorf("%w: inputs is required: the JSON Schema of the form", ErrInvalid)
	}
	if schema["type"] != "object" {
		return fmt.Errorf("%w: inputs must be a JSON Schema of type object", ErrInvalid)
	}
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) > maxCatalogFields {
		return fmt.Errorf("%w: inputs has %d fields; a catalog form allows at most %d", ErrInvalid, len(properties), maxCatalogFields)
	}
	for key, value := range properties {
		if !catalogFieldPattern.MatchString(key) {
			return fmt.Errorf("%w: input field %q must start with a letter and use only letters, digits, and underscores", ErrInvalid, key)
		}
		field, _ := value.(map[string]any)
		fieldType, _ := field["type"].(string)
		switch fieldType {
		case "string", "integer", "number", "boolean":
		default:
			return fmt.Errorf("%w: input field %s must be a string, integer, number, or boolean, not %v", ErrInvalid, key, field["type"])
		}
		if source, present := field[PortalSourceKeyword]; present {
			if _, err := portalSourceCIType(source); err != nil {
				return fmt.Errorf("input field %s: %w", key, err)
			}
			if fieldType != "string" {
				return fmt.Errorf("%w: input field %s picks a CMDB record, so it must be a string", ErrInvalid, key)
			}
		}
	}
	if _, err := compileSchema(schema); err != nil {
		return fmt.Errorf("%w: inputs is not a valid JSON Schema: %v", ErrInvalid, err)
	}
	return nil
}

// portalSourceCIType reads an x-portal-source value: cmdb:<ciType>.
func portalSourceCIType(source any) (CIType, error) {
	text, _ := source.(string)
	kind, ciType, found := strings.Cut(text, ":")
	if !found || kind != "cmdb" {
		return "", fmt.Errorf("%w: %s must be cmdb:<ciType>, such as cmdb:application, not %q", ErrInvalid, PortalSourceKeyword, text)
	}
	return ParseCIType(ciType)
}

func compileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("catalog-schema.json", document); err != nil {
		return nil, err
	}
	return compiler.Compile("catalog-schema.json")
}

// ListCatalogItems returns the items an actor may raise: those visible to a
// group it belongs to, or owned by one. The super admin sees every item.
func (s *Service) ListCatalogItems(ctx context.Context, actorID string) ([]CatalogItemView, error) {
	actorID = strings.TrimSpace(actorID)
	if actorID == PlatformSuperAdminID {
		actorID = ""
	}
	return s.store.ListCatalogItems(ctx, actorID)
}

func (s *Service) GetCatalogItem(ctx context.Context, id string) (*CatalogItemView, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.store.GetCatalogItem(ctx, id)
}

// CatalogOption is one record a picker field offers.
type CatalogOption struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// CatalogFieldOptions lists the active CMDB records a picker field offers, so
// requesters can fill in the form without read access to the whole CMDB.
func (s *Service) CatalogFieldOptions(ctx context.Context, item *CatalogItemView, field string) ([]CatalogOption, error) {
	properties, _ := item.Inputs["properties"].(map[string]any)
	definition, _ := properties[field].(map[string]any)
	source, present := definition[PortalSourceKeyword]
	if !present {
		return nil, fmt.Errorf("%w: %s has no field %q that picks a CMDB record", ErrInvalid, item.Name, field)
	}
	ciType, err := portalSourceCIType(source)
	if err != nil {
		return nil, err
	}
	nodes, _, err := s.store.ListNodes(ctx, CI, ListOptions{})
	if err != nil {
		return nil, err
	}
	options := []CatalogOption{}
	for _, node := range nodes {
		if node.Status == "active" && node.Properties["ciType"] == string(ciType) {
			options = append(options, CatalogOption{ID: node.ID, Name: stringOf(node.Properties["name"])})
		}
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	return options, nil
}

// CatalogItemVisibleTo reports whether an identity may see and raise an item.
func (s *Service) CatalogItemVisibleTo(ctx context.Context, item *CatalogItemView, identityID string) (bool, error) {
	if identityID == "" || identityID == PlatformSuperAdminID {
		return true, nil
	}
	groups, err := s.store.IdentityGroups(ctx, identityID)
	if err != nil {
		return false, err
	}
	if containsString(groups, item.Owner.ID) {
		return true, nil
	}
	for _, group := range item.Visibility {
		if containsString(groups, group.ID) {
			return true, nil
		}
	}
	return false, nil
}

// CreateCatalogRequest raises a request from a catalog item. The requester
// must be able to see the item and the inputs must satisfy its schema; fields
// that pick CMDB records must name active CIs of the right type. With
// approvals the request goes into review; without, it is handed straight to
// the team's workflow.
func (s *Service) CreateCatalogRequest(ctx context.Context, input CatalogRequestInput) (*Node, error) {
	input.CatalogItemID = strings.TrimSpace(input.CatalogItemID)
	input.RequestedByID = strings.TrimSpace(input.RequestedByID)
	if input.CatalogItemID == "" {
		return nil, fmt.Errorf("%w: catalogItemId is required", ErrInvalid)
	}
	if input.RequestedByID == "" {
		return nil, fmt.Errorf("%w: requestedById is required: say who is raising the request", ErrInvalid)
	}
	if _, err := s.store.GetNode(ctx, Identity, input.RequestedByID, false); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: requestedById %s is not an active identity", ErrInvalid, input.RequestedByID)
		}
		return nil, err
	}
	item, err := s.store.GetCatalogItem(ctx, input.CatalogItemID)
	if err != nil {
		return nil, err
	}
	if item.Status != "active" {
		return nil, fmt.Errorf("%w: catalog item %s is retired", ErrInvalid, item.ID)
	}
	visible, err := s.CatalogItemVisibleTo(ctx, item, input.RequestedByID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, fmt.Errorf("%w: %s is not offered to %s", ErrForbidden, item.Title, input.RequestedByID)
	}
	if input.Inputs == nil {
		input.Inputs = map[string]any{}
	}
	if err := validateCatalogInputs(item.Inputs, input.Inputs); err != nil {
		return nil, err
	}
	references, err := s.catalogReferences(ctx, item.Inputs, input.Inputs)
	if err != nil {
		return nil, err
	}
	inputsJSON, err := json.Marshal(input.Inputs)
	if err != nil {
		return nil, fmt.Errorf("encode catalog inputs: %w", err)
	}
	record := CatalogRequestRecord{Item: *item, RequestedByID: input.RequestedByID, InputsJSON: string(inputsJSON), ReferencedCIs: references, SLABusinessDays: item.SLABusinessDays, WorkflowID: item.WorkflowID}
	if record.SLABusinessDays > 0 {
		holidays, err := s.store.ListHolidays(ctx)
		if err != nil {
			return nil, err
		}
		record.SLADueAt = AddBusinessDays(time.Now().UTC(), record.SLABusinessDays, holidays).Format(holidayDateLayout)
	}
	request, err := s.store.CreateCatalogRequest(ctx, record)
	if err != nil {
		return nil, err
	}
	if item.WorkflowID == "" {
		return s.handOffCatalogRequest(ctx, request.ID)
	}
	return request, nil
}

// validateCatalogInputs checks submitted inputs against the item's schema.
func validateCatalogInputs(schema, inputs map[string]any) error {
	compiled, err := compileSchema(schema)
	if err != nil {
		return fmt.Errorf("catalog item schema: %w", err)
	}
	raw, err := json.Marshal(inputs)
	if err != nil {
		return fmt.Errorf("%w: inputs must be a JSON object", ErrInvalid)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%w: inputs must be a JSON object", ErrInvalid)
	}
	if err := compiled.Validate(instance); err != nil {
		var validation *jsonschema.ValidationError
		if errors.As(err, &validation) {
			return fmt.Errorf("%w: the form is not complete: %s", ErrInvalid, describeValidation(validation))
		}
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

// describeValidation flattens a schema validation error into one line, field
// by field, for people filling in the form.
func describeValidation(validation *jsonschema.ValidationError) string {
	output := validation.BasicOutput()
	messages := make([]string, 0, len(output.Errors))
	for _, unit := range output.Errors {
		if unit.Error == nil {
			continue
		}
		message := unit.Error.String()
		if location := strings.TrimPrefix(unit.InstanceLocation, "/"); location != "" {
			message = location + ": " + message
		}
		if !containsString(messages, message) {
			messages = append(messages, message)
		}
	}
	if len(messages) == 0 {
		return strings.TrimSpace(validation.Error())
	}
	return strings.Join(messages, "; ")
}

// catalogReferences checks every field that picks a CMDB record names an
// active CI of the declared type, returning their ids for references links.
func (s *Service) catalogReferences(ctx context.Context, schema, inputs map[string]any) ([]string, error) {
	properties, _ := schema["properties"].(map[string]any)
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	references := []string{}
	for _, key := range keys {
		field, _ := properties[key].(map[string]any)
		source, present := field[PortalSourceKeyword]
		if !present {
			continue
		}
		value, _ := inputs[key].(string)
		if strings.TrimSpace(value) == "" {
			continue
		}
		ciType, err := portalSourceCIType(source)
		if err != nil {
			return nil, err
		}
		ci, err := s.store.GetNode(ctx, CI, value, false)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("%w: %s must be an active %s CI; %s is not one", ErrInvalid, key, ciType, value)
			}
			return nil, err
		}
		if ci.Properties["ciType"] != string(ciType) {
			return nil, fmt.Errorf("%w: %s must be a %s CI; %s is a %v", ErrInvalid, key, ciType, value, ci.Properties["ciType"])
		}
		if !containsString(references, value) {
			references = append(references, value)
		}
	}
	return references, nil
}

// actOnCatalogTask applies an approver's decision on a catalog request.
// Approving the last approval hands the request to the team's workflow;
// denying it (with a reason) closes the request as denied.
func (s *Service) actOnCatalogTask(ctx context.Context, view *TaskView, actor Assignee, action TaskAction, comment string, properties map[string]any) (*TaskView, error) {
	if len(properties) > 0 {
		return nil, fmt.Errorf("%w: catalog approvals have no fields to change; the requester's inputs are fixed once submitted", ErrInvalid)
	}
	outcome := TaskOutcome{Comment: comment}
	switch action {
	case ApproveTask:
		outcome.Action = TaskApproved
		outcome.Advance = approvalSatisfied(view, actor.ID)
		outcome.Handoff = outcome.Advance && view.FinalStep
	case RejectTask:
		if comment == "" {
			return nil, fmt.Errorf("%w: a comment is required when denying a request, so the requester knows why", ErrInvalid)
		}
		outcome.Action, outcome.DenyRequest = TaskRejected, true
	default:
		return nil, fmt.Errorf("%w: %s is an approval: approve or deny it", ErrInvalid, view.Step.Name)
	}
	result, err := s.completeTaskAction(ctx, view.ID, actor.ID, outcome)
	if err != nil {
		return nil, err
	}
	if !outcome.Handoff {
		return result, nil
	}
	if _, err := s.handOffCatalogRequest(ctx, result.Request.ID); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, view.ID)
}

// handOffCatalogRequest starts the team's workflow for an approved request.
// The workflow id is derived from the request, so a retry never starts a
// second run. If it cannot be started the request is marked failed with why.
func (s *Service) handOffCatalogRequest(ctx context.Context, requestID string) (*Node, error) {
	if s.automation == nil {
		return s.store.SettleCatalogRequest(ctx, requestID, CatalogOutcome{Error: "no automation engine is configured on this platform (set TEMPORAL_ADDRESS)"})
	}
	request, err := s.store.GetNode(ctx, Request, requestID, false)
	if err != nil {
		return nil, err
	}
	var inputs map[string]any
	if raw, _ := request.Properties[CatalogInputsProperty].(string); raw != "" {
		if err := json.Unmarshal([]byte(raw), &inputs); err != nil {
			return nil, fmt.Errorf("decode inputs of request %s: %w", request.ID, err)
		}
	}
	requester := ""
	for _, relationship := range request.Relationships {
		if relationship.Kind == FormSubmitted && relationship.ToID == request.ID {
			requester = relationship.FromID
		}
	}
	fulfilment := CatalogFulfilment{
		RequestID:    request.ID,
		CatalogItem:  stringOf(request.Properties[CatalogItemProperty]),
		Version:      stringOf(request.Properties[CatalogItemVersionProperty]),
		TaskQueue:    stringOf(request.Properties[CatalogTaskQueueProperty]),
		WorkflowType: stringOf(request.Properties[CatalogWorkflowTypeProperty]),
		RequestedBy:  requester,
		Inputs:       inputs,
	}
	workflowID, err := s.automation.StartCatalogFulfilment(ctx, fulfilment)
	if err != nil {
		return s.store.SettleCatalogRequest(ctx, request.ID, CatalogOutcome{Error: "the fulfilment workflow could not be started: " + err.Error()})
	}
	return s.store.UpdateNode(ctx, Request, request.ID, map[string]any{AutomationIDProperty: workflowID})
}

// RecordCatalogOutcome closes an in-progress catalog request with what the
// team's workflow returned. It is safe to repeat: a request that is no longer
// in progress is left as it is.
func (s *Service) RecordCatalogOutcome(ctx context.Context, requestID string, outcome CatalogOutcome) (*Node, error) {
	if err := validateID(requestID); err != nil {
		return nil, err
	}
	return s.store.SettleCatalogRequest(ctx, requestID, outcome)
}

// sameManifestContent compares a stored manifest with a new one, ignoring
// where each was published from: CI rebuilding an unchanged item from a new
// commit is a no-op, not a conflict.
func sameManifestContent(stored string, manifest CatalogManifest) (bool, error) {
	var previous CatalogManifest
	if err := json.Unmarshal([]byte(stored), &previous); err != nil {
		return false, fmt.Errorf("decode stored catalog manifest: %w", err)
	}
	previous.Source, manifest.Source = "", ""
	left, err := json.Marshal(previous)
	if err != nil {
		return false, err
	}
	right, err := json.Marshal(manifest)
	if err != nil {
		return false, err
	}
	return bytes.Equal(left, right), nil
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

func containsAssignee(list []Assignee, id string) bool {
	_, found := findAssignee(list, id)
	return found
}

func assigneeIDs(list []Assignee) []string {
	ids := make([]string, 0, len(list))
	for _, item := range list {
		ids = append(ids, item.ID)
	}
	return ids
}

func parseVersion(version string) ([3]int, bool) {
	var parsed [3]int
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return parsed, false
	}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || (len(part) > 1 && part[0] == '0') {
			return parsed, false
		}
		parsed[index] = number
	}
	return parsed, true
}

// compareVersions orders two major.minor.patch versions (-1, 0, 1).
func compareVersions(a, b string) int {
	left, _ := parseVersion(a)
	right, _ := parseVersion(b)
	for index := range left {
		switch {
		case left[index] < right[index]:
			return -1
		case left[index] > right[index]:
			return 1
		}
	}
	return 0
}
