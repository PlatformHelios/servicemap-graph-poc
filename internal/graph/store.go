package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	if _, err := s.execute(ctx, true, "CREATE CONSTRAINT cmdb_counter_kind_unique IF NOT EXISTS FOR (counter:CMDBCounter) REQUIRE counter.kind IS UNIQUE", nil); err != nil {
		return fmt.Errorf("create CMDB counter constraint: %w", err)
	}
	return nil
}

func (s *Store) CreateGeneratedNode(ctx context.Context, kind cmdb.NodeKind, properties map[string]any, ciIDs []string) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	if !cmdb.RequiresGeneratedID(kind) {
		return nil, fmt.Errorf("%w: %s IDs cannot be generated", cmdb.ErrInvalid, kind)
	}
	prefix, err := cmdb.GeneratedIDPrefix(kind)
	if err != nil {
		return nil, err
	}
	if kind != cmdb.Incident && kind != cmdb.Change && kind != cmdb.Event {
		query := fmt.Sprintf("MERGE (counter:CMDBCounter {kind: $kind}) ON CREATE SET counter.value = 0 SET counter.value = counter.value + 1 WITH $prefix + '-' + right('000000' + toString(counter.value), 6) AS generatedID CREATE (node:%s {%s: generatedID, status: 'active'}) SET node += $properties RETURN node AS node", label, key)
		params := map[string]any{"kind": kind, "prefix": prefix, "properties": properties}
		records, err := s.execute(ctx, true, query, params)
		if err != nil {
			return nil, fmt.Errorf("create %s with generated ID: %w", kind, err)
		}
		if len(records) == 0 {
			return nil, cmdb.ErrNotFound
		}
		return decodeNode(kind, key, records[0])
	}
	relationshipType := ""
	switch kind {
	case cmdb.Incident:
		relationshipType = "AFFECTS"
	case cmdb.Change:
		relationshipType = "CHANGES"
	case cmdb.Event:
		relationshipType = "OBSERVED_ON"
	}
	query := fmt.Sprintf("WITH $ciIDs AS ciIDs OPTIONAL MATCH (ci:CI) WHERE ci.id IN ciIDs WITH ciIDs, collect(ci) AS cis WHERE size(cis) = size(ciIDs) AND all(ci IN cis WHERE coalesce(ci.status, 'active') <> 'retired') MERGE (counter:CMDBCounter {kind: $kind}) ON CREATE SET counter.value = 0 SET counter.value = counter.value + 1 WITH $prefix + '-' + right('000000' + toString(counter.value), 6) AS generatedID, cis CREATE (node:%s {%s: generatedID, status: 'active'}) SET node += $properties WITH node, cis FOREACH (ci IN cis | CREATE (node)-[:%s {status: 'active'}]->(ci)) OPTIONAL MATCH (node)-[relationship:%s]->(ci:CI) RETURN node AS node, collect(CASE WHEN relationship IS NULL THEN null ELSE {type: type(relationship), fromId: node.id, toId: ci.id, properties: properties(relationship)} END) AS relationships", label, key, relationshipType, relationshipType)
	params := map[string]any{"kind": kind, "prefix": prefix, "ciIDs": ciIDs, "properties": properties}
	records, err := s.execute(ctx, true, query, params)
	if err != nil {
		return nil, fmt.Errorf("create %s with generated ID: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%w: every linked CI must exist and be active", cmdb.ErrInvalid)
	}
	node, err := decodeNode(kind, "id", records[0])
	if err != nil {
		return nil, err
	}
	value, ok := records[0].Get("relationships")
	if !ok {
		return nil, errors.New("Neo4j result did not contain created CI relationships")
	}
	node.Relationships, err = decodeListedRelationships(value)
	if err != nil {
		return nil, err
	}
	return node, nil
}

func (s *Store) GetNode(ctx context.Context, kind cmdb.NodeKind, id string, includeRetired bool) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (node:%s {%s: $id}) WHERE $includeRetired OR coalesce(node.status, 'active') <> 'retired' RETURN node AS node", label, key)
	records, err := s.execute(ctx, false, query, map[string]any{"id": id, "includeRetired": includeRetired})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeNode(kind, key, records[0])
}

func (s *Store) ListNodes(ctx context.Context, kind cmdb.NodeKind, includeRetired bool) ([]cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (node:%s) WHERE $includeRetired OR coalesce(node.status, 'active') <> 'retired' OPTIONAL MATCH (node)-[relationship:HAS_JOB_CODE|QUALIFIES_FOR|GRANTS|INCLUDES|AFFECTS|CHANGES|OBSERVED_ON|DEPENDS_ON|HOSTED_ON|USES|USED_BY]-(other) WHERE $includeRetired OR coalesce(relationship.status, 'active') <> 'retired' WITH node, collect(CASE WHEN relationship IS NULL THEN null ELSE {type: type(relationship), fromId: coalesce(startNode(relationship).id, startNode(relationship).code), toId: coalesce(endNode(relationship).id, endNode(relationship).code), properties: properties(relationship)} END) AS relationships RETURN node AS node, relationships ORDER BY node.%s", label, key)
	records, err := s.execute(ctx, false, query, map[string]any{"includeRetired": includeRetired})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", kind, err)
	}
	nodes := make([]cmdb.Node, 0, len(records))
	for _, record := range records {
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
		nodes = append(nodes, *node)
	}
	return nodes, nil
}

func (s *Store) UpdateNode(ctx context.Context, kind cmdb.NodeKind, id string, properties map[string]any) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (node:%s {%s: $id}) WHERE coalesce(node.status, 'active') <> 'retired' SET node += $properties RETURN node AS node", label, key)
	records, err := s.execute(ctx, true, query, map[string]any{"id": id, "properties": properties})
	if err != nil {
		return nil, fmt.Errorf("update %s: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeNode(kind, key, records[0])
}

func (s *Store) RetireNode(ctx context.Context, kind cmdb.NodeKind, id string) (*cmdb.Node, error) {
	label, key, err := cmdb.NodeDefinition(kind)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (node:%s {%s: $id}) OPTIONAL MATCH (node)-[attached]-() WITH node, collect(attached) AS relationships SET node.status = 'retired', node.retiredAt = coalesce(node.retiredAt, $retiredAt) FOREACH (relationship IN relationships | SET relationship.status = 'retired', relationship.retiredAt = coalesce(relationship.retiredAt, $retiredAt)) RETURN node AS node", label, key)
	records, err := s.execute(ctx, true, query, map[string]any{"id": id, "retiredAt": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return nil, fmt.Errorf("retire %s: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeNode(kind, key, records[0])
}

func (s *Store) CreateRelationship(ctx context.Context, kind cmdb.RelationshipKind, fromID, toID string, properties map[string]any) (*cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	fromLabel, fromKey, err := cmdb.NodeDefinition(definition.From)
	if err != nil {
		return nil, err
	}
	toLabel, toKey, err := cmdb.NodeDefinition(definition.To)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (source:%s {%s: $fromId}), (target:%s {%s: $toId}) WHERE coalesce(source.status, 'active') <> 'retired' AND coalesce(target.status, 'active') <> 'retired' MERGE (source)-[relationship:%s]->(target) ON CREATE SET relationship.status = 'active' SET relationship += $properties WITH relationship WHERE coalesce(relationship.status, 'active') <> 'retired' RETURN properties(relationship) AS properties", fromLabel, fromKey, toLabel, toKey, definition.TypeName)
	records, err := s.execute(ctx, true, query, map[string]any{"fromId": fromID, "toId": toID, "properties": properties})
	if err != nil {
		return nil, fmt.Errorf("create %s relationship: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeRelationship(kind, fromID, toID, records[0])
}

func (s *Store) GetRelationship(ctx context.Context, kind cmdb.RelationshipKind, fromID, toID string, includeRetired bool) (*cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	fromLabel, fromKey, err := cmdb.NodeDefinition(definition.From)
	if err != nil {
		return nil, err
	}
	toLabel, toKey, err := cmdb.NodeDefinition(definition.To)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (source:%s {%s: $fromId})-[relationship:%s]->(target:%s {%s: $toId}) WHERE $includeRetired OR coalesce(relationship.status, 'active') <> 'retired' RETURN properties(relationship) AS properties", fromLabel, fromKey, definition.TypeName, toLabel, toKey)
	params := map[string]any{"fromId": fromID, "toId": toID, "includeRetired": includeRetired}
	records, err := s.execute(ctx, false, query, params)
	if err != nil {
		return nil, fmt.Errorf("get %s relationship: %w", kind, err)
	}
	if len(records) == 0 {
		return nil, cmdb.ErrNotFound
	}
	return decodeRelationship(kind, fromID, toID, records[0])
}

func (s *Store) ListRelationships(ctx context.Context, kind cmdb.RelationshipKind, includeRetired bool) ([]cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	fromLabel, fromKey, err := cmdb.NodeDefinition(definition.From)
	if err != nil {
		return nil, err
	}
	toLabel, toKey, err := cmdb.NodeDefinition(definition.To)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (source:%s)-[relationship:%s]->(target:%s) WHERE $includeRetired OR coalesce(relationship.status, 'active') <> 'retired' RETURN source.%s AS fromId, target.%s AS toId, properties(relationship) AS properties ORDER BY fromId, toId", fromLabel, definition.TypeName, toLabel, fromKey, toKey)
	records, err := s.execute(ctx, false, query, map[string]any{"includeRetired": includeRetired})
	if err != nil {
		return nil, fmt.Errorf("list %s relationships: %w", kind, err)
	}
	relationships := make([]cmdb.Relationship, 0, len(records))
	for _, record := range records {
		fromID := fmt.Sprint(record.Values[0])
		toID := fmt.Sprint(record.Values[1])
		relationship, err := decodeRelationship(kind, fromID, toID, record)
		if err != nil {
			return nil, err
		}
		relationships = append(relationships, *relationship)
	}
	return relationships, nil
}

func (s *Store) UpdateRelationship(ctx context.Context, kind cmdb.RelationshipKind, fromID, toID string, properties map[string]any) (*cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	fromLabel, fromKey, err := cmdb.NodeDefinition(definition.From)
	if err != nil {
		return nil, err
	}
	toLabel, toKey, err := cmdb.NodeDefinition(definition.To)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (source:%s {%s: $fromId})-[relationship:%s]->(target:%s {%s: $toId}) WHERE coalesce(relationship.status, 'active') <> 'retired' SET relationship += $properties RETURN properties(relationship) AS properties", fromLabel, fromKey, definition.TypeName, toLabel, toKey)
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

func (s *Store) RetireRelationship(ctx context.Context, kind cmdb.RelationshipKind, fromID, toID string) (*cmdb.Relationship, error) {
	definition, err := cmdb.RelationshipDefinitionFor(kind)
	if err != nil {
		return nil, err
	}
	fromLabel, fromKey, err := cmdb.NodeDefinition(definition.From)
	if err != nil {
		return nil, err
	}
	toLabel, toKey, err := cmdb.NodeDefinition(definition.To)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("MATCH (source:%s {%s: $fromId})-[relationship:%s]->(target:%s {%s: $toId}) SET relationship.status = 'retired', relationship.retiredAt = coalesce(relationship.retiredAt, $retiredAt) RETURN properties(relationship) AS properties", fromLabel, fromKey, definition.TypeName, toLabel, toKey)
	params := map[string]any{"fromId": fromID, "toId": toID, "retiredAt": time.Now().UTC().Format(time.RFC3339Nano)}
	records, err := s.execute(ctx, true, query, params)
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
		kind, err := cmdb.ParseRelationshipKind(typeName)
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
