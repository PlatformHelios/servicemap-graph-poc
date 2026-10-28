package cmdb

import (
	"fmt"
	"strconv"
	"strings"
)

// Markdown renders the map as a Markdown document: an overview, the Mermaid
// flowchart in a fenced block, then Start, Steps, and Outputs sections. Each
// part is its own section so further documentation can be added alongside.
// Used by GET /api/process-maps/{id}/markdown.
func (m *ProcessMap) Markdown() string {
	var b strings.Builder
	title := m.Name
	if title == "" {
		title = m.ProcessID
	}
	fmt.Fprintf(&b, "# %s\n\n", markdownInline(title))
	fmt.Fprintf(&b, "Process CI: %s\n\n", markdownCode(m.ProcessID))
	if m.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", markdownText(m.Description))
	}

	b.WriteString("## Process flow\n\n```mermaid\n")
	b.WriteString(m.Mermaid())
	b.WriteString("```\n\n")

	b.WriteString("## Start\n\n")
	writeMarkdownEnds(&b, m.TriggerDescription, m.TriggerActions, "Trigger actions", m.Triggers, "Triggered by", "No trigger yet.")

	b.WriteString("## Steps\n\n")
	if len(m.Steps) == 0 {
		b.WriteString("No steps yet.\n\n")
	} else {
		b.WriteString("| # | Step | Execution | Performers | Depends on | Case | OLA |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
		deps := mermaidDependencies(m.Steps, stepNodeIDs(m.Steps))
		for i, step := range m.Steps {
			name := step.Name
			if name == "" {
				name = fmt.Sprintf("Step %d", i+1)
			}
			cell := "**" + markdownCell(name) + "**"
			if step.Description != "" {
				cell += "<br>" + markdownCell(step.Description)
			}
			performers := make([]string, 0, len(step.Performers))
			for _, performer := range step.Performers {
				performers = append(performers, markdownRef(performer.Name, performer.ID))
			}
			waitsOn := make([]string, 0, len(deps[i]))
			for _, source := range deps[i] {
				waitsOn = append(waitsOn, markdownCell(m.dependencyLabel(source)))
			}
			caseCell, olaCell := "No", "—"
			if step.CreatesCase {
				caseCell = "Yes"
			}
			if step.OLAEnabled {
				days := step.OLABusinessDays
				if days < 1 {
					days = 5
				}
				olaCell = fmt.Sprintf("%d business days", days)
			}
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s | %s |\n",
				i+1, cell, titleWords(step.Execution), orDash(strings.Join(performers, ", ")),
				strings.Join(waitsOn, ", "), caseCell, olaCell)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Outputs\n\n")
	writeMarkdownEnds(&b, m.OutputDescription, m.OutputActions, "Output actions", m.Outputs, "Produces", "No outputs yet.")
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeMarkdownEnds(b *strings.Builder, description string, actions []string, actionsLabel string, refs []ProcessMapRef, refsLabel, empty string) {
	if description == "" && len(actions) == 0 && len(refs) == 0 {
		fmt.Fprintf(b, "%s\n\n", empty)
		return
	}
	if description != "" {
		fmt.Fprintf(b, "%s\n\n", markdownText(description))
	}
	if len(actions) > 0 {
		names := make([]string, 0, len(actions))
		for _, action := range actions {
			names = append(names, titleWords(action))
		}
		fmt.Fprintf(b, "- **%s:** %s\n", actionsLabel, strings.Join(names, ", "))
	}
	if len(refs) > 0 {
		names := make([]string, 0, len(refs))
		for _, ref := range refs {
			names = append(names, markdownRef(ref.Name, ref.ID))
		}
		fmt.Fprintf(b, "- **%s:** %s\n", refsLabel, strings.Join(names, ", "))
	}
	if len(actions) > 0 || len(refs) > 0 {
		b.WriteString("\n")
	}
}

// dependencyLabel names a Mermaid node ID ("start" or "stepN") for the Steps table.
func (m *ProcessMap) dependencyLabel(nodeID string) string {
	if nodeID == "start" {
		return "Process Start"
	}
	order, err := strconv.Atoi(strings.TrimPrefix(nodeID, "step"))
	if err != nil || order < 1 || order > len(m.Steps) {
		return nodeID
	}
	if name := m.Steps[order-1].Name; name != "" {
		return fmt.Sprintf("%d. %s", order, name)
	}
	return fmt.Sprintf("Step %d", order)
}

func stepNodeIDs(steps []ProcessMapStep) map[string]string {
	ids := make(map[string]string, len(steps))
	for i, step := range steps {
		if step.ID != "" {
			ids[step.ID] = fmt.Sprintf("step%d", i+1)
		}
	}
	return ids
}

func markdownRef(name, id string) string {
	if name == "" || name == id {
		return markdownCode(id)
	}
	return markdownCell(name) + " (" + markdownCode(id) + ")"
}

func orDash(text string) string {
	if text == "" {
		return "—"
	}
	return text
}

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`,
	"<", "&lt;", ">", "&gt;", "|", `\|`, "#", `\#`,
)

// markdownInline escapes text for a single Markdown line.
func markdownInline(text string) string {
	return markdownEscaper.Replace(strings.Join(strings.Fields(text), " "))
}

// markdownText escapes a paragraph, keeping the author's line breaks.
func markdownText(text string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = markdownInline(line)
	}
	return strings.Join(lines, "  \n")
}

// markdownCell escapes text for a table cell, where line breaks become <br>.
func markdownCell(text string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = markdownInline(line)
	}
	return strings.Join(lines, "<br>")
}

func markdownCode(text string) string {
	return "`" + strings.ReplaceAll(strings.Join(strings.Fields(text), " "), "`", "'") + "`"
}
