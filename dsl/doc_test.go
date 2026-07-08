package dsl

import (
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
		"ServerData",
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
		"Artifact",
		"Compress",
	} {
		if strings.Contains(text, "["+name+"]") {
			t.Fatalf("package doc index links obsolete [%s]", name)
		}
	}
}
