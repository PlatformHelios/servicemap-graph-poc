package graph

import (
	"strings"
	"testing"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
)

func TestDecodeListedRelationships(t *testing.T) {
	relationships, err := decodeListedRelationships([]any{
		map[string]any{
			"type":   "HAS_JOB_CODE",
			"fromId": "e0001",
			"toId":   "0001",
			"properties": map[string]any{
				"status": "active",
				"source": "birthright",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(relationships) != 1 {
		t.Fatalf("decoded %d relationships, want 1", len(relationships))
	}
	got := relationships[0]
	if got.Kind != cmdb.HasJobCode || got.FromID != "e0001" || got.ToID != "0001" || got.Status != "active" {
		t.Fatalf("decoded relationship = %#v", got)
	}
	if got.Properties["source"] != "birthright" {
		t.Fatalf("relationship properties = %#v", got.Properties)
	}
}

// A retired edge lives under the kind's retired type; decoding maps it back to
// the kind and reports it as retired.
func TestDecodeListedRelationshipsRetiredType(t *testing.T) {
	relationships, err := decodeListedRelationships([]any{
		map[string]any{
			"type":       "HAS_JOB_CODE_RETIRED",
			"fromId":     "e0001",
			"toId":       "0001",
			"properties": map[string]any{"status": "retired", "retiredAt": "2026-01-01T00:00:00Z"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(relationships) != 1 || relationships[0].Kind != cmdb.HasJobCode || relationships[0].Status != "retired" || relationships[0].RetiredAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("decoded retired relationship = %#v", relationships)
	}
}

func TestRelationshipTypesUnionRetired(t *testing.T) {
	live := relationshipTypes(false)
	if strings.Contains(live, "_RETIRED") {
		t.Fatalf("live types include retired twins: %s", live)
	}
	both := relationshipTypes(true)
	if !strings.Contains(both, "HAS_JOB_CODE|HAS_JOB_CODE_RETIRED") {
		t.Fatalf("include-retired types = %s, want each live type followed by its retired twin", both)
	}
	if liveOrRetired("MEMBER") != "MEMBER|MEMBER_RETIRED" {
		t.Fatalf("liveOrRetired = %q", liveOrRetired("MEMBER"))
	}
}

// The request list anchored at an identity must start from that identity, and
// the status filter must be a plain equality the index can serve.
func TestNodeListSource(t *testing.T) {
	source, params := nodeListSource("Request", "id", cmdb.ListOptions{InvolvedID: "IDN-1", Query: "acme"})
	if !strings.HasPrefix(source, "MATCH (actor:Identity {id: $involvedId})") {
		t.Fatalf("involved source = %s", source)
	}
	if !strings.Contains(source, "node.status = 'active'") || strings.Contains(source, "coalesce(node.status") {
		t.Fatalf("source status filter = %s", source)
	}
	if params["involvedId"] != "IDN-1" || params["query"] != "acme" {
		t.Fatalf("params = %#v", params)
	}
	plain, _ := nodeListSource("Identity", "id", cmdb.ListOptions{IncludeRetired: true})
	if plain != "MATCH (node:Identity)" {
		t.Fatalf("include-retired source = %q, want no WHERE", plain)
	}
}

func TestDecodeListedRelationshipsEmpty(t *testing.T) {
	relationships, err := decodeListedRelationships([]any{})
	if err != nil {
		t.Fatal(err)
	}
	if relationships == nil || len(relationships) != 0 {
		t.Fatalf("empty decode = %#v, want empty slice", relationships)
	}
}
