package graph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/config"
)

func TestRetireNodeRetiresOnlyIncidentRelationships(t *testing.T) {
	uri := os.Getenv("NEO4J_URI")
	database := os.Getenv("NEO4J_TEST_DATABASE")
	username := os.Getenv("NEO4J_USERNAME")
	password := os.Getenv("NEO4J_PASSWORD")
	if uri == "" || database == "" || username == "" || password == "" {
		t.Skip("set NEO4J_URI, NEO4J_USERNAME, NEO4J_PASSWORD, and NEO4J_TEST_DATABASE to run Neo4j integration tests")
	}

	ctx := context.Background()
	store, err := Open(ctx, config.Config{URI: uri, Username: username, Password: password, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	if err := store.EnsureConstraints(ctx); err != nil {
		t.Fatal(err)
	}

	service := cmdb.NewService(store)
	createNode := func(kind cmdb.NodeKind) string {
		node, err := service.CreateGeneratedNode(ctx, kind, nil, nil)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return node.ID
	}
	identityID := createNode(cmdb.Identity)
	jobCodeID := createNode(cmdb.JobCode)
	birthrightID := createNode(cmdb.Birthright)
	roleID := createNode(cmdb.Role)
	if _, err := service.CreateRelationship(ctx, cmdb.HasJobCode, identityID, jobCodeID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateRelationship(ctx, cmdb.QualifiesFor, jobCodeID, birthrightID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateRelationship(ctx, cmdb.Grants, birthrightID, roleID, nil); err != nil {
		t.Fatal(err)
	}
	identities, _, err := service.ListNodes(ctx, cmdb.Identity, cmdb.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var listedIdentity *cmdb.Node
	for index := range identities {
		if identities[index].ID == identityID {
			listedIdentity = &identities[index]
			break
		}
	}
	if listedIdentity == nil || len(listedIdentity.Relationships) != 1 {
		t.Fatalf("listed identity relationships = %v, want its HAS_JOB_CODE relationship", listedIdentity)
	}
	listedRelationship := listedIdentity.Relationships[0]
	if listedRelationship.Kind != cmdb.HasJobCode || listedRelationship.FromID != identityID || listedRelationship.ToID != jobCodeID {
		t.Fatalf("listed relationship = %#v, want directed HAS_JOB_CODE edge", listedRelationship)
	}

	if _, err := service.RetireNode(ctx, cmdb.JobCode, jobCodeID); err != nil {
		t.Fatal(err)
	}
	jobCode, err := service.GetNode(ctx, cmdb.JobCode, jobCodeID, true)
	if err != nil {
		t.Fatal(err)
	}
	if jobCode.Status != "retired" {
		t.Fatalf("job code status = %q, want retired", jobCode.Status)
	}
	identities, _, err = service.ListNodes(ctx, cmdb.Identity, cmdb.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range identities {
		if identity.ID == identityID && len(identity.Relationships) != 0 {
			t.Errorf("default node list included retired relationship: %#v", identity.Relationships)
		}
	}
	identities, _, err = service.ListNodes(ctx, cmdb.Identity, cmdb.ListOptions{IncludeRetired: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range identities {
		if identity.ID == identityID && (len(identity.Relationships) != 1 || identity.Relationships[0].Status != "retired") {
			t.Errorf("include-retired node list relationships = %#v, want retired edge", identity.Relationships)
		}
	}

	for _, check := range []struct {
		kind   cmdb.RelationshipKind
		fromID string
		toID   string
		want   string
	}{
		{cmdb.HasJobCode, identityID, jobCodeID, "retired"},
		{cmdb.QualifiesFor, jobCodeID, birthrightID, "retired"},
		{cmdb.Grants, birthrightID, roleID, "active"},
	} {
		relationship, err := service.GetRelationship(ctx, check.kind, check.fromID, check.toID, true)
		if err != nil {
			t.Fatalf("read %s relationship: %v", check.kind, err)
		}
		if relationship.Status != check.want {
			t.Errorf("%s relationship status = %q, want %q", check.kind, relationship.Status, check.want)
		}
	}

	for _, check := range []struct {
		kind cmdb.NodeKind
		id   string
	}{
		{cmdb.Identity, identityID},
		{cmdb.Birthright, birthrightID},
		{cmdb.Role, roleID},
	} {
		node, err := service.GetNode(ctx, check.kind, check.id, true)
		if err != nil {
			t.Fatalf("read %s node: %v", check.kind, err)
		}
		if node.Status != "active" {
			t.Errorf("%s node status = %q, want active", check.kind, node.Status)
		}
	}
}

func TestCreateIncidentWithCIsIsAtomic(t *testing.T) {
	uri := os.Getenv("NEO4J_URI")
	database := os.Getenv("NEO4J_TEST_DATABASE")
	username := os.Getenv("NEO4J_USERNAME")
	password := os.Getenv("NEO4J_PASSWORD")
	if uri == "" || database == "" || username == "" || password == "" {
		t.Skip("set NEO4J_URI, NEO4J_USERNAME, NEO4J_PASSWORD, and NEO4J_TEST_DATABASE to run Neo4j integration tests")
	}

	ctx := context.Background()
	store, err := Open(ctx, config.Config{URI: uri, Username: username, Password: password, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	if err := store.EnsureConstraints(ctx); err != nil {
		t.Fatal(err)
	}

	service := cmdb.NewService(store)
	suffix := fmt.Sprintf("cmdb-create-test-%d", time.Now().UnixNano())
	ci, err := service.CreateGeneratedNode(ctx, cmdb.CI, map[string]any{"ciType": "server", "testRun": suffix}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ciID := ci.ID
	if !strings.HasPrefix(ciID, "CI-") {
		t.Fatalf("generated CI ID = %q, want CI- prefix", ciID)
	}
	incident, err := service.CreateGeneratedNode(ctx, cmdb.Incident, map[string]any{"name": "Test incident"}, []string{ciID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(incident.ID, "INC-") {
		t.Fatalf("generated incident ID = %q, want INC- prefix", incident.ID)
	}
	if len(incident.Relationships) != 1 || incident.Relationships[0].Kind != cmdb.Affects || incident.Relationships[0].ToID != ciID {
		t.Fatalf("created incident relationships = %#v, want one AFFECTS link to %s", incident.Relationships, ciID)
	}

	_, before, err := service.ListNodes(ctx, cmdb.Incident, cmdb.ListOptions{IncludeRetired: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateGeneratedNode(ctx, cmdb.Incident, nil, []string{"missing-" + suffix}); !errors.Is(err, cmdb.ErrInvalid) {
		t.Fatalf("create incident with missing CI error = %v, want ErrInvalid", err)
	}
	_, after, err := service.ListNodes(ctx, cmdb.Incident, cmdb.ListOptions{IncludeRetired: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if after.Total != before.Total {
		t.Fatalf("missing-CI create left an incident behind: counts before=%d after=%d", before.Total, after.Total)
	}
}
