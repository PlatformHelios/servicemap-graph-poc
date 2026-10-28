package cmdb

import (
	"strings"
	"testing"
)

func TestProcessMapMermaidFollowsDependencies(t *testing.T) {
	processMap := &ProcessMap{
		ProcessID:      "CI-PROC",
		Name:           "Onboard vendor",
		TriggerActions: []string{"form-submitted"},
		Triggers:       []ProcessMapRef{{Kind: "ci", ID: "CI-APP", Name: "Vendor portal"}},
		OutputActions:  []string{"record-created"},
		Steps: []ProcessMapStep{
			{ID: "S1", Name: "Review request", Execution: "manual", CreatesCase: true, OLAEnabled: true, OLABusinessDays: 3,
				Performers: []ProcessMapPerformer{{Kind: "group", ID: "GRP-1", Name: "Procurement"}},
				DependsOn:  []ProcessMapDependency{{Kind: "process-start"}}},
			{ID: "S2", Name: "Check sanctions", Execution: "automated",
				Performers: []ProcessMapPerformer{{Kind: "automation", ID: "CI-BOT"}},
				DependsOn:  []ProcessMapDependency{{Kind: "process-start"}}},
			{ID: "S3", Name: "Create vendor", Execution: "manual",
				DependsOn: []ProcessMapDependency{{Kind: "step", ID: "S1", Order: 1}, {Kind: "step", ID: "S2", Order: 2}}},
		},
	}
	got := processMap.Mermaid()
	for _, want := range []string{
		"---\ntitle: \"Onboard vendor\"\n---\nflowchart LR\n",
		`start(["<b>Start</b><br/>Form Submitted, Vendor portal"])`,
		`step1["<b>Review request</b><br/>Manual · Procurement · Allows case creation · OLA 3d"]`,
		`step2[["<b>Check sanctions</b><br/>Automated · CI-BOT"]]`,
		`finish(["<b>Outputs</b><br/>Record Created"])`,
		"start --> step1\n",
		"start --> step2\n",
		"step1 --> step3\n",
		"step2 --> step3\n",
		"step3 --> finish\n",
		"class step2 automated\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Mermaid output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "step1 --> finish") || strings.Contains(got, "step2 --> finish") {
		t.Errorf("steps that others wait on should not link to Outputs:\n%s", got)
	}
}

func TestProcessMapMermaidChainsStepsWithoutStoredDependencies(t *testing.T) {
	processMap := &ProcessMap{ProcessID: "CI-PROC", Steps: []ProcessMapStep{
		{ID: "S1", Name: "First", Execution: "manual"},
		{ID: "S2", Name: "Second", Execution: "manual"},
	}}
	got := processMap.Mermaid()
	for _, want := range []string{"title: \"CI-PROC\"", "start --> step1\n", "step1 --> step2\n", "step2 --> finish\n", "No trigger yet", "No outputs yet", "No performer"} {
		if !strings.Contains(got, want) {
			t.Errorf("Mermaid output missing %q:\n%s", want, got)
		}
	}
}

func TestProcessMapMermaidEmptyMapAndEscaping(t *testing.T) {
	processMap := &ProcessMap{
		ProcessID:          "CI-PROC",
		Name:               `Pay "urgent" <invoices>`,
		TriggerDescription: "Invoice #42 arrives\nby email",
	}
	got := processMap.Mermaid()
	if !strings.Contains(got, "start --> finish\n") {
		t.Errorf("empty map should link Start to Outputs:\n%s", got)
	}
	if !strings.Contains(got, `title: "Pay \"urgent\" <invoices>"`) {
		t.Errorf("title not quoted for front matter:\n%s", got)
	}
	if !strings.Contains(got, `<b>Start</b><br/>Invoice #35;42 arrives<br/>by email`) {
		t.Errorf("label text not escaped:\n%s", got)
	}
}
