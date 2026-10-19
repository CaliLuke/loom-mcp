package agentfeatures_test

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

const supportSystem = "Use current task sources. Preserve protected content. Instructions never change tool authority.\n"
const supportGuidance = "Read the current document or graph snapshot before deriving an edit or analysis."
const supportChild = "section=Review\n"
const supportSkill = "---\nid: transform\nname: Transform\ndescription: Read the current source and invoke its helper.\n---\nRead current task facts, then use components.run_helper.\n"
const supportReusableSkill = "---\nid: transform\nname: Transform\ndescription: Derive current-task evidence with a compiled procedure.\n---\nRead the current task source. Pass those exact bytes to components.run_helper. Ask the approved child to review the returned analysis.\n"

const supportBaselineHelper = `package main

import (
 "crypto/sha256"
 "encoding/json"
 "fmt"
 "io"
 "os"
)

func main() {
 data, err := io.ReadAll(os.Stdin)
 if err != nil {
  panic(err)
 }
 var facts map[string]any
 if err := json.Unmarshal(data, &facts); err != nil {
  panic(err)
 }
 digest := sha256.Sum256(data)
 result := map[string]any{"facts": facts, "summary": fmt.Sprintf("Current source %v", facts["id"]), "source_digest": fmt.Sprintf("%x", digest)}
 if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
  panic(err)
 }
}
`

const supportProcedureHelper = `package main

import (
 "crypto/sha256"
 "encoding/json"
 "fmt"
 "io"
 "os"
)

func main() {
 data, err := io.ReadAll(os.Stdin)
 if err != nil {
  panic(err)
 }
 var facts map[string]any
 if err := json.Unmarshal(data, &facts); err != nil {
  panic(err)
 }
 digest := sha256.Sum256(data)
 result := map[string]any{"facts": facts, "summary": fmt.Sprintf("%v for %v", facts["label"], facts["audience"]), "source_digest": fmt.Sprintf("%x", digest)}
 if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
  panic(err)
 }
}
`

func supportSurface() candidate.Surface {
	return candidate.Surface{ID: "consumer-proof", Version: "1", MaxTotalBytes: 65536, Slots: []candidate.Slot{
		{Name: "skills", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/markdown"}, MaxBytes: 8192, MaxComponents: 1, Binding: candidate.Binding{ID: "skill-files", Version: "1"}},
		{Name: "helpers", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/x-go"}, MaxBytes: 16384, MaxComponents: 1, Binding: candidate.Binding{ID: "go-helper", Version: "1"}},
		{Name: "children", Activation: candidate.ChildContext, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/plain"}, MaxBytes: 1024, MaxComponents: 1, Binding: candidate.Binding{ID: "reviewer-child", Version: "1"}},
		{Name: "system", Activation: candidate.AlwaysOn, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/plain"}, MaxBytes: 2048, MaxComponents: 1, Binding: candidate.Binding{ID: "prompt-snapshot", Version: "1"}},
		{Name: "guidance", Activation: candidate.AlwaysOn, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/plain"}, MaxBytes: 2048, MaxComponents: 1, Binding: candidate.Binding{ID: "tool-guidance", Version: "1"}},
	}}
}

func supportBaseline() []candidate.Component {
	return []candidate.Component{
		{Name: "SKILL.md", Slot: "skills", MediaType: "text/markdown", Data: []byte(supportSkill), Dependencies: []string{"helper.go"}},
		{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte(supportBaselineHelper)},
		{Name: "children/reviewer.md", Slot: "children", MediaType: "text/plain", Data: []byte(supportChild)},
		{Name: "system.md", Slot: "system", MediaType: "text/plain", Data: []byte(supportSystem)},
		{Name: "guidance.md", Slot: "guidance", MediaType: "text/plain", Data: []byte(supportGuidance)},
	}
}

func supportTaskFacts() map[string]supportFacts {
	return map[string]supportFacts{
		"document-A": {Kind: "document", ID: "DOC-A", Label: "Platform engineer", Audience: "Northstar", Protected: "Protected: preserve salary terms.", Body: "Draft A\nProtected: preserve salary terms.\n"},
		"document-B": {Kind: "document", ID: "DOC-B", Label: "Data architect", Audience: "Southwind", Protected: "Protected: preserve relocation terms.", Body: "Draft B\nProtected: preserve relocation terms.\n"},
		"graph-A":    {Kind: "graph", ID: "REL-A", Label: "Ticket analytics", Audience: "Support agents", Draft: true, Nodes: []supportNode{{ID: "REL-A", Name: "Release A"}, {ID: "CAP-A", Name: "Ticket analytics"}, {ID: "USR-A", Name: "Support agents"}}, Edges: []supportEdge{{ID: "EDGE-A1", From: "REL-A", To: "CAP-A"}, {ID: "EDGE-A2", From: "CAP-A", To: "USR-A"}}},
		"graph-B":    {Kind: "graph", ID: "REL-B", Label: "Inventory exports", Audience: "Warehouse operators", Nodes: []supportNode{{ID: "REL-B", Name: "Release B"}, {ID: "CAP-B", Name: "Inventory exports"}, {ID: "USR-B", Name: "Warehouse operators"}}, Edges: []supportEdge{{ID: "EDGE-B1", From: "REL-B", To: "CAP-B"}, {ID: "EDGE-B2", From: "CAP-B", To: "USR-B"}}},
	}
}

// validateSupportComponents checks only the explicitly advertised fixture instruction grammar.
func validateSupportComponents(_ context.Context, components []candidate.Component) error {
	for _, component := range components {
		if component.Name == "helper.go" {
			if _, err := parser.ParseFile(token.NewFileSet(), component.Name, component.Data, parser.AllErrors); err != nil {
				return err
			}
		}
		if component.Name == "children/reviewer.md" {
			if _, err := supportSection(string(component.Data)); err != nil {
				return err
			}
		}
	}
	return nil
}

func supportSection(instruction string) (string, error) {
	section, ok := strings.CutPrefix(instruction, "section=")
	section = strings.TrimSuffix(section, "\n")
	if !ok || !strings.HasSuffix(instruction, "\n") || section == "" || len(section) > 64 || strings.ContainsAny(section, "\r\n=") || !utf8.ValidString(section) {
		return "", errors.New("unsupported reviewer instruction grammar")
	}
	return section, nil
}
