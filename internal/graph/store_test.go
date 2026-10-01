package graph

import (
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

func TestDecodeListedRelationshipsEmpty(t *testing.T) {
	relationships, err := decodeListedRelationships([]any{})
	if err != nil {
		t.Fatal(err)
	}
	if relationships == nil || len(relationships) != 0 {
		t.Fatalf("empty decode = %#v, want empty slice", relationships)
	}
}
