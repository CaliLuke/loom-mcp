package dsl

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestPackageDocIndexMentionsCurrentDSLFunctions(t *testing.T) {
	doc, err := os.ReadFile("doc.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)

	for _, name := range []string{
		"ServerData", "Cache", "AfterSystem", "AfterTools",
		"Context", "TriggerAt", "KeepRecentTokens", "Summarize", "Recall", "Disable", "Instructions", "Prompt", "KeepResultInContext",
		"FromSkills",
		"FromArtifacts",
		"FromMemory",
		"Workflow",
		"Step",
		"Parallel",
		"Join",
		"RequestInput",
		"Loop",
		"Branch",
	} {
		if !strings.Contains(text, "["+name+"]") {
			t.Fatalf("package doc index is missing [%s]", name)
		}
	}

	for _, name := range []string{
		"Artifact", "History", "KeepRecentTurns", "CompressAtTurns", "CompressAtMaxInputTokens", "KeepMaxTurns", "KeepMaxInputTokens",
		"Compress",
	} {
		if strings.Contains(text, "["+name+"]") {
			t.Fatalf("package doc index links obsolete [%s]", name)
		}
	}
}

func TestPackageDocAttaches(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "doc.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if file.Doc == nil {
		t.Fatal("package dsl has no attached package documentation")
	}
	if !strings.HasPrefix(file.Doc.Text(), "Package dsl ") {
		t.Fatal("package dsl documentation is missing its package description")
	}
}
