package cmdb

import (
	"strings"
	"testing"
)

func TestProcessMapMarkdownDocument(t *testing.T) {
	processMap := &ProcessMap{
		ProcessID:      "CI-PROC",
		Name:           "Onboard vendor",
		Description:    "Vendor intake\nfor new suppliers",
		TriggerActions: []string{"form-submitted"},
		Triggers:       []ProcessMapRef{{Kind: "ci", ID: "CI-APP", Name: "Vendor portal"}},
		Steps: []ProcessMapStep{
			{ID: "S1", Name: "Review request", Description: "Check the form", Execution: "manual", CreatesCase: true, OLAEnabled: true, OLABusinessDays: 3,
				Performers: []ProcessMapPerformer{{Kind: "group", ID: "GRP-1", Name: "Procurement"}},
				DependsOn:  []ProcessMapDependency{{Kind: "process-start"}}},
			{ID: "S2", Name: "Create vendor | record", Execution: "automated",
				DependsOn: []ProcessMapDependency{{Kind: "step", ID: "S1", Order: 1}}},
		},
	}
	got := processMap.Markdown()
	for _, want := range []string{
		"# Onboard vendor\n\nProcess CI: `CI-PROC`\n\nVendor intake  \nfor new suppliers\n\n",
		"## Process flow\n\n```mermaid\n---\ntitle: \"Onboard vendor\"\n---\nflowchart LR\n",
		"    step1 --> step2\n",
		"```\n\n## Start\n\n- **Trigger actions:** Form Submitted\n- **Triggered by:** Vendor portal (`CI-APP`)\n",
		"| 1 | **Review request**<br>Check the form | Manual | Procurement (`GRP-1`) | Process Start | Yes | 3 business days |\n",
		`| 2 | **Create vendor \| record** | Automated | — | 1. Review request | No | — |` + "\n",
		"## Outputs\n\nNo outputs yet.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Markdown output missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "```") != 2 {
		t.Errorf("Markdown should hold exactly one fenced block:\n%s", got)
	}
}

func TestProcessMapMarkdownEscapesUserText(t *testing.T) {
	processMap := &ProcessMap{ProcessID: "CI-PROC", Name: "Pay `urgent` *invoices* <now>"}
	got := processMap.Markdown()
	if !strings.HasPrefix(got, "# Pay \\`urgent\\` \\*invoices\\* &lt;now&gt;\n") {
		t.Errorf("heading not escaped:\n%s", got)
	}
	if strings.Count(got, "```") != 2 {
		t.Errorf("a backtick in the name must not break the mermaid fence:\n%s", got)
	}
	if !strings.Contains(got, "No steps yet.") || !strings.Contains(got, "No trigger yet.") {
		t.Errorf("empty sections not described:\n%s", got)
	}
}
