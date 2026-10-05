package cmdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Access requests. An identity normally gets roles and entitlements through
// its job code's birthrights; anything else is exception access and must be
// approved by the item's Accountable, then provisioned by the identity team.
// The Access Request Form only offers what the identity does not already hold
// and what has an Accountable to approve it. Submitting raises one approval
// task per item; each approval raises a fulfilment task for that item, and
// completing it records the permission as a permissions link from the role or
// entitlement to the identity.

// AccessOptions lists what an identity may request, grouped by application,
// alongside what it already holds (and why it is not offered).
func (s *Service) AccessOptions(ctx context.Context, identityID string) (*AccessOptions, error) {
	identityID = strings.TrimSpace(identityID)
	if err := validateID(identityID); err != nil {
		return nil, fmt.Errorf("identityId: %w", err)
	}
	return s.store.AccessOptions(ctx, identityID)
}

// ActorAccess resolves platform capabilities for the actor named by actorID.
// The platform super admin (or a blank id, treated the same for local CLI/tools)
// receives every capability. An identity receives capabilities from the
// entitlements it holds through birthrights, included roles, or direct permissions.
func (s *Service) ActorAccess(ctx context.Context, actorID string) (*ActorAccess, error) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" || actorID == PlatformSuperAdminID {
		return SuperAdminAccess(), nil
	}
	if err := validateID(actorID); err != nil {
		return nil, fmt.Errorf("actorId: %w", err)
	}
	if _, err := s.store.GetNode(ctx, Identity, actorID, false); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: actor %s is not an active identity", ErrInvalid, actorID)
		}
		return nil, err
	}
	options, err := s.store.AccessOptions(ctx, actorID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0)
	for _, holding := range options.Held {
		if holding.Kind != Entitlement || holding.Via == "pending" || holding.Name == "" {
			continue
		}
		names = append(names, holding.Name)
	}
	return AccessFromEntitlements(actorID, names), nil
}

// AccessAnomalies reads the last stored check for identities that hold the
// same role or entitlement through more than one path (birthright, included
// by a held role, and/or direct permissions). The check itself walks every
// identity, so it runs on the server's schedule (or RefreshAccessAnomalies).
func (s *Service) AccessAnomalies(ctx context.Context) (*AccessAnomalyReport, error) {
	report, err := s.store.AccessAnomalies(ctx)
	if err != nil {
		return nil, err
	}
	return normalizeAnomalyReport(report), nil
}

// RefreshAccessAnomalies recomputes the anomaly check from the live graph,
// stores it as the current snapshot, and returns it.
func (s *Service) RefreshAccessAnomalies(ctx context.Context) (*AccessAnomalyReport, error) {
	report, err := s.store.RefreshAccessAnomalies(ctx)
	if err != nil {
		return nil, err
	}
	return normalizeAnomalyReport(report), nil
}

func normalizeAnomalyReport(report *AccessAnomalyReport) *AccessAnomalyReport {
	if report == nil {
		return &AccessAnomalyReport{Anomalies: []AccessAnomaly{}}
	}
	if report.Anomalies == nil {
		report.Anomalies = []AccessAnomaly{}
	}
	for i := range report.Anomalies {
		if report.Anomalies[i].Birthrights == nil {
			report.Anomalies[i].Birthrights = []Assignee{}
		}
		if report.Anomalies[i].Roles == nil {
			report.Anomalies[i].Roles = []Assignee{}
		}
		if report.Anomalies[i].Paths == nil {
			report.Anomalies[i].Paths = []string{}
		}
	}
	return report
}

// CreateAccessRequest raises an access request. The requester, subject, and
// items are checked here (every item must be offered to the subject right
// now), then the request, its run, and its approval tasks are written together.
// The access workflow must be enabled so the fulfilment assignees are known.
func (s *Service) CreateAccessRequest(ctx context.Context, input AccessRequestInput) (*Node, error) {
	input.RequestedByID = strings.TrimSpace(input.RequestedByID)
	input.RequestForID = strings.TrimSpace(input.RequestForID)
	if input.RequestedByID == "" {
		return nil, fmt.Errorf("%w: requestedById is required: say who is raising the request", ErrInvalid)
	}
	if input.RequestForID == "" {
		return nil, fmt.Errorf("%w: requestForId is required: say who the access is for", ErrInvalid)
	}
	if len(input.Items) == 0 {
		return nil, fmt.Errorf("%w: choose at least one role or entitlement to request", ErrInvalid)
	}
	if _, err := s.store.GetNode(ctx, Identity, input.RequestedByID, false); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: requestedById %s is not an active identity", ErrInvalid, input.RequestedByID)
		}
		return nil, err
	}
	options, err := s.store.AccessOptions(ctx, input.RequestForID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: requestForId %s is not an active identity", ErrInvalid, input.RequestForID)
		}
		return nil, err
	}
	offered := make(map[string]AccessOption)
	for _, application := range options.Applications {
		for _, option := range append(append([]AccessOption{}, application.Roles...), application.Entitlements...) {
			offered[option.ID] = option
		}
	}
	seen := make(map[string]bool, len(input.Items))
	clean := make([]AccessRequestItemInput, 0, len(input.Items))
	for index, item := range input.Items {
		item.ID = strings.TrimSpace(item.ID)
		item.Note = strings.TrimSpace(item.Note)
		if item.ID == "" {
			return nil, fmt.Errorf("%w: item %d has no id", ErrInvalid, index+1)
		}
		kind, err := ParseItemKind(string(item.Kind))
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", item.ID, err)
		}
		item.Kind = kind
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		option, ok := offered[item.ID]
		if !ok || option.Kind != kind {
			return nil, fmt.Errorf("%w: %s %s cannot be requested for %s: %s", ErrInvalid, kind, item.ID, options.Identity.Name, explainUnavailable(options, item.ID))
		}
		clean = append(clean, item)
	}
	input.Items = clean
	workflow, err := s.enabledWorkflow(ctx, AccessRequest)
	if err != nil {
		return nil, err
	}
	if workflow == nil {
		return nil, fmt.Errorf("%w: the access request workflow is not enabled; open the Access Request Form in Workflow Creator, choose who fulfils access, and enable it", ErrInvalid)
	}
	slaProps := map[string]any{"slaEnabled": workflow.SLAEnabled, "slaBusinessDays": workflow.SLABusinessDays}
	if err := normalizeRequestSLA(slaProps); err != nil {
		return nil, err
	}
	input.SLAEnabled, _ = slaProps["slaEnabled"].(bool)
	input.SLABusinessDays, _ = slaProps["slaBusinessDays"].(int)
	holidays, err := s.store.ListHolidays(ctx)
	if err != nil {
		return nil, err
	}
	if input.SLAEnabled {
		input.SLADueAt = AddBusinessDays(time.Now().UTC(), input.SLABusinessDays, holidays).Format(holidayDateLayout)
	}
	return s.store.CreateAccessRequest(ctx, input, workflow.ID)
}

// FormSLA is the SLA stamped onto requests for a catalog form: the enabled
// workflow's setting, or none when that form has no enabled workflow SLA.
func (s *Service) FormSLA(ctx context.Context, requestType RequestType) (enabled bool, days int, err error) {
	workflow, err := s.enabledWorkflow(ctx, requestType)
	if err != nil || workflow == nil || !workflow.SLAEnabled {
		return false, 0, err
	}
	return true, workflow.SLABusinessDays, nil
}

// enabledWorkflow finds the workflow currently enabled for a form, if any.
func (s *Service) enabledWorkflow(ctx context.Context, requestType RequestType) (*WorkflowDefinition, error) {
	workflows, err := s.store.ListWorkflows(ctx, false)
	if err != nil {
		return nil, err
	}
	for index := range workflows {
		if workflows[index].Enabled && workflows[index].RequestType == requestType && len(workflows[index].Steps) > 0 {
			return &workflows[index], nil
		}
	}
	return nil, nil
}

// explainUnavailable says why an item is not on offer: already held, already
// asked for, or not requestable (no application or no Accountable).
func explainUnavailable(options *AccessOptions, itemID string) string {
	for _, holding := range options.Held {
		if holding.ID != itemID {
			continue
		}
		switch holding.Via {
		case "pending":
			return fmt.Sprintf("it is already on open request %s", holding.Source)
		case "birthright":
			return fmt.Sprintf("it is already granted by birthright %s", holding.Source)
		case "role":
			return fmt.Sprintf("it is already included by role %s", holding.Source)
		default:
			return "it is already permissioned directly"
		}
	}
	return "it is not provided by an active application or has no Accountable to approve it"
}

// actOnAccessTask applies an action to one task of an access request. Approval
// tasks are approved or denied per item (a denial needs a reason and does not
// return the request); fulfilment tasks are completed once the access has been
// provisioned, which records the permission.
func (s *Service) actOnAccessTask(ctx context.Context, view *TaskView, actor Assignee, action TaskAction, comment string, properties map[string]any) (*TaskView, error) {
	if view.Item == nil {
		return nil, fmt.Errorf("%w: task %s is not tied to a requested role or entitlement", ErrInvalid, view.ID)
	}
	if len(properties) > 0 {
		return nil, fmt.Errorf("%w: access request tasks have no fields to change", ErrInvalid)
	}
	outcome := TaskOutcome{Comment: comment, Item: view.Item.ID}
	switch {
	case view.Step.StepType == ApprovalStep && action == ApproveTask:
		outcome.Action, outcome.Approve = TaskApproved, true
	case view.Step.StepType == ApprovalStep && action == RejectTask:
		if comment == "" {
			return nil, fmt.Errorf("%w: a comment is required when denying access, so the requester knows why", ErrInvalid)
		}
		outcome.Action, outcome.Deny = TaskRejected, true
	case view.Step.StepType == ApprovalStep:
		return nil, fmt.Errorf("%w: %s decides %s %s: approve or deny it", ErrInvalid, view.Step.Name, view.Item.Kind, view.Item.Name)
	case action == CompleteTask:
		outcome.Action, outcome.Provision = TaskCompleted, true
	default:
		return nil, fmt.Errorf("%w: %s records that %s %s has been provisioned; complete it once that is done", ErrInvalid, view.Step.Name, view.Item.Kind, view.Item.Name)
	}
	return s.completeTaskAction(ctx, view.ID, actor.ID, outcome)
}
