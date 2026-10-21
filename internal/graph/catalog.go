package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Service Catalog storage. A CatalogItem node holds the current published
// version of an item (its manifest as JSON plus the fields lists need), with
// links to its owner group, the groups it is visible to, and its generated
// approval workflow. Catalog requests are ordinary Request nodes.

func (s *Store) ResolveAssignees(ctx context.Context, reference string) ([]cmdb.Assignee, error) {
	records, err := s.execute(ctx, false, "MATCH (node) WHERE (node:Identity OR node:Group) AND "+active("node")+" AND (node.id = $reference OR node.name = $reference) "+
		"RETURN node.id AS id, node.name AS name, CASE WHEN node:Group THEN 'group' ELSE 'identity' END AS kind ORDER BY id", map[string]any{"reference": reference})
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", reference, err)
	}
	matches := make([]cmdb.Assignee, 0, len(records))
	for _, record := range records {
		matches = append(matches, cmdb.Assignee{ID: stringValue(record.Values[0]), Name: stringValue(record.Values[1]), Kind: cmdb.NodeKind(stringValue(record.Values[2]))})
	}
	return matches, nil
}

func (s *Store) IdentityGroups(ctx context.Context, identityID string) ([]string, error) {
	records, err := s.execute(ctx, false, "MATCH (group:Group)-[:MEMBER]->(identity:Identity {id: $id}) WHERE "+active("group")+" RETURN group.id ORDER BY group.id", map[string]any{"id": identityID})
	if err != nil {
		return nil, fmt.Errorf("list groups of %s: %w", identityID, err)
	}
	groups := make([]string, 0, len(records))
	for _, record := range records {
		groups = append(groups, stringValue(record.Values[0]))
	}
	return groups, nil
}

// SaveCatalogItem stores a published version as the item's current state,
// creating the item the first time its name is published. Owner, visibility,
// and approval workflow links are definitional, so they are rebuilt.
func (s *Store) SaveCatalogItem(ctx context.Context, manifest cmdb.CatalogManifest, manifestJSON, workflowID, publishedBy string) (*cmdb.CatalogItemView, error) {
	timestamp := now()
	var id string
	err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		existing, err := runQuery(ctx, tx, "MATCH (item:CatalogItem {name: $name}) WHERE "+active("item")+" RETURN item.id", map[string]any{"name": manifest.Name})
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			id = stringValue(existing[0].Values[0])
		} else {
			if id, err = nextID(ctx, tx, cmdb.CatalogItem); err != nil {
				return err
			}
			if _, err := runQuery(ctx, tx, "CREATE (item:CatalogItem {id: $id, status: 'active', createdAt: $now})", map[string]any{"id": id, "now": timestamp}); err != nil {
				return err
			}
		}
		properties := map[string]any{"name": manifest.Name, "version": manifest.Version, "title": manifest.Title, "description": manifest.Description, "taskQueue": manifest.Target.TaskQueue, "workflowType": manifest.Target.WorkflowType, "slaBusinessDays": manifest.SLABusinessDays, "manifest": manifestJSON, "workflowId": workflowID, "source": manifest.Source, "publishedAt": timestamp, "publishedBy": publishedBy}
		if _, err := runQuery(ctx, tx, "MATCH (item:CatalogItem {id: $id}) SET item += $properties "+
			"WITH item OPTIONAL MATCH (item)-[link:CATALOG_OWNED_BY|VISIBLE_TO|APPROVED_THROUGH]->() DELETE link", map[string]any{"id": id, "properties": properties}); err != nil {
			return err
		}
		linked, err := runQuery(ctx, tx, "MATCH (item:CatalogItem {id: $id}), (owner:Group {id: $owner}) WHERE "+active("owner")+" CREATE (item)-[:CATALOG_OWNED_BY {status: 'active'}]->(owner) RETURN owner.id", map[string]any{"id": id, "owner": manifest.Owner})
		if err != nil {
			return err
		}
		if len(linked) == 0 {
			return fmt.Errorf("%w: owner %s is not an active group", cmdb.ErrInvalid, manifest.Owner)
		}
		if _, err := runQuery(ctx, tx, "MATCH (item:CatalogItem {id: $id}) UNWIND $groups AS groupId MATCH (group:Group {id: groupId}) WHERE "+active("group")+" CREATE (item)-[:VISIBLE_TO {status: 'active'}]->(group)", map[string]any{"id": id, "groups": toAnyList(manifest.Visibility)}); err != nil {
			return err
		}
		if workflowID != "" {
			if _, err := runQuery(ctx, tx, "MATCH (item:CatalogItem {id: $id}), (workflow:Workflow {id: $workflowId}) CREATE (item)-[:APPROVED_THROUGH {status: 'active'}]->(workflow)", map[string]any{"id": id, "workflowId": workflowID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("save catalog item: %w", err)
	}
	return s.GetCatalogItem(ctx, id)
}

// catalogItemQuery reads items with their owner and visibility. Callers supply
// the WHERE conditions.
func catalogItemQuery(conditions string) string {
	return "MATCH (item:CatalogItem) WHERE " + conditions + " " +
		"OPTIONAL MATCH (item)-[:CATALOG_OWNED_BY]->(owner:Group) " +
		"OPTIONAL MATCH (item)-[:VISIBLE_TO]->(visible:Group) WHERE " + active("visible") + " " +
		"WITH item, owner, collect(DISTINCT {id: visible.id, name: visible.name, kind: 'group'}) AS visibility " +
		"RETURN item, CASE WHEN owner IS NULL THEN null ELSE {id: owner.id, name: owner.name, kind: 'group'} END AS owner, visibility ORDER BY item.title, item.id"
}

func (s *Store) GetCatalogItem(ctx context.Context, id string) (*cmdb.CatalogItemView, error) {
	return s.oneCatalogItem(ctx, "item.id = $id", map[string]any{"id": id})
}

func (s *Store) GetCatalogItemByName(ctx context.Context, name string) (*cmdb.CatalogItemView, error) {
	return s.oneCatalogItem(ctx, "item.name = $name AND "+active("item"), map[string]any{"name": name})
}

func (s *Store) oneCatalogItem(ctx context.Context, conditions string, params map[string]any) (*cmdb.CatalogItemView, error) {
	records, err := s.execute(ctx, false, catalogItemQuery(conditions), params)
	if err != nil {
		return nil, fmt.Errorf("get catalog item: %w", err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeCatalogItem(records[0])
}

func (s *Store) CatalogManifest(ctx context.Context, id string) (string, error) {
	records, err := s.execute(ctx, false, "MATCH (item:CatalogItem {id: $id}) RETURN item.manifest", map[string]any{"id": id})
	if err != nil {
		return "", fmt.Errorf("read catalog manifest: %w", err)
	}
	if len(records) == 0 {
		return "", cmdb.ErrNotFound
	}
	return stringValue(records[0].Values[0]), nil
}

func (s *Store) ListCatalogItems(ctx context.Context, identityID string) ([]cmdb.CatalogItemView, error) {
	conditions := active("item")
	if identityID != "" {
		conditions += " AND EXISTS { MATCH (item)-[:VISIBLE_TO|CATALOG_OWNED_BY]->(group:Group)-[:MEMBER]->(:Identity {id: $identityId}) WHERE " + active("group") + " }"
	}
	records, err := s.execute(ctx, false, catalogItemQuery(conditions), map[string]any{"identityId": identityID})
	if err != nil {
		return nil, fmt.Errorf("list catalog items: %w", err)
	}
	items := make([]cmdb.CatalogItemView, 0, len(records))
	for _, record := range records {
		item, err := decodeCatalogItem(record)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, nil
}

func decodeCatalogItem(record *neo4j.Record) (*cmdb.CatalogItemView, error) {
	node, ok := record.Values[0].(neo4j.Node)
	if !ok {
		return nil, fmt.Errorf("unexpected catalog item result type %T", record.Values[0])
	}
	var manifest cmdb.CatalogManifest
	if err := json.Unmarshal([]byte(stringProperty(node.Props, "manifest")), &manifest); err != nil {
		return nil, fmt.Errorf("decode catalog item %s: %w", stringProperty(node.Props, "id"), err)
	}
	item := &cmdb.CatalogItemView{
		ID:              stringProperty(node.Props, "id"),
		Name:            manifest.Name,
		Version:         manifest.Version,
		Title:           manifest.Title,
		Description:     manifest.Description,
		Visibility:      []cmdb.Assignee{},
		Approvals:       manifest.Approvals,
		SLABusinessDays: manifest.SLABusinessDays,
		Inputs:          manifest.Inputs,
		Outputs:         manifest.Outputs,
		Target:          manifest.Target,
		WorkflowID:      stringProperty(node.Props, "workflowId"),
		Source:          manifest.Source,
		PublishedAt:     stringProperty(node.Props, "publishedAt"),
		PublishedBy:     stringProperty(node.Props, "publishedBy"),
		Status:          stringProperty(node.Props, "status"),
	}
	if item.Approvals == nil {
		item.Approvals = []cmdb.CatalogApproval{}
	}
	if owners := decodeAssignees([]any{record.Values[1]}); len(owners) > 0 {
		item.Owner = owners[0]
	}
	item.Visibility = append(item.Visibility, decodeAssignees(record.Values[2])...)
	return item, nil
}

// CreateCatalogRequest writes a catalog request with its requester, item, and
// referenced CIs. With an approval workflow it opens a run and raises the
// first approval task in the same write; otherwise the request goes straight
// to in-progress for the service to hand to the team's workflow.
func (s *Store) CreateCatalogRequest(ctx context.Context, record cmdb.CatalogRequestRecord) (*cmdb.Node, error) {
	timestamp := now()
	var requestID string
	err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		generated, err := nextID(ctx, tx, cmdb.Request)
		if err != nil {
			return err
		}
		requestID = generated
		state := cmdb.RequestInProgress
		if record.WorkflowID != "" {
			state = cmdb.RequestInReview
		}
		// The item's version, target, and inputs are copied onto the request so
		// it is fulfilled as submitted even if the item is republished meanwhile.
		properties := map[string]any{
			"requestType":                    string(cmdb.CatalogRequest),
			"state":                          string(state),
			"name":                           record.Item.Title,
			"submittedAt":                    timestamp,
			cmdb.CatalogItemProperty:         record.Item.Name,
			cmdb.CatalogItemVersionProperty:  record.Item.Version,
			cmdb.CatalogTaskQueueProperty:    record.Item.Target.TaskQueue,
			cmdb.CatalogWorkflowTypeProperty: record.Item.Target.WorkflowType,
			cmdb.CatalogInputsProperty:       record.InputsJSON,
			"slaEnabled":                     record.SLABusinessDays > 0,
			"slaBusinessDays":                record.SLABusinessDays,
		}
		if record.SLADueAt != "" {
			properties["slaDueAt"] = record.SLADueAt
		}
		created, err := runQuery(ctx, tx, "MATCH (requester:Identity {id: $requestedBy}), (item:CatalogItem {id: $itemId}) WHERE "+active("requester")+" AND "+active("item")+" "+
			"CREATE (request:Request {id: $requestId, status: 'active'}) SET request += $properties "+
			"CREATE (requester)-[:FORM_SUBMITTED {status: 'active'}]->(request) CREATE (request)-[:FOR_CATALOG_ITEM {status: 'active', version: $version}]->(item) "+
			"RETURN request.id", map[string]any{"requestedBy": record.RequestedByID, "itemId": record.Item.ID, "requestId": requestID, "properties": properties, "version": record.Item.Version})
		if err != nil {
			return err
		}
		if len(created) == 0 {
			return fmt.Errorf("%w: the requester and the catalog item must both be active", cmdb.ErrInvalid)
		}
		if _, err := runQuery(ctx, tx, "MATCH (request:Request {id: $requestId}) UNWIND $references AS ciId MATCH (ci:CI {id: ciId}) WHERE "+active("ci")+" CREATE (request)-[:REFERENCES {status: 'active'}]->(ci)", map[string]any{"requestId": requestID, "references": toAnyList(record.ReferencedCIs)}); err != nil {
			return err
		}
		if record.WorkflowID == "" {
			return nil
		}
		runID, err := nextID(ctx, tx, cmdb.WorkflowRun)
		if err != nil {
			return err
		}
		first, err := runQuery(ctx, tx, "MATCH (request:Request {id: $requestId}), (workflow:Workflow {id: $workflowId}) WHERE "+active("workflow")+" "+
			"MATCH (workflow)-[has:HAS_STEP]->(first:WorkflowStep) WHERE "+active("has")+" AND "+active("first")+" "+
			"WITH request, workflow, first ORDER BY first.order LIMIT 1 "+
			"CREATE (run:WorkflowRun {id: $runId, status: 'active', state: $runState, name: workflow.name, startedAt: $now, currentOrder: first.order}) "+
			"CREATE (run)-[:RUN_FOR {status: 'active'}]->(request) CREATE (run)-[:INSTANCE_OF {status: 'active'}]->(workflow) "+
			"RETURN first.id", map[string]any{"requestId": requestID, "workflowId": record.WorkflowID, "runId": runID, "runState": string(cmdb.RunActive), "now": timestamp})
		if err != nil {
			return err
		}
		if len(first) == 0 {
			return fmt.Errorf("%w: the approval workflow %s of %s has no active steps", cmdb.ErrInvalid, record.WorkflowID, record.Item.Name)
		}
		return createTask(ctx, tx, runID, stringValue(first[0].Values[0]), timestamp)
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("create catalog request: %w", err)
	}
	return s.GetNode(ctx, cmdb.Request, requestID, false)
}

// SettleCatalogRequest records how the team's workflow ended on an
// in-progress request: fulfilled with its outputs, or failed with why. A
// request that is no longer in progress is returned unchanged, so repeating
// the call (an activity retry) is harmless.
func (s *Store) SettleCatalogRequest(ctx context.Context, requestID string, outcome cmdb.CatalogOutcome) (*cmdb.Node, error) {
	properties := map[string]any{}
	if outcome.Succeeded {
		properties["state"] = string(cmdb.RequestFulfilled)
		properties["fulfilledAt"] = now()
		outputs, err := json.Marshal(outcome.Outputs)
		if err != nil {
			return nil, fmt.Errorf("encode outputs of request %s: %w", requestID, err)
		}
		properties[cmdb.CatalogOutputsProperty] = string(outputs)
	} else {
		properties["state"] = string(cmdb.RequestFailed)
		properties["failedAt"] = now()
		properties[cmdb.FailureReasonProperty] = outcome.Error
	}
	if _, err := s.execute(ctx, true, "MATCH (request:Request {id: $id}) WHERE "+active("request")+" AND request.requestType = $catalog AND request.state = $inProgress SET request += $properties", map[string]any{"id": requestID, "catalog": string(cmdb.CatalogRequest), "inProgress": string(cmdb.RequestInProgress), "properties": properties}); err != nil {
		return nil, fmt.Errorf("settle catalog request %s: %w", requestID, err)
	}
	return s.GetNode(ctx, cmdb.Request, requestID, false)
}

func (s *Store) GroupMembers(ctx context.Context, groupIDs []string) (map[string][]cmdb.Assignee, error) {
	records, err := s.execute(ctx, false, "UNWIND $ids AS groupId MATCH (group:Group {id: groupId})-[:MEMBER]->(member:Identity) WHERE "+active("group")+" AND "+active("member")+" "+
		"RETURN group.id, member.id, member.name ORDER BY member.name, member.id", map[string]any{"ids": toAnyList(groupIDs)})
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	members := map[string][]cmdb.Assignee{}
	for _, record := range records {
		groupID := stringValue(record.Values[0])
		members[groupID] = append(members[groupID], cmdb.Assignee{ID: stringValue(record.Values[1]), Name: stringValue(record.Values[2]), Kind: cmdb.Identity})
	}
	return members, nil
}

// CatalogRequestSummaries reads every request raised from an item, retired
// ones included, for the Workflow Analyzer.
func (s *Store) CatalogRequestSummaries(ctx context.Context, itemName string) ([]cmdb.CatalogRequestSummary, error) {
	records, err := s.execute(ctx, false, "MATCH (request:Request) WHERE request.requestType = $catalog AND request.catalogItem = $name "+
		"RETURN request.id, request.state, request.catalogItemVersion, request.submittedAt, request.approvedAt, coalesce(request.fulfilledAt, request.failedAt, request.deniedAt), request.failureReason",
		map[string]any{"catalog": string(cmdb.CatalogRequest), "name": itemName})
	if err != nil {
		return nil, fmt.Errorf("read catalog requests of %s: %w", itemName, err)
	}
	summaries := make([]cmdb.CatalogRequestSummary, 0, len(records))
	for _, record := range records {
		summaries = append(summaries, cmdb.CatalogRequestSummary{
			ID: stringValue(record.Values[0]), State: cmdb.RequestState(stringValue(record.Values[1])), Version: stringValue(record.Values[2]),
			SubmittedAt: stringValue(record.Values[3]), ApprovedAt: stringValue(record.Values[4]), FinishedAt: stringValue(record.Values[5]), FailureReason: stringValue(record.Values[6]),
		})
	}
	return summaries, nil
}
