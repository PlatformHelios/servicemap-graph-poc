package cmdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Workflow Analyzer. For one catalog item (published, or a draft manifest a
// team wants checked before publishing) it lays out the whole path a request
// takes as a graph: who can raise it, the form, each approval and who can
// actually approve, the platform's hand-off, the team's Temporal workflow and
// the activities it runs, and how requests end. It joins in what the platform
// and Temporal know (request history, worker health, recent runs) and reports
// findings: problems that would stop or slow requests, and policy concerns.

// AutomationInsight is what the analyzer reads from the automation engine.
type AutomationInsight interface {
	PortalTaskQueue() string
	UIURL() string // the Temporal UI, for links to runs; empty when unknown
	TaskQueueWorkers(context.Context, string) WorkerStatus
	RecentRuns(context.Context, string) RunSummary
}

// WorkerStatus is who is polling a task queue right now.
type WorkerStatus struct {
	TaskQueue       string   `json:"taskQueue"`
	WorkflowPollers int      `json:"workflowPollers"`
	ActivityPollers int      `json:"activityPollers"`
	Workers         []string `json:"workers"`
	LastSeen        string   `json:"lastSeen,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// RunSummary is how a workflow type's recent runs went.
type RunSummary struct {
	WorkflowType   string         `json:"workflowType"`
	Total          int            `json:"total"`
	StatusCounts   map[string]int `json:"statusCounts"`
	AverageSeconds float64        `json:"averageSeconds,omitempty"` // completed runs
	Activities     []ActivityStat `json:"activities"`
	Recent         []RunRecord    `json:"recent"`
	Error          string         `json:"error,omitempty"`
}

// ActivityStat is one activity across the sampled runs.
type ActivityStat struct {
	Name           string  `json:"name"`
	Position       int     `json:"position"` // first appearance in a run, for ordering
	Scheduled      int     `json:"scheduled"`
	Completed      int     `json:"completed"`
	Failed         int     `json:"failed"`
	AverageSeconds float64 `json:"averageSeconds,omitempty"`
	LastError      string  `json:"lastError,omitempty"`
}

type RunRecord struct {
	WorkflowID string  `json:"workflowId"`
	RunID      string  `json:"runId"`
	Status     string  `json:"status"`
	StartedAt  string  `json:"startedAt"`
	ClosedAt   string  `json:"closedAt,omitempty"`
	Seconds    float64 `json:"seconds,omitempty"`
}

// CatalogRequestSummary is one request of an item, as the analyzer needs it.
type CatalogRequestSummary struct {
	ID            string
	State         RequestState
	Version       string
	SubmittedAt   string
	ApprovedAt    string
	FinishedAt    string // fulfilled, failed, or denied
	FailureReason string
}

// RequestStats is how an item's requests have gone.
type RequestStats struct {
	Total                  int            `json:"total"`
	ByState                map[string]int `json:"byState"`
	ByVersion              map[string]int `json:"byVersion"`
	AverageApprovalHours   *float64       `json:"averageApprovalHours,omitempty"`
	AverageFulfilmentHours *float64       `json:"averageFulfilmentHours,omitempty"`
	RecentFailures         []string       `json:"recentFailures"`
}

// AnalysisNode is one box of the flow diagram.
type AnalysisNode struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"` // requesters, form, cmdb, approval, handoff, workflow, activity, fulfilled, failed, denied
	Label  string   `json:"label"`
	Detail string   `json:"detail,omitempty"`
	Lines  []string `json:"lines"`
	Health string   `json:"health"` // ok, warning, error, unknown
	Count  *int     `json:"count,omitempty"`
}

// AnalysisEdge links two boxes; Count is how many requests went that way.
type AnalysisEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Count *int   `json:"count,omitempty"`
}

// AnalysisFinding is something the analyzer wants a person to look at.
type AnalysisFinding struct {
	Severity string `json:"severity"` // error, warning, info
	Code     string `json:"code"`
	Message  string `json:"message"`
	NodeID   string `json:"nodeId,omitempty"`
}

// ApproverView is one approval step with who can actually act on it.
type ApproverView struct {
	Step      string     `json:"step"`
	Rule      string     `json:"rule"`
	Assignees []Assignee `json:"assignees"`
	Eligible  []Assignee `json:"eligible"` // identities, directly or through a group
}

type CatalogAnalysis struct {
	Item               CatalogItemView   `json:"item"`
	Draft              bool              `json:"draft"`
	GeneratedAt        string            `json:"generatedAt"`
	TemporalConfigured bool              `json:"temporalConfigured"`
	TemporalUIURL      string            `json:"temporalUiUrl,omitempty"` // with namespace, e.g. http://localhost:8233/namespaces/default
	Nodes              []AnalysisNode    `json:"nodes"`
	Edges              []AnalysisEdge    `json:"edges"`
	Findings           []AnalysisFinding `json:"findings"`
	Approvers          []ApproverView    `json:"approvers"`
	Requests           RequestStats      `json:"requests"`
	Workers            []WorkerStatus    `json:"workers"`
	Runs               *RunSummary       `json:"runs,omitempty"`
}

// CatalogAnalysisSummary is one row of the analyzer's overview.
type CatalogAnalysisSummary struct {
	Item     CatalogItemView `json:"item"`
	Errors   int             `json:"errors"`
	Warnings int             `json:"warnings"`
	Infos    int             `json:"infos"`
	Requests int             `json:"requests"`
	Health   string          `json:"health"`
}

// WithInsight lets the analyzer read worker health and run history.
func (s *Service) WithInsight(insight AutomationInsight) *Service {
	s.insight = insight
	return s
}

// AnalyzeCatalogItem analyzes a published item.
func (s *Service) AnalyzeCatalogItem(ctx context.Context, id string) (*CatalogAnalysis, error) {
	item, err := s.GetCatalogItem(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.analyze(ctx, *item, false, nil)
}

// AnalyzeCatalogItems summarizes every item the actor may see.
func (s *Service) AnalyzeCatalogItems(ctx context.Context, actorID string) ([]CatalogAnalysisSummary, error) {
	items, err := s.ListCatalogItems(ctx, actorID)
	if err != nil {
		return nil, err
	}
	summaries := make([]CatalogAnalysisSummary, 0, len(items))
	for _, item := range items {
		analysis, err := s.analyze(ctx, item, false, nil)
		if err != nil {
			return nil, err
		}
		summary := CatalogAnalysisSummary{Item: item, Requests: analysis.Requests.Total, Health: "ok"}
		for _, finding := range analysis.Findings {
			switch finding.Severity {
			case "error":
				summary.Errors++
			case "warning":
				summary.Warnings++
			default:
				summary.Infos++
			}
		}
		if summary.Errors > 0 {
			summary.Health = "error"
		} else if summary.Warnings > 0 {
			summary.Health = "warning"
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// AnalyzeManifest checks a manifest the way publishing would, without storing
// anything, so a team can see the result before its CI publishes. What would
// make publishing fail is reported as error findings rather than refused.
func (s *Service) AnalyzeManifest(ctx context.Context, publisherID string, manifest CatalogManifest) (*CatalogAnalysis, error) {
	clean, err := normalizeCatalogManifest(manifest)
	if err != nil {
		return nil, err
	}
	findings := []AnalysisFinding{}
	block := func(code, message string) {
		findings = append(findings, AnalysisFinding{Severity: "error", Code: code, Message: message})
	}
	item := CatalogItemView{Name: clean.Name, Version: clean.Version, Title: clean.Title, Description: clean.Description, Visibility: []Assignee{}, SLABusinessDays: clean.SLABusinessDays, Inputs: clean.Inputs, Outputs: clean.Outputs, Target: clean.Target, Source: clean.Source, PublishedBy: publisherID, Status: "draft"}
	if owner, err := s.resolveOne(ctx, clean.Owner, "owner", true); err != nil {
		block("unresolved-owner", unwrapInvalid(err))
		item.Owner = Assignee{ID: clean.Owner, Name: clean.Owner, Kind: Group}
	} else {
		item.Owner = owner
		if err := s.requirePublisher(ctx, publisherID, owner); err != nil {
			block("publisher-not-owner", unwrapInvalid(err))
		}
	}
	for _, reference := range clean.Visibility {
		group, err := s.resolveOne(ctx, reference, "visibility", true)
		if err != nil {
			block("unresolved-visibility", unwrapInvalid(err))
			continue
		}
		if group.ID != item.Owner.ID && !containsAssignee(item.Visibility, group.ID) {
			item.Visibility = append(item.Visibility, group)
		}
	}
	if len(item.Visibility) > 0 && len(clean.Approvals) == 0 {
		block("approval-required", fmt.Sprintf("%s is visible outside %s, so it needs at least one approval before the workflow runs.", clean.Name, item.Owner.Name))
	}
	for index, approval := range clean.Approvals {
		resolved := []string{}
		for _, reference := range approval.Assignees {
			assignee, err := s.resolveOne(ctx, reference, fmt.Sprintf("approval %d (%s)", index+1, approval.Name), false)
			if err != nil {
				block("unresolved-approver", unwrapInvalid(err))
				continue
			}
			resolved = append(resolved, assignee.ID)
		}
		clean.Approvals[index].Assignees = resolved
	}
	item.Approvals = clean.Approvals
	if current, err := s.store.GetCatalogItemByName(ctx, clean.Name); err == nil {
		item.ID = current.ID
		switch compareVersions(clean.Version, current.Version) {
		case -1:
			block("version-behind", fmt.Sprintf("%s is already at version %s; publishing %s would be refused.", clean.Name, current.Version, clean.Version))
		case 0:
			stored, err := s.store.CatalogManifest(ctx, current.ID)
			if err != nil {
				return nil, err
			}
			publishable := clean
			publishable.Owner, publishable.Visibility = item.Owner.ID, assigneeIDs(item.Visibility)
			if same, _ := sameManifestContent(stored, publishable); same {
				findings = append(findings, AnalysisFinding{Severity: "info", Code: "unchanged", Message: fmt.Sprintf("%s %s is already published with this content; publishing it again changes nothing.", clean.Name, clean.Version)})
			} else {
				block("version-not-bumped", fmt.Sprintf("%s %s is already published with different content; bump the version to publish this.", clean.Name, clean.Version))
			}
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.analyze(ctx, item, true, findings)
}

func unwrapInvalid(err error) string {
	return strings.TrimPrefix(strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "), ErrForbidden.Error()+": ")
}

func (s *Service) analyze(ctx context.Context, item CatalogItemView, draft bool, findings []AnalysisFinding) (*CatalogAnalysis, error) {
	analysis := &CatalogAnalysis{Item: item, Draft: draft, GeneratedAt: time.Now().UTC().Format(time.RFC3339), TemporalConfigured: s.insight != nil, Findings: findings, Workers: []WorkerStatus{}, Approvers: []ApproverView{}}
	if s.insight != nil {
		analysis.TemporalUIURL = s.insight.UIURL()
	}
	if analysis.Findings == nil {
		analysis.Findings = []AnalysisFinding{}
	}
	add := func(severity, code, nodeID, message string, args ...any) {
		analysis.Findings = append(analysis.Findings, AnalysisFinding{Severity: severity, Code: code, NodeID: nodeID, Message: fmt.Sprintf(message, args...)})
	}

	// Who is in each group the item names.
	groupIDs := []string{item.Owner.ID}
	for _, group := range item.Visibility {
		groupIDs = append(groupIDs, group.ID)
	}
	approverRefs := map[string]Assignee{}
	for _, approval := range item.Approvals {
		for _, id := range approval.Assignees {
			if _, seen := approverRefs[id]; seen {
				continue
			}
			matches, err := s.store.ResolveAssignees(ctx, id)
			if err != nil {
				return nil, err
			}
			if len(matches) == 0 {
				approverRefs[id] = Assignee{ID: id, Name: id}
				continue
			}
			approverRefs[id] = matches[0]
			if matches[0].Kind == Group {
				groupIDs = append(groupIDs, id)
			}
		}
	}
	members, err := s.store.GroupMembers(ctx, uniqueTrimmed(groupIDs))
	if err != nil {
		return nil, err
	}

	// Requests so far.
	requests := []CatalogRequestSummary{}
	if item.Name != "" {
		if requests, err = s.store.CatalogRequestSummaries(ctx, item.Name); err != nil {
			return nil, err
		}
	}
	analysis.Requests = summarizeRequests(requests)
	stateCount := func(state RequestState) *int {
		count := analysis.Requests.ByState[string(state)]
		return &count
	}
	total := analysis.Requests.Total

	// Who can raise it.
	requesters := map[string]Assignee{}
	requesterLines := []string{}
	for _, group := range append([]Assignee{item.Owner}, item.Visibility...) {
		groupMembers := members[group.ID]
		for _, member := range groupMembers {
			requesters[member.ID] = member
		}
		role := "visible to"
		if group.ID == item.Owner.ID {
			role = "owner"
		}
		requesterLines = append(requesterLines, fmt.Sprintf("%s (%s) · %d %s", displayName(group), role, len(groupMembers), plural(len(groupMembers), "member", "members")))
		if len(groupMembers) == 0 {
			add("warning", "empty-group", "requesters", "%s has no active members, so nobody can raise %s through it.", displayName(group), item.Title)
		}
	}
	requestersHealth := "ok"
	if len(requesters) == 0 {
		requestersHealth = "error"
		add("error", "no-requesters", "requesters", "Nobody can raise %s: the owner and visibility groups have no active members.", item.Title)
	}
	analysis.Nodes = append(analysis.Nodes, AnalysisNode{ID: "requesters", Kind: "requesters", Label: "Who can request", Detail: fmt.Sprintf("%d %s", len(requesters), plural(len(requesters), "identity", "identities")), Lines: requesterLines, Health: requestersHealth})

	// The form, and the CMDB records its pickers draw on.
	formLines, formHealth := []string{}, "ok"
	properties, _ := item.Inputs["properties"].(map[string]any)
	required := map[string]bool{}
	if list, ok := item.Inputs["required"].([]any); ok {
		for _, key := range list {
			required[fmt.Sprint(key)] = true
		}
	}
	for _, key := range fieldOrder(item.Inputs) {
		field, _ := properties[key].(map[string]any)
		label := stringOf(field["title"])
		if label == "" {
			label = key
		}
		kind := map[string]string{"string": "text", "integer": "whole number", "number": "number", "boolean": "yes/no"}[stringOf(field["type"])]
		if enum, ok := field["enum"].([]any); ok {
			kind = fmt.Sprintf("one of %d", len(enum))
		}
		suffix := ""
		if required[key] {
			suffix = " · required"
		}
		source, picker := field[PortalSourceKeyword]
		if !picker {
			formLines = append(formLines, fmt.Sprintf("%s · %s%s", label, kind, suffix))
			continue
		}
		ciType, err := portalSourceCIType(source)
		if err != nil {
			continue
		}
		options, err := s.CatalogFieldOptions(ctx, &item, key)
		if err != nil {
			return nil, err
		}
		formLines = append(formLines, fmt.Sprintf("%s · CMDB %s picker%s", label, ciType, suffix))
		nodeID := "cmdb-" + string(ciType)
		if !hasNode(analysis.Nodes, nodeID) {
			count := len(options)
			health := "ok"
			if count == 0 {
				health = "warning"
				if required[key] {
					health = "error"
				}
			}
			analysis.Nodes = append(analysis.Nodes, AnalysisNode{ID: nodeID, Kind: "cmdb", Label: "CMDB: " + string(ciType) + " CIs", Detail: fmt.Sprintf("%d active %s", count, plural(count, "record", "records")), Lines: optionNames(options), Health: health, Count: &count})
			analysis.Edges = append(analysis.Edges, AnalysisEdge{From: "form", To: nodeID, Label: "references"})
		}
		if len(options) == 0 {
			severity := "warning"
			if required[key] {
				severity, formHealth = "error", "error"
			}
			add(severity, "empty-picker", nodeID, "%s picks a %s CI, but there are no active %s CIs in the CMDB%s.", label, ciType, ciType, map[bool]string{true: ", so nobody can complete the form", false: ""}[required[key]])
		}
	}
	analysis.Nodes = append(analysis.Nodes, AnalysisNode{ID: "form", Kind: "form", Label: "Request form", Detail: fmt.Sprintf("%d %s, %d required", len(formLines), plural(len(formLines), "field", "fields"), len(required)), Lines: formLines, Health: formHealth, Count: &total})
	analysis.Edges = append(analysis.Edges, AnalysisEdge{From: "requesters", To: "form", Label: "submit", Count: &total})

	// Approvals, with who can actually act on each.
	previous := "form"
	for index, approval := range item.Approvals {
		nodeID := fmt.Sprintf("approval-%d", index+1)
		view := ApproverView{Step: approval.Name, Rule: string(approval.Rule), Assignees: []Assignee{}, Eligible: []Assignee{}}
		lines := []string{}
		for _, id := range approval.Assignees {
			assignee := approverRefs[id]
			view.Assignees = append(view.Assignees, assignee)
			if assignee.Kind == Group {
				lines = append(lines, fmt.Sprintf("%s (group) · %d %s", displayName(assignee), len(members[id]), plural(len(members[id]), "member", "members")))
				for _, member := range members[id] {
					if !containsAssignee(view.Eligible, member.ID) {
						view.Eligible = append(view.Eligible, member)
					}
				}
			} else {
				lines = append(lines, displayName(assignee))
				if assignee.Kind == Identity && !containsAssignee(view.Eligible, assignee.ID) {
					view.Eligible = append(view.Eligible, assignee)
				}
			}
		}
		rule := "any one approver"
		if approval.Rule == AllApprovers {
			rule = "every eligible approver"
		}
		lines = append(lines, "Rule: "+rule)
		health := "ok"
		if len(view.Eligible) == 0 {
			health = "error"
			add("error", "no-approvers", nodeID, "%s has nobody who can approve it, so every request would wait there indefinitely.", approval.Name)
		} else if approval.Rule == AllApprovers && len(view.Eligible) > 5 {
			health = "warning"
			add("warning", "large-all-approval", nodeID, "%s needs all %d eligible approvers to approve; any one of them being away stalls every request.", approval.Name, len(view.Eligible))
		}
		selfApprovers := []string{}
		for _, eligible := range view.Eligible {
			if _, canRequest := requesters[eligible.ID]; canRequest {
				selfApprovers = append(selfApprovers, displayName(eligible))
			}
		}
		if len(selfApprovers) > 0 {
			if health == "ok" {
				health = "warning"
			}
			add("warning", "self-approval", nodeID, "%s can be approved by people who can also raise the request (%s), so they could approve their own.", approval.Name, strings.Join(selfApprovers, ", "))
		}
		if item.PublishedBy != "" && containsAssignee(view.Eligible, item.PublishedBy) {
			if health == "ok" {
				health = "warning"
			}
			add("warning", "publisher-approves", nodeID, "The identity that publishes this item (%s) can also approve %s; keep pipeline accounts out of approver groups.", nameOf(view.Eligible, item.PublishedBy), approval.Name)
		}
		analysis.Approvers = append(analysis.Approvers, view)
		analysis.Nodes = append(analysis.Nodes, AnalysisNode{ID: nodeID, Kind: "approval", Label: approval.Name, Detail: fmt.Sprintf("Approval %d · %d can approve", index+1, len(view.Eligible)), Lines: lines, Health: health})
		label := "approved"
		if index == 0 {
			label = "review"
		}
		analysis.Edges = append(analysis.Edges, AnalysisEdge{From: previous, To: nodeID, Label: label})
		previous = nodeID
	}
	if len(item.Approvals) > 0 {
		analysis.Nodes = append(analysis.Nodes, AnalysisNode{ID: "denied", Kind: "denied", Label: "Denied", Detail: "An approver said no", Lines: []string{}, Health: "ok", Count: stateCount(RequestDenied)})
		for index := range item.Approvals {
			analysis.Edges = append(analysis.Edges, AnalysisEdge{From: fmt.Sprintf("approval-%d", index+1), To: "denied", Label: "denied"})
		}
	}

	// The platform's hand-off and the team's workflow.
	portalQueue := "servicemap-portal"
	if s.insight != nil {
		portalQueue = s.insight.PortalTaskQueue()
	}
	handedOff := total - analysis.Requests.ByState[string(RequestInReview)] - analysis.Requests.ByState[string(RequestDenied)]
	handoffHealth, workflowHealth := "unknown", "unknown"
	handoffLines := []string{"CatalogFulfilment on " + portalQueue, "Starts the team workflow as a child, then records the outcome"}
	workflowLines := []string{"Task queue " + item.Target.TaskQueue, "Owned by " + displayName(item.Owner)}
	if s.insight == nil {
		add("warning", "no-temporal", "handoff", "The platform has no Temporal connection (TEMPORAL_ADDRESS), so approved requests are marked failed instead of being fulfilled.")
	} else {
		portal := s.insight.TaskQueueWorkers(ctx, portalQueue)
		team := s.insight.TaskQueueWorkers(ctx, item.Target.TaskQueue)
		analysis.Workers = append(analysis.Workers, portal, team)
		handoffHealth, handoffLines = workerHealth(portal, handoffLines)
		workflowHealth, workflowLines = workerHealth(team, workflowLines)
		if portal.Error == "" && portal.WorkflowPollers == 0 {
			add("error", "no-portal-worker", "handoff", "No platform worker is polling %s, so approved requests will not start their fulfilment.", portalQueue)
		}
		if team.Error == "" && (team.WorkflowPollers == 0 || team.ActivityPollers == 0) {
			add("error", "no-team-worker", "workflow", "No worker is polling the team's task queue %s right now; approved requests will wait in progress until %s starts one.", item.Target.TaskQueue, displayName(item.Owner))
		}
		if team.Error != "" {
			add("warning", "temporal-unreachable", "workflow", "Temporal could not be asked about task queue %s: %s", item.Target.TaskQueue, team.Error)
		}
		runs := s.insight.RecentRuns(ctx, item.Target.WorkflowType)
		analysis.Runs = &runs
		if runs.Error == "" {
			finished := runs.StatusCounts["completed"] + runs.StatusCounts["failed"] + runs.StatusCounts["timed-out"] + runs.StatusCounts["terminated"]
			failed := finished - runs.StatusCounts["completed"]
			workflowLines = append(workflowLines, fmt.Sprintf("%d recent %s · %d completed · %d failed", runs.Total, plural(runs.Total, "run", "runs"), runs.StatusCounts["completed"], failed))
			if runs.AverageSeconds > 0 {
				workflowLines = append(workflowLines, "Typical run "+formatSeconds(runs.AverageSeconds))
			}
			if runs.Total == 0 {
				add("info", "never-run", "workflow", "%s has not run on Temporal yet, so its activities are not known; they appear here after the first run.", item.Target.WorkflowType)
			} else if finished >= 3 && float64(failed)/float64(finished) >= 0.2 {
				if workflowHealth == "ok" {
					workflowHealth = "warning"
				}
				add("warning", "failure-rate", "workflow", "%d of the last %d finished runs of %s failed.", failed, finished, item.Target.WorkflowType)
			}
			previousActivity := "workflow"
			for index, activity := range runs.Activities {
				nodeID := fmt.Sprintf("activity-%d", index+1)
				lines := []string{fmt.Sprintf("Ran %d times in sampled runs · %d failed", activity.Scheduled, activity.Failed)}
				if activity.AverageSeconds > 0 {
					lines = append(lines, "Typically "+formatSeconds(activity.AverageSeconds))
				}
				if activity.LastError != "" {
					lines = append(lines, "Last error: "+activity.LastError)
				}
				health := "ok"
				if activity.Failed > 0 {
					health = "warning"
				}
				analysis.Nodes = append(analysis.Nodes, AnalysisNode{ID: nodeID, Kind: "activity", Label: activity.Name, Detail: "Activity", Lines: lines, Health: health})
				label := "runs"
				if index > 0 {
					label = "then"
				}
				analysis.Edges = append(analysis.Edges, AnalysisEdge{From: previousActivity, To: nodeID, Label: label})
				previousActivity = nodeID
			}
		}
	}
	analysis.Nodes = append(analysis.Nodes,
		AnalysisNode{ID: "handoff", Kind: "handoff", Label: "Platform fulfilment", Detail: "Temporal workflow", Lines: handoffLines, Health: handoffHealth, Count: &handedOff},
		AnalysisNode{ID: "workflow", Kind: "workflow", Label: item.Target.WorkflowType, Detail: displayName(item.Owner) + "'s workflow", Lines: workflowLines, Health: workflowHealth},
	)
	handoffLabel := "approved"
	if len(item.Approvals) == 0 {
		handoffLabel = "no approval needed"
	}
	analysis.Edges = append(analysis.Edges,
		AnalysisEdge{From: previous, To: "handoff", Label: handoffLabel, Count: &handedOff},
		AnalysisEdge{From: "handoff", To: "workflow", Label: "child workflow"},
	)

	// How requests end.
	outputLines := fieldOrder(item.Outputs)
	if len(outputLines) == 0 {
		add("info", "no-outputs", "fulfilled", "The workflow declares no outputs, so requesters see nothing but \"fulfilled\" when it finishes.")
	}
	failedLines := analysis.Requests.RecentFailures
	analysis.Nodes = append(analysis.Nodes,
		AnalysisNode{ID: "fulfilled", Kind: "fulfilled", Label: "Fulfilled", Detail: "Outputs recorded on the request", Lines: outputLines, Health: "ok", Count: stateCount(RequestFulfilled)},
		AnalysisNode{ID: "failed", Kind: "failed", Label: "Failed", Detail: "Stays open for the owning team", Lines: failedLines, Health: "ok", Count: stateCount(RequestFailed)},
	)
	analysis.Edges = append(analysis.Edges,
		AnalysisEdge{From: "workflow", To: "fulfilled", Label: "completed", Count: stateCount(RequestFulfilled)},
		AnalysisEdge{From: "workflow", To: "failed", Label: "failed", Count: stateCount(RequestFailed)},
	)

	// Platform-level notes.
	if item.SLABusinessDays == 0 {
		add("info", "no-sla", "form", "%s has no SLA, so requests are not measured against a target.", item.Title)
	}
	if open := analysis.Requests.ByState[string(RequestFailed)]; open > 0 {
		add("warning", "failed-open", "failed", "%d failed %s still open for %s to look at.", open, plural(open, "request is", "requests are"), displayName(item.Owner))
	}
	for version, count := range analysis.Requests.ByVersion {
		if version != item.Version && count > 0 {
			add("info", "older-version-in-flight", "form", "%d open %s still on version %s; they finish on the approvals of that version.", count, plural(count, "request is", "requests are"), version)
		}
	}
	sort.SliceStable(analysis.Findings, func(i, j int) bool {
		return severityRank(analysis.Findings[i].Severity) < severityRank(analysis.Findings[j].Severity)
	})
	return analysis, nil
}

func summarizeRequests(requests []CatalogRequestSummary) RequestStats {
	stats := RequestStats{Total: len(requests), ByState: map[string]int{}, ByVersion: map[string]int{}, RecentFailures: []string{}}
	var approvalHours, fulfilmentHours []float64
	sort.Slice(requests, func(i, j int) bool { return requests[i].SubmittedAt > requests[j].SubmittedAt })
	for _, request := range requests {
		stats.ByState[string(request.State)]++
		if request.State == RequestInReview || request.State == RequestInProgress {
			stats.ByVersion[request.Version]++
		}
		if hours, ok := hoursBetween(request.SubmittedAt, request.ApprovedAt); ok {
			approvalHours = append(approvalHours, hours)
		}
		if request.State == RequestFulfilled {
			start := request.ApprovedAt
			if start == "" {
				start = request.SubmittedAt
			}
			if hours, ok := hoursBetween(start, request.FinishedAt); ok {
				fulfilmentHours = append(fulfilmentHours, hours)
			}
		}
		if request.State == RequestFailed && request.FailureReason != "" && len(stats.RecentFailures) < 3 && !containsString(stats.RecentFailures, request.FailureReason) {
			stats.RecentFailures = append(stats.RecentFailures, request.FailureReason)
		}
	}
	stats.AverageApprovalHours = average(approvalHours)
	stats.AverageFulfilmentHours = average(fulfilmentHours)
	return stats
}

func hoursBetween(start, end string) (float64, bool) {
	from, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return 0, false
	}
	to, err := time.Parse(time.RFC3339Nano, end)
	if err != nil {
		return 0, false
	}
	return to.Sub(from).Hours(), true
}

func average(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	result := sum / float64(len(values))
	return &result
}

func workerHealth(status WorkerStatus, lines []string) (string, []string) {
	switch {
	case status.Error != "":
		return "unknown", append(lines, "Worker status unknown")
	case status.WorkflowPollers == 0 || status.ActivityPollers == 0:
		return "error", append(lines, "No worker polling")
	default:
		return "ok", append(lines, fmt.Sprintf("%d %s polling", len(status.Workers), plural(len(status.Workers), "worker", "workers")))
	}
}

// fieldOrder lists a schema's fields in display order (x-order, then the rest).
func fieldOrder(schema map[string]any) []string {
	properties, _ := schema["properties"].(map[string]any)
	keys := []string{}
	if order, ok := schema["x-order"].([]any); ok {
		for _, key := range order {
			if _, present := properties[fmt.Sprint(key)]; present {
				keys = append(keys, fmt.Sprint(key))
			}
		}
	}
	rest := []string{}
	for key := range properties {
		if !containsString(keys, key) {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func optionNames(options []CatalogOption) []string {
	names := []string{}
	for index, option := range options {
		if index == 5 {
			names = append(names, fmt.Sprintf("and %d more", len(options)-5))
			break
		}
		names = append(names, option.Name)
	}
	return names
}

func hasNode(nodes []AnalysisNode, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func displayName(assignee Assignee) string {
	if assignee.Name != "" {
		return assignee.Name
	}
	return assignee.ID
}

func nameOf(list []Assignee, id string) string {
	if assignee, found := findAssignee(list, id); found {
		return displayName(assignee)
	}
	return id
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func formatSeconds(seconds float64) string {
	switch {
	case seconds < 90:
		return fmt.Sprintf("%.0fs", seconds)
	case seconds < 90*60:
		return fmt.Sprintf("%.0f min", seconds/60)
	default:
		return fmt.Sprintf("%.1f h", seconds/3600)
	}
}

func severityRank(severity string) int {
	switch severity {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}
