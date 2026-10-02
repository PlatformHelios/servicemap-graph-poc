package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
)

func TestParseNodeCreate(t *testing.T) {
	parsed, err := parseCommand([]string{"node", "create", "--kind", "identity", "--properties", `{"department":"Platform Engineering"}`})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.nodeKind != cmdb.Identity || parsed.id != "" || parsed.properties["department"] != "Platform Engineering" {
		t.Fatalf("unexpected parsed command: %#v", parsed)
	}
	if _, err := parseCommand([]string{"node", "create", "--kind", "identity", "--id", "e0001"}); err == nil || !strings.Contains(err.Error(), "assigned automatically") {
		t.Fatalf("parseCommand() error = %v, want automatic-ID error", err)
	}
}

func TestParseRelationshipRetire(t *testing.T) {
	parsed, err := parseCommand([]string{"relationship", "retire", "--kind", "HAS_JOB_CODE", "--from-id", "e0001", "--to-id", "0001"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.relationship != cmdb.HasJobCode || parsed.fromID != "e0001" || parsed.toID != "0001" {
		t.Fatalf("unexpected parsed command: %#v", parsed)
	}
}

func TestParseCommandRequiresIdentifier(t *testing.T) {
	_, err := parseCommand([]string{"node", "get", "--kind", "identity"})
	if err == nil || !strings.Contains(err.Error(), "--id is required") {
		t.Fatalf("parseCommand() error = %v, want missing-id error", err)
	}
}

func TestIncidentCreateRequiresCIs(t *testing.T) {
	_, err := parseCommand([]string{"node", "create", "--kind", "incident"})
	if err == nil || !strings.Contains(err.Error(), "--ci-ids is required") {
		t.Fatalf("parseCommand() error = %v, want required-CI error", err)
	}
	parsed, err := parseCommand([]string{"node", "create", "--kind", "incident", "--ci-ids", "ci-1,ci-2"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.id != "" || len(parsed.ciIDs) != 2 || parsed.ciIDs[0] != "ci-1" || parsed.ciIDs[1] != "ci-2" {
		t.Fatalf("parsed generated-ID incident command = %#v", parsed)
	}
	if _, err := parseCommand([]string{"node", "create", "--kind", "incident", "--id", "INC-1001", "--ci-ids", "ci-1"}); err == nil || !strings.Contains(err.Error(), "assigned automatically") {
		t.Fatalf("parseCommand() error = %v, want automatic-ID error", err)
	}
}

func TestCICreateUsesGeneratedID(t *testing.T) {
	parsed, err := parseCommand([]string{"node", "create", "--kind", "ci", "--properties", `{"ciType":"server"}`})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.id != "" || parsed.nodeKind != cmdb.CI {
		t.Fatalf("parsed generated-ID CI command = %#v", parsed)
	}
	if _, err := parseCommand([]string{"node", "create", "--kind", "ci", "--id", "custom-ci", "--properties", `{"ciType":"server"}`}); err == nil || !strings.Contains(err.Error(), "assigned automatically") {
		t.Fatalf("parseCommand() error = %v, want automatic-ID error", err)
	}
	if _, err := parseCommand([]string{"node", "create", "--kind", "ci", "--ci-ids", "ci-1", "--properties", `{"ciType":"server"}`}); err == nil || !strings.Contains(err.Error(), "only valid") {
		t.Fatalf("parseCommand() error = %v, want invalid-CI-links error", err)
	}
}

func TestCreateNodeRejectsNestedProperties(t *testing.T) {
	parsed, err := parseCommand([]string{"node", "create", "--kind", "identity", "--properties", `{"metadata":{"team":"platform"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cmdb.NewService(nil).CreateGeneratedNode(t.Context(), parsed.nodeKind, parsed.properties, nil); err == nil {
		t.Fatal("CreateGeneratedNode() accepted nested properties")
	}
}

func TestRunHelpDoesNotRequireNeo4j(t *testing.T) {
	var output bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "relationship") {
		t.Fatalf("help output did not include relationship commands: %s", output.String())
	}
}
