package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/config"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Store struct {
	driver   neo4j.DriverWithContext
	database string
}

func Open(ctx context.Context, cfg config.Config) (*Store, error) {
	driver, err := neo4j.NewDriverWithContext(cfg.URI, neo4j.BasicAuth(cfg.Username, cfg.Password, ""))
	if err != nil {
		return nil, fmt.Errorf("create Neo4j driver: %w", err)
	}
	if err := driver.VerifyConnectivity(ctx); err != nil {
		_ = driver.Close(ctx)
		return nil, fmt.Errorf("connect to Neo4j: %w", err)
	}
	return &Store{driver: driver, database: cfg.Database}, nil
}

func (s *Store) Close(ctx context.Context) error {
	return s.driver.Close(ctx)
}

func (s *Store) execute(ctx context.Context, write bool, query string, parameters map[string]any) ([]*neo4j.Record, error) {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)

	work := func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, query, parameters)
		if err != nil {
			return nil, err
		}
		return result.Collect(ctx)
	}
	var result any
	var err error
	if write {
		result, err = session.ExecuteWrite(ctx, work)
	} else {
		result, err = session.ExecuteRead(ctx, work)
	}
	if err != nil {
		return nil, err
	}
	records, ok := result.([]*neo4j.Record)
	if !ok {
		return nil, fmt.Errorf("unexpected Neo4j result type %T", result)
	}
	return records, nil
}

// executeAutocommit runs a statement outside a managed transaction, which is
// what `CALL { … } IN TRANSACTIONS` batch migrations need.
func (s *Store) executeAutocommit(ctx context.Context, query string, parameters map[string]any) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.database})
	defer session.Close(ctx)
	result, err := session.Run(ctx, query, parameters)
	if err != nil {
		return err
	}
	_, err = result.Consume(ctx)
	return err
}

// relationshipListCap bounds how many direct relationships a listed record
// carries: enough for the list views' summary column, without letting one
// densely connected record (a group with every identity as a member) make a
// page read expand everything attached to it. Get returns the full set.
const relationshipListCap = 200

// listedRelationshipMap renders one relationship (bound as `relationship`)
// the way the list and get reads return it: type, both endpoint ids, the
// endpoints' display names so people see names rather than ids, and properties.
const listedRelationshipMap = "{type: type(relationship), fromId: coalesce(startNode(relationship).id, startNode(relationship).code), toId: coalesce(endNode(relationship).id, endNode(relationship).code), fromName: coalesce(startNode(relationship).name, startNode(relationship).title, startNode(relationship).department), toName: coalesce(endNode(relationship).name, endNode(relationship).title, endNode(relationship).department), properties: properties(relationship)}"

// relationshipTypes lists the Neo4j types a read should traverse: the live
// types, plus their retired twins when retired history is wanted.
func relationshipTypes(includeRetired bool) string {
	names := cmdb.RelationshipTypeNames()
	types := make([]string, 0, len(names)*2)
	for _, name := range names {
		types = append(types, name)
		if includeRetired {
			types = append(types, cmdb.RetiredTypeName(name))
		}
	}
	return strings.Join(types, "|")
}

// liveOrRetired is "TYPE|TYPE_RETIRED": for reads that must see history.
func liveOrRetired(typeName string) string {
	return typeName + "|" + cmdb.RetiredTypeName(typeName)
}

// EnsureConstraints creates the schema (uniqueness constraints and the status
// and state indexes the read predicates rely on) and applies the one-time data
// migrations older graphs need. `servicemap serve` runs it at startup and
// `servicemap init` runs it on demand; both are safe to repeat.
func (s *Store) EnsureConstraints(ctx context.Context) error {
	for _, kind := range cmdb.NodeKinds() {
		label, key, err := cmdb.NodeDefinition(kind)
		if err != nil {
			return err
		}
		constraintName := strings.ReplaceAll(string(kind), "-", "_") + "_key_unique"
		query := fmt.Sprintf("CREATE CONSTRAINT %s IF NOT EXISTS FOR (node:%s) REQUIRE node.%s IS UNIQUE", constraintName, label, key)
		if _, err := s.execute(ctx, true, query, nil); err != nil {
			return fmt.Errorf("create uniqueness constraint for %s: %w", kind, err)
		}
	}
	if _, err := s.execute(ctx, true, "CREATE CONSTRAINT holiday_date_unique IF NOT EXISTS FOR (holiday:Holiday) REQUIRE holiday.date IS UNIQUE", nil); err != nil {
		return fmt.Errorf("create holiday date constraint: %w", err)
	}
	if _, err := s.execute(ctx, true, "CREATE CONSTRAINT cmdb_migration_name_unique IF NOT EXISTS FOR (migration:CMDBMigration) REQUIRE migration.name IS UNIQUE", nil); err != nil {
		return fmt.Errorf("create migration marker constraint: %w", err)
	}
	if err := s.migrateLegacyRelationships(ctx); err != nil {
		return err
	}
	if err := s.migrateLegacyCITypes(ctx); err != nil {
		return err
	}
	// Order matters: the reads filter on `status = 'active'` (so the status
	// index serves them) and traverse live types only, which is correct once
	// every record has a status and every retired edge sits under its retired type.
	if err := s.runMigration(ctx, "status-backfill", s.backfillStatus); err != nil {
		return err
	}
	if err := s.runMigration(ctx, "retired-relationship-types", s.moveRetiredRelationships); err != nil {
		return err
	}
	if err := s.ensureIndexes(ctx); err != nil {
		return err
	}
	return s.ensureSuperAdminIdentity(ctx)
}

// ensureSuperAdminIdentity keeps an Identity record for the platform super
// admin, so its approvals, requests, and other actions link to a person like
// everyone else's.
func (s *Store) ensureSuperAdminIdentity(ctx context.Context) error {
	if _, err := s.execute(ctx, true, "MERGE (admin:Identity {id: $id}) ON CREATE SET admin.name = $name, admin.title = $title, admin.createdAt = $now "+
		"SET admin.status = 'active' REMOVE admin.retiredAt", map[string]any{"id": cmdb.PlatformSuperAdminID, "name": cmdb.PlatformSuperAdminName, "title": "Platform administrator (system account)", "now": now()}); err != nil {
		return fmt.Errorf("ensure super admin identity: %w", err)
	}
	return nil
}

// runMigration applies a data migration once, recording a CMDBMigration
// marker so later startups skip it.
func (s *Store) runMigration(ctx context.Context, name string, migrate func(context.Context) error) error {
	records, err := s.execute(ctx, false, "MATCH (migration:CMDBMigration {name: $name}) RETURN migration.name", map[string]any{"name": name})
	if err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if len(records) > 0 {
		return nil
	}
	if err := migrate(ctx); err != nil {
		return fmt.Errorf("migration %s: %w", name, err)
	}
	if _, err := s.execute(ctx, true, "CREATE (:CMDBMigration {name: $name, appliedAt: $now})", map[string]any{"name": name, "now": now()}); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	return nil
}

// batched runs an updating query in committed batches when the server
// supports CALL … IN TRANSACTIONS, falling back to a single transaction.
// `body` must bind the rows to update (as `row`), e.g. "MATCH (row:Identity) WHERE row.status IS NULL".
func (s *Store) batched(ctx context.Context, body, update string) error {
	err := s.executeAutocommit(ctx, body+" CALL { WITH row "+update+" } IN TRANSACTIONS OF 10000 ROWS", nil)
	if err == nil {
		return nil
	}
	_, fallbackErr := s.execute(ctx, true, body+" "+update, nil)
	if fallbackErr != nil {
		return fmt.Errorf("%w (batched attempt: %v)", fallbackErr, err)
	}
	return nil
}

// backfillStatus gives every record and live edge written before status was
// mandatory an explicit 'active', so reads can test equality instead of coalescing.
func (s *Store) backfillStatus(ctx context.Context) error {
	for _, kind := range cmdb.NodeKinds() {
		label, _, err := cmdb.NodeDefinition(kind)
		if err != nil {
			return err
		}
		if err := s.batched(ctx, fmt.Sprintf("MATCH (row:%s) WHERE row.status IS NULL", label), "SET row.status = 'active'"); err != nil {
			return fmt.Errorf("backfill %s status: %w", kind, err)
		}
	}
	for _, typeName := range cmdb.RelationshipTypeNames() {
		if err := s.batched(ctx, fmt.Sprintf("MATCH ()-[row:%s]->() WHERE row.status IS NULL", typeName), "SET row.status = 'active'"); err != nil {
			return fmt.Errorf("backfill %s status: %w", typeName, err)
		}
	}
	return nil
}

// moveRetiredRelationships rewrites edges retired in place under their live
// type into the kind's retired type, keeping direction and properties.
func (s *Store) moveRetiredRelationships(ctx context.Context) error {
	for _, typeName := range cmdb.RelationshipTypeNames() {
		body := fmt.Sprintf("MATCH (a)-[row:%s]->(b) WHERE row.status = 'retired' WITH row, a, b", typeName)
		update := fmt.Sprintf("CREATE (a)-[retired:%s]->(b) SET retired = properties(row) DELETE row", cmdb.RetiredTypeName(typeName))
		err := s.executeAutocommit(ctx, body+" CALL { WITH row, a, b "+update+" } IN TRANSACTIONS OF 5000 ROWS", nil)
		if err != nil {
			if _, fallbackErr := s.execute(ctx, true, body+" "+update, nil); fallbackErr != nil {
				return fmt.Errorf("move retired %s edges: %w (batched attempt: %v)", typeName, fallbackErr, err)
			}
		}
	}
	return nil
}

// ensureIndexes creates the property indexes the read predicates use: status
// on every record label, and state on the records the queues filter by.
func (s *Store) ensureIndexes(ctx context.Context) error {
	for _, kind := range cmdb.NodeKinds() {
		label, _, err := cmdb.NodeDefinition(kind)
		if err != nil {
			return err
		}
		name := strings.ReplaceAll(string(kind), "-", "_")
		if _, err := s.execute(ctx, true, fmt.Sprintf("CREATE INDEX %s_status_idx IF NOT EXISTS FOR (node:%s) ON (node.status)", name, label), nil); err != nil {
			return fmt.Errorf("create %s status index: %w", kind, err)
		}
	}
	for _, kind := range []cmdb.NodeKind{cmdb.Task, cmdb.Request, cmdb.WorkflowRun} {
		label, _, err := cmdb.NodeDefinition(kind)
		if err != nil {
			return err
		}
		name := strings.ReplaceAll(string(kind), "-", "_")
		if _, err := s.execute(ctx, true, fmt.Sprintf("CREATE INDEX %s_state_idx IF NOT EXISTS FOR (node:%s) ON (node.state)", name, label), nil); err != nil {
			return fmt.Errorf("create %s state index: %w", kind, err)
		}
	}
	return nil
}

// migrateLegacyCITypes rewrites CI records stored under a retired ciType value
// (for example business-process) to the current value.
func (s *Store) migrateLegacyCITypes(ctx context.Context) error {
	label, _, err := cmdb.NodeDefinition(cmdb.CI)
	if err != nil {
		return err
	}
	for _, legacy := range cmdb.LegacyCITypes() {
		query := fmt.Sprintf("MATCH (ci:%s {ciType: $oldType}) SET ci.ciType = $newType", label)
		if _, err := s.execute(ctx, true, query, map[string]any{"oldType": legacy.OldType, "newType": string(legacy.NewType)}); err != nil {
			return fmt.Errorf("migrate %s CIs to %s: %w", legacy.OldType, legacy.NewType, err)
		}
	}
	return nil
}

// migrateLegacyRelationships rewrites edges stored under retired relationship
// types (for example HOSTED_ON, USED_BY) into their current forward type so
// every relationship is stored once in the canonical direction.
func (s *Store) migrateLegacyRelationships(ctx context.Context) error {
	for _, legacy := range cmdb.LegacyRelationshipTypes() {
		pattern := "(a)-[current:%s]->(b)"
		if legacy.Flip {
			pattern = "(b)-[current:%s]->(a)"
		}
		query := fmt.Sprintf("MATCH (a)-[legacy:%s]->(b) MERGE "+pattern+" ON CREATE SET current = properties(legacy) DELETE legacy", legacy.OldType, legacy.NewType)
		if _, err := s.execute(ctx, true, query, nil); err != nil {
			return fmt.Errorf("migrate %s relationships to %s: %w", legacy.OldType, legacy.NewType, err)
		}
	}
	return nil
}

// isConstraintViolation reports a uniqueness constraint failure, which for a
// generated ID means an (astronomically unlikely) collision worth one retry.
func isConstraintViolation(err error) bool {
	var neo4jErr *neo4j.Neo4jError
	return errors.As(err, &neo4jErr) && neo4jErr.Code == "Neo.ClientError.Schema.ConstraintValidationFailed"
}

func (s *Store) CreateGeneratedNode(ctx context.Context, kind cmdb.NodeKind, properties map[string]any, ciIDs []string) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	if !cmdb.RequiresGeneratedID(kind) {
		return nil, fmt.Errorf("%w: %s IDs cannot be generated", cmdb.ErrInvalid, kind)
	}
	var node *cmdb.Node
	for attempt := 0; attempt < 8; attempt++ {
		var id string
		if err := s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
			generated, err := nextID(ctx, tx, kind)
			if err != nil {
				return err
			}
			id = generated
			return nil
		}); err != nil {
			return nil, err
		}
		node, err = s.createNodeWithID(ctx, kind, label, key, id, properties, ciIDs)
		if err == nil || !isConstraintViolation(err) {
			return node, err
		}
	}
	return nil, fmt.Errorf("create %s: could not draw a unique generated ID", kind)
}

func (s *Store) createNodeWithID(ctx context.Context, kind cmdb.NodeKind, label, key, id string, properties map[string]any, ciIDs []string) (*cmdb.Node, error) {
	linkedKind, linkedCIType, linkable := cmdb.LinkedCIRelationship(kind)
	if !linkable || len(ciIDs) == 0 {
		query := fmt.Sprintf("CREATE (node:%s {%s: $id, status: 'active'}) SET node += $properties RETURN node AS node", label, key)
		records, err := s.execute(ctx, true, query, map[string]any{"id": id, "properties": properties})
		if err != nil {
			return nil, fmt.Errorf("create %s with generated ID: %w", kind, err)
		}
		if len(records) == 0 {
			return nil, cmdb.ErrNotFound
		}
		return decodeNode(kind, key, records[0])
	}
	linkedDefinition, err := cmdb.RelationshipDefinitionFor(linkedKind)
	if err != nil {
		return nil, err
	}
	relationshipType := linkedDefinition.TypeName
	query := fmt.Sprintf("WITH $ciIDs AS ciIDs OPTIONAL MATCH (ci:CI) WHERE ci.id IN ciIDs WITH ciIDs, collect(ci) AS cis WHERE size(cis) = size(ciIDs) AND all(ci IN cis WHERE ci.status = 'active') AND ($ciType IS NULL OR all(ci IN cis WHERE ci.ciType = $ciType)) CREATE (node:%s {%s: $id, status: 'active'}) SET node += $properties WITH node, cis FOREACH (ci IN cis | CREATE (node)-[:%s {status: 'active'}]->(ci)) WITH node OPTIONAL MATCH (node)-[relationship:%s]->(ci:CI) RETURN node AS node, collect(CASE WHEN relationship IS NULL THEN null ELSE %s END) AS relationships", label, key, relationshipType, relationshipType, listedRelationshipMap)
	var ciTypeParam any
	if linkedCIType != "" {
		ciTypeParam = string(linkedCIType)
	}
	params := map[string]any{"id": id, "ciIDs": ciIDs, "properties": properties, "ciType": ciTypeParam}
	records, err := s.execute(ctx, true, query, params)
	if err != nil {
		return nil, fmt.Errorf("create %s with generated ID: %w", kind, err)
	}
	if len(records) == 0 {
		if linkedCIType != "" {
			return nil, fmt.Errorf("%w: every linked CI must exist, be active, and be a %s CI", cmdb.ErrInvalid, linkedCIType)
		}
		return nil, fmt.Errorf("%w: every linked CI must exist and be active", cmdb.ErrInvalid)
	}
	return decodeNodeWithRelationships(kind, key, records[0])
}

// GetNode reads one record with every direct relationship (live ones; retired
// edges too when includeRetired is set, which is how a retired record's
// history is reviewed).
func (s *Store) GetNode(ctx context.Context, kind cmdb.NodeKind, id string, includeRetired bool) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	filter := ""
	if !includeRetired {
		filter = " WHERE node.status = 'active'"
	}
	query := fmt.Sprintf("MATCH (node:%s {%s: $id})%s OPTIONAL MATCH (node)-[relationship:%s]-(other) WITH node, collect(CASE WHEN relationship IS NULL THEN null ELSE %s END) AS relationships RETURN node AS node, relationships", label, key, filter, relationshipTypes(includeRetired), listedRelationshipMap)
	records, err := s.execute(ctx, false, query, map[string]any{"id": id})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeNodeWithRelationships(kind, key, records[0])
}

// nodeListSource is the MATCH … WHERE prefix of a node list, binding `node`.
// Requests involving one identity are anchored at that identity rather than
// scanned, and the filters are plain equalities so the status index applies.
func nodeListSource(label, key string, options cmdb.ListOptions) (string, map[string]any) {
	params := map[string]any{}
	source := fmt.Sprintf("MATCH (node:%s)", label)
	if options.InvolvedID != "" {
		raised := "DRAFTED_REQUEST|FORM_SUBMITTED"
		subject := "REQUESTED_FOR"
		if options.IncludeRetired {
			raised += "|DRAFTED_REQUEST_RETIRED|FORM_SUBMITTED_RETIRED"
			subject = liveOrRetired(subject)
		}
		source = fmt.Sprintf("MATCH (actor:Identity {id: $involvedId}) OPTIONAL MATCH (actor)-[:%s]->(raised:%s) OPTIONAL MATCH (forActor:%s)-[:%s]->(actor) WITH collect(DISTINCT raised) + collect(DISTINCT forActor) AS involved UNWIND involved AS node WITH DISTINCT node", raised, label, label, subject)
		params["involvedId"] = options.InvolvedID
	}
	conditions := make([]string, 0, 3)
	if !options.IncludeRetired {
		conditions = append(conditions, "node.status = 'active'")
	}
	if options.RequestType != "" {
		conditions = append(conditions, "node.requestType = $requestType")
		params["requestType"] = string(options.RequestType)
	}
	if options.Query != "" {
		conditions = append(conditions, fmt.Sprintf("(toLower(toString(node.%s)) CONTAINS $query OR any(name IN keys(node) WHERE toLower(toStringOrNull(node[name])) CONTAINS $query))", key))
		params["query"] = options.Query
	}
	return source + whereClause(conditions...), params
}

// pageClause renders SKIP/LIMIT for a page, or nothing when every record is wanted.
func pageClause(limit, offset int, params map[string]any) string {
	clause := ""
	if offset > 0 {
		clause += " SKIP $offset"
		params["offset"] = offset
	}
	if limit > 0 {
		clause += " LIMIT $limit"
		params["limit"] = limit
	}
	return clause
}

func (s *Store) ListNodes(ctx context.Context, kind cmdb.NodeKind, options cmdb.ListOptions) ([]cmdb.Node, cmdb.ListTotals, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, cmdb.ListTotals{}, err
	}
	source, params := nodeListSource(label, key, options)
	// Page first, then attach a bounded sample of each listed record's direct
	// relationships (with endpoint names) for the list views' summary column.
	query := fmt.Sprintf("%s WITH node ORDER BY node.%s%s CALL { WITH node OPTIONAL MATCH (node)-[relationship:%s]-(other) RETURN CASE WHEN relationship IS NULL THEN null ELSE %s END AS relationship LIMIT %d } WITH node, collect(relationship) AS relationships RETURN node AS node, relationships", source, key, pageClause(options.Limit, options.Offset, params), relationshipTypes(options.IncludeRetired), listedRelationshipMap, relationshipListCap)
	records, err := s.execute(ctx, false, query, params)
	if err != nil {
		return nil, cmdb.ListTotals{}, fmt.Errorf("list %s: %w", kind, err)
	}
	nodes := make([]cmdb.Node, 0, len(records))
	totals := cmdb.ListTotals{}
	for _, record := range records {
		node, err := decodeNodeWithRelationships(kind, key, record)
		if err != nil {
			return nil, cmdb.ListTotals{}, err
		}
		nodes = append(nodes, *node)
		totals.Total++
		if node.Status != "retired" {
			totals.Active++
		}
	}
	if options.Limit > 0 || options.Offset > 0 {
		// A page cannot count itself; count the whole match.
		totals, err = s.countTotals(ctx, source, params)
		if err != nil {
			return nil, cmdb.ListTotals{}, fmt.Errorf("count %s: %w", kind, err)
		}
	}
	return nodes, totals, nil
}

// countTotals counts the rows a list source matches, as total and active.
// `alias` in the source must be `node` or `relationship`; both carry status.
func (s *Store) countTotals(ctx context.Context, source string, params map[string]any) (cmdb.ListTotals, error) {
	alias := "node"
	if strings.Contains(source, "[relationship:") {
		alias = "relationship"
	}
	counted := map[string]any{}
	for name, value := range params {
		if name != "limit" && name != "offset" {
			counted[name] = value
		}
	}
	records, err := s.execute(ctx, false, fmt.Sprintf("%s RETURN count(%s) AS total, count(CASE WHEN %s.status = 'active' THEN 1 END) AS active", source, alias, alias), counted)
	if err != nil {
		return cmdb.ListTotals{}, err
	}
	if len(records) == 0 {
		return cmdb.ListTotals{}, nil
	}
	return cmdb.ListTotals{Total: toInt(records[0].Values[0]), Active: toInt(records[0].Values[1])}, nil
}

func (s *Store) UpdateNode(ctx context.Context, kind cmdb.NodeKind, id string, properties map[string]any) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (node:%s {%s: $id}) WHERE node.status = 'active' SET node += $properties RETURN node AS node", label, key)
	records, err := s.execute(ctx, true, query, map[string]any{"id": id, "properties": properties})
	if err != nil {
		return nil, fmt.Errorf("update %s: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeNode(kind, key, records[0])
}

// SubmitRequest moves an active draft request to submitted in one write: the
// properties are applied, state and submittedAt are set, every drafted-request
// link from a requester is retired (moved to its retired type), and a
// form-submitted link from the same requester is created. A draft with no
// active requester cannot be submitted.
func (s *Store) SubmitRequest(ctx context.Context, id string, properties map[string]any) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(cmdb.Request)
	if err != nil {
		return nil, err
	}
	requesterLabel, _, err := cmdb.NodeDefinition(cmdb.Identity)
	if err != nil {
		return nil, err
	}
	drafted, err := cmdb.RelationshipDefinitionFor(cmdb.DraftedRequest)
	if err != nil {
		return nil, err
	}
	submitted, err := cmdb.RelationshipDefinitionFor(cmdb.FormSubmitted)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (node:%s {%s: $id}) WHERE node.status = 'active' AND coalesce(node.state, '%s') = '%s' "+
		"MATCH (requester:%s)-[draft:%s]->(node) WHERE requester.status = 'active' "+
		"WITH node, collect(draft) AS drafts, collect(DISTINCT requester) AS requesters "+
		"SET node += $properties, node.state = '%s', node.submittedAt = $submittedAt REMOVE node.returnComment, node.returnedAt "+
		"WITH node, drafts, requesters UNWIND drafts AS draft WITH node, requesters, draft, startNode(draft) AS drafter "+
		"CREATE (drafter)-[retired:%s]->(node) SET retired = properties(draft) SET retired.status = 'retired', retired.retiredAt = $submittedAt DELETE draft "+
		"WITH DISTINCT node, requesters UNWIND requesters AS requester MERGE (requester)-[link:%s]->(node) ON CREATE SET link.status = 'active' "+
		"RETURN DISTINCT node AS node", label, key, cmdb.RequestDraft, cmdb.RequestDraft, requesterLabel, drafted.TypeName, cmdb.RequestSubmitted, cmdb.RetiredTypeName(drafted.TypeName), submitted.TypeName)
	records, err := s.execute(ctx, true, query, map[string]any{"id": id, "properties": properties, "submittedAt": now()})
	if err != nil {
		return nil, fmt.Errorf("submit request: %w", err)
	}
	if len(records) == 0 {
		// Work out why nothing matched so the caller gets a useful error.
		existing, err := s.GetNode(ctx, cmdb.Request, id, false)
		if err != nil {
			return nil, err
		}
		if existing.Properties["state"] == string(cmdb.RequestSubmitted) {
			return nil, fmt.Errorf("%w: request %s has already been submitted", cmdb.ErrInvalid, id)
		}
		return nil, fmt.Errorf("%w: request %s has no active requester; link one with %s before submitting", cmdb.ErrInvalid, id, cmdb.DraftedRequest)
	}
	return decodeNode(cmdb.Request, key, records[0])
}

// RetireNode marks a record retired and moves every relationship attached to
// it under the retired relationship types, in one transaction.
func (s *Store) RetireNode(ctx context.Context, kind cmdb.NodeKind, id string) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	timestamp := now()
	var node *cmdb.Node
	err = s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		records, err := runQuery(ctx, tx, fmt.Sprintf("MATCH (node:%s {%s: $id}) SET node.status = 'retired', node.retiredAt = coalesce(node.retiredAt, $retiredAt) RETURN node AS node", label, key), map[string]any{"id": id, "retiredAt": timestamp})
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return cmdb.ErrNotFound
		}
		node, err = decodeNode(kind, key, records[0])
		if err != nil {
			return err
		}
		return retireEdgesOf(ctx, tx, kind, []string{id}, nil, timestamp)
	})
	if err != nil {
		if errors.Is(err, cmdb.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("retire %s: %w", kind, err)
	}
	return node, nil
}

// retireEdgesOf moves every live relationship touching the given records of
// one kind under the retired relationship types, keeping direction and
// properties and stamping retiredAt. Only the types that can touch the kind
// are visited, and types in `except` are left alone (a fulfilled request keeps
// its live fulfilled-by link so the CI still reads where it came from).
func retireEdgesOf(ctx context.Context, tx neo4j.ManagedTransaction, kind cmdb.NodeKind, ids []string, except map[string]bool, timestamp string) error {
	if len(ids) == 0 {
		return nil
	}
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return err
	}
	for _, typeName := range cmdb.RelationshipTypeNamesFor(kind) {
		if except[typeName] {
			continue
		}
		match := fmt.Sprintf("MATCH (node:%s) WHERE node.%s IN $ids MATCH (node)-[r:%s]-()", label, key, typeName)
		if _, err := moveToRetired(ctx, tx, typeName, match, map[string]any{"ids": toAnyList(ids), "now": timestamp}); err != nil {
			return err
		}
	}
	return nil
}

// moveToRetired retires the live relationships `match` binds as `r`: each is
// recreated between the same endpoints under the type's retired twin with its
// properties, status retired, and retiredAt, then deleted. Returns each moved
// edge's endpoint ids and properties. params must carry `now`.
func moveToRetired(ctx context.Context, tx neo4j.ManagedTransaction, typeName, match string, params map[string]any) ([]*neo4j.Record, error) {
	query := match + " WITH DISTINCT r, startNode(r) AS a, endNode(r) AS b " +
		"CREATE (a)-[retired:" + cmdb.RetiredTypeName(typeName) + "]->(b) SET retired = properties(r) SET retired.status = 'retired', retired.retiredAt = coalesce(r.retiredAt, $now) DELETE r " +
		"RETURN coalesce(a.id, a.code) AS fromId, coalesce(b.id, b.code) AS toId, properties(retired) AS properties"
	return runQuery(ctx, tx, query, params)
}

// relationshipEndpoints resolves the Cypher needed to match both ends of a
// relationship. An end that allows several node kinds (e.g. identity or group,
// or every RACI-owned kind) is matched without a label in the pattern and
// constrained in WHERE; when those kinds do not share a key property (job codes
// use code, the rest id) the id is matched through coalesce.
type relationshipEndpoints struct {
	source, target endpointSide
}

type endpointSide struct {
	pattern     string // "(target:Identity {id: $toId})" or "(target)" when the id is matched in the filter
	listPattern string // "(target:Identity)" or "(target)"
	keyExpr     string // "target.id" or "coalesce(target.id, target.code)"
	filter      string // "" or "(target:Identity OR target:Group) AND coalesce(...) = $toId" (pattern form)
	listFilter  string // "" or "(target:Identity OR target:Group)"
}

func endpointsFor(definition cmdb.RelationshipDefinition) (relationshipEndpoints, error) {
	source, err := endpointSideFor("source", "$fromId", definition.FromKinds)
	if err != nil {
		return relationshipEndpoints{}, err
	}
	target, err := endpointSideFor("target", "$toId", definition.ToKinds)
	if err != nil {
		return relationshipEndpoints{}, err
	}
	return relationshipEndpoints{source: source, target: target}, nil
}

func endpointSideFor(alias, param string, kinds []cmdb.NodeKind) (endpointSide, error) {
	labels := make([]string, 0, len(kinds))
	keys := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		label, key, err := cmdb.NodeDefinition(kind)
		if err != nil {
			return endpointSide{}, err
		}
		labels = append(labels, alias+":"+label)
		found := false
		for _, existing := range keys {
			if existing == key {
				found = true
				break
			}
		}
		if !found {
			keys = append(keys, key)
		}
	}
	side := endpointSide{keyExpr: fmt.Sprintf("%s.%s", alias, keys[0])}
	if len(keys) > 1 {
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, alias+"."+key)
		}
		side.keyExpr = "coalesce(" + strings.Join(parts, ", ") + ")"
	}
	if len(labels) == 1 {
		side.listPattern = "(" + labels[0] + ")"
		side.pattern = fmt.Sprintf("(%s {%s: %s})", labels[0], keys[0], param)
		return side, nil
	}
	side.listPattern = "(" + alias + ")"
	side.pattern = "(" + alias + ")"
	side.listFilter = "(" + strings.Join(labels, " OR ") + ")"
	side.filter = side.listFilter + " AND " + side.keyExpr + " = " + param
	return side, nil
}

// whereClause joins non-empty conditions into a WHERE clause, or returns "".
func whereClause(conditions ...string) string {
	parts := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		if condition != "" {
			parts = append(parts, condition)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(parts, " AND ")
}

func (s *Store) CreateRelationship(ctx context.Context, kind cmdb.RelationshipKind, fromID, toID string, properties map[string]any) (*cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	endpoints, err := endpointsFor(definition)
	if err != nil {
		return nil, err
	}
	typeFilter := ""
	params := map[string]any{"fromId": fromID, "toId": toID, "properties": properties}
	if len(definition.CITypeRules) > 0 {
		// The endpoints must satisfy at least one allowed (from, to) CI type pairing;
		// an empty side of a rule accepts any type at that end.
		typeFilter = " AND ANY(rule IN $ciTypeRules WHERE (size(rule.from) = 0 OR source.ciType IN rule.from) AND (size(rule.to) = 0 OR target.ciType IN rule.to))"
		rules := make([]map[string]any, 0, len(definition.CITypeRules))
		for _, rule := range definition.CITypeRules {
			rules = append(rules, map[string]any{"from": cmdb.CITypeNamesOf(rule.From), "to": cmdb.CITypeNamesOf(rule.To)})
		}
		params["ciTypeRules"] = rules
	}
	// Live edges are always active (retired ones live under the retired type),
	// so MERGE either finds the existing live link or creates it.
	query := fmt.Sprintf("MATCH %s, %s%s MERGE (source)-[relationship:%s]->(target) ON CREATE SET relationship.status = 'active' SET relationship += $properties RETURN properties(relationship) AS properties", endpoints.source.pattern, endpoints.target.pattern, whereClause(endpoints.source.filter, endpoints.target.filter, "source.status = 'active' AND target.status = 'active'"+typeFilter), definition.TypeName)
	records, err := s.execute(ctx, true, query, params)
	if err != nil {
		return nil, fmt.Errorf("create %s relationship: %w", kind, err)
	}
	if len(records) == 0 {
		if len(definition.CITypeRules) > 1 {
			return nil, fmt.Errorf("%w: %s relationships must connect active records as %s", cmdb.ErrInvalid, kind, rulesDescription(definition))
		}
		if len(definition.CITypeRules) == 1 || len(definition.ToKinds) > 1 || len(definition.FromKinds) > 1 {
			return nil, fmt.Errorf("%w: %s relationships must start from an active %s and end on an active %s", cmdb.ErrInvalid, kind, endpointDescription(definition.FromKinds, definition.FromCITypes), endpointDescription(definition.ToKinds, definition.ToCITypes))
		}
		return nil, cmdb.ErrNotFound
	}
	return decodeRelationship(kind, fromID, toID, records[0])
}

// rulesDescription lists every allowed CI type pairing for error messages,
// e.g. "service or function CI to service or function CI, or vendor CI to application, server or printer CI".
func rulesDescription(definition cmdb.RelationshipDefinition) string {
	parts := make([]string, 0, len(definition.CITypeRules))
	for _, rule := range definition.CITypeRules {
		parts = append(parts, endpointDescription(definition.FromKinds, rule.From)+" to "+endpointDescription(definition.ToKinds, rule.To))
	}
	return strings.Join(parts, ", or ")
}

// endpointDescription names a relationship endpoint for error messages,
// e.g. "contract CI", "service or function CI", or "identity or group".
func endpointDescription(kinds []cmdb.NodeKind, ciTypes []cmdb.CIType) string {
	if len(ciTypes) > 0 {
		return strings.Join(cmdb.CITypeNamesOf(ciTypes), " or ") + " CI"
	}
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return strings.Join(names, " or ")
}

// GetRelationship reads the live link between two records, or with
// includeRetired the live link if there is one and otherwise the most recently
// retired one.
func (s *Store) GetRelationship(ctx context.Context, kind cmdb.RelationshipKind, fromID, toID string, includeRetired bool) (*cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	endpoints, err := endpointsFor(definition)
	if err != nil {
		return nil, err
	}
	types := definition.TypeName
	if includeRetired {
		types = liveOrRetired(definition.TypeName)
	}
	query := fmt.Sprintf("MATCH %s-[relationship:%s]->%s%s RETURN properties(relationship) AS properties ORDER BY CASE WHEN type(relationship) = '%s' THEN 0 ELSE 1 END, relationship.retiredAt DESC LIMIT 1", endpoints.source.pattern, types, endpoints.target.pattern, whereClause(endpoints.source.filter, endpoints.target.filter), definition.TypeName)
	params := map[string]any{"fromId": fromID, "toId": toID}
	records, err := s.execute(ctx, false, query, params)
	if err != nil {
		return nil, fmt.Errorf("get %s relationship: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeRelationship(kind, fromID, toID, records[0])
}

func (s *Store) ListRelationships(ctx context.Context, kind cmdb.RelationshipKind, options cmdb.ListOptions) ([]cmdb.Relationship, cmdb.ListTotals, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, cmdb.ListTotals{}, err
	}
	endpoints, err := endpointsFor(definition)
	if err != nil {
		return nil, cmdb.ListTotals{}, err
	}
	types := definition.TypeName
	if options.IncludeRetired {
		types = liveOrRetired(definition.TypeName)
	}
	params := map[string]any{}
	queryFilter := ""
	if options.Query != "" {
		queryFilter = fmt.Sprintf("(toLower(%s) CONTAINS $query OR toLower(%s) CONTAINS $query OR any(name IN keys(relationship) WHERE toLower(toStringOrNull(relationship[name])) CONTAINS $query))", endpoints.source.keyExpr, endpoints.target.keyExpr)
		params["query"] = options.Query
	}
	source := fmt.Sprintf("MATCH %s-[relationship:%s]->%s%s", endpoints.source.listPattern, types, endpoints.target.listPattern, whereClause(endpoints.source.listFilter, endpoints.target.listFilter, queryFilter))
	query := fmt.Sprintf("%s WITH relationship, %s AS fromId, %s AS toId ORDER BY fromId, toId, relationship.retiredAt%s RETURN fromId, toId, properties(relationship) AS properties", source, endpoints.source.keyExpr, endpoints.target.keyExpr, pageClause(options.Limit, options.Offset, params))
	records, err := s.execute(ctx, false, query, params)
	if err != nil {
		return nil, cmdb.ListTotals{}, fmt.Errorf("list %s relationships: %w", kind, err)
	}
	relationships := make([]cmdb.Relationship, 0, len(records))
	totals := cmdb.ListTotals{}
	for _, record := range records {
		fromID := fmt.Sprint(record.Values[0])
		toID := fmt.Sprint(record.Values[1])
		relationship, err := decodeRelationship(kind, fromID, toID, record)
		if err != nil {
			return nil, cmdb.ListTotals{}, err
		}
		relationships = append(relationships, *relationship)
		totals.Total++
		if relationship.Status != "retired" {
			totals.Active++
		}
	}
	if options.Limit > 0 || options.Offset > 0 {
		totals, err = s.countTotals(ctx, source, params)
		if err != nil {
			return nil, cmdb.ListTotals{}, fmt.Errorf("count %s relationships: %w", kind, err)
		}
	}
	return relationships, totals, nil
}

func (s *Store) UpdateRelationship(ctx context.Context, kind cmdb.RelationshipKind, fromID, toID string, properties map[string]any) (*cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	endpoints, err := endpointsFor(definition)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH %s-[relationship:%s]->%s%s SET relationship += $properties RETURN properties(relationship) AS properties", endpoints.source.pattern, definition.TypeName, endpoints.target.pattern, whereClause(endpoints.source.filter, endpoints.target.filter))
	params := map[string]any{"fromId": fromID, "toId": toID, "properties": properties}
	records, err := s.execute(ctx, true, query, params)
	if err != nil {
		return nil, fmt.Errorf("update %s relationship: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeRelationship(kind, fromID, toID, records[0])
}

// RetireRelationship moves the live link under the kind's retired type.
func (s *Store) RetireRelationship(ctx context.Context, kind cmdb.RelationshipKind, fromID, toID string) (*cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	endpoints, err := endpointsFor(definition)
	if err != nil {
		return nil, err
	}
	match := fmt.Sprintf("MATCH %s-[r:%s]->%s%s", endpoints.source.pattern, definition.TypeName, endpoints.target.pattern, whereClause(endpoints.source.filter, endpoints.target.filter))
	var records []*neo4j.Record
	err = s.writeTx(ctx, func(tx neo4j.ManagedTransaction) error {
		moved, err := moveToRetired(ctx, tx, definition.TypeName, match, map[string]any{"fromId": fromID, "toId": toID, "now": now()})
		records = moved
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("retire %s relationship: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeRelationship(kind, fromID, toID, records[0])
}

func decodeNode(kind cmdb.NodeKind, key string, record *neo4j.Record) (*cmdb.Node, error) {
	value, ok := record.Get("node")
	if !ok {
		return nil, errors.New("Neo4j result did not contain a node")
	}
	node, ok := value.(neo4j.Node)
	if !ok {
		return nil, fmt.Errorf("unexpected node result type %T", value)
	}
	properties := make(map[string]any, len(node.Props))
	for name, property := range node.Props {
		if name != key && name != "status" && name != "retiredAt" {
			properties[name] = property
		}
	}
	status, _ := node.Props["status"].(string)
	if status == "" {
		status = "active"
	}
	return &cmdb.Node{Kind: kind, ID: fmt.Sprint(node.Props[key]), Properties: properties, Status: status, RetiredAt: stringProperty(node.Props, "retiredAt")}, nil
}

// decodeNodeWithRelationships decodes a `node, relationships` row.
func decodeNodeWithRelationships(kind cmdb.NodeKind, key string, record *neo4j.Record) (*cmdb.Node, error) {
	node, err := decodeNode(kind, key, record)
	if err != nil {
		return nil, err
	}
	value, ok := record.Get("relationships")
	if !ok {
		return nil, errors.New("Neo4j result did not contain node relationships")
	}
	node.Relationships, err = decodeListedRelationships(value)
	if err != nil {
		return nil, err
	}
	return node, nil
}

func decodeRelationship(kind cmdb.RelationshipKind, fromID, toID string, record *neo4j.Record) (*cmdb.Relationship, error) {
	value, ok := record.Get("properties")
	if !ok {
		return nil, errors.New("Neo4j result did not contain relationship properties")
	}
	properties, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected relationship result type %T", value)
	}
	return decodeRelationshipProperties(kind, fromID, toID, properties)
}

func decodeListedRelationships(value any) ([]cmdb.Relationship, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected relationship list type %T", value)
	}
	relationships := make([]cmdb.Relationship, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unexpected listed relationship type %T", value)
		}
		typeName, ok := item["type"].(string)
		if !ok {
			return nil, errors.New("listed relationship did not contain its type")
		}
		kind, retired, err := cmdb.ParseStoredRelationshipType(typeName)
		if err != nil {
			return nil, err
		}
		fromID, ok := item["fromId"].(string)
		if !ok {
			return nil, errors.New("listed relationship did not contain its source id")
		}
		toID, ok := item["toId"].(string)
		if !ok {
			return nil, errors.New("listed relationship did not contain its target id")
		}
		properties, ok := item["properties"].(map[string]any)
		if !ok {
			return nil, errors.New("listed relationship did not contain its properties")
		}
		relationship, err := decodeRelationshipProperties(kind, fromID, toID, properties)
		if err != nil {
			return nil, err
		}
		if retired {
			relationship.Status = "retired"
		}
		relationship.FromName = stringProperty(item, "fromName")
		relationship.ToName = stringProperty(item, "toName")
		relationships = append(relationships, *relationship)
	}
	return relationships, nil
}

func decodeRelationshipProperties(kind cmdb.RelationshipKind, fromID, toID string, properties map[string]any) (*cmdb.Relationship, error) {
	cleanProperties := make(map[string]any, len(properties))
	for name, property := range properties {
		if name != "status" && name != "retiredAt" {
			cleanProperties[name] = property
		}
	}
	status, _ := properties["status"].(string)
	if status == "" {
		status = "active"
	}
	return &cmdb.Relationship{Kind: kind, FromID: fromID, ToID: toID, Properties: cleanProperties, Status: status, RetiredAt: stringProperty(properties, "retiredAt")}, nil
}

func stringProperty(properties map[string]any, key string) string {
	value, _ := properties[key].(string)
	return value
}
