package cmdb

import (
	"errors"
	"strings"
	"testing"
)

func serverManifest() CatalogManifest {
	return CatalogManifest{
		Name: "server-request", Version: "1.0.0", Title: "Request a server", Owner: "Network Engineering",
		Visibility: []string{"Platform Engineering"},
		Approvals:  []CatalogApproval{{Name: "Network review", Assignees: []string{"Network Engineering"}}},
		Inputs: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"hostname":    map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]{2,30}$"},
				"size":        map[string]any{"type": "string", "enum": []any{"small", "large"}},
				"application": map[string]any{"type": "string", PortalSourceKeyword: "cmdb:application"},
				"cores":       map[string]any{"type": "integer", "minimum": 1},
			},
			"required":             []any{"hostname", "size"},
			"additionalProperties": false,
		},
		Target: CatalogTarget{TaskQueue: "network-engineering", WorkflowType: "ProvisionServer"},
	}
}

func TestCatalogManifestValidation(t *testing.T) {
	clean, err := normalizeCatalogManifest(serverManifest())
	if err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	if clean.Approvals[0].Rule != AnyApprover {
		t.Errorf("approval rule defaults to any, got %q", clean.Approvals[0].Rule)
	}
	broken := func(change func(*CatalogManifest)) CatalogManifest {
		manifest := serverManifest()
		change(&manifest)
		return manifest
	}
	for name, manifest := range map[string]CatalogManifest{
		"name not a slug":     broken(func(m *CatalogManifest) { m.Name = "Server Request" }),
		"version not semver":  broken(func(m *CatalogManifest) { m.Version = "1.0" }),
		"leading zero":        broken(func(m *CatalogManifest) { m.Version = "1.01.0" }),
		"no title":            broken(func(m *CatalogManifest) { m.Title = " " }),
		"no owner":            broken(func(m *CatalogManifest) { m.Owner = "" }),
		"no target":           broken(func(m *CatalogManifest) { m.Target.WorkflowType = "" }),
		"unnamed approval":    broken(func(m *CatalogManifest) { m.Approvals[0].Name = "" }),
		"unassigned approval": broken(func(m *CatalogManifest) { m.Approvals[0].Assignees = nil }),
		"bad approval rule":   broken(func(m *CatalogManifest) { m.Approvals[0].Rule = "majority" }),
		"no form":             broken(func(m *CatalogManifest) { m.Inputs = nil }),
		"form not an object":  broken(func(m *CatalogManifest) { m.Inputs["type"] = "array" }),
		"nested field": broken(func(m *CatalogManifest) {
			m.Inputs["properties"].(map[string]any)["tags"] = map[string]any{"type": "array"}
		}),
		"bad field name": broken(func(m *CatalogManifest) {
			m.Inputs["properties"].(map[string]any)["host name"] = map[string]any{"type": "string"}
		}),
		"unknown CI type": broken(func(m *CatalogManifest) {
			m.Inputs["properties"].(map[string]any)["site"] = map[string]any{"type": "string", PortalSourceKeyword: "cmdb:building"}
		}),
		"picker not a string": broken(func(m *CatalogManifest) {
			m.Inputs["properties"].(map[string]any)["app"] = map[string]any{"type": "integer", PortalSourceKeyword: "cmdb:application"}
		}),
		"invalid JSON Schema": broken(func(m *CatalogManifest) { m.Inputs["required"] = "hostname" }),
		"negative SLA":        broken(func(m *CatalogManifest) { m.SLABusinessDays = -1 }),
	} {
		if _, err := normalizeCatalogManifest(manifest); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestCatalogInputsValidation(t *testing.T) {
	schema := serverManifest().Inputs
	if err := validateCatalogInputs(schema, map[string]any{"hostname": "web-01", "size": "small", "cores": float64(4)}); err != nil {
		t.Fatalf("valid inputs: %v", err)
	}
	err := validateCatalogInputs(schema, map[string]any{"hostname": "Web 01", "size": "huge", "extra": true})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid inputs error = %v, want ErrInvalid", err)
	}
	for _, want := range []string{"hostname", "size", "extra"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
	if err := validateCatalogInputs(schema, map[string]any{"size": "small"}); err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Errorf("missing required field error = %v", err)
	}
}

func TestCatalogVersionOrder(t *testing.T) {
	for _, test := range []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.10.0", "1.9.9", 1},
		{"2.0.0", "10.0.0", -1},
	} {
		if got := compareVersions(test.a, test.b); got != test.want {
			t.Errorf("compareVersions(%s, %s) = %d, want %d", test.a, test.b, got, test.want)
		}
	}
}

func TestSameManifestContentIgnoresSource(t *testing.T) {
	manifest := serverManifest()
	manifest.Source = "network-team@abc123"
	stored := `{"name":"server-request","version":"1.0.0","title":"Request a server","owner":"Network Engineering","visibility":["Platform Engineering"],"approvals":[{"name":"Network review","assignees":["Network Engineering"]}],"inputs":{"type":"object","properties":{"application":{"type":"string","x-portal-source":"cmdb:application"},"cores":{"type":"integer","minimum":1},"hostname":{"type":"string","pattern":"^[a-z][a-z0-9-]{2,30}$"},"size":{"type":"string","enum":["small","large"]}},"required":["hostname","size"],"additionalProperties":false},"target":{"taskQueue":"network-engineering","workflowType":"ProvisionServer"},"source":"network-team@old"}`
	if same, err := sameManifestContent(stored, manifest); err != nil || !same {
		t.Errorf("same content from another commit: same=%v err=%v", same, err)
	}
	manifest.Title = "Request a virtual machine"
	if same, _ := sameManifestContent(stored, manifest); same {
		t.Error("a changed title must not count as the same content")
	}
}

func TestCatalogRequestsAreNotDrafted(t *testing.T) {
	if err := validateRequestProperties(map[string]any{"requestType": "catalog"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("drafting a catalog request error = %v, want ErrInvalid", err)
	}
	if err := validateRequestProperties(map[string]any{"state": "in-progress"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("setting in-progress by hand error = %v, want ErrInvalid", err)
	}
	if err := rejectWorkflowKind(CatalogItem); !errors.Is(err, ErrInvalid) {
		t.Errorf("generic catalog item write error = %v, want ErrInvalid", err)
	}
}
