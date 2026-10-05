package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Access requests. The request, its run, and one approval task per requested
// item are written together; each item then moves on its own (approved ->
// fulfilment task -> permissions link, or denied) and the request closes once
// every item has landed.

// AccessOptions works out what an identity may ask for: everything an active
// application provides that has an Accountable, minus what the identity already
// holds through a birthright, through a role it holds, directly, or on an open
// request.
func (s *Store) AccessOptions(ctx context.Context, identityID string) (*cmdb.AccessOptions, error) {
	identity, err := s.GetNode(ctx, cmdb.Identity, identityID, false)
	if err != nil {
		return nil, err
	}
	options := &cmdb.AccessOptions{Identity: cmdb.Assignee{ID: identity.ID, Kind: cmdb.Identity, Name: stringProperty(identity.Properties, "name")}, Applications: []cmdb.AccessApplication{}, Held: []cmdb.AccessHolding{}}
	params := map[string]any{"id": identityID}
	holdingQueries := []string{
		// Roles and entitlements granted by a birthright the identity's job code qualifies for.
		"MATCH (identity:Identity {id: $id})-[hasJob:HAS_JOB_CODE]->(job:JobCode)-[qualifies:QUALIFIES_FOR]->(birthright:Birthright)-[grants:GRANTS]->(item) " +
			"WHERE (item:Role OR item:Entitlement) AND " + active("hasJob") + " AND " + active("job") + " AND " + active("qualifies") + " AND " + active("birthright") + " AND " + active("grants") + " AND " + active("item") + " " +
			"RETURN DISTINCT item.id AS id, CASE WHEN item:Role THEN 'role' ELSE 'entitlement' END AS kind, item.name AS name, 'birthright' AS via, coalesce(birthright.name, birthright.id) AS source",
		// Roles and entitlements permissioned directly.
		"MATCH (item)-[permission:PERMISSIONS]->(identity:Identity {id: $id}) WHERE (item:Role OR item:Entitlement) AND " + active("permission") + " AND " + active("item") + " " +
			"RETURN DISTINCT item.id AS id, CASE WHEN item:Role THEN 'role' ELSE 'entitlement' END AS kind, item.name AS name, 'direct' AS via, coalesce(permission.requestId, '') AS source",
		// Items still open on another access request for this identity.
		"MATCH (request:Request)-[requestedFor:REQUESTED_FOR]->(identity:Identity {id: $id}) WHERE " + active("request") + " AND " + active("requestedFor") + " " +
			"MATCH (request)-[requested:REQUESTS_ACCESS]->(item) WHERE " + active("requested") + " AND requested.decision IN ['pending', 'approved'] " +
			"RETURN DISTINCT item.id AS id, CASE WHEN item:Role THEN 'role' ELSE 'entitlement' END AS kind, item.name AS name, 'pending' AS via, request.id AS source",
	}
	held := make(map[string]bool)
	heldRoleIDs := make([]any, 0)
	addHolding := func(record *neo4j.Record) {
		holding := cmdb.AccessHolding{ID: fmt.Sprint(record.Values[0]), Kind: cmdb.NodeKind(fmt.Sprint(record.Values[1])), Name: stringValue(record.Values[2]), Via: fmt.Sprint(record.Values[3]), Source: stringValue(record.Values[4])}
		if holding.Kind == cmdb.Role && holding.Via != "pending" {
			heldRoleIDs = append(heldRoleIDs, holding.ID)
		}
		if held[holding.ID] {
			return
		}
		held[holding.ID] = true
		options.Held = append(options.Held, holding)
	}
	for _, query := range holdingQueries {
		records, err := s.execute(ctx, false, query, params)
		if err != nil {
			return nil, fmt.Errorf("access holdings: %w", err)
		}
		for _, record := range records {
			addHolding(record)
		}
	}
	// Entitlements included by any role the identity holds (by birthright or directly).
	if len(heldRoleIDs) > 0 {
		records, err := s.execute(ctx, false, "MATCH (role:Role)-[includes:INCLUDES]->(entitlement:Entitlement) WHERE role.id IN $roleIds AND "+active("includes")+" AND "+active("entitlement")+" "+
			"RETURN DISTINCT entitlement.id AS id, 'entitlement' AS kind, entitlement.name AS name, 'role' AS via, coalesce(role.name, role.id) AS source", map[string]any{"roleIds": heldRoleIDs})
		if err != nil {
			return nil, fmt.Errorf("access holdings: %w", err)
		}
		for _, record := range records {
			addHolding(record)
		}
	}
	// What is on offer: roles an active application has and entitlements it is
	// entitled by, as long as they have an Accountable.
	records, err := s.execute(ctx, false, "MATCH (application:CI {ciType: 'application'})-[provides:HAS_ROLE|ENTITLED_BY]->(item) WHERE (item:Role OR item:Entitlement) AND "+active("application")+" AND "+active("provides")+" AND "+active("item")+" "+
		"MATCH (item)-[accountable:ACCOUNTABLE]->(owner:Identity) WHERE "+active("accountable")+" AND "+active("owner")+" "+
		"RETURN application.id, application.name, item.id, CASE WHEN item:Role THEN 'role' ELSE 'entitlement' END, item.name, item.description, owner.id, owner.name ORDER BY application.name, application.id, item.name, item.id", nil)
	if err != nil {
		return nil, fmt.Errorf("access options: %w", err)
	}
	// Records arrive ordered by application, so applications are built in order.
	byApplication := make(map[string]int)
	for _, record := range records {
		itemID := fmt.Sprint(record.Values[2])
		if held[itemID] {
			continue
		}
		applicationID := fmt.Sprint(record.Values[0])
		index, ok := byApplication[applicationID]
		if !ok {
			index = len(options.Applications)
			byApplication[applicationID] = index
			options.Applications = append(options.Applications, cmdb.AccessApplication{ID: applicationID, Name: stringValue(record.Values[1]), Roles: []cmdb.AccessOption{}, Entitlements: []cmdb.AccessOption{}})
		}
		option := cmdb.AccessOption{ID: itemID, Kind: cmdb.NodeKind(fmt.Sprint(record.Values[3])), Name: stringValue(record.Values[4]), Description: stringValue(record.Values[5]), Owner: cmdb.Assignee{ID: fmt.Sprint(record.Values[6]), Kind: cmdb.Identity, Name: stringValue(record.Values[7])}}
		application := &options.Applications[index]
		if option.Kind == cmdb.Role {
			application.Roles = append(application.Roles, option)
		} else {
			application.Entitlements = append(application.Entitlements, option)
		}
	}
	sort.SliceStable(options.Held, func(i, j int) bool {
		if options.Held[i].Kind != options.Held[j].Kind {
			return options.Held[i].Kind == cmdb.Role
		}
		return options.Held[i].Name < options.Held[j].Name
	})
	return options, nil
}

// CreateAccessRequest writes the request, links it to its requester and
// subject, records each item on it, opens the run, and raises the approval
// task for every item in one transaction.
func (s *Store) CreateAccessRequest(ctx context.Context, input cmdb.AccessRequestInput, workflowID string) (*cmdb.Node, error) {
	timestamp := now()
	var requestID string
	err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		generated, err := nextID(ctx, tx, cmdb.Request)
		if err != nil {
			return err
		}
		requestID = generated
		runID, err := nextID(ctx, tx, cmdb.WorkflowRun)
		if err != nil {
			return err
		}
		records, err := runQuery(ctx, tx, "MATCH (requester:Identity {id: $requestedBy}), (subject:Identity {id: $requestFor}), (workflow:Workflow {id: $workflowId}) "+
			"WHERE "+active("requester")+" AND "+active("subject")+" AND "+active("workflow")+" "+
			"MATCH (workflow)-[has:HAS_STEP]->(first:WorkflowStep) WHERE "+active("has")+" AND "+active("first")+" "+
			"WITH requester, subject, workflow, first ORDER BY first.order LIMIT 1 "+
			"CREATE (request:Request {id: $requestId, status: 'active', requestType: $requestType, state: $state, name: 'Access request for ' + coalesce(subject.name, subject.id), submittedAt: $now, slaEnabled: $slaEnabled, slaBusinessDays: $slaDays, slaDueAt: $slaDueAt}) "+
			"CREATE (requester)-[:FORM_SUBMITTED {status: 'active'}]->(request) CREATE (request)-[:REQUESTED_FOR {status: 'active'}]->(subject) "+
			"CREATE (run:WorkflowRun {id: $runId, status: 'active', state: $runState, name: workflow.name, startedAt: $now, currentOrder: first.order}) "+
			"CREATE (run)-[:RUN_FOR {status: 'active'}]->(request) CREATE (run)-[:INSTANCE_OF {status: 'active'}]->(workflow) "+
			"RETURN first.id AS stepId", map[string]any{"requestedBy": input.RequestedByID, "requestFor": input.RequestForID, "workflowId": workflowID, "requestId": requestID, "runId": runID, "requestType": string(cmdb.AccessRequest), "state": string(cmdb.RequestInReview), "runState": string(cmdb.RunActive), "now": timestamp, "slaEnabled": input.SLAEnabled, "slaDays": input.SLABusinessDays, "slaDueAt": input.SLADueAt})
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return fmt.Errorf("%w: the requester, the identity the access is for, and the access workflow must all be active", cmdb.ErrInvalid)
		}
		stepID := fmt.Sprint(records[0].Values[0])
		for _, item := range input.Items {
			linked, err := runQuery(ctx, tx, "MATCH (request:Request {id: $requestId}), (item {id: $itemId}) WHERE (item:Role OR item:Entitlement) AND "+active("item")+" "+
				"CREATE (request)-[:REQUESTS_ACCESS {status: 'active', note: $note, decision: $decision}]->(item) RETURN item.id", map[string]any{"requestId": requestID, "itemId": item.ID, "note": item.Note, "decision": string(cmdb.ItemPending)})
			if err != nil {
				return err
			}
			if len(linked) == 0 {
				return fmt.Errorf("%w: %s %s is not an active record", cmdb.ErrInvalid, item.Kind, item.ID)
			}
			if err := createItemTask(ctx, tx, runID, stepID, item.ID, timestamp); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrInvalid) || errors.Is(err, cmdb.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("create access request: %w", err)
	}
	return s.GetNode(ctx, cmdb.Request, requestID, false)
}

// createItemTask raises the task for one step of an access run about one
// requested item. A step assigned by rule goes to the item's Accountable;
// otherwise to the step's named assignees.
func createItemTask(ctx context.Context, tx neo4j.ManagedTransaction, runID, stepID, itemID, timestamp string) error {
	taskID, err := nextID(ctx, tx, cmdb.Task)
	if err != nil {
		return err
	}
	records, err := runQuery(ctx, tx, "MATCH (run:WorkflowRun {id: $runId}), (step:WorkflowStep {id: $stepId}), (item {id: $itemId}) WHERE item:Role OR item:Entitlement "+
		"CREATE (task:Task {id: $taskId, status: 'active', state: $pending, name: step.name + ' · ' + coalesce(item.name, item.id), createdAt: $now, olaEnabled: coalesce(step.olaEnabled, false), olaBusinessDays: toInteger(coalesce(step.olaBusinessDays, 0))}) "+
		"CREATE (task)-[:TASK_FOR {status: 'active'}]->(run) CREATE (task)-[:TASK_STEP {status: 'active'}]->(step) CREATE (task)-[:TASK_ITEM {status: 'active'}]->(item) "+
		"SET run.currentOrder = step.order "+
		"WITH task, step, item "+
		"OPTIONAL MATCH (item)-[accountable:ACCOUNTABLE]->(owner:Identity) WHERE step.assigneeRule = $byOwner AND "+active("accountable")+" AND "+active("owner")+" "+
		"OPTIONAL MATCH (step)-[assigned:STEP_ASSIGNED_TO]->(named) WHERE coalesce(step.assigneeRule, '') = '' AND "+active("assigned")+" AND "+active("named")+" "+
		"WITH task, step, collect(DISTINCT owner) + collect(DISTINCT named) AS assignees "+
		"FOREACH (assignee IN assignees | CREATE (task)-[:TASK_ASSIGNED_TO {status: 'active'}]->(assignee)) "+
		"RETURN size(assignees) AS assigned, step.name AS stepName", map[string]any{"runId": runID, "stepId": stepID, "itemId": itemID, "taskId": taskID, "pending": string(cmdb.TaskPending), "byOwner": string(cmdb.ItemAccountable), "now": timestamp})
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("%w: step %s or item %s is missing, so the task cannot be raised", cmdb.ErrInvalid, stepID, itemID)
	}
	if toInt(records[0].Values[0]) == 0 {
		return fmt.Errorf("%w: %s has nobody to assign the task for %s to (no active Accountable or step assignees)", cmdb.ErrInvalid, fmt.Sprint(records[0].Values[1]), itemID)
	}
	return nil
}

type accessAction struct {
	taskID, actorID, runID, requestID, workflowID, timestamp string
	stepOrder                                                 int
}

// actOnAccessItem applies a decision about one item after the action has been
// recorded on its task: deny it, approve it (raising its fulfilment task), or
// mark it provisioned (creating the permissions link). Denials and
// provisioning may be the last word on the request, so they settle it.
func actOnAccessItem(ctx context.Context, tx neo4j.ManagedTransaction, action accessAction, outcome cmdb.TaskOutcome) error {
	params := map[string]any{"taskId": action.taskID, "requestId": action.requestID, "itemId": outcome.Item, "actorId": action.actorID, "state": string(outcome.Action), "now": action.timestamp}
	switch {
	case outcome.Deny:
		params["decision"] = string(cmdb.ItemDenied)
		if _, err := runQuery(ctx, tx, "MATCH (task:Task {id: $taskId}), (request:Request {id: $requestId})-[requested:REQUESTS_ACCESS]->(item {id: $itemId}) "+
			"SET task.state = $state, task.completedAt = $now, requested.decision = $decision, requested.decidedAt = $now, requested.decidedBy = $actorId", params); err != nil {
			return err
		}
		return settleAccessRequest(ctx, tx, action)
	case outcome.Approve:
		params["decision"] = string(cmdb.ItemApproved)
		if _, err := runQuery(ctx, tx, "MATCH (task:Task {id: $taskId}), (request:Request {id: $requestId})-[requested:REQUESTS_ACCESS]->(item {id: $itemId}) "+
			"SET task.state = $state, task.completedAt = $now, requested.decision = $decision, requested.decidedAt = $now, requested.decidedBy = $actorId", params); err != nil {
			return err
		}
		next, err := runQuery(ctx, tx, "MATCH (workflow:Workflow {id: $workflowId})-[has:HAS_STEP]->(next:WorkflowStep) WHERE "+active("has")+" AND "+active("next")+" AND next.order > $order RETURN next.id ORDER BY next.order LIMIT 1", map[string]any{"workflowId": action.workflowID, "order": action.stepOrder})
		if err != nil {
			return err
		}
		if len(next) == 0 {
			return fmt.Errorf("%w: the access workflow has no fulfilment step after %d", cmdb.ErrInvalid, action.stepOrder)
		}
		return createItemTask(ctx, tx, action.runID, fmt.Sprint(next[0].Values[0]), outcome.Item, action.timestamp)
	case outcome.Provision:
		params["decision"] = string(cmdb.ItemFulfilled)
		records, err := runQuery(ctx, tx, "MATCH (task:Task {id: $taskId}), (request:Request {id: $requestId})-[requested:REQUESTS_ACCESS]->(item {id: $itemId}), (request)-[:REQUESTED_FOR]->(subject:Identity) "+
			"SET task.state = $state, task.completedAt = $now, requested.decision = $decision, requested.fulfilledAt = $now "+
			"MERGE (item)-[permission:PERMISSIONS]->(subject) ON CREATE SET permission.status = 'active' "+
			"SET permission.requestId = $requestId, permission.grantedAt = $now, permission.grantedBy = $actorId, permission.note = coalesce(requested.note, '') "+
			"RETURN subject.id", params)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return fmt.Errorf("%w: request %s no longer names who the access is for, so %s cannot be provisioned", cmdb.ErrInvalid, action.requestID, outcome.Item)
		}
		return settleAccessRequest(ctx, tx, action)
	default:
		return nil
	}
}

// AccessAnomalies reads the stored snapshot of the last anomaly check. The
// check is a whole-graph walk, so it is not run per request; see
// RefreshAccessAnomalies and the scheduler in the HTTP server.
func (s *Store) AccessAnomalies(ctx context.Context) (*cmdb.AccessAnomalyReport, error) {
	records, err := s.execute(ctx, false, "OPTIONAL MATCH (snapshot:AccessAnomalySnapshot {name: 'current'}) "+
		"OPTIONAL MATCH (anomaly:AccessAnomaly) "+
		"WITH snapshot, anomaly ORDER BY anomaly.position "+
		"RETURN snapshot.computedAt AS computedAt, collect(CASE WHEN anomaly IS NULL THEN null ELSE properties(anomaly) END) AS anomalies", nil)
	if err != nil {
		return nil, fmt.Errorf("access anomalies: %w", err)
	}
	report := &cmdb.AccessAnomalyReport{Anomalies: []cmdb.AccessAnomaly{}}
	if len(records) == 0 {
		return report, nil
	}
	report.ComputedAt = stringValue(records[0].Values[0])
	items, _ := records[0].Values[1].([]any)
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		anomaly := cmdb.AccessAnomaly{
			Identity:    cmdb.Assignee{ID: stringProperty(entry, "identityId"), Kind: cmdb.Identity, Name: stringProperty(entry, "identityName")},
			Item:        cmdb.Assignee{ID: stringProperty(entry, "itemId"), Kind: cmdb.NodeKind(stringProperty(entry, "itemKind")), Name: stringProperty(entry, "itemName")},
			Birthrights: zipAssignees(entry["birthrightIds"], entry["birthrightNames"], cmdb.Birthright),
			Roles:       zipAssignees(entry["roleIds"], entry["roleNames"], cmdb.Role),
			Paths:       toStringList(entry["paths"]),
		}
		report.Anomalies = append(report.Anomalies, anomaly)
	}
	return report, nil
}

// zipAssignees pairs parallel id and name lists back into assignees.
func zipAssignees(ids, names any, kind cmdb.NodeKind) []cmdb.Assignee {
	idList := toStringList(ids)
	nameList := toStringList(names)
	assignees := make([]cmdb.Assignee, 0, len(idList))
	for index, id := range idList {
		name := ""
		if index < len(nameList) {
			name = nameList[index]
		}
		assignees = append(assignees, cmdb.Assignee{ID: id, Kind: kind, Name: name})
	}
	return assignees
}

// RefreshAccessAnomalies recomputes the anomaly check from the live graph and
// replaces the stored snapshot with the result. Each anomaly is one
// AccessAnomaly node (flat properties, parallel id/name lists) under an
// AccessAnomalySnapshot that records when the check ran; neither label is a
// record kind, so they stay off the map and out of the record lists.
func (s *Store) RefreshAccessAnomalies(ctx context.Context) (*cmdb.AccessAnomalyReport, error) {
	anomalies, err := s.computeAccessAnomalies(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]any, 0, len(anomalies))
	for position, anomaly := range anomalies {
		birthrightIDs, birthrightNames := splitAssignees(anomaly.Birthrights)
		roleIDs, roleNames := splitAssignees(anomaly.Roles)
		rows = append(rows, map[string]any{
			"position": position, "identityId": anomaly.Identity.ID, "identityName": anomaly.Identity.Name,
			"itemId": anomaly.Item.ID, "itemKind": string(anomaly.Item.Kind), "itemName": anomaly.Item.Name,
			"birthrightIds": birthrightIDs, "birthrightNames": birthrightNames, "roleIds": roleIDs, "roleNames": roleNames,
			"paths": toAnyList(anomaly.Paths),
		})
	}
	computedAt := now()
	err = s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		if _, err := runQuery(ctx, tx, "MATCH (anomaly:AccessAnomaly) DELETE anomaly", nil); err != nil {
			return err
		}
		if _, err := runQuery(ctx, tx, "UNWIND $rows AS row CREATE (anomaly:AccessAnomaly) SET anomaly = row", map[string]any{"rows": rows}); err != nil {
			return err
		}
		_, err := runQuery(ctx, tx, "MERGE (snapshot:AccessAnomalySnapshot {name: 'current'}) SET snapshot.computedAt = $now, snapshot.count = $count", map[string]any{"now": computedAt, "count": len(rows)})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("store access anomalies: %w", err)
	}
	return &cmdb.AccessAnomalyReport{ComputedAt: computedAt, Anomalies: anomalies}, nil
}

func splitAssignees(assignees []cmdb.Assignee) ([]any, []any) {
	ids := make([]any, 0, len(assignees))
	names := make([]any, 0, len(assignees))
	for _, assignee := range assignees {
		ids = append(ids, assignee.ID)
		names = append(names, assignee.Name)
	}
	return ids, names
}

// computeAccessAnomalies finds identities that hold the same role or
// entitlement through more than one path: a birthright (including entitlements
// included by a granted role), a held role that includes the entitlement, and
// a direct permissions link. It reads every identity's access, so callers
// store the result rather than running it per request.
func (s *Store) computeAccessAnomalies(ctx context.Context) ([]cmdb.AccessAnomaly, error) {
	// Birthright path (job → qualifies-for → grants → item, plus role includes)
	// overlapping a direct permissions link to the same item. The OPTIONAL
	// MATCH keeps rows when there is no include (WHERE includes IS NULL OR …)
	// so a birthright that grants a role or entitlement directly is not dropped.
	birthrightQuery := "MATCH (identity:Identity)-[hasJob:HAS_JOB_CODE]->(job:JobCode)-[qualifies:QUALIFIES_FOR]->(birthright:Birthright)-[grants:GRANTS]->(granted) " +
		"WHERE (granted:Role OR granted:Entitlement) AND " + active("hasJob") + " AND " + active("job") + " AND " + active("qualifies") + " AND " + active("birthright") + " AND " + active("grants") + " AND " + active("granted") + " AND " + active("identity") + " " +
		"OPTIONAL MATCH (granted)-[includes:INCLUDES]->(included:Entitlement) " +
		"WHERE includes IS NULL OR (granted:Role AND " + active("includes") + " AND " + active("included") + ") " +
		"WITH identity, birthright, granted, collect(DISTINCT included) AS includedEnts " +
		"WITH identity, birthright, granted, " +
		"[{item: granted, kind: CASE WHEN granted:Role THEN 'role' ELSE 'entitlement' END, viaRole: null}] + " +
		"[ent IN includedEnts WHERE ent IS NOT NULL | {item: ent, kind: 'entitlement', viaRole: granted}] AS entries " +
		"UNWIND entries AS entry " +
		"WITH identity, birthright, entry.item AS item, entry.kind AS itemKind, entry.viaRole AS viaRole " +
		"WHERE item IS NOT NULL " +
		"MATCH (item)-[permission:PERMISSIONS]->(identity) WHERE " + active("permission") + " " +
		"RETURN identity.id AS identityId, coalesce(identity.name, identity.id) AS identityName, " +
		"item.id AS itemId, itemKind AS itemKind, coalesce(item.name, item.id) AS itemName, " +
		"birthright.id AS birthrightId, coalesce(birthright.name, birthright.id) AS birthrightName, " +
		"viaRole.id AS roleId, coalesce(viaRole.name, viaRole.id) AS roleName"
	// Role path: identity is permissioned to a role that includes an entitlement,
	// and is also permissioned to that entitlement directly (exception overlap).
	roleQuery := "MATCH (role:Role)-[includes:INCLUDES]->(entitlement:Entitlement) " +
		"WHERE " + active("role") + " AND " + active("includes") + " AND " + active("entitlement") + " " +
		"MATCH (role)-[rolePermission:PERMISSIONS]->(identity:Identity) WHERE " + active("rolePermission") + " AND " + active("identity") + " " +
		"MATCH (entitlement)-[entPermission:PERMISSIONS]->(identity) WHERE " + active("entPermission") + " " +
		"RETURN identity.id AS identityId, coalesce(identity.name, identity.id) AS identityName, " +
		"entitlement.id AS itemId, 'entitlement' AS itemKind, coalesce(entitlement.name, entitlement.id) AS itemName, " +
		"null AS birthrightId, null AS birthrightName, " +
		"role.id AS roleId, coalesce(role.name, role.id) AS roleName"
	type key struct{ identityID, itemID string }
	byKey := make(map[key]*cmdb.AccessAnomaly)
	order := make([]key, 0)
	addRecord := func(record *neo4j.Record) {
		identityID := fmt.Sprint(record.Values[0])
		itemID := fmt.Sprint(record.Values[2])
		k := key{identityID: identityID, itemID: itemID}
		anomaly, ok := byKey[k]
		if !ok {
			anomaly = &cmdb.AccessAnomaly{
				Identity:    cmdb.Assignee{ID: identityID, Kind: cmdb.Identity, Name: stringValue(record.Values[1])},
				Item:        cmdb.Assignee{ID: itemID, Kind: cmdb.NodeKind(fmt.Sprint(record.Values[3])), Name: stringValue(record.Values[4])},
				Birthrights: []cmdb.Assignee{},
				Roles:       []cmdb.Assignee{},
				Paths:       []string{},
			}
			byKey[k] = anomaly
			order = append(order, k)
		}
		addPath := func(path string) {
			for _, existing := range anomaly.Paths {
				if existing == path {
					return
				}
			}
			anomaly.Paths = append(anomaly.Paths, path)
		}
		addPath("direct")
		if birthrightID := stringValue(record.Values[5]); birthrightID != "" {
			addPath("birthright")
			already := false
			for _, birthright := range anomaly.Birthrights {
				if birthright.ID == birthrightID {
					already = true
					break
				}
			}
			if !already {
				anomaly.Birthrights = append(anomaly.Birthrights, cmdb.Assignee{ID: birthrightID, Kind: cmdb.Birthright, Name: stringValue(record.Values[6])})
			}
		}
		if roleID := stringValue(record.Values[7]); roleID != "" {
			addPath("role")
			already := false
			for _, role := range anomaly.Roles {
				if role.ID == roleID {
					already = true
					break
				}
			}
			if !already {
				anomaly.Roles = append(anomaly.Roles, cmdb.Assignee{ID: roleID, Kind: cmdb.Role, Name: stringValue(record.Values[8])})
			}
		}
	}
	for _, query := range []string{birthrightQuery, roleQuery} {
		records, err := s.execute(ctx, false, query, nil)
		if err != nil {
			return nil, fmt.Errorf("access anomalies: %w", err)
		}
		for _, record := range records {
			addRecord(record)
		}
	}
	// Prefer a stable path order: birthright, role, direct.
	pathOrder := map[string]int{"birthright": 0, "role": 1, "direct": 2}
	anomalies := make([]cmdb.AccessAnomaly, 0, len(order))
	for _, k := range order {
		anomaly := *byKey[k]
		sort.SliceStable(anomaly.Paths, func(i, j int) bool { return pathOrder[anomaly.Paths[i]] < pathOrder[anomaly.Paths[j]] })
		anomalies = append(anomalies, anomaly)
	}
	sort.SliceStable(anomalies, func(i, j int) bool {
		if anomalies[i].Identity.Name != anomalies[j].Identity.Name {
			return anomalies[i].Identity.Name < anomalies[j].Identity.Name
		}
		if anomalies[i].Identity.ID != anomalies[j].Identity.ID {
			return anomalies[i].Identity.ID < anomalies[j].Identity.ID
		}
		if anomalies[i].Item.Name != anomalies[j].Item.Name {
			return anomalies[i].Item.Name < anomalies[j].Item.Name
		}
		return anomalies[i].Item.ID < anomalies[j].Item.ID
	})
	return anomalies, nil
}

// settleAccessRequest closes the request once no item is still pending or
// awaiting fulfilment: fulfilled when at least one item was provisioned,
// denied when every item was refused. Either way the request, its run, and
// its tasks retire; the permissions links are the live record.
func settleAccessRequest(ctx context.Context, tx neo4j.ManagedTransaction, action accessAction) error {
	records, err := runQuery(ctx, tx, "MATCH (request:Request {id: $requestId})-[requested:REQUESTS_ACCESS]->() WITH collect(requested.decision) AS decisions "+
		"RETURN size([decision IN decisions WHERE decision IN $open]) AS open, size([decision IN decisions WHERE decision = $fulfilled]) AS fulfilled", map[string]any{"requestId": action.requestID, "open": []any{string(cmdb.ItemPending), string(cmdb.ItemApproved)}, "fulfilled": string(cmdb.ItemFulfilled)})
	if err != nil {
		return err
	}
	if len(records) == 0 || toInt(records[0].Values[0]) > 0 {
		return nil
	}
	state := cmdb.RequestDenied
	if toInt(records[0].Values[1]) > 0 {
		state = cmdb.RequestFulfilled
	}
	if _, err := runQuery(ctx, tx, "MATCH (run:WorkflowRun {id: $runId}), (request:Request {id: $requestId}) SET run.state = $completed, run.completedAt = $now, request.state = $state, request.fulfilledAt = $now", map[string]any{"runId": action.runID, "requestId": action.requestID, "completed": string(cmdb.RunCompleted), "state": string(state), "now": action.timestamp}); err != nil {
		return err
	}
	return closeOutRun(ctx, tx, action.runID, action.requestID, action.timestamp)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
