package cmdb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// workflowStore fakes the workflow side of the store: it serves one task view
// and records the outcome the service decides on.
type workflowStore struct {
	Store
	workflows []WorkflowDefinition
	saved     *WorkflowDefinition
	task      *TaskView
	outcome   *TaskOutcome
	actor     string
	started   string
	request   map[string]any
}

func (w *workflowStore) ListHolidays(context.Context) ([]Holiday, error) {
	return nil, nil
}

func (w *workflowStore) SaveHolidays(_ context.Context, _ []Holiday) error {
	return nil
}

func (w *workflowStore) ListWorkflows(context.Context, bool) ([]WorkflowDefinition, error) {
	return w.workflows, nil
}

func (w *workflowStore) SaveWorkflow(_ context.Context, id string, definition WorkflowDefinition) (*WorkflowDefinition, error) {
	if id == "" {
		id = "WFL-000001"
	}
	definition.ID = id
	w.saved = &definition
	return &definition, nil
}

func (w *workflowStore) GetTask(context.Context, string) (*TaskView, error) {
	return w.task, nil
}

func (w *workflowStore) ActOnTask(_ context.Context, _ string, actorID string, outcome TaskOutcome) (*TaskView, error) {
	w.actor = actorID
	w.outcome = &outcome
	return w.task, nil
}

func (w *workflowStore) GetNode(_ context.Context, kind NodeKind, id string, _ bool) (*Node, error) {
	return &Node{Kind: kind, ID: id, Properties: w.request, Status: "active"}, nil
}

func (w *workflowStore) SubmitRequest(_ context.Context, id string, properties map[string]any) (*Node, error) {
	for key, value := range properties {
		w.request[key] = value
	}
	w.request["state"] = string(RequestSubmitted)
	return &Node{Kind: Request, ID: id, Properties: w.request, Status: "active"}, nil
}

func (w *workflowStore) StartWorkflowRun(_ context.Context, requestID, workflowID string) error {
	w.started = workflowID
	w.request["state"] = string(RequestInReview)
	return nil
}

func vendorWorkflow(enabled bool) WorkflowDefinition {
	return WorkflowDefinition{Name: "Vendor onboarding", RequestType: VendorRequest, Enabled: enabled, Steps: []WorkflowStepDefinition{
		{Name: "IT review", StepType: ReviewStep, AssigneeIDs: []string{"GRP-000002"}, EditableFields: []string{"criticality", "description"}, RequiredFields: []string{"criticality"}},
		{Name: "Security approval", StepType: ApprovalStep, ApprovalRule: AllApprovers, AssigneeIDs: []string{"GRP-000003", "IDN-000003"}, EditableFields: []string{}, RequiredFields: []string{}},
	}}
}

func TestWorkflowDefinitionValidation(t *testing.T) {
	for _, kind := range WorkflowNodeKinds() {
		if _, err := ParseNodeKind(string(kind)); err != nil {
			t.Errorf("ParseNodeKind(%s): %v", kind, err)
		}
		if _, err := GeneratedIDPrefix(kind); err != nil {
			t.Errorf("GeneratedIDPrefix(%s): %v", kind, err)
		}
	}
	if fields := RequestFieldKeys(VendorRequest); strings.Join(fields, ",") != "name,description,criticality,contactName,contactPhone,contactEmail" {
		t.Errorf("RequestFieldKeys(vendor) = %v", fields)
	}
	store := &workflowStore{}
	service := NewService(store)
	saved, err := service.SaveWorkflow(context.Background(), "", vendorWorkflow(true))
	if err != nil {
		t.Fatalf("save valid workflow: %v", err)
	}
	if saved.Steps[0].Order != 1 || saved.Steps[1].Order != 2 || saved.Steps[1].ApprovalRule != AllApprovers || saved.Steps[0].ApprovalRule != "" {
		t.Errorf("steps were not normalized: %#v", saved.Steps)
	}

	broken := func(mutate func(*WorkflowDefinition)) WorkflowDefinition {
		definition := vendorWorkflow(true)
		mutate(&definition)
		return definition
	}
	for name, definition := range map[string]WorkflowDefinition{
		"no name":              broken(func(d *WorkflowDefinition) { d.Name = " " }),
		"unknown form":         broken(func(d *WorkflowDefinition) { d.RequestType = "server" }),
		"enabled without steps": broken(func(d *WorkflowDefinition) { d.Steps = nil }),
		"unnamed step":         broken(func(d *WorkflowDefinition) { d.Steps[0].Name = "" }),
		"bad step type":        broken(func(d *WorkflowDefinition) { d.Steps[0].StepType = "vote" }),
		"bad approval rule":    broken(func(d *WorkflowDefinition) { d.Steps[1].ApprovalRule = "majority" }),
		"unassigned step":      broken(func(d *WorkflowDefinition) { d.Steps[1].AssigneeIDs = nil }),
		"unknown field":        broken(func(d *WorkflowDefinition) { d.Steps[0].EditableFields = []string{"criticality", "ciType"} }),
		"required not editable": broken(func(d *WorkflowDefinition) { d.Steps[0].RequiredFields = []string{"criticality", "name"} }),
		"criticality never set": broken(func(d *WorkflowDefinition) { d.Steps[0].RequiredFields = nil }),
		"sla without days":     broken(func(d *WorkflowDefinition) { d.SLAEnabled = true }),
		"ola without days":     broken(func(d *WorkflowDefinition) { d.Steps[0].OLAEnabled = true }),
	} {
		if _, err := service.SaveWorkflow(context.Background(), "", definition); !errors.Is(err, ErrInvalid) {
			t.Errorf("save workflow (%s) error = %v, want ErrInvalid", name, err)
		}
	}
	// A disabled draft of a workflow may be saved incomplete.
	if _, err := service.SaveWorkflow(context.Background(), "", broken(func(d *WorkflowDefinition) { d.Enabled = false; d.Steps = nil })); err != nil {
		t.Errorf("save disabled workflow without steps: %v", err)
	}
	// Only one enabled workflow per form.
	store.workflows = []WorkflowDefinition{{ID: "WFL-000001", Name: "Existing", RequestType: VendorRequest, Enabled: true}}
	if _, err := service.SaveWorkflow(context.Background(), "", vendorWorkflow(true)); !errors.Is(err, ErrInvalid) {
		t.Errorf("second enabled workflow error = %v, want ErrInvalid", err)
	}
	if _, err := service.SaveWorkflow(context.Background(), "WFL-000001", vendorWorkflow(true)); err != nil {
		t.Errorf("re-saving the enabled workflow itself: %v", err)
	}
	// Workflow records are not created through the generic node endpoints.
	if _, err := service.CreateGeneratedNode(context.Background(), Workflow, map[string]any{"name": "x"}, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("generic workflow create error = %v, want ErrInvalid", err)
	}
}

func TestSubmitStartsEnabledWorkflow(t *testing.T) {
	store := &workflowStore{request: map[string]any{"requestType": "vendor", "state": "draft", "name": "Acme"}}
	store.workflows = []WorkflowDefinition{{ID: "WFL-000001", RequestType: VendorRequest, Enabled: true, Steps: vendorWorkflow(true).Steps}}
	service := NewService(store)
	node, err := service.UpdateNode(context.Background(), Request, "REQ-000001", map[string]any{"state": "submitted"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if store.started != "WFL-000001" || node.Properties["state"] != string(RequestInReview) {
		t.Errorf("submit did not start the enabled workflow: started=%q state=%v", store.started, node.Properties["state"])
	}
	// Callers cannot set workflow-owned states or move a request backwards.
	for _, state := range []string{"in-review", "fulfilled", "draft", "submitted"} {
		if _, err := service.UpdateNode(context.Background(), Request, "REQ-000001", map[string]any{"state": state}); state != "in-review" && !errors.Is(err, ErrInvalid) {
			t.Errorf("set state %s on an in-review request error = %v, want ErrInvalid", state, err)
		}
	}
	// A returned request carries the reviewer's comment, which callers cannot forge.
	if _, err := service.UpdateNode(context.Background(), Request, "REQ-000001", map[string]any{"returnComment": "x"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("caller returnComment error = %v, want ErrInvalid", err)
	}
}

func TestActOnTask(t *testing.T) {
	review := &TaskView{ID: "TSK-000001", Status: TaskPending, Step: WorkflowStepDefinition{Name: "IT review", StepType: ReviewStep, EditableFields: []string{"criticality", "description"}, RequiredFields: []string{"criticality"}},
		Request:        Node{Kind: Request, ID: "REQ-000001", Properties: map[string]any{"requestType": "vendor", "state": "in-review", "name": "Acme"}},
		Assignees:      []Assignee{{ID: "GRP-000002", Kind: Group, Name: "IT"}},
		EligibleActors: []Assignee{{ID: "IDN-000001", Kind: Identity, Name: "TestUser1"}}}
	store := &workflowStore{task: review}
	service := NewService(store)
	act := func(input TaskActionInput) error {
		_, err := service.ActOnTask(context.Background(), "TSK-000001", input)
		return err
	}
	for name, input := range map[string]TaskActionInput{
		"no actor":          {Action: CompleteTask},
		"unknown action":    {ActorID: "IDN-000001", Action: "skip"},
		"not assigned":      {ActorID: "IDN-000003", Action: CompleteTask},
		"approve a review":  {ActorID: "IDN-000001", Action: ApproveTask},
		"field not exposed": {ActorID: "IDN-000001", Action: CompleteTask, Properties: map[string]any{"criticality": "critical", "name": "Other"}},
		"bad field value":   {ActorID: "IDN-000001", Action: CompleteTask, Properties: map[string]any{"criticality": "low"}},
		"required missing":  {ActorID: "IDN-000001", Action: CompleteTask, Properties: map[string]any{"description": "x"}},
		"reject no comment": {ActorID: "IDN-000001", Action: RejectTask},
		"reject with edits": {ActorID: "IDN-000001", Action: RejectTask, Comment: "no", Properties: map[string]any{"description": "x"}},
	} {
		if err := act(input); !errors.Is(err, ErrInvalid) || store.outcome != nil {
			t.Errorf("act (%s) error = %v, want ErrInvalid with no outcome", name, err)
		}
	}
	// Completing a review with its required field advances the run; this is not the final step, so no CI yet.
	if err := act(TaskActionInput{ActorID: "IDN-000001", Action: CompleteTask, Properties: map[string]any{"criticality": "Important"}}); err != nil {
		t.Fatalf("complete review: %v", err)
	}
	if store.actor != "IDN-000001" || store.outcome.Action != TaskCompleted || !store.outcome.Advance || store.outcome.Return || store.outcome.CIProperties != nil || store.outcome.Properties["criticality"] != "important" {
		t.Errorf("review outcome = %#v", store.outcome)
	}
	// Returning the request records the comment and does not advance.
	store.outcome = nil
	if err := act(TaskActionInput{ActorID: "IDN-000001", Action: RejectTask, Comment: "Need a contract first"}); err != nil {
		t.Fatalf("return request: %v", err)
	}
	if !store.outcome.Return || store.outcome.Advance || store.outcome.Action != TaskRejected || store.outcome.Comment != "Need a contract first" {
		t.Errorf("return outcome = %#v", store.outcome)
	}

	// An "all" approval waits for every eligible approver; the last approval on the final step creates the vendor CI.
	approval := &TaskView{ID: "TSK-000002", Status: TaskPending, FinalStep: true, Step: WorkflowStepDefinition{Name: "Security approval", StepType: ApprovalStep, ApprovalRule: AllApprovers},
		Request:        Node{Kind: Request, ID: "REQ-000001", Properties: map[string]any{"requestType": "vendor", "state": "in-review", "name": "Acme", "criticality": "important", "contactPhone": "(555) 010-0100"}},
		EligibleActors: []Assignee{{ID: "IDN-000001", Kind: Identity}, {ID: "IDN-000003", Kind: Identity}}}
	store = &workflowStore{task: approval}
	service = NewService(store)
	if _, err := service.ActOnTask(context.Background(), "TSK-000002", TaskActionInput{ActorID: "IDN-000001", Action: ApproveTask}); err != nil {
		t.Fatalf("first approval: %v", err)
	}
	if store.outcome.Advance || store.outcome.Action != TaskApproved {
		t.Errorf("first of two approvals should keep the task pending: %#v", store.outcome)
	}
	approval.Actions = []TaskActionRecord{{ActorID: "IDN-000001", Action: TaskApproved}}
	if _, err := service.ActOnTask(context.Background(), "TSK-000002", TaskActionInput{ActorID: "IDN-000003", Action: ApproveTask}); err != nil {
		t.Fatalf("second approval: %v", err)
	}
	if !store.outcome.Advance || store.outcome.CIProperties == nil || store.outcome.CIProperties["ciType"] != "vendor" || store.outcome.CIProperties["criticality"] != "important" || store.outcome.CIProperties["name"] != "Acme" {
		t.Errorf("final approval should fulfil the request as a vendor CI: %#v", store.outcome)
	}
	if _, ok := store.outcome.CIProperties["requestType"]; ok {
		t.Errorf("request-only properties leaked into the CI: %#v", store.outcome.CIProperties)
	}
	// The final step cannot complete while the CI would be rejected (criticality missing here).
	delete(approval.Request.Properties, "criticality")
	if _, err := service.ActOnTask(context.Background(), "TSK-000002", TaskActionInput{ActorID: "IDN-000003", Action: ApproveTask}); !errors.Is(err, ErrInvalid) {
		t.Errorf("final step without CI requirements error = %v, want ErrInvalid", err)
	}
	// Done tasks cannot be acted on again.
	approval.Status = TaskApproved
	if _, err := service.ActOnTask(context.Background(), "TSK-000002", TaskActionInput{ActorID: "IDN-000003", Action: ApproveTask}); !errors.Is(err, ErrInvalid) {
		t.Errorf("acting on a finished task error = %v, want ErrInvalid", err)
	}
}
