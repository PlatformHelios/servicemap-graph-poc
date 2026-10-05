package cmdb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// accessStore fakes the access side of the store on top of the workflow fake.
type accessStore struct {
	workflowStore
	options  *AccessOptions
	created  *AccessRequestInput
	workflow string
}

func (a *accessStore) AccessOptions(context.Context, string) (*AccessOptions, error) {
	return a.options, nil
}

func (a *accessStore) CreateAccessRequest(_ context.Context, input AccessRequestInput, workflowID string) (*Node, error) {
	a.created = &input
	a.workflow = workflowID
	return &Node{Kind: Request, ID: "REQ-000002", Status: "active", Properties: map[string]any{"requestType": string(AccessRequest), "state": string(RequestInReview)}}, nil
}

func accessWorkflow(enabled bool, fulfilers ...string) WorkflowDefinition {
	workflow := AccessWorkflowTemplate()
	workflow.ID = "WFL-000002"
	workflow.Enabled = enabled
	workflow.Steps[1].AssigneeIDs = fulfilers
	return workflow
}

func TestAccessWorkflowShape(t *testing.T) {
	store := &workflowStore{}
	service := NewService(store)
	if _, err := service.SaveWorkflow(context.Background(), "", accessWorkflow(true, "GRP-000004")); err != nil {
		t.Fatalf("template with fulfilment assignees: %v", err)
	}
	if store.saved.Steps[0].AssigneeRule != ItemAccountable || len(store.saved.Steps[0].AssigneeIDs) != 0 {
		t.Errorf("approval step saved as %+v", store.saved.Steps[0])
	}
	cases := map[string]func(*WorkflowDefinition){
		"fulfilment unassigned":       func(w *WorkflowDefinition) { w.Steps[1].AssigneeIDs = nil },
		"approval named instead":      func(w *WorkflowDefinition) { w.Steps[0].AssigneeRule = ""; w.Steps[0].AssigneeIDs = []string{"IDN-000003"} },
		"approval rule and names":     func(w *WorkflowDefinition) { w.Steps[0].AssigneeIDs = []string{"IDN-000003"} },
		"extra step":                  func(w *WorkflowDefinition) { w.Steps = append(w.Steps, w.Steps[1]) },
		"approval as review":          func(w *WorkflowDefinition) { w.Steps[0].StepType = ReviewStep },
		"fulfilment as approval":      func(w *WorkflowDefinition) { w.Steps[1].StepType = ApprovalStep },
		"fields exposed":              func(w *WorkflowDefinition) { w.Steps[1].EditableFields = []string{"name"} },
		"rule on vendor workflow step": func(w *WorkflowDefinition) { *w = vendorWorkflow(false); w.Steps[0].AssigneeRule = ItemAccountable; w.Steps[0].AssigneeIDs = nil },
	}
	for name, mutate := range cases {
		workflow := accessWorkflow(false, "GRP-000004")
		mutate(&workflow)
		if _, err := service.SaveWorkflow(context.Background(), "", workflow); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestCreateAccessRequestChecksOffer(t *testing.T) {
	owner := Assignee{ID: "IDN-000003", Kind: Identity, Name: "Javid Dohnson"}
	store := &accessStore{workflowStore: workflowStore{request: map[string]any{}}, options: &AccessOptions{
		Identity:     Assignee{ID: "IDN-000001", Kind: Identity, Name: "TestUser1"},
		Applications: []AccessApplication{{ID: "CI-000020", Name: "LoanApp01", Roles: []AccessOption{{ID: "ROLE-000002", Kind: Role, Name: "Underwriter", Owner: owner}}, Entitlements: []AccessOption{{ID: "ENT-000002", Kind: Entitlement, Name: "Approve loans", Owner: owner}}}},
		Held:         []AccessHolding{{ID: "ROLE-000001", Kind: Role, Name: "Role1", Via: "birthright", Source: "Birthright1"}, {ID: "ENT-000003", Kind: Entitlement, Via: "pending", Source: "REQ-000009"}},
	}}
	service := NewService(store)
	input := AccessRequestInput{RequestedByID: "IDN-000001", RequestForID: "IDN-000001", Items: []AccessRequestItemInput{{ID: "ROLE-000002", Kind: Role, Note: " needs it for underwriting "}, {ID: "ENT-000002", Kind: Entitlement}, {ID: "ROLE-000002", Kind: Role}}}

	if _, err := service.CreateAccessRequest(context.Background(), input); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("without an enabled access workflow: err = %v", err)
	}
	store.workflows = []WorkflowDefinition{accessWorkflow(true, "GRP-000004")}
	if _, err := service.CreateAccessRequest(context.Background(), input); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	if store.workflow != "WFL-000002" || len(store.created.Items) != 2 || store.created.Items[0].Note != "needs it for underwriting" {
		t.Errorf("created = %+v via %s", store.created, store.workflow)
	}
	bad := map[string]AccessRequestInput{
		"nobody raising":      {RequestForID: "IDN-000001", Items: input.Items},
		"nobody for":          {RequestedByID: "IDN-000001", Items: input.Items},
		"no items":            {RequestedByID: "IDN-000001", RequestForID: "IDN-000001"},
		"held by birthright":  {RequestedByID: "IDN-000001", RequestForID: "IDN-000001", Items: []AccessRequestItemInput{{ID: "ROLE-000001", Kind: Role}}},
		"already pending":     {RequestedByID: "IDN-000001", RequestForID: "IDN-000001", Items: []AccessRequestItemInput{{ID: "ENT-000003", Kind: Entitlement}}},
		"not offered":         {RequestedByID: "IDN-000001", RequestForID: "IDN-000001", Items: []AccessRequestItemInput{{ID: "ENT-000099", Kind: Entitlement}}},
		"wrong kind for item": {RequestedByID: "IDN-000001", RequestForID: "IDN-000001", Items: []AccessRequestItemInput{{ID: "ROLE-000002", Kind: Entitlement}}},
		"not an access kind":  {RequestedByID: "IDN-000001", RequestForID: "IDN-000001", Items: []AccessRequestItemInput{{ID: "GRP-000002", Kind: Group}}},
	}
	for name, attempt := range bad {
		if _, err := service.CreateAccessRequest(context.Background(), attempt); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if err := validateRequestProperties(map[string]any{"requestType": "access"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("creating an access request as a draft: err = %v, want ErrInvalid", err)
	}
}

func TestActOnAccessTasks(t *testing.T) {
	owner := Assignee{ID: "IDN-000003", Kind: Identity, Name: "Javid Dohnson"}
	item := AccessItemView{ID: "ROLE-000002", Kind: Role, Name: "Underwriter", Decision: ItemPending}
	approval := &TaskView{ID: "TSK-000010", Status: TaskPending, Step: WorkflowStepDefinition{Name: "Owner approval", StepType: ApprovalStep, ApprovalRule: AnyApprover, AssigneeRule: ItemAccountable}, Request: Node{Kind: Request, ID: "REQ-000002", Properties: map[string]any{"requestType": "access", "state": "in-review"}}, Assignees: []Assignee{owner}, EligibleActors: []Assignee{owner}, Item: &item, Items: []AccessItemView{item}}
	store := &accessStore{workflowStore: workflowStore{task: approval}}
	service := NewService(store)

	if _, err := service.ActOnTask(context.Background(), approval.ID, TaskActionInput{ActorID: "IDN-000001", Action: ApproveTask}); !errors.Is(err, ErrInvalid) {
		t.Errorf("non-owner approving: err = %v", err)
	}
	if _, err := service.ActOnTask(context.Background(), approval.ID, TaskActionInput{ActorID: owner.ID, Action: RejectTask}); !errors.Is(err, ErrInvalid) {
		t.Errorf("denying without a comment: err = %v", err)
	}
	if _, err := service.ActOnTask(context.Background(), approval.ID, TaskActionInput{ActorID: owner.ID, Action: CompleteTask}); !errors.Is(err, ErrInvalid) {
		t.Errorf("completing an approval: err = %v", err)
	}
	if _, err := service.ActOnTask(context.Background(), approval.ID, TaskActionInput{ActorID: owner.ID, Action: ApproveTask, Properties: map[string]any{"name": "x"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("field changes on an access task: err = %v", err)
	}
	if _, err := service.ActOnTask(context.Background(), approval.ID, TaskActionInput{ActorID: owner.ID, Action: ApproveTask}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if store.outcome.Item != item.ID || !store.outcome.Approve || store.outcome.Deny || store.outcome.Provision || store.outcome.Action != TaskApproved || store.outcome.Return || store.outcome.Advance {
		t.Errorf("approve outcome = %+v", store.outcome)
	}
	if _, err := service.ActOnTask(context.Background(), approval.ID, TaskActionInput{ActorID: owner.ID, Action: RejectTask, Comment: "Not for this team"}); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if !store.outcome.Deny || store.outcome.Action != TaskRejected || store.outcome.Comment != "Not for this team" || store.outcome.Return {
		t.Errorf("deny outcome = %+v", store.outcome)
	}

	fulfiler := Assignee{ID: "IDN-000005", Kind: Identity, Name: "Identity Team Member"}
	fulfilment := &TaskView{ID: "TSK-000011", Status: TaskPending, FinalStep: true, Step: WorkflowStepDefinition{Name: "Fulfilment", StepType: ReviewStep}, Request: approval.Request, Assignees: []Assignee{{ID: "GRP-000004", Kind: Group, Name: "IAA-RequestFulfillment"}}, EligibleActors: []Assignee{fulfiler}, Item: &item, Items: []AccessItemView{item}}
	store.task = fulfilment
	if _, err := service.ActOnTask(context.Background(), fulfilment.ID, TaskActionInput{ActorID: fulfiler.ID, Action: ApproveTask}); !errors.Is(err, ErrInvalid) {
		t.Errorf("approving a fulfilment task: err = %v", err)
	}
	if _, err := service.ActOnTask(context.Background(), fulfilment.ID, TaskActionInput{ActorID: fulfiler.ID, Action: RejectTask, Comment: "no"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("rejecting a fulfilment task: err = %v", err)
	}
	if _, err := service.ActOnTask(context.Background(), fulfilment.ID, TaskActionInput{ActorID: fulfiler.ID, Action: CompleteTask}); err != nil {
		t.Fatalf("complete fulfilment: %v", err)
	}
	if !store.outcome.Provision || store.outcome.Action != TaskCompleted || store.outcome.Item != item.ID || store.outcome.CIProperties != nil {
		t.Errorf("provision outcome = %+v", store.outcome)
	}
	fulfilment.Item = nil
	if _, err := service.ActOnTask(context.Background(), fulfilment.ID, TaskActionInput{ActorID: fulfiler.ID, Action: CompleteTask}); !errors.Is(err, ErrInvalid) {
		t.Errorf("access task without an item: err = %v", err)
	}
}
