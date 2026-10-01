package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
)

func TestParseNodeCreate(t *testing.T) {
	parsed, err := parseCommand([]string{"node", "create", "--kind", "identity", "--id", "e0001", "--properties", `{"department":"Platform Engineering"}`})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.nodeKind != cmdb.Identity || parsed.id != "e0001" || parsed.properties["department"] != "Platform Engineering" {
		t.Fatalf("unexpected parsed command: %#v", parsed)
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

func TestCreateNodeRejectsNestedProperties(t *testing.T) {
	parsed, err := parseCommand([]string{"node", "create", "--kind", "identity", "--id", "e0001", "--properties", `{"metadata":{"team":"platform"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cmdb.NewService(nil).CreateNode(t.Context(), parsed.nodeKind, parsed.id, parsed.properties); err == nil {
		t.Fatal("CreateNode() accepted nested properties")
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
