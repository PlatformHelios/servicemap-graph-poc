package cmdb

import (
	"fmt"
	"strconv"
	"strings"
)

// Mermaid renders the map as a Mermaid flowchart with the same shape the
// Process Mapper canvas draws: Start, each step wired by depends-on, and
// Outputs after every step nothing else waits on. Used by
// GET /api/process-maps/{id}/mermaid.
func (m *ProcessMap) Mermaid() string {
	var b strings.Builder
	title := m.Name
	if title == "" {
		title = m.ProcessID
	}
	fmt.Fprintf(&b, "---\ntitle: %s\n---\n", strconv.Quote(title))
	b.WriteString("flowchart LR\n")
	if m.Description != "" {
		fmt.Fprintf(&b, "    %%%% %s\n", mermaidComment(m.Description))
	}

	startDetail := m.TriggerDescription
	if startDetail == "" {
		startDetail = joinActionsAndRefs(m.TriggerActions, m.Triggers, "No trigger yet")
	}
	endDetail := m.OutputDescription
	if endDetail == "" {
		endDetail = joinActionsAndRefs(m.OutputActions, m.Outputs, "No outputs yet")
	}
	fmt.Fprintf(&b, "    start([\"%s\"])\n", mermaidLabel("Start", startDetail))

	nodeIDs := make(map[string]string, len(m.Steps))
	for i, step := range m.Steps {
		nodeID := fmt.Sprintf("step%d", i+1)
		if step.ID != "" {
			nodeIDs[step.ID] = nodeID
		}
		name := step.Name
		if name == "" {
			name = fmt.Sprintf("Step %d", i+1)
		}
		open, closing := "[", "]"
		if step.Execution == string(AutomatedExecution) {
			open, closing = "[[", "]]"
		}
		fmt.Fprintf(&b, "    %s%s\"%s\"%s\n", nodeID, open, mermaidLabel(name, stepDetail(step)), closing)
	}
	fmt.Fprintf(&b, "    finish([\"%s\"])\n", mermaidLabel("Outputs", endDetail))

	if len(m.Steps) == 0 {
		b.WriteString("    start --> finish\n")
		return b.String()
	}
	deps := mermaidDependencies(m.Steps, nodeIDs)
	waitedOn := map[string]bool{}
	for i, stepDeps := range deps {
		target := fmt.Sprintf("step%d", i+1)
		for _, source := range stepDeps {
			waitedOn[source] = true
			fmt.Fprintf(&b, "    %s --> %s\n", source, target)
		}
	}
	for i := range m.Steps {
		nodeID := fmt.Sprintf("step%d", i+1)
		if !waitedOn[nodeID] {
			fmt.Fprintf(&b, "    %s --> finish\n", nodeID)
		}
	}
	b.WriteString("    classDef anchor fill:#eef4ff,stroke:#2f5fd0\n")
	b.WriteString("    classDef automated fill:#eefaf3,stroke:#1f8a4c\n")
	b.WriteString("    class start,finish anchor\n")
	var automated []string
	for i, step := range m.Steps {
		if step.Execution == string(AutomatedExecution) {
			automated = append(automated, fmt.Sprintf("step%d", i+1))
		}
	}
	if len(automated) > 0 {
		fmt.Fprintf(&b, "    class %s automated\n", strings.Join(automated, ","))
	}
	return b.String()
}

// mermaidDependencies returns, per step, the node IDs it waits on. Like the
// Process Mapper, a map with no stored dependencies reads as an ordered chain,
// and a step with none of its own starts with the process.
func mermaidDependencies(steps []ProcessMapStep, nodeIDs map[string]string) [][]string {
	anyStored := false
	for _, step := range steps {
		if len(step.DependsOn) > 0 {
			anyStored = true
			break
		}
	}
	result := make([][]string, len(steps))
	for i, step := range steps {
		if !anyStored {
			if i == 0 {
				result[i] = []string{"start"}
			} else {
				result[i] = []string{fmt.Sprintf("step%d", i)}
			}
			continue
		}
		seen := map[string]bool{}
		for _, dep := range step.DependsOn {
			source := ""
			switch {
			case dep.Kind == processStartDependency:
				source = "start"
			case dep.ID != "" && nodeIDs[dep.ID] != "":
				source = nodeIDs[dep.ID]
			case dep.ID == "" && dep.Order >= 1 && dep.Order <= len(steps):
				source = fmt.Sprintf("step%d", dep.Order)
			}
			if source == "" || source == fmt.Sprintf("step%d", i+1) || seen[source] {
				continue
			}
			seen[source] = true
			result[i] = append(result[i], source)
		}
		if len(result[i]) == 0 {
			result[i] = []string{"start"}
		}
	}
	return result
}

func stepDetail(step ProcessMapStep) string {
	parts := []string{titleWords(step.Execution)}
	if len(step.Performers) > 0 {
		names := make([]string, 0, len(step.Performers))
		for _, performer := range step.Performers {
			if performer.Name != "" {
				names = append(names, performer.Name)
			} else {
				names = append(names, performer.ID)
			}
		}
		parts = append(parts, strings.Join(names, ", "))
	} else {
		parts = append(parts, "No performer")
	}
	if step.CreatesCase {
		parts = append(parts, "Allows case creation")
	}
	if step.OLAEnabled {
		days := step.OLABusinessDays
		if days < 1 {
			days = 5
		}
		parts = append(parts, fmt.Sprintf("OLA %dd", days))
	}
	return strings.Join(parts, " · ")
}

func joinActionsAndRefs(actions []string, refs []ProcessMapRef, empty string) string {
	parts := make([]string, 0, len(actions)+len(refs))
	for _, action := range actions {
		parts = append(parts, titleWords(action))
	}
	for _, ref := range refs {
		if ref.Name != "" {
			parts = append(parts, ref.Name)
		} else {
			parts = append(parts, ref.ID)
		}
	}
	if len(parts) == 0 {
		return empty
	}
	return strings.Join(parts, ", ")
}

// titleWords turns "email-received" into "Email Received", matching the UI's titleCase.
func titleWords(value string) string {
	words := strings.Fields(strings.ReplaceAll(value, "-", " "))
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

// mermaidLabel builds a quoted-label body: a bold title over a detail line.
// Mermaid entity codes keep user text from closing the quote or reading as markup.
func mermaidLabel(title, detail string) string {
	label := "<b>" + mermaidEscape(title) + "</b>"
	if detail != "" {
		label += "<br/>" + mermaidEscape(detail)
	}
	return label
}

var mermaidEscaper = strings.NewReplacer(
	"#", "#35;",
	`"`, "#quot;",
	"<", "#lt;",
	">", "#gt;",
	"\r\n", "<br/>",
	"\n", "<br/>",
	"\r", "<br/>",
)

func mermaidEscape(text string) string {
	return mermaidEscaper.Replace(strings.TrimSpace(text))
}

func mermaidComment(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
