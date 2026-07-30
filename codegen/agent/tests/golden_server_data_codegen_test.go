package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	codegen "github.com/CaliLuke/loom/codegen"
	"github.com/stretchr/testify/require"
)

func TestGolden_ServerData_UsesGeneratedCodec(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.ServiceToolsetBindSelfServerData())

	provider := generatedContentBySuffix(t, files, "toolsets/lookup/provider.go")
	require.Contains(t, provider, "ByIDAuraEvidenceServerDataCodec.ToJSON")
	require.Contains(t, provider, "InitByIDAuraEvidenceServerData(typedMethodOut.Evidence)")
	require.NotContains(t, provider, "json.Marshal(methodOut.")
	require.Contains(t, provider, "var serverData rawjson.Message")
	require.Contains(t, provider, "serverData = rawjson.Message(data)")
	require.NotContains(t, provider, "rawjson.RawJSON")

	executor := generatedContentBySuffix(t, files, "agents/scribe/lookup/service_executor.go")
	require.Contains(t, executor, "lookup.DispatchByIDMethod(ctx, meta, json.RawMessage(call.Payload), call.Labels, lookup.ByIDDispatchOptions{")
	require.Contains(t, executor, "Call: caller,")
	require.Contains(t, executor, "MapPayload: cfg.mapPayload,")
	require.Contains(t, executor, "MapResult: cfg.mapResult,")
	require.Contains(t, executor, "Injectors: dispatchInjectors(cfg.injectors),")
	require.NotContains(t, executor, "ByIDAuraEvidenceServerDataCodec.ToJSON")
	require.NotContains(t, executor, "lookup.InitByIDAuraEvidenceServerData")
	require.NotContains(t, executor, "rawjson.Message")
}

func generatedContentBySuffix(t *testing.T, files []*codegen.File, suffix string) string {
	t.Helper()

	normSuffix := filepath.ToSlash(suffix)
	for _, f := range files {
		p := filepath.ToSlash(f.Path)
		if strings.HasSuffix(p, normSuffix) {
			return fileContent(t, files, p)
		}
	}

	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, filepath.ToSlash(f.Path))
	}
	require.Failf(t, "generated file not found", "suffix %q not found in generated files: %s", normSuffix, strings.Join(paths, ", "))
	return ""
}
