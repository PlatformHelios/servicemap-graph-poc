package cmdb

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Workflow definitions and execution. A workflow belongs to one catalog form
// (requestType); at most one workflow per form may be enabled. Submitting a
// request starts a run of the enabled workflow, which raises a task for the
// first step. Assignees act on tasks; completing the final step creates the CI
// the request describes and marks the request fulfilled.

// SaveWorkflow validates a workflow definition and stores it. An empty id
// creates a new workflow; otherwise the named workflow is replaced, keeping
// the steps whose ids are supplied so running tasks still point at them.
func (s *Service) SaveWorkflow(ctx context.Context, id string, definition WorkflowDefinition) (*WorkflowDefinition, error) {
	if id != "" {
		if err := validateID(id); err != nil {
			return nil, err
		}
	}
	clean, err := validateWorkflowDefinition(definition)
	if err != nil {
		return nil, err
	}
	if clean.Enabled {
		// Only one workflow may start for a given form.
		existing, err := s.store.ListWorkflows(ctx, false)
		if err != nil {
			return nil, err
		}
		for _, other := range existing {
			if other.Enabled && other.RequestType == clean.RequestType && other.ID != id {
				return nil, fmt.Errorf("%w: %s (%s) is already enabled for %s requests; disable it before enabling another", ErrInvalid, other.Name, other.ID, clean.RequestType)
			}
		}
	}
	return s.store.SaveWorkflow(ctx, id, *clean)
}

func (s *Service) GetWorkflow(ctx context.Context, id string) (*WorkflowDefinition, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.store.GetWorkflow(ctx, id)
}

func (s *Service) ListWorkflows(ctx context.Context, includeRetired bool) ([]WorkflowDefinition, error) {
	return s.store.ListWorkflows(ctx, includeRetired)
}

func (s *Service) RetireWorkflow(ctx context.Context, id string) (*WorkflowDefinition, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.store.RetireWorkflow(ctx, id)
}

// validateWorkflowDefinition normalizes a definition and checks it can run:
// every step is named, typed and assigned; exposed fields exist on the form;
// and, when enabled, the steps together collect everything the CI needs.
func validateWorkflowDefinition(definition WorkflowDefinition) (*WorkflowDefinition, error) {
	clean := WorkflowDefinition{Name: strings.TrimSpace(definition.Name), Description: strings.TrimSpace(definition.Description), Enabled: definition.Enabled, SLAEnabled: definition.SLAEnabled}
	if clean.Name == "" {
		return nil, fmt.Errorf("%w: workflow name is required", ErrInvalid)
	}
	requestType, err := ParseRequestType(string(definition.RequestType))
	if err != nil {
		return nil, err
	}
	clean.RequestType = requestType
	slaDays, err := parseBusinessDays(definition.SLABusinessDays, "slaBusinessDays")
	if err != nil {
		return nil, err
	}
	if clean.SLAEnabled && slaDays < 1 {
		return nil, fmt.Errorf("%w: slaBusinessDays is required when the form SLA is enabled", ErrInvalid)
	}
	if !clean.SLAEnabled {
		slaDays = 0
	}
	clean.SLABusinessDays = slaDays
	formFields := RequestFieldKeys(requestType)
	if clean.Enabled && len(definition.Steps) == 0 {
		return nil, fmt.Errorf("%w: an enabled workflow needs at least one step", ErrInvalid)
	}
	required := make(map[string]bool)
	for index, step := range definition.Steps {
		position := index + 1
		cleanStep := WorkflowStepDefinition{ID: strings.TrimSpace(step.ID), Order: position, Name: strings.TrimSpace(step.Name), Instructions: strings.TrimSpace(step.Instructions)}
		if cleanStep.Name == "" {
			return nil, fmt.Errorf("%w: step %d needs a name", ErrInvalid, position)
		}
		stepType, err := ParseStepType(string(step.StepType))
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", position, cleanStep.Name, err)
		}
		cleanStep.StepType = stepType
		if stepType == ApprovalStep {
			rule := AnyApprover
			if strings.TrimSpace(string(step.ApprovalRule)) != "" {
				if rule, err = ParseApprovalRule(string(step.ApprovalRule)); err != nil {
					return nil, fmt.Errorf("step %d (%s): %w", position, cleanStep.Name, err)
				}
			}
			cleanStep.ApprovalRule = rule
		}
		cleanStep.AssigneeIDs = uniqueTrimmed(step.AssigneeIDs)
		assigneeRule, err := ParseAssigneeRule(string(step.AssigneeRule))
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", position, cleanStep.Name, err)
		}
		cleanStep.AssigneeRule = assigneeRule
		if assigneeRule != "" && len(cleanStep.AssigneeIDs) > 0 {
			return nil, fmt.Errorf("%w: step %d (%s) is assigned by rule (%s), so it cannot also name assignees", ErrInvalid, position, cleanStep.Name, assigneeRule)
		}
		if assigneeRule == "" && len(cleanStep.AssigneeIDs) == 0 {
			return nil, fmt.Errorf("%w: step %d (%s) must be assigned to at least one identity or group", ErrInvalid, position, cleanStep.Name)
		}
		olaDays, err := parseBusinessDays(step.OLABusinessDays, "olaBusinessDays")
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", position, cleanStep.Name, err)
		}
		cleanStep.OLAEnabled = step.OLAEnabled
		if cleanStep.OLAEnabled && olaDays < 1 {
			return nil, fmt.Errorf("%w: step %d (%s) needs how many business days the OLA allows", ErrInvalid, position, cleanStep.Name)
		}
		if !cleanStep.OLAEnabled {
			olaDays = 0
		}
		cleanStep.OLABusinessDays = olaDays
		cleanStep.EditableFields = uniqueTrimmed(step.EditableFields)
		for _, field := range cleanStep.EditableFields {
			if !containsString(formFields, field) {
				return nil, fmt.Errorf("%w: step %d (%s) exposes %q, which is not a field on the %s form (%s)", ErrInvalid, position, cleanStep.Name, field, requestType, strings.Join(formFields, ", "))
			}
		}
		cleanStep.RequiredFields = uniqueTrimmed(step.RequiredFields)
		for _, field := range cleanStep.RequiredFields {
			if !containsString(cleanStep.EditableFields, field) {
				return nil, fmt.Errorf("%w: step %d (%s) requires %q, so it must also be editable at that step", ErrInvalid, position, cleanStep.Name, field)
			}
			required[field] = true
		}
		clean.Steps = append(clean.Steps, cleanStep)
	}
	if clean.Enabled {
		missing := make([]string, 0)
		for _, field := range RequestFulfilmentProperties(requestType) {
			if !required[field] {
				missing = append(missing, field)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("%w: the %s CI needs %s, which the requester does not supply; make it a required field on one of the steps before enabling this workflow", ErrInvalid, requestType, strings.Join(missing, ", "))
		}
	}
	if clean.Steps == nil {
		clean.Steps = []WorkflowStepDefinition{}
	}
	if requestType == AccessRequest {
		if err := validateAccessWorkflow(clean); err != nil {
			return nil, err
		}
	} else {
		for _, step := range clean.Steps {
			if step.AssigneeRule != "" {
				return nil, fmt.Errorf("%w: step %d (%s): assignee rules only apply to the access request workflow", ErrInvalid, step.Order, step.Name)
			}
		}
	}
	return &clean, nil
}

// validateAccessWorkflow holds the access workflow to its fixed shape: an
// approval step assigned to each requested item's Accountable, then a review
// step (fulfilment) assigned to the identity team. The admin names the steps
// and chooses who fulfils; nothing else moves.
func validateAccessWorkflow(definition WorkflowDefinition) error {
	if len(definition.Steps) != 2 {
		return fmt.Errorf("%w: the access request workflow has exactly two steps: owner approval, then fulfilment", ErrInvalid)
	}
	approval, fulfilment := definition.Steps[0], definition.Steps[1]
	if approval.StepType != ApprovalStep || approval.AssigneeRule != ItemAccountable {
		return fmt.Errorf("%w: step 1 (%s) of the access request workflow must be an approval step assigned by rule to each requested item's Accountable (%s)", ErrInvalid, approval.Name, ItemAccountable)
	}
	if fulfilment.StepType != ReviewStep || fulfilment.AssigneeRule != "" {
		return fmt.Errorf("%w: step 2 (%s) of the access request workflow must be a review step assigned to the identities or groups that fulfil access", ErrInvalid, fulfilment.Name)
	}
	for _, step := range definition.Steps {
		if len(step.EditableFields) > 0 {
			return fmt.Errorf("%w: the access request form has no fields for step %d (%s) to expose", ErrInvalid, step.Order, step.Name)
		}
	}
	return nil
}

// AccessWorkflowTemplate is the access request workflow as it starts out in
// the Workflow Creator: the admin fills in who fulfils and enables it.
func AccessWorkflowTemplate() WorkflowDefinition {
	return WorkflowDefinition{Name: "Access Request Workflow", RequestType: AccessRequest, Steps: []WorkflowStepDefinition{
		{Order: 1, Name: "Owner approval", StepType: ApprovalStep, ApprovalRule: AnyApprover, AssigneeRule: ItemAccountable, Instructions: "Approve or deny the requested role or entitlement for this identity.", EditableFields: []string{}, RequiredFields: []string{}, AssigneeIDs: []string{}},
		{Order: 2, Name: "Fulfilment", StepType: ReviewStep, Instructions: "Provision the approved role or entitlement, then mark the task complete to record the permission.", EditableFields: []string{}, RequiredFields: []string{}, AssigneeIDs: []string{}},
	}}
}

func uniqueTrimmed(values []string) []string {
	seen := make(map[string]bool, len(values))
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		clean = append(clean, value)
	}
	return clean
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// startWorkflow begins a run for a freshly submitted request when its form has
// an enabled workflow. Without one the request simply stays submitted.
func (s *Service) startWorkflow(ctx context.Context, request *Node) (*Node, error) {
	requestType, _ := request.Properties["requestType"].(string)
	workflows, err := s.store.ListWorkflows(ctx, false)
	if err != nil {
		return nil, err
	}
	for _, workflow := range workflows {
		if workflow.Enabled && string(workflow.RequestType) == requestType && len(workflow.Steps) > 0 {
			if err := s.store.StartWorkflowRun(ctx, request.ID, workflow.ID); err != nil {
				return nil, err
			}
			return s.store.GetNode(ctx, Request, request.ID, false)
		}
	}
	return request, nil
}

func (s *Service) GetTask(ctx context.Context, id string) (*TaskView, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	view, err := s.store.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.withAgreementClocks(ctx, view)
}

// ListTasks returns one page (or, with a zero Limit, all) of the tasks the
// filter matches along with how many matched in total.
func (s *Service) ListTasks(ctx context.Context, filter TaskFilter) ([]TaskView, int, error) {
	filter.ActorID = strings.TrimSpace(filter.ActorID)
	filter.RequestID = strings.TrimSpace(filter.RequestID)
	if filter.Limit < 0 || filter.Offset < 0 {
		return nil, 0, fmt.Errorf("%w: limit and offset cannot be negative", ErrInvalid)
	}
	tasks, total, err := s.store.ListTasks(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	holidays, err := s.store.ListHolidays(ctx)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now().UTC()
	for index := range tasks {
		attachAgreementClocks(&tasks[index], now, holidays)
	}
	return tasks, total, nil
}

func (s *Service) withAgreementClocks(ctx context.Context, view *TaskView) (*TaskView, error) {
	holidays, err := s.store.ListHolidays(ctx)
	if err != nil {
		return nil, err
	}
	attachAgreementClocks(view, time.Now().UTC(), holidays)
	return view, nil
}

func attachAgreementClocks(view *TaskView, now time.Time, holidays []Holiday) {
	view.SLA = slaClockForRequest(view.Request, now, holidays)
	view.OLA = olaClockForTask(*view, now, holidays)
}

func (s *Service) ListHolidays(ctx context.Context) ([]Holiday, error) {
	return s.store.ListHolidays(ctx)
}

func (s *Service) SaveHolidays(ctx context.Context, holidays []Holiday) ([]Holiday, error) {
	clean, err := normalizeHolidays(holidays)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveHolidays(ctx, clean); err != nil {
		return nil, err
	}
	return clean, nil
}

// ActOnTask applies an assignee's action to a pending task. The actor must be
// eligible (assigned directly or through a group). Field updates are limited
// to what the step exposes; completing or approving requires the step's
// required fields, and the final step additionally needs everything the CI
// requires so the request can be fulfilled in the same write.
func (s *Service) ActOnTask(ctx context.Context, taskID string, input TaskActionInput) (*TaskView, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	actorID := strings.TrimSpace(input.ActorID)
	if actorID == "" {
		return nil, fmt.Errorf("%w: actorId is required: say who is acting on the task", ErrInvalid)
	}
	action, err := ParseTaskAction(string(input.Action))
	if err != nil {
		return nil, err
	}
	view, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if view.Status != TaskPending {
		return nil, fmt.Errorf("%w: task %s is already %s", ErrInvalid, taskID, view.Status)
	}
	actor, eligible := findAssignee(view.EligibleActors, actorID)
	if !eligible {
		return nil, fmt.Errorf("%w: %s is not assigned to this step; it is assigned to %s", ErrInvalid, actorID, describeAssignees(view.Assignees))
	}
	comment := strings.TrimSpace(input.Comment)
	outcome := TaskOutcome{Comment: comment}
	if view.Request.Properties["requestType"] == string(AccessRequest) {
		return s.actOnAccessTask(ctx, view, actor, action, comment, input.Properties)
	}
	switch action {
	case RejectTask:
		if comment == "" {
			return nil, fmt.Errorf("%w: a comment is required when returning a request to its requester", ErrInvalid)
		}
		if len(input.Properties) > 0 {
			return nil, fmt.Errorf("%w: field changes cannot be saved while returning a request; complete or approve the step to keep them", ErrInvalid)
		}
		outcome.Action, outcome.Return = TaskRejected, true
		return s.completeTaskAction(ctx, taskID, actor.ID, outcome)
	case CompleteTask:
		if view.Step.StepType != ReviewStep {
			return nil, fmt.Errorf("%w: %s is an approval step; approve or reject it", ErrInvalid, view.Step.Name)
		}
		outcome.Action, outcome.Advance = TaskCompleted, true
	case ApproveTask:
		if view.Step.StepType != ApprovalStep {
			return nil, fmt.Errorf("%w: %s is a review step; complete it instead", ErrInvalid, view.Step.Name)
		}
		outcome.Action = TaskApproved
		outcome.Advance = approvalSatisfied(view, actor.ID)
	}
	// Field updates: only what the step exposes, validated like any request edit.
	updates, err := validateProperties(input.Properties)
	if err != nil {
		return nil, err
	}
	for key := range updates {
		if !containsString(view.Step.EditableFields, key) {
			return nil, fmt.Errorf("%w: %s cannot be changed at step %s; it exposes %s", ErrInvalid, key, view.Step.Name, describeFields(view.Step.EditableFields))
		}
	}
	if err := validateRequestProperties(updates, false); err != nil {
		return nil, err
	}
	outcome.Properties = updates
	merged := mergeProperties(view.Request.Properties, updates)
	if outcome.Advance {
		missing := missingFields(merged, view.Step.RequiredFields)
		if len(missing) > 0 {
			return nil, fmt.Errorf("%w: complete %s before finishing step %s", ErrInvalid, strings.Join(missing, ", "), view.Step.Name)
		}
		if view.FinalStep {
			ciProperties, err := ciPropertiesFromRequest(merged)
			if err != nil {
				return nil, err
			}
			outcome.CIProperties = ciProperties
		}
	}
	return s.completeTaskAction(ctx, taskID, actor.ID, outcome)
}

func (s *Service) completeTaskAction(ctx context.Context, taskID, actorID string, outcome TaskOutcome) (*TaskView, error) {
	view, err := s.store.ActOnTask(ctx, taskID, actorID, outcome)
	if err != nil {
		return nil, err
	}
	return s.withAgreementClocks(ctx, view)
}

// approvalSatisfied reports whether this approval, added to those already
// recorded, meets the step's rule.
func approvalSatisfied(view *TaskView, actorID string) bool {
	if view.Step.ApprovalRule != AllApprovers {
		return true
	}
	approved := map[string]bool{actorID: true}
	for _, record := range view.Actions {
		if record.Action == TaskApproved {
			approved[record.ActorID] = true
		}
	}
	for _, eligible := range view.EligibleActors {
		if !approved[eligible.ID] {
			return false
		}
	}
	return true
}

// ciPropertiesFromRequest builds the CI a fulfilled request becomes and checks
// it would be accepted, so the final step cannot complete with a request the
// CI validation would reject.
func ciPropertiesFromRequest(request map[string]any) (map[string]any, error) {
	requestType, _ := request["requestType"].(string)
	ciType, ok := RequestCIType(RequestType(requestType))
	if !ok {
		return nil, fmt.Errorf("%w: %s requests do not create a CI", ErrInvalid, requestType)
	}
	properties := map[string]any{"ciType": string(ciType)}
	for _, key := range RequestFieldKeys(RequestType(requestType)) {
		if value, present := request[key]; present {
			if text, isText := value.(string); !isText || strings.TrimSpace(text) != "" {
				properties[key] = value
			}
		}
	}
	if err := validateCIProperties(CI, properties, true); err != nil {
		return nil, fmt.Errorf("the request cannot be fulfilled as a %s CI yet: %w", ciType, err)
	}
	return properties, nil
}

func mergeProperties(base, updates map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(updates))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range updates {
		merged[key] = value
	}
	return merged
}

func missingFields(properties map[string]any, required []string) []string {
	missing := make([]string, 0)
	for _, key := range required {
		value, present := properties[key]
		if !present {
			missing = append(missing, key)
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func findAssignee(list []Assignee, id string) (Assignee, bool) {
	for _, item := range list {
		if item.ID == id {
			return item, true
		}
	}
	return Assignee{}, false
}

func describeAssignees(list []Assignee) string {
	if len(list) == 0 {
		return "nobody"
	}
	names := make([]string, 0, len(list))
	for _, item := range list {
		if item.Name != "" {
			names = append(names, fmt.Sprintf("%s (%s)", item.Name, item.ID))
		} else {
			names = append(names, item.ID)
		}
	}
	return strings.Join(names, ", ")
}

func describeFields(fields []string) string {
	if len(fields) == 0 {
		return "no fields"
	}
	return strings.Join(fields, ", ")
}
