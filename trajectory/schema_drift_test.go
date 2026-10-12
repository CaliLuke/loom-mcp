package trajectory_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type schemaWalker struct {
	root    map[string]any
	visited map[reflect.Type]bool
	errors  []string
}

func TestSchemaJSONTagDrift(t *testing.T) {
	assert.Empty(t, schemaDrift(schemaObject(t)))
}

func TestSchemaLimitsInvalidOptionalBoolean(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("trajectory.v1.schema.json", schemaValue(t, trajectory.Schema())))
	schema, err := compiler.Compile("trajectory.v1.schema.json#/$defs/RequestFacts")
	require.NoError(t, err)
	for _, test := range []struct {
		name, member string
		valid        bool
	}{
		{"legacy absent", "", true},
		{"invalid limits", `,"limits_invalid":true`, true},
		{"valid limits", `,"limits_invalid":false`, true},
		{"wrong type", `,"limits_invalid":"true"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := `{"call":{"RunID":"run","Seq":1,"Attempt":1},"model":"model","outcome":"ok","context_window":6000,"output_reserve":1000,"input_tokens":0,"text_bytes":3,"fixed_bytes":0,"non_text":0,"view_bearing":true,"count_latency":0` + test.member + `}`
			err := schema.Validate(schemaValue(t, []byte(data)))
			if test.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestSchemaDriftDetectsMissingAndExtraProperties(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing", func(root map[string]any) {
			delete(root["properties"].(map[string]any), "schema")
		}},
		{"extra", func(root map[string]any) {
			root["properties"].(map[string]any)["extra"] = map[string]any{}
		}},
		{"nested", func(root map[string]any) {
			defs := root["$defs"].(map[string]any)
			delete(defs["ToolStep"].(map[string]any)["properties"].(map[string]any), "duration")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := schemaObject(t)
			test.mutate(root)
			assert.NotEmpty(t, schemaDrift(root))
		})
	}
}

func schemaObject(t *testing.T) map[string]any {
	t.Helper()
	var root map[string]any
	require.NoError(t, json.Unmarshal(trajectory.Schema(), &root))
	return root
}

func schemaDrift(root map[string]any) []string {
	w := schemaWalker{root: root, visited: make(map[reflect.Type]bool)}
	w.walk(reflect.TypeFor[trajectory.Document](), root, false)
	return w.errors
}

// walk follows struct fields, slice elements, map values and nullable references.
// Raw JSON and arbitrary interface values are leaves. The sealed model.Part
// interface is expanded into every concrete wire variant, including Kind.
func (w *schemaWalker) walk(typ reflect.Type, node map[string]any, part bool) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[time.Time]() {
		return
	}
	if typ == reflect.TypeFor[model.ToolResultContentPart]() {
		w.toolResultParts(node)
		return
	}
	if typ == reflect.TypeFor[model.Part]() {
		w.parts(node)
		return
	}
	node = w.resolve(node)
	switch typ.Kind() {
	case reflect.Slice, reflect.Array:
		if typ.Elem().Kind() != reflect.Uint8 {
			w.walk(typ.Elem(), objectNode(node["items"]), false)
		}
	case reflect.Map:
		w.walk(typ.Elem(), objectNode(node["additionalProperties"]), false)
	case reflect.Struct:
		if !w.visited[typ] {
			w.visited[typ] = true
			w.fields(typ, node, part)
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.String, reflect.Interface:
		// Scalar and arbitrary JSON interface values have no properties to walk.
	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func,
		reflect.Pointer, reflect.UnsafePointer:
		w.errors = append(w.errors, "unsupported JSON type: "+typ.String())
	}
}

func (w *schemaWalker) fields(typ reflect.Type, node map[string]any, part bool) {
	props := objectNode(node["properties"])
	var want []string
	if part {
		want = append(want, "Kind")
	}
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			if typ == reflect.TypeFor[model.ToolResultPart]() && field.Name == "Parts" {
				name = "Parts"
			} else {
				continue
			}
		}
		if name == "" {
			name = field.Name
		}
		want = append(want, name)
		w.walk(field.Type, objectNode(props[name]), false)
	}
	got := make([]string, 0, len(props))
	for name := range props {
		got = append(got, name)
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		w.errors = append(w.errors, fmt.Sprintf("%s: Go properties %v, schema properties %v", typ, want, got))
	}
}

func (w *schemaWalker) parts(node map[string]any) {
	node = w.resolve(node)
	variants, ok := node["oneOf"].([]any)
	if !ok {
		w.errors = append(w.errors, "model.Part: missing wire variants")
		return
	}
	types := map[string]reflect.Type{
		"text": reflect.TypeFor[model.TextPart](), "thinking": reflect.TypeFor[model.ThinkingPart](),
		"image": reflect.TypeFor[model.ImagePart](), "document": reflect.TypeFor[model.DocumentPart](),
		"citations": reflect.TypeFor[model.CitationsPart](), "tool_use": reflect.TypeFor[model.ToolUsePart](),
		"tool_result": reflect.TypeFor[model.ToolResultPart](), "cache_checkpoint": reflect.TypeFor[model.CacheCheckpointPart](),
	}
	for _, variant := range variants {
		shape := w.resolve(objectNode(variant))
		kind, ok := objectNode(objectNode(shape["properties"])["Kind"])["const"].(string)
		typ, known := types[kind]
		if !ok || !known {
			w.errors = append(w.errors, "model.Part: unknown schema variant "+kind)
			continue
		}
		delete(types, kind)
		w.walk(typ, shape, true)
	}
	if len(types) > 0 {
		w.errors = append(w.errors, "model.Part: missing concrete wire variants")
	}
}

func (w *schemaWalker) resolve(node map[string]any) map[string]any {
	if ref, ok := node["$ref"].(string); ok {
		defs := objectNode(w.root["$defs"])
		return w.resolve(objectNode(defs[strings.TrimPrefix(ref, "#/$defs/")]))
	}
	if nullable, ok := node["anyOf"].([]any); ok {
		for _, branch := range nullable {
			shape := objectNode(branch)
			if shape["type"] != "null" {
				return w.resolve(shape)
			}
		}
	}
	return node
}

func objectNode(value any) map[string]any {
	if node, ok := value.(map[string]any); ok {
		return node
	}
	return map[string]any{}
}

func (w *schemaWalker) toolResultParts(node map[string]any) {
	node = w.resolve(node)
	variants, ok := node["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		w.errors = append(w.errors, "model.ToolResultContentPart: requires two variants")
		return
	}
	types := map[string]reflect.Type{"image": reflect.TypeFor[model.ImagePart](), "document": reflect.TypeFor[model.DocumentPart]()}
	for _, variant := range variants {
		shape := w.resolve(objectNode(variant))
		kind, _ := objectNode(objectNode(shape["properties"])["Kind"])["const"].(string)
		typ, ok := types[kind]
		if !ok {
			w.errors = append(w.errors, "model.ToolResultContentPart: invalid variant "+kind)
			continue
		}
		delete(types, kind)
		w.walk(typ, shape, true)
	}
	if len(types) > 0 {
		w.errors = append(w.errors, "model.ToolResultContentPart: missing variants")
	}
}
