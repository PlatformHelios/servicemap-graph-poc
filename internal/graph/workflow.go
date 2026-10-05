package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Workflow storage. Definitions (Workflow, WorkflowStep) and executions
// (WorkflowRun, Task) are ordinary graph nodes so they show up on the map and
// in direct relationships, but they are written through multi-statement
// transactions here rather than the generic node endpoints.

// writeTx runs several statements in one write transaction.
func (s *Store) writeTx(ctx context.Context, work func(tx neo4j.ManagedTransaction) error) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return nil, work(tx)
	})
	return err
}

func runQuery(ctx context.Context, tx neo4j.ManagedTransaction, query string, params map[string]any) ([]*neo4j.Record, error) {
	result, err := tx.Run(ctx, query, params)
	if err != nil {
		return nil, err
	}
	return result.Collect(ctx)
}

// nextID draws the next compact PREFIX-000001 identifier for a kind, using the
// highest numeric suffix already stored (ULiD leftovers are ignored). Run it
// on the current write transaction so several creates in one save see each other.
func nextID(ctx context.Context, tx neo4j.ManagedTransaction, kind cmdb.NodeKind) (string, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return "", err
	}
	prefix, err := cmdb.GeneratedIDPrefix(kind)
	if err != nil {
		return "", err
	}
	records, err := runQuery(ctx, tx, fmt.Sprintf("MATCH (node:%s) WHERE toString(node.%s) =~ $pattern RETURN coalesce(max(toInteger(substring(toString(node.%s), $digitStart))), 0) AS n", label, key, key), map[string]any{
		"pattern":    "^" + prefix + "-[0-9]+$",
		"digitStart": len(prefix) + 2,
	})
	if err != nil {
		return "", err
	}
	sequence := 0
	if len(records) > 0 {
		sequence = toInt(records[0].Values[0])
	}
	return cmdb.FormatGeneratedID(kind, sequence+1)
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// activeFilter is the predicate for a live record. Every record carries an
// explicit status (EnsureConstraints backfills older data), so this is a plain
// equality the per-label status index can serve. Live relationships are
// always active too — retired edges move under the kind's retired type — so
// applying it to an edge is harmless but never necessary.
const activeFilter = "%s.status = 'active'"

func active(alias string) string {
	return fmt.Sprintf(activeFilter, alias)
}

// SaveWorkflow creates or replaces a workflow definition. Steps whose ids are
// supplied are updated in place (so tasks already raised for them keep
// pointing at a live step); others are created; steps left out are retired.
// Assignments and the next-step chain are rebuilt from the definition.
func (s *Store) SaveWorkflow(ctx context.Context, id string, definition cmdb.WorkflowDefinition) (*cmdb.WorkflowDefinition, error) {
	timestamp := now()
	err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		if id == "" {
			generated, err := nextID(ctx, tx, cmdb.Workflow)
			if err != nil {
				return err
			}
			id = generated
			if _, err := runQuery(ctx, tx, "CREATE (workflow:Workflow {id: $id, status: 'active', createdAt: $now})", map[string]any{"id": id, "now": timestamp}); err != nil {
				return err
			}
		}
		records, err := runQuery(ctx, tx, "MATCH (workflow:Workflow {id: $id}) WHERE "+active("workflow")+" SET workflow.name = $name, workflow.description = $description, workflow.requestType = $requestType, workflow.enabled = $enabled, workflow.slaEnabled = $slaEnabled, workflow.slaBusinessDays = $slaDays, workflow.updatedAt = $now RETURN workflow.id", map[string]any{"id": id, "name": definition.Name, "description": definition.Description, "requestType": string(definition.RequestType), "enabled": definition.Enabled, "slaEnabled": definition.SLAEnabled, "slaDays": definition.SLABusinessDays, "now": timestamp})
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return cmdb.ErrNotFound
		}
		// Assignments and ordering are definitional, not audit, so they are rebuilt.
		if _, err := runQuery(ctx, tx, "MATCH (workflow:Workflow {id: $id})-[:HAS_STEP]->(step:WorkflowStep) OPTIONAL MATCH (step)-[link:NEXT_STEP|STEP_ASSIGNED_TO]->() DELETE link", map[string]any{"id": id}); err != nil {
			return err
		}
		kept := make([]string, 0, len(definition.Steps))
		for _, step := range definition.Steps {
			properties := map[string]any{"name": step.Name, "stepType": string(step.StepType), "instructions": step.Instructions, "order": step.Order, "approvalRule": string(step.ApprovalRule), "assigneeRule": string(step.AssigneeRule), "editableFields": toAnyList(step.EditableFields), "requiredFields": toAnyList(step.RequiredFields), "olaEnabled": step.OLAEnabled, "olaBusinessDays": step.OLABusinessDays}
			stepID := step.ID
			if stepID != "" {
				// A step kept by id stays live; one that had been removed earlier (its
				// has-step link sits under the retired type) is revived with a new live link.
				records, err := runQuery(ctx, tx, "MATCH (workflow:Workflow {id: $id})-[:"+liveOrRetired("HAS_STEP")+"]->(step:WorkflowStep {id: $stepId}) WITH DISTINCT workflow, step SET step += $properties, step.status = 'active' REMOVE step.retiredAt MERGE (workflow)-[has:HAS_STEP]->(step) ON CREATE SET has.status = 'active' RETURN step.id", map[string]any{"id": id, "stepId": stepID, "properties": properties})
				if err != nil {
					return err
				}
				if len(records) == 0 {
					return fmt.Errorf("%w: step %s does not belong to workflow %s", cmdb.ErrInvalid, stepID, id)
				}
			} else {
				generated, err := nextID(ctx, tx, cmdb.WorkflowStep)
				if err != nil {
					return err
				}
				stepID = generated
				if _, err := runQuery(ctx, tx, "MATCH (workflow:Workflow {id: $id}) CREATE (step:WorkflowStep {id: $stepId, status: 'active'}) SET step += $properties CREATE (workflow)-[:HAS_STEP {status: 'active'}]->(step)", map[string]any{"id": id, "stepId": stepID, "properties": properties}); err != nil {
					return err
				}
			}
			kept = append(kept, stepID)
			records, err := runQuery(ctx, tx, "MATCH (step:WorkflowStep {id: $stepId}) UNWIND $assigneeIds AS assigneeId MATCH (assignee {id: assigneeId}) WHERE (assignee:Identity OR assignee:Group) AND "+active("assignee")+" CREATE (step)-[:STEP_ASSIGNED_TO {status: 'active'}]->(assignee) RETURN count(assignee) AS linked", map[string]any{"stepId": stepID, "assigneeIds": toAnyList(step.AssigneeIDs)})
			if err != nil {
				return err
			}
			if len(records) == 0 || toInt(records[0].Values[0]) != len(step.AssigneeIDs) {
				return fmt.Errorf("%w: step %d (%s) must be assigned to active identities or groups", cmdb.ErrInvalid, step.Order, step.Name)
			}
		}
		// Steps left out of the definition retire, and their has-step link moves
		// under the retired type so the workflow's live step chain no longer sees them.
		removed, err := runQuery(ctx, tx, "MATCH (workflow:Workflow {id: $id})-[:HAS_STEP]->(step:WorkflowStep) WHERE NOT step.id IN $kept AND "+active("step")+" SET step.status = 'retired', step.retiredAt = $now RETURN step.id", map[string]any{"id": id, "kept": toAnyList(kept), "now": timestamp})
		if err != nil {
			return err
		}
		removedIDs := make([]string, 0, len(removed))
		for _, record := range removed {
			removedIDs = append(removedIDs, fmt.Sprint(record.Values[0]))
		}
		if len(removedIDs) > 0 {
			if _, err := moveToRetired(ctx, tx, "HAS_STEP", "MATCH (workflow:Workflow {id: $id})-[r:HAS_STEP]->(step:WorkflowStep) WHERE step.id IN $removed", map[string]any{"id": id, "removed": toAnyList(removedIDs), "now": timestamp}); err != nil {
				return err
			}
		}
		for index := 0; index+1 < len(kept); index++ {
			if _, err := runQuery(ctx, tx, "MATCH (current:WorkflowStep {id: $fromId}), (next:WorkflowStep {id: $toId}) CREATE (current)-[:NEXT_STEP {status: 'active'}]->(next)", map[string]any{"fromId": kept[index], "toId": kept[index+1]}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrInvalid) || errors.Is(err, cmdb.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("save workflow: %w", err)
	}
	return s.GetWorkflow(ctx, id)
}

func (s *Store) GetWorkflow(ctx context.Context, id string) (*cmdb.WorkflowDefinition, error) {
	workflows, err := s.loadWorkflows(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if len(workflows) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return &workflows[0], nil
}

func (s *Store) ListWorkflows(ctx context.Context, includeRetired bool) ([]cmdb.WorkflowDefinition, error) {
	return s.loadWorkflows(ctx, "", includeRetired)
}

func (s *Store) loadWorkflows(ctx context.Context, id string, includeRetired bool) ([]cmdb.WorkflowDefinition, error) {
	// A retired workflow still reads in full: the steps and assignments retired
	// with it (same retiredAt) are kept so it can be reviewed or copied; steps
	// removed by earlier edits stay out.
	query := "MATCH (workflow:Workflow) WHERE ($includeRetired OR " + active("workflow") + ") AND ($id = '' OR workflow.id = $id) " +
		"OPTIONAL MATCH (workflow)-[has:" + liveOrRetired("HAS_STEP") + "]->(step:WorkflowStep) WHERE (type(has) = 'HAS_STEP' AND " + active("step") + ") OR (workflow.status = 'retired' AND step.retiredAt = workflow.retiredAt) " +
		"OPTIONAL MATCH (step)-[assigned:" + liveOrRetired("STEP_ASSIGNED_TO") + "]->(assignee) WHERE type(assigned) = 'STEP_ASSIGNED_TO' OR (workflow.status = 'retired' AND assigned.retiredAt = workflow.retiredAt) " +
		"WITH workflow, step, collect(DISTINCT CASE WHEN assignee IS NULL THEN null ELSE {id: assignee.id, name: assignee.name, kind: CASE WHEN assignee:Group THEN 'group' ELSE 'identity' END} END) AS assignees " +
		"ORDER BY step.order " +
		"WITH workflow, collect(CASE WHEN step IS NULL THEN null ELSE {step: properties(step), assignees: assignees} END) AS steps " +
		"RETURN workflow, steps ORDER BY workflow.id"
	records, err := s.execute(ctx, false, query, map[string]any{"includeRetired": includeRetired, "id": id})
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	workflows := make([]cmdb.WorkflowDefinition, 0, len(records))
	for _, record := range records {
		node, ok := record.Values[0].(neo4j.Node)
		if !ok {
			return nil, fmt.Errorf("unexpected workflow result type %T", record.Values[0])
		}
		workflow := cmdb.WorkflowDefinition{ID: stringProperty(node.Props, "id"), Name: stringProperty(node.Props, "name"), Description: stringProperty(node.Props, "description"), RequestType: cmdb.RequestType(stringProperty(node.Props, "requestType")), Status: stringProperty(node.Props, "status"), Steps: []cmdb.WorkflowStepDefinition{}}
		if workflow.Status == "" {
			workflow.Status = "active"
		}
		workflow.Enabled, _ = node.Props["enabled"].(bool)
		workflow.SLAEnabled, _ = node.Props["slaEnabled"].(bool)
		workflow.SLABusinessDays = toInt(node.Props["slaBusinessDays"])
		steps, _ := record.Values[1].([]any)
		for _, item := range steps {
			entry, _ := item.(map[string]any)
			stepProperties, _ := entry["step"].(map[string]any)
			step := decodeStep(stepProperties)
			step.Assignees = decodeAssignees(entry["assignees"])
			for _, assignee := range step.Assignees {
				step.AssigneeIDs = append(step.AssigneeIDs, assignee.ID)
			}
			workflow.Steps = append(workflow.Steps, step)
		}
		sort.SliceStable(workflow.Steps, func(i, j int) bool { return workflow.Steps[i].Order < workflow.Steps[j].Order })
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

func decodeStep(properties map[string]any) cmdb.WorkflowStepDefinition {
	step := cmdb.WorkflowStepDefinition{
		ID:              stringProperty(properties, "id"),
		Order:           toInt(properties["order"]),
		Name:            stringProperty(properties, "name"),
		StepType:        cmdb.StepType(stringProperty(properties, "stepType")),
		Instructions:    stringProperty(properties, "instructions"),
		ApprovalRule:    cmdb.ApprovalRule(stringProperty(properties, "approvalRule")),
		AssigneeRule:    cmdb.AssigneeRule(stringProperty(properties, "assigneeRule")),
		EditableFields:  toStringList(properties["editableFields"]),
		RequiredFields:  toStringList(properties["requiredFields"]),
		AssigneeIDs:     []string{},
		OLAEnabled:      boolProperty(properties, "olaEnabled"),
		OLABusinessDays: toInt(properties["olaBusinessDays"]),
	}
	return step
}

func boolProperty(properties map[string]any, key string) bool {
	value, ok := properties[key]
	if !ok {
		return false
	}
	flag, _ := value.(bool)
	return flag
}

func decodeAssignees(value any) []cmdb.Assignee {
	items, _ := value.([]any)
	assignees := make([]cmdb.Assignee, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || stringProperty(entry, "id") == "" {
			continue
		}
		assignees = append(assignees, cmdb.Assignee{ID: stringProperty(entry, "id"), Name: stringProperty(entry, "name"), Kind: cmdb.NodeKind(stringProperty(entry, "kind"))})
	}
	return assignees
}

// RetireWorkflow retires a workflow, its steps, and the relationships attached to them.
func (s *Store) RetireWorkflow(ctx context.Context, id string) (*cmdb.WorkflowDefinition, error) {
	timestamp := now()
	err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		records, err := runQuery(ctx, tx, "MATCH (workflow:Workflow {id: $id}) OPTIONAL MATCH (workflow)-[:HAS_STEP]->(step:WorkflowStep) "+
			"WITH workflow, collect(DISTINCT step) AS steps "+
			"SET workflow.status = 'retired', workflow.retiredAt = coalesce(workflow.retiredAt, $now), workflow.enabled = false "+
			"FOREACH (step IN steps | SET step.status = 'retired', step.retiredAt = coalesce(step.retiredAt, $now)) "+
			"RETURN [step IN steps | step.id] AS stepIds", map[string]any{"id": id, "now": timestamp})
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return cmdb.ErrNotFound
		}
		if err := retireEdgesOf(ctx, tx, cmdb.Workflow, []string{id}, nil, timestamp); err != nil {
			return err
		}
		return retireEdgesOf(ctx, tx, cmdb.WorkflowStep, toStringList(records[0].Values[0]), nil, timestamp)
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("retire workflow: %w", err)
	}
	return s.GetWorkflow(ctx, id)
}

// StartWorkflowRun opens a run of a workflow for a submitted request and
// raises the task for its first step, moving the request into review.
func (s *Store) StartWorkflowRun(ctx context.Context, requestID, workflowID string) error {
	timestamp := now()
	err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		runID, err := nextID(ctx, tx, cmdb.WorkflowRun)
		if err != nil {
			return err
		}
		records, err := runQuery(ctx, tx, "MATCH (request:Request {id: $requestId}), (workflow:Workflow {id: $workflowId}) WHERE "+active("request")+" AND "+active("workflow")+" "+
			"MATCH (workflow)-[has:HAS_STEP]->(first:WorkflowStep) WHERE "+active("has")+" AND "+active("first")+" "+
			"WITH request, workflow, first ORDER BY first.order LIMIT 1 "+
			"CREATE (run:WorkflowRun {id: $runId, status: 'active', state: $state, name: workflow.name, startedAt: $now, currentOrder: first.order}) "+
			"CREATE (run)-[:RUN_FOR {status: 'active'}]->(request) CREATE (run)-[:INSTANCE_OF {status: 'active'}]->(workflow) "+
			"SET request.state = $inReview RETURN first.id AS stepId", map[string]any{"requestId": requestID, "workflowId": workflowID, "runId": runID, "state": string(cmdb.RunActive), "inReview": string(cmdb.RequestInReview), "now": timestamp})
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return fmt.Errorf("%w: workflow %s has no active steps to start for request %s", cmdb.ErrInvalid, workflowID, requestID)
		}
		return createTask(ctx, tx, runID, fmt.Sprint(records[0].Values[0]), timestamp)
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrInvalid) {
			return err
		}
		return fmt.Errorf("start workflow run: %w", err)
	}
	return nil
}

// createTask raises the task for one step of a run, assigned to whoever the
// step is assigned to at that moment.
func createTask(ctx context.Context, tx neo4j.ManagedTransaction, runID, stepID, timestamp string) error {
	taskID, err := nextID(ctx, tx, cmdb.Task)
	if err != nil {
		return err
	}
	records, err := runQuery(ctx, tx, "MATCH (run:WorkflowRun {id: $runId}), (step:WorkflowStep {id: $stepId}) "+
		"CREATE (task:Task {id: $taskId, status: 'active', state: $pending, name: step.name, createdAt: $now, olaEnabled: coalesce(step.olaEnabled, false), olaBusinessDays: toInteger(coalesce(step.olaBusinessDays, 0))}) "+
		"CREATE (task)-[:TASK_FOR {status: 'active'}]->(run) CREATE (task)-[:TASK_STEP {status: 'active'}]->(step) "+
		"SET run.currentOrder = step.order "+
		"WITH task, step MATCH (step)-[assigned:STEP_ASSIGNED_TO]->(assignee) WHERE "+active("assigned")+" AND "+active("assignee")+" "+
		"CREATE (task)-[:TASK_ASSIGNED_TO {status: 'active'}]->(assignee) RETURN count(assignee) AS assigned", map[string]any{"runId": runID, "stepId": stepID, "taskId": taskID, "pending": string(cmdb.TaskPending), "now": timestamp})
	if err != nil {
		return err
	}
	if len(records) == 0 || toInt(records[0].Values[0]) == 0 {
		return fmt.Errorf("%w: step %s has no active assignees, so its task cannot be raised", cmdb.ErrInvalid, stepID)
	}
	return nil
}

// closedTaskLink accepts a relationship on a task that is either still live
// (under its live type) or belongs to a task retired when its run completed,
// so closed-out tasks keep reading their assignees as history. The pattern
// must match both the live type and its retired twin (see liveOrRetired).
func closedTaskLink(alias, typeName string) string {
	return "(type(" + alias + ") = '" + typeName + "' OR task.status = 'retired')"
}

// taskStructure is the core task pattern. A completed run retires its tasks,
// run, and request and moves their links under the retired types, so the
// history reads traverse both the live and retired types of each link.
var taskStructure = "MATCH (task:Task)-[:" + liveOrRetired("TASK_FOR") + "]->(run:WorkflowRun)-[:" + liveOrRetired("RUN_FOR") + "]->(request:Request) MATCH (task)-[:" + liveOrRetired("TASK_STEP") + "]->(step:WorkflowStep) MATCH (run)-[:" + liveOrRetired("INSTANCE_OF") + "]->(workflow:Workflow) "

// taskViewQuery assembles everything a TaskView needs. Callers supply the
// WHERE conditions (joined with AND) applied after the core pattern. Retired
// tasks are included: a completed run retires its tasks, and they remain the
// request's history.
func taskViewQuery(conditions string) string {
	return taskStructure +
		"WHERE " + conditions + " " +
		"OPTIONAL MATCH (workflow)-[laterHas:HAS_STEP]->(later:WorkflowStep) WHERE " + active("later") + " AND later.order > step.order " +
		"WITH task, run, request, step, workflow, count(DISTINCT later) AS laterSteps " +
		"OPTIONAL MATCH (task)-[assignment:" + liveOrRetired("TASK_ASSIGNED_TO") + "]->(assignee) WHERE " + closedTaskLink("assignment", "TASK_ASSIGNED_TO") + " " +
		"OPTIONAL MATCH (assignee)-[membership:MEMBER]->(memberIdentity:Identity) WHERE assignee:Group AND " + active("memberIdentity") + " " +
		"WITH task, run, request, step, workflow, laterSteps, " +
		"collect(DISTINCT CASE WHEN assignee IS NULL THEN null ELSE {id: assignee.id, name: assignee.name, kind: CASE WHEN assignee:Group THEN 'group' ELSE 'identity' END} END) AS assignees, " +
		"collect(DISTINCT CASE WHEN memberIdentity IS NOT NULL THEN {id: memberIdentity.id, name: memberIdentity.name, kind: 'identity'} WHEN assignee:Identity THEN {id: assignee.id, name: assignee.name, kind: 'identity'} ELSE null END) AS eligible " +
		"OPTIONAL MATCH (task)-[acted:" + liveOrRetired("ACTED_BY") + "]->(actor:Identity) " +
		"WITH task, run, request, step, workflow, laterSteps, assignees, eligible, " +
		"collect(DISTINCT CASE WHEN acted IS NULL THEN null ELSE {actorId: actor.id, actorName: actor.name, action: acted.action, comment: acted.comment, actedAt: acted.actedAt} END) AS actions " +
		// The requester is whoever holds the live request link; once the request or task is closed out and
		// that link is retired, the most recent retired submitted-by link still names them for the history.
		"OPTIONAL MATCH (requester:Identity)-[link:FORM_SUBMITTED|DRAFTED_REQUEST|FORM_SUBMITTED_RETIRED]->(request) " +
		"WITH task, run, request, step, workflow, laterSteps, assignees, eligible, actions, requester, link ORDER BY CASE WHEN type(link) = 'FORM_SUBMITTED_RETIRED' THEN 1 ELSE 0 END, link.retiredAt DESC " +
		"WITH task, run, request, step, workflow, laterSteps, assignees, eligible, actions, head(collect(CASE WHEN requester IS NULL THEN null ELSE {id: requester.id, name: requester.name, kind: 'identity'} END)) AS requester " +
		// Access requests: who the access is for, the item this task is about, and every item on the request.
		"OPTIONAL MATCH (request)-[:" + liveOrRetired("REQUESTED_FOR") + "]->(subject:Identity) " +
		"WITH task, run, request, step, workflow, laterSteps, assignees, eligible, actions, requester, head(collect(CASE WHEN subject IS NULL THEN null ELSE {id: subject.id, name: subject.name, kind: 'identity'} END)) AS requestedFor " +
		"OPTIONAL MATCH (task)-[:" + liveOrRetired("TASK_ITEM") + "]->(item) OPTIONAL MATCH (itemApp:CI)-[:HAS_ROLE|ENTITLED_BY]->(item) " +
		"WITH task, run, request, step, workflow, laterSteps, assignees, eligible, actions, requester, requestedFor, head(collect(CASE WHEN item IS NULL THEN null ELSE " + accessItemMap("item", "itemApp", "") + " END)) AS item " +
		"OPTIONAL MATCH (request)-[requested:" + liveOrRetired("REQUESTS_ACCESS") + "]->(requestedItem) OPTIONAL MATCH (requestedApp:CI)-[:HAS_ROLE|ENTITLED_BY]->(requestedItem) " +
		"WITH task, run, request, step, workflow, laterSteps, assignees, eligible, actions, requester, requestedFor, item, collect(DISTINCT CASE WHEN requestedItem IS NULL THEN null ELSE " + accessItemMap("requestedItem", "requestedApp", "requested") + " END) AS items " +
		"RETURN task, run, request, step, workflow, laterSteps, assignees, eligible, actions, requester, requestedFor, item, items ORDER BY task.createdAt, task.id"
}

// accessItemMap renders a requested role or entitlement, with the application
// that provides it and (when the requests-access edge alias is given) the
// note and decision recorded on the request.
func accessItemMap(item, app, edge string) string {
	entry := "{id: " + item + ".id, name: " + item + ".name, kind: CASE WHEN " + item + ":Role THEN 'role' ELSE 'entitlement' END, applicationId: " + app + ".id, applicationName: " + app + ".name"
	if edge != "" {
		entry += ", note: " + edge + ".note, decision: " + edge + ".decision, decidedAt: " + edge + ".decidedAt, decidedBy: " + edge + ".decidedBy, fulfilledAt: " + edge + ".fulfilledAt"
	}
	return entry + "}"
}

func decodeAccessItem(value any) (cmdb.AccessItemView, bool) {
	entry, ok := value.(map[string]any)
	if !ok || stringProperty(entry, "id") == "" {
		return cmdb.AccessItemView{}, false
	}
	return cmdb.AccessItemView{
		ID:              stringProperty(entry, "id"),
		Kind:            cmdb.NodeKind(stringProperty(entry, "kind")),
		Name:            stringProperty(entry, "name"),
		ApplicationID:   stringProperty(entry, "applicationId"),
		ApplicationName: stringProperty(entry, "applicationName"),
		Note:            stringProperty(entry, "note"),
		Decision:        cmdb.ItemDecision(stringProperty(entry, "decision")),
		DecidedAt:       stringProperty(entry, "decidedAt"),
		DecidedBy:       stringProperty(entry, "decidedBy"),
		FulfilledAt:     stringProperty(entry, "fulfilledAt"),
	}, true
}

func (s *Store) GetTask(ctx context.Context, id string) (*cmdb.TaskView, error) {
	records, err := s.execute(ctx, false, taskViewQuery("task.id = $id"), map[string]any{"id": id})
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeTaskView(records[0])
}

func (s *Store) ListTasks(ctx context.Context, filter cmdb.TaskFilter) ([]cmdb.TaskView, int, error) {
	// The conditions are built as literal clauses rather than `$param = '' OR …`
	// so the planner can anchor on the Task.state index (pending queue), the
	// request, or the actor instead of scanning every task.
	conditions := make([]string, 0, 3)
	params := map[string]any{}
	if !filter.IncludeDone {
		conditions = append(conditions, "task.state = $pending AND "+active("task"))
		params["pending"] = string(cmdb.TaskPending)
	}
	if filter.RequestID != "" {
		conditions = append(conditions, "request.id = $requestId")
		params["requestId"] = filter.RequestID
	}
	if filter.ActorID != "" {
		conditions = append(conditions, "size([(task)-[assignment:"+liveOrRetired("TASK_ASSIGNED_TO")+"]->(assignee) WHERE "+closedTaskLink("assignment", "TASK_ASSIGNED_TO")+" AND (assignee.id = $actorId OR size([(assignee)-[:MEMBER]->(member:Identity {id: $actorId}) | member]) > 0) | assignee]) > 0")
		params["actorId"] = filter.ActorID
	}
	if len(conditions) == 0 {
		conditions = append(conditions, "true")
	}
	where := strings.Join(conditions, " AND ")
	query := taskViewQuery(where) + pageClause(filter.Limit, filter.Offset, params)
	records, err := s.execute(ctx, false, query, params)
	if err != nil {
		return nil, 0, fmt.Errorf("list tasks: %w", err)
	}
	tasks := make([]cmdb.TaskView, 0, len(records))
	for _, record := range records {
		view, err := decodeTaskView(record)
		if err != nil {
			return nil, 0, err
		}
		tasks = append(tasks, *view)
	}
	total := len(tasks)
	if filter.Limit > 0 || filter.Offset > 0 {
		delete(params, "limit")
		delete(params, "offset")
		counted, err := s.execute(ctx, false, taskStructure+"WHERE "+where+" RETURN count(DISTINCT task) AS total", params)
		if err != nil {
			return nil, 0, fmt.Errorf("count tasks: %w", err)
		}
		if len(counted) > 0 {
			total = toInt(counted[0].Values[0])
		}
	}
	return tasks, total, nil
}

func decodeTaskView(record *neo4j.Record) (*cmdb.TaskView, error) {
	nodes := make([]neo4j.Node, 5)
	for index := range nodes {
		node, ok := record.Values[index].(neo4j.Node)
		if !ok {
			return nil, fmt.Errorf("unexpected task view value %T at %d", record.Values[index], index)
		}
		nodes[index] = node
	}
	task, run, request, step, workflow := nodes[0], nodes[1], nodes[2], nodes[3], nodes[4]
	requestNode, err := decodeNode(cmdb.Request, "id", &neo4j.Record{Keys: []string{"node"}, Values: []any{request}})
	if err != nil {
		return nil, err
	}
	view := &cmdb.TaskView{
		ID:           stringProperty(task.Props, "id"),
		Status:       cmdb.TaskStatus(stringProperty(task.Props, "state")),
		CreatedAt:    stringProperty(task.Props, "createdAt"),
		CompletedAt:  stringProperty(task.Props, "completedAt"),
		RetiredAt:    stringProperty(task.Props, "retiredAt"),
		Step:         decodeStep(step.Props),
		FinalStep:    toInt(record.Values[5]) == 0,
		RunID:        stringProperty(run.Props, "id"),
		RunStatus:    cmdb.RunStatus(stringProperty(run.Props, "state")),
		WorkflowID:   stringProperty(workflow.Props, "id"),
		WorkflowName: stringProperty(workflow.Props, "name"),
		Request:      *requestNode,
		Assignees:    decodeAssignees(record.Values[6]),
		EligibleActors: decodeAssignees(record.Values[7]),
		Actions:      []cmdb.TaskActionRecord{},
	}
	view.Step.Assignees = view.Assignees
	if _, stamped := task.Props["olaBusinessDays"]; stamped {
		view.Step.OLAEnabled = boolProperty(task.Props, "olaEnabled")
		view.Step.OLABusinessDays = toInt(task.Props["olaBusinessDays"])
	}
	for _, assignee := range view.Assignees {
		view.Step.AssigneeIDs = append(view.Step.AssigneeIDs, assignee.ID)
	}
	actions, _ := record.Values[8].([]any)
	for _, item := range actions {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		view.Actions = append(view.Actions, cmdb.TaskActionRecord{ActorID: stringProperty(entry, "actorId"), ActorName: stringProperty(entry, "actorName"), Action: cmdb.TaskStatus(stringProperty(entry, "action")), Comment: stringProperty(entry, "comment"), ActedAt: stringProperty(entry, "actedAt")})
	}
	sort.SliceStable(view.Actions, func(i, j int) bool { return view.Actions[i].ActedAt < view.Actions[j].ActedAt })
	if requesters := decodeAssignees([]any{record.Values[9]}); len(requesters) > 0 {
		view.Requester = &requesters[0]
	}
	if subjects := decodeAssignees([]any{record.Values[10]}); len(subjects) > 0 {
		view.RequestedFor = &subjects[0]
	}
	items, _ := record.Values[12].([]any)
	for _, entry := range items {
		if item, ok := decodeAccessItem(entry); ok {
			view.Items = append(view.Items, item)
		}
	}
	sort.SliceStable(view.Items, func(i, j int) bool {
		if view.Items[i].ApplicationName != view.Items[j].ApplicationName {
			return view.Items[i].ApplicationName < view.Items[j].ApplicationName
		}
		return view.Items[i].Name < view.Items[j].Name
	})
	if item, ok := decodeAccessItem(record.Values[11]); ok {
		// The task's item reads its note and decision from the request.
		for _, listed := range view.Items {
			if listed.ID == item.ID {
				item = listed
				break
			}
		}
		view.Item = &item
	}
	return view, nil
}

// ActOnTask records an action against a pending task and applies its outcome
// in one transaction: request field updates, the task's new state, and then
// either the next task, the run's completion (creating the CI and fulfilling
// the request), or the request's return to its requester as a draft.
func (s *Store) ActOnTask(ctx context.Context, taskID, actorID string, outcome cmdb.TaskOutcome) (*cmdb.TaskView, error) {
	timestamp := now()
	err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		records, err := runQuery(ctx, tx, "MATCH (task:Task {id: $taskId}) WHERE "+active("task")+" AND task.state = $pending "+
			"MATCH (task)-[:TASK_FOR]->(run:WorkflowRun)-[:RUN_FOR]->(request:Request) MATCH (task)-[:TASK_STEP]->(step:WorkflowStep) MATCH (run)-[:INSTANCE_OF]->(workflow:Workflow) "+
			"MATCH (actor:Identity {id: $actorId}) WHERE "+active("actor")+" "+
			"CREATE (task)-[:ACTED_BY {status: 'active', action: $action, comment: $comment, actedAt: $now}]->(actor) "+
			"SET request += $properties RETURN run.id AS runId, request.id AS requestId, step.order AS stepOrder, workflow.id AS workflowId", map[string]any{"taskId": taskID, "pending": string(cmdb.TaskPending), "actorId": actorID, "action": string(outcome.Action), "comment": outcome.Comment, "now": timestamp, "properties": emptyIfNil(outcome.Properties)})
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return fmt.Errorf("%w: task %s is not pending or actor %s is not an active identity", cmdb.ErrInvalid, taskID, actorID)
		}
		runID, requestID, stepOrder, workflowID := fmt.Sprint(records[0].Values[0]), fmt.Sprint(records[0].Values[1]), toInt(records[0].Values[2]), fmt.Sprint(records[0].Values[3])
		if outcome.Item != "" {
			return actOnAccessItem(ctx, tx, accessAction{taskID: taskID, actorID: actorID, runID: runID, requestID: requestID, workflowID: workflowID, stepOrder: stepOrder, timestamp: timestamp}, outcome)
		}
		switch {
		case outcome.Return:
			if _, err := runQuery(ctx, tx, "MATCH (task:Task {id: $taskId}), (run:WorkflowRun {id: $runId}), (request:Request {id: $requestId}) "+
				"SET task.state = $rejected, task.completedAt = $now, run.state = $returned, run.completedAt = $now, request.state = $draft, request.returnComment = $comment, request.returnedAt = $now", map[string]any{"taskId": taskID, "runId": runID, "requestId": requestID, "rejected": string(cmdb.TaskRejected), "returned": string(cmdb.RunReturned), "draft": string(cmdb.RequestDraft), "comment": outcome.Comment, "now": timestamp}); err != nil {
				return err
			}
			// The submitted-by link retires and the requester gets the draft back.
			moved, err := moveToRetired(ctx, tx, "FORM_SUBMITTED", "MATCH (requester:Identity)-[r:FORM_SUBMITTED]->(request:Request {id: $requestId})", map[string]any{"requestId": requestID, "now": timestamp})
			if err != nil {
				return err
			}
			requesterIDs := make([]any, 0, len(moved))
			for _, record := range moved {
				requesterIDs = append(requesterIDs, fmt.Sprint(record.Values[0]))
			}
			_, err = runQuery(ctx, tx, "MATCH (request:Request {id: $requestId}) UNWIND $requesterIds AS requesterId MATCH (requester:Identity {id: requesterId}) MERGE (requester)-[draft:DRAFTED_REQUEST]->(request) ON CREATE SET draft.status = 'active'", map[string]any{"requestId": requestID, "requesterIds": requesterIDs})
			return err
		case outcome.Advance:
			if _, err := runQuery(ctx, tx, "MATCH (task:Task {id: $taskId}) SET task.state = $state, task.completedAt = $now", map[string]any{"taskId": taskID, "state": string(outcome.Action), "now": timestamp}); err != nil {
				return err
			}
			next, err := runQuery(ctx, tx, "MATCH (workflow:Workflow {id: $workflowId})-[has:HAS_STEP]->(next:WorkflowStep) WHERE "+active("has")+" AND "+active("next")+" AND next.order > $order RETURN next.id ORDER BY next.order LIMIT 1", map[string]any{"workflowId": workflowID, "order": stepOrder})
			if err != nil {
				return err
			}
			if len(next) > 0 {
				return createTask(ctx, tx, runID, fmt.Sprint(next[0].Values[0]), timestamp)
			}
			// Last step done: the run completes and the request is fulfilled as a CI.
			params := map[string]any{"runId": runID, "requestId": requestID, "completed": string(cmdb.RunCompleted), "fulfilled": string(cmdb.RequestFulfilled), "now": timestamp}
			if outcome.CIProperties != nil {
				ciID, err := nextID(ctx, tx, cmdb.CI)
				if err != nil {
					return err
				}
				params["ciId"] = ciID
				params["ciProperties"] = outcome.CIProperties
				if _, err = runQuery(ctx, tx, "MATCH (run:WorkflowRun {id: $runId}), (request:Request {id: $requestId}) "+
					"CREATE (ci:CI {id: $ciId, status: 'active'}) SET ci += $ciProperties CREATE (request)-[:FULFILLED_BY {status: 'active'}]->(ci) "+
					"SET run.state = $completed, run.completedAt = $now, request.state = $fulfilled, request.fulfilledAt = $now", params); err != nil {
					return err
				}
			} else if _, err = runQuery(ctx, tx, "MATCH (run:WorkflowRun {id: $runId}), (request:Request {id: $requestId}) SET run.state = $completed, run.completedAt = $now, request.state = $fulfilled, request.fulfilledAt = $now", params); err != nil {
				return err
			}
			return closeOutRun(ctx, tx, runID, requestID, timestamp)
		default:
			return nil // the task stays pending (an approval waiting on the rest of its approvers)
		}
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrInvalid) || errors.Is(err, cmdb.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("act on task: %w", err)
	}
	return s.GetTask(ctx, taskID)
}

// closeOutRun retires a fulfilled request, its run, and every task the run
// raised, along with their attached relationships, the same way RetireNode
// does: the CI is now the live record. The request's fulfilled-by link to that
// CI stays active so the CI still reads where it came from. The workflow
// definition and its steps are untouched and keep serving new submissions.
func closeOutRun(ctx context.Context, tx neo4j.ManagedTransaction, runID, requestID, timestamp string) error {
	if err := retireRun(ctx, tx, runID, timestamp); err != nil {
		return err
	}
	if _, err := runQuery(ctx, tx, "MATCH (request:Request {id: $requestId}) SET request.status = 'retired', request.retiredAt = coalesce(request.retiredAt, $now)", map[string]any{"requestId": requestID, "now": timestamp}); err != nil {
		return err
	}
	return retireEdgesOf(ctx, tx, cmdb.Request, []string{requestID}, map[string]bool{"FULFILLED_BY": true}, timestamp)
}

// retireRun retires a run and every task it raised, moving their attached
// relationships under the retired types. ErrNotFound when the run is missing.
func retireRun(ctx context.Context, tx neo4j.ManagedTransaction, runID, timestamp string) error {
	records, err := runQuery(ctx, tx, "MATCH (run:WorkflowRun {id: $runId}) OPTIONAL MATCH (task:Task)-[:TASK_FOR]->(run) "+
		"WITH run, collect(task) AS tasks SET run.status = 'retired', run.retiredAt = coalesce(run.retiredAt, $now) "+
		"FOREACH (task IN tasks | SET task.status = 'retired', task.retiredAt = coalesce(task.retiredAt, $now)) "+
		"RETURN [task IN tasks | task.id] AS taskIds", map[string]any{"runId": runID, "now": timestamp})
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return cmdb.ErrNotFound
	}
	if err := retireEdgesOf(ctx, tx, cmdb.Task, toStringList(records[0].Values[0]), nil, timestamp); err != nil {
		return err
	}
	return retireEdgesOf(ctx, tx, cmdb.WorkflowRun, []string{runID}, nil, timestamp)
}

// RetireWorkflowRun retires a run and every task it raised, with their
// attached relationships. The request is left alone: it has its own lifecycle.
func (s *Store) RetireWorkflowRun(ctx context.Context, id string) error {
	err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		return retireRun(ctx, tx, id, now())
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrNotFound) {
			return err
		}
		return fmt.Errorf("retire workflow run: %w", err)
	}
	return nil
}

func emptyIfNil(properties map[string]any) map[string]any {
	if properties == nil {
		return map[string]any{}
	}
	return properties
}

func toAnyList(values []string) []any {
	list := make([]any, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	return list
}

func toStringList(value any) []string {
	items, _ := value.([]any)
	list := make([]string, 0, len(items))
	for _, item := range items {
		list = append(list, fmt.Sprint(item))
	}
	return list
}

func toInt(value any) int {
	switch number := value.(type) {
	case int64:
		return int(number)
	case int:
		return number
	case float64:
		return int(number)
	default:
		return 0
	}
}
