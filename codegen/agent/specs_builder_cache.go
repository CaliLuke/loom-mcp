package codegen

import (
	"fmt"
	"strings"

	"github.com/CaliLuke/loom/codegen/service"
)

type toolSpecsDataBuildFunc func(genpkg string, svc *service.Data, tools []*ToolData) (*toolSpecsData, error)

type toolSpecsDataCacheKey struct {
	genpkg               string
	sourceServiceName    string
	toolsetQualifiedName string
	toolsSignature       string
}

type toolSpecsDataCache struct {
	build   toolSpecsDataBuildFunc
	entries map[toolSpecsDataCacheKey]*toolSpecsData
}

func newToolSpecsDataCache() *toolSpecsDataCache {
	return &toolSpecsDataCache{
		build:   buildToolSpecsDataFor,
		entries: make(map[toolSpecsDataCacheKey]*toolSpecsData),
	}
}

func (c *toolSpecsDataCache) specsForToolset(genpkg string, ts *ToolsetData) (*toolSpecsData, error) {
	if ts == nil {
		return nil, fmt.Errorf("agent codegen: nil toolset while building tool specs data")
	}
	svc := ts.SourceService
	if svc == nil && ts.Agent != nil {
		svc = ts.Agent.Service
	}
	key := toolSpecsDataCacheKey{
		genpkg:               genpkg,
		sourceServiceName:    toolSpecsDataCacheServiceName(ts, svc),
		toolsetQualifiedName: ts.QualifiedName,
		toolsSignature:       toolSpecsDataCacheToolsSignature(ts.Tools),
	}
	if key.toolsetQualifiedName == "" {
		key.toolsetQualifiedName = ts.Name
	}
	if c == nil {
		return buildToolSpecsDataFor(genpkg, svc, ts.Tools)
	}
	if specs, ok := c.entries[key]; ok {
		return specs, nil
	}
	specs, err := c.build(genpkg, svc, ts.Tools)
	if err != nil {
		return nil, err
	}
	c.entries[key] = specs
	return specs, nil
}

func toolSpecsDataCacheToolsSignature(tools []*ToolData) string {
	if len(tools) == 0 {
		return ""
	}
	var b strings.Builder
	for _, tool := range tools {
		if tool == nil {
			b.WriteString("<nil>")
			b.WriteByte(0)
			continue
		}
		if tool.QualifiedName != "" {
			b.WriteString(tool.QualifiedName)
		} else {
			b.WriteString(tool.Name)
		}
		b.WriteByte(0)
	}
	return b.String()
}

func toolSpecsDataCacheServiceName(ts *ToolsetData, svc *service.Data) string {
	if svc == nil {
		if ts != nil {
			return ts.SourceServiceName
		}
		return ""
	}
	return svc.Name
}
