package codegen

import (
	"path/filepath"
	"time"

	"github.com/CaliLuke/loom-mcp/codegen/shared"
	agentsExpr "github.com/CaliLuke/loom-mcp/expr/agent"
	"github.com/CaliLuke/loom/codegen"
	loomexpr "github.com/CaliLuke/loom/expr"
	"github.com/dave/jennifer/jen"
)

type (
	// RegistryClientData holds the template-ready data for generating a registry
	// client. Each declared Registry in the DSL produces one client package.
	RegistryClientData struct {
		// Name is the DSL-provided registry identifier.
		Name string
		// GoName is the exported Go identifier derived from Name.
		GoName string
		// Description is the DSL-provided description.
		Description string
		// URL is the registry endpoint URL.
		URL string
		// APIVersion is the registry API version (e.g., "v1").
		APIVersion string
		// PackageName is the Go package name for the generated client.
		PackageName string
		// Dir is the output directory for the client package.
		Dir string
		// ImportPath is the full Go import path to the client package.
		ImportPath string
		// Timeout is the HTTP request timeout.
		Timeout time.Duration
		// RetryPolicy contains retry configuration.
		RetryPolicy *RetryPolicyData
		// SyncInterval is how often to refresh the catalog.
		SyncInterval time.Duration
		// CacheTTL is the local cache duration.
		CacheTTL time.Duration
		// SecuritySchemes contains the security requirements.
		SecuritySchemes []*SecuritySchemeData
		// Federation contains federation configuration if present.
		Federation *FederationData
	}

	// RetryPolicyData holds retry configuration for code generation.
	RetryPolicyData struct {
		// MaxRetries is the maximum number of retry attempts.
		MaxRetries int
		// BackoffBase is the initial backoff duration.
		BackoffBase time.Duration
		// BackoffMax is the maximum backoff duration.
		BackoffMax time.Duration
	}

	// SecuritySchemeData holds security scheme information for code generation.
	SecuritySchemeData struct {
		// Name is the security scheme name.
		Name string
		// Kind is the Goa security scheme kind.
		Kind loomexpr.SchemeKind
		// In is where the credential is sent (e.g., "header", "query").
		In string
		// ParamName is the parameter name (e.g., "Authorization").
		ParamName string
		// Scopes lists required OAuth2 scopes.
		Scopes []string
	}

	// FederationData holds federation configuration for code generation.
	FederationData struct {
		// Include patterns for namespaces to import.
		Include []string
		// Exclude patterns for namespaces to skip.
		Exclude []string
	}
)

const authLocationHeader = "header"

// registryClientFiles generates the registry client files for all declared
// registries. Each registry produces a client package under
// gen/<service>/registry/<name>/.
func registryClientFiles(genpkg string, svc *ServiceAgentsData) []*codegen.File {
	if svc == nil || svc.Service == nil {
		return nil
	}

	var files []*codegen.File
	for _, reg := range agentsExpr.Root.Registries {
		if reg == nil {
			continue
		}
		data := newRegistryClientData(genpkg, svc.Service.PathName, reg)
		if data == nil {
			continue
		}

		// Generate client.go
		clientFile := registryClientFile(data)
		if clientFile != nil {
			files = append(files, clientFile)
		}

		// Generate options.go
		optionsFile := registryClientOptionsFile(data)
		if optionsFile != nil {
			files = append(files, optionsFile)
		}
	}
	return files
}

// newRegistryClientData transforms a RegistryExpr into template-ready data.
func newRegistryClientData(genpkg, svcPath string, reg *agentsExpr.RegistryExpr) *RegistryClientData {
	if reg == nil {
		return nil
	}

	goName := codegen.Goify(reg.Name, true)
	pkgName := codegen.SnakeCase(reg.Name)
	dir := filepath.Join("gen", svcPath, "registry", pkgName)
	importPath := shared.JoinImportPath(genpkg, filepath.Join(svcPath, "registry", pkgName))

	data := &RegistryClientData{
		Name:         reg.Name,
		GoName:       goName,
		Description:  reg.Description,
		URL:          reg.URL,
		APIVersion:   reg.APIVersion,
		PackageName:  pkgName,
		Dir:          dir,
		ImportPath:   importPath,
		Timeout:      reg.Timeout,
		SyncInterval: reg.SyncInterval,
		CacheTTL:     reg.CacheTTL,
	}

	// Convert retry policy
	if reg.RetryPolicy != nil {
		data.RetryPolicy = &RetryPolicyData{
			MaxRetries:  reg.RetryPolicy.MaxRetries,
			BackoffBase: reg.RetryPolicy.BackoffBase,
			BackoffMax:  reg.RetryPolicy.BackoffMax,
		}
	}

	// Convert security schemes
	for _, sec := range reg.Requirements {
		for _, scheme := range sec.Schemes {
			if scheme.Kind == loomexpr.NoKind {
				// Skip schemes with no kind specified.
				continue
			}
			schemeData := &SecuritySchemeData{
				Name: scheme.SchemeName,
				Kind: scheme.Kind,
			}
			switch scheme.Kind {
			case loomexpr.APIKeyKind:
				schemeData.In = scheme.In
				schemeData.ParamName = scheme.Name
			case loomexpr.OAuth2Kind:
				schemeData.Scopes = sec.Scopes
			case loomexpr.JWTKind:
				schemeData.In = authLocationHeader
				schemeData.ParamName = "Authorization"
			case loomexpr.BasicAuthKind:
				schemeData.In = authLocationHeader
				schemeData.ParamName = "Authorization"
			case loomexpr.NoKind:
				// Already handled above
			}
			data.SecuritySchemes = append(data.SecuritySchemes, schemeData)
		}
	}

	// Convert federation
	if reg.Federation != nil {
		data.Federation = &FederationData{
			Include: reg.Federation.Include,
			Exclude: reg.Federation.Exclude,
		}
	}

	return data
}

// registryClientFile generates the main client.go file for a registry.
func registryClientFile(data *RegistryClientData) *codegen.File {
	if data == nil {
		return nil
	}

	imports := []*codegen.ImportSpec{
		{Path: "context"},
		{Path: "encoding/json"},
		{Path: "errors"},
		{Path: "fmt"},
		{Path: "io"},
		{Path: "net/http"},
		{Path: "net/url"},
		{Path: "time"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/registry", Name: "registry"},
	}

	sections := []codegen.Section{
		codegen.Header(data.GoName+" registry client", data.PackageName, imports),
		registryClientSection(data),
	}

	return &codegen.File{
		Path:     filepath.Join(data.Dir, "client.go"),
		Sections: sections,
	}
}

// registryClientOptionsFile generates the options.go file for a registry client.
func registryClientOptionsFile(data *RegistryClientData) *codegen.File {
	if data == nil {
		return nil
	}

	imports := []*codegen.ImportSpec{
		{Path: "net/http"},
		{Path: "time"},
	}

	sections := []codegen.Section{
		codegen.Header(data.GoName+" registry client options", data.PackageName, imports),
		registryClientOptionsSection(data),
	}

	return &codegen.File{
		Path:     filepath.Join(data.Dir, "options.go"),
		Sections: sections,
	}
}

func registryClientSection(data *RegistryClientData) codegen.Section {
	return codegen.MustJenniferSection("registry-client", func(stmt *jen.Statement) {
		emitRegistryClientTypes(stmt, data)
		emitRegistryClientConstants(stmt, data)
		emitRegistryClientConstructor(stmt, data)
		emitRegistryClientMethods(stmt)
		emitRegistryClientPrivateMethods(stmt)
	})
}

func registryClientOptionsSection(data *RegistryClientData) codegen.Section {
	return codegen.MustJenniferSection("registry-client-options", func(stmt *jen.Statement) {
		emitRegistryClientOptionTypes(stmt, data)
		emitRegistryClientOptionFuncs(stmt, data)
		emitRegistryClientAuthMethods(stmt, data)
	})
}

func emitRegistryClientTypes(stmt *jen.Statement, data *RegistryClientData) {
	codegen.Doc(stmt, "ToolsetInfo contains metadata about a toolset available in the registry.")
	stmt.Type().Id("ToolsetInfo").Struct(
		jen.Comment("ID is the unique identifier for the toolset."),
		jen.Id("ID").String().Tag(map[string]string{"json": "id"}),
		jen.Comment("Name is the human-readable name."),
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Comment("Description provides details about the toolset."),
		jen.Id("Description").String().Tag(map[string]string{"json": "description,omitempty"}),
		jen.Comment("Version is the toolset version."),
		jen.Id("Version").String().Tag(map[string]string{"json": "version,omitempty"}),
		jen.Comment("Tags are metadata tags for discovery."),
		jen.Id("Tags").Index().String().Tag(map[string]string{"json": "tags,omitempty"}),
	)
	stmt.Line()

	codegen.Doc(stmt, "ToolsetSchema contains the full schema for a toolset including its tools.")
	stmt.Type().Id("ToolsetSchema").Struct(
		jen.Comment("ID is the unique identifier for the toolset."),
		jen.Id("ID").String().Tag(map[string]string{"json": "id"}),
		jen.Comment("Name is the human-readable name."),
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Comment("Description provides details about the toolset."),
		jen.Id("Description").String().Tag(map[string]string{"json": "description,omitempty"}),
		jen.Comment("Version is the toolset version."),
		jen.Id("Version").String().Tag(map[string]string{"json": "version,omitempty"}),
		jen.Comment("Tools contains the tool definitions."),
		jen.Id("Tools").Index().Op("*").Id("ToolSchema").Tag(map[string]string{"json": "tools,omitempty"}),
	)
	stmt.Line()

	codegen.Doc(stmt, "ToolSchema contains the schema for a single tool.")
	stmt.Type().Id("ToolSchema").Struct(
		jen.Comment("Name is the tool identifier."),
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Comment("Description explains what the tool does."),
		jen.Id("Description").String().Tag(map[string]string{"json": "description,omitempty"}),
		jen.Comment("InputSchema is the JSON Schema for tool input."),
		jen.Id("InputSchema").Qual("encoding/json", "RawMessage").Tag(map[string]string{"json": "inputSchema,omitempty"}),
	)
	stmt.Line()

	codegen.Doc(stmt, "SearchResult contains a single search result from the registry.")
	stmt.Type().Id("SearchResult").Struct(
		jen.Comment("ID is the unique identifier."),
		jen.Id("ID").String().Tag(map[string]string{"json": "id"}),
		jen.Comment("Name is the human-readable name."),
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Comment("Description provides details."),
		jen.Id("Description").String().Tag(map[string]string{"json": "description,omitempty"}),
		jen.Comment(`Type indicates the result type (e.g., "tool", "toolset", "agent").`),
		jen.Id("Type").String().Tag(map[string]string{"json": "type"}),
		jen.Comment("SchemaRef is a reference to the full schema."),
		jen.Id("SchemaRef").String().Tag(map[string]string{"json": "schemaRef,omitempty"}),
		jen.Comment("RelevanceScore indicates how relevant this result is to the query."),
		jen.Id("RelevanceScore").Float64().Tag(map[string]string{"json": "relevanceScore"}),
		jen.Comment("Tags are metadata tags."),
		jen.Id("Tags").Index().String().Tag(map[string]string{"json": "tags,omitempty"}),
		jen.Comment("Origin indicates the federation source if applicable."),
		jen.Id("Origin").String().Tag(map[string]string{"json": "origin,omitempty"}),
	)
	stmt.Line()

	codegen.Doc(stmt, "AuthProvider provides authentication credentials for registry requests.")
	stmt.Type().Id("AuthProvider").Interface(
		jen.Comment("ApplyAuth adds authentication to the request."),
		jen.Id("ApplyAuth").Params(jen.Id("req").Op("*").Qual("net/http", "Request")).Error(),
	)
	stmt.Line()

	codegen.Doc(stmt, "Client is a typed client for the "+data.Name+" registry.")
	stmt.Type().Id("Client").Struct(
		jen.Id("endpoint").String(),
		jen.Id("httpClient").Op("*").Qual("net/http", "Client"),
		jen.Id("auth").Id("AuthProvider"),
		jen.Id("timeout").Qual("time", "Duration"),
		jen.Id("retryMax").Int(),
		jen.Id("retryBase").Qual("time", "Duration"),
	)
	stmt.Line()

	codegen.Doc(stmt, "SemanticSearchOptions configures semantic search behavior.")
	stmt.Type().Id("SemanticSearchOptions").Struct(
		jen.Comment("Types filters results by type."),
		jen.Id("Types").Index().String(),
		jen.Comment("Tags filters results by tags."),
		jen.Id("Tags").Index().String(),
		jen.Comment("MaxResults limits the number of results."),
		jen.Id("MaxResults").Int(),
	)
	stmt.Line()

	codegen.Doc(stmt, "SearchCapabilities describes what search features the registry supports.")
	stmt.Type().Id("SearchCapabilities").Struct(
		jen.Comment("SemanticSearch indicates if the registry supports semantic/vector search."),
		jen.Id("SemanticSearch").Bool(),
		jen.Comment("KeywordSearch indicates if the registry supports keyword-based search."),
		jen.Id("KeywordSearch").Bool(),
		jen.Comment("TagFiltering indicates if the registry supports filtering by tags."),
		jen.Id("TagFiltering").Bool(),
		jen.Comment("TypeFiltering indicates if the registry supports filtering by type."),
		jen.Id("TypeFiltering").Bool(),
	)
	stmt.Line()

	codegen.Doc(stmt, "RegistryError represents an error response from the registry.")
	stmt.Type().Id("RegistryError").Struct(
		jen.Id("StatusCode").Int(),
		jen.Id("Message").String(),
	)
	stmt.Line()

	codegen.Doc(stmt, "bytesReader is a simple io.Reader for byte slices.")
	stmt.Type().Id("bytesReader").Struct(
		jen.Id("data").Index().Byte(),
		jen.Id("pos").Int(),
	)
	stmt.Line()
}

func emitRegistryClientConstants(stmt *jen.Statement, data *RegistryClientData) {
	stmt.Comment("Static URL path constants for the " + data.Name + " registry.").Line()
	stmt.Comment("These are generated at compile time based on the registry's API version.").Line()
	stmt.Const().Defs(
		jen.Comment("pathToolsets is the base path for toolset operations.").Line().Id("pathToolsets").Op("=").Lit("/"+data.APIVersion+"/toolsets"),
		jen.Line().Comment("pathSearch is the path for keyword search.").Line().Id("pathSearch").Op("=").Lit("/"+data.APIVersion+"/search"),
		jen.Line().Comment("pathSemanticSearch is the path for semantic search.").Line().Id("pathSemanticSearch").Op("=").Lit("/"+data.APIVersion+"/search/semantic"),
		jen.Line().Comment("pathCapabilities is the path for capabilities endpoint.").Line().Id("pathCapabilities").Op("=").Lit("/"+data.APIVersion+"/capabilities"),
	)
	stmt.Line()
}

func emitRegistryClientConstructor(stmt *jen.Statement, data *RegistryClientData) {
	timeout := jen.Lit(30).Op("*").Qual("time", "Second")
	if data.Timeout > 0 {
		timeout = jen.Qual("time", "Duration").Call(jen.Lit(int64(data.Timeout)))
	}
	retryMax := jen.Lit(3)
	retryBase := jen.Qual("time", "Second")
	if data.RetryPolicy != nil {
		retryMax = jen.Lit(data.RetryPolicy.MaxRetries)
		retryBase = jen.Qual("time", "Duration").Call(jen.Lit(int64(data.RetryPolicy.BackoffBase)))
	}

	codegen.Doc(stmt, "NewClient creates a new registry client with the given options.")
	stmt.Func().Id("NewClient").Params(jen.Id("opts").Op("...").Id("Option")).Op("*").Id("Client").Block(
		jen.Id("c").Op(":=").Op("&").Id("Client").Values(jen.Dict{
			jen.Id("endpoint"):   jen.Lit(data.URL),
			jen.Id("httpClient"): jen.Qual("net/http", "DefaultClient"),
			jen.Id("timeout"):    timeout,
			jen.Id("retryMax"):   retryMax,
			jen.Id("retryBase"):  retryBase,
		}),
		jen.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
			jen.If(jen.Id("opt").Op("!=").Nil()).Block(
				jen.Id("opt").Call(jen.Id("c")),
			),
		),
		jen.Return(jen.Id("c")),
	)
	stmt.Line()
}

func emitRegistryClientMethods(stmt *jen.Statement) {
	codegen.Doc(stmt, "ListToolsets returns all available toolsets from the registry.")
	stmt.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("ListToolsets").
		Params(jen.Id("ctx").Qual("context", "Context")).
		Params(jen.Index().Op("*").Id("ToolsetInfo"), jen.Error()).
		Block(
			jen.Id("u").Op(":=").Id("c").Dot("endpoint").Op("+").Id("pathToolsets"),
			jen.Var().Id("result").Index().Op("*").Id("ToolsetInfo"),
			jen.If(jen.Id("err").Op(":=").Id("c").Dot("doRequest").Call(jen.Id("ctx"), jen.Qual("net/http", "MethodGet"), jen.Id("u"), jen.Nil(), jen.Op("&").Id("result")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Id("result"), jen.Nil()),
		)
	stmt.Line()

	codegen.Doc(stmt, "GetToolset retrieves the full schema for a specific toolset.")
	stmt.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("GetToolset").
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("name").String()).
		Params(jen.Op("*").Id("ToolsetSchema"), jen.Error()).
		Block(
			jen.Id("u").Op(":=").Id("c").Dot("endpoint").Op("+").Id("pathToolsets").Op("+").Lit("/").Op("+").Id("name"),
			jen.Var().Id("result").Id("ToolsetSchema"),
			jen.If(jen.Id("err").Op(":=").Id("c").Dot("doRequest").Call(jen.Id("ctx"), jen.Qual("net/http", "MethodGet"), jen.Id("u"), jen.Nil(), jen.Op("&").Id("result")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Op("&").Id("result"), jen.Nil()),
		)
	stmt.Line()

	codegen.Doc(stmt, "Search performs a keyword search on the registry.\nThis is the basic search method that all registries support.")
	stmt.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Search").
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("query").String()).
		Params(jen.Index().Op("*").Id("SearchResult"), jen.Error()).
		Block(
			jen.Id("u").Op(":=").Id("c").Dot("endpoint").Op("+").Id("pathSearch").Op("+").Lit("?q=").Op("+").Qual("net/url", "QueryEscape").Call(jen.Id("query")),
			jen.Var().Id("result").Index().Op("*").Id("SearchResult"),
			jen.If(jen.Id("err").Op(":=").Id("c").Dot("doRequest").Call(jen.Id("ctx"), jen.Qual("net/http", "MethodGet"), jen.Id("u"), jen.Nil(), jen.Op("&").Id("result")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Id("result"), jen.Nil()),
		)
	stmt.Line()

	codegen.Doc(stmt, "SemanticSearch performs a semantic/vector search on the registry.\nThis method uses the registry's semantic search endpoint if available.\nFalls back to keyword search if semantic search is not supported.")
	stmt.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("SemanticSearch").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("query").String(),
			jen.Id("opts").Id("SemanticSearchOptions"),
		).
		Params(jen.Index().Op("*").Id("SearchResult"), jen.Error()).
		Block(
			jen.Comment("Build query string"),
			jen.Id("q").Op(":=").Qual("net/url", "Values").Values(),
			jen.Id("q").Dot("Set").Call(jen.Lit("q"), jen.Id("query")),
			jen.If(jen.Id("opts").Dot("MaxResults").Op(">").Lit(0)).Block(
				jen.Id("q").Dot("Set").Call(jen.Lit("limit"), jen.Qual("fmt", "Sprintf").Call(jen.Lit("%d"), jen.Id("opts").Dot("MaxResults"))),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("t")).Op(":=").Range().Id("opts").Dot("Types")).Block(
				jen.Id("q").Dot("Add").Call(jen.Lit("type"), jen.Id("t")),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("tag")).Op(":=").Range().Id("opts").Dot("Tags")).Block(
				jen.Id("q").Dot("Add").Call(jen.Lit("tag"), jen.Id("tag")),
			),
			jen.Id("u").Op(":=").Id("c").Dot("endpoint").Op("+").Id("pathSemanticSearch").Op("+").Lit("?").Op("+").Id("q").Dot("Encode").Call(),
			jen.Var().Id("result").Index().Op("*").Id("SearchResult"),
			jen.If(jen.Id("err").Op(":=").Id("c").Dot("doRequest").Call(jen.Id("ctx"), jen.Qual("net/http", "MethodGet"), jen.Id("u"), jen.Nil(), jen.Op("&").Id("result")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Comment("Check if semantic search is not supported (404 or 501)"),
				jen.Var().Id("regErr").Op("*").Id("RegistryError"),
				jen.If(
					jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("regErr")).Op("&&").
						Parens(jen.Id("regErr").Dot("StatusCode").Op("==").Qual("net/http", "StatusNotFound").Op("||").Id("regErr").Dot("StatusCode").Op("==").Qual("net/http", "StatusNotImplemented")),
				).Block(
					jen.Comment("Fall back to keyword search"),
					jen.Return(jen.Id("c").Dot("Search").Call(jen.Id("ctx"), jen.Id("query"))),
				),
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Id("result"), jen.Nil()),
		)
	stmt.Line()

	codegen.Doc(stmt, "Capabilities returns the search capabilities of this registry.\nThis queries the registry's capabilities endpoint to determine\nwhat search features are supported.")
	stmt.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("Capabilities").Params().Id("SearchCapabilities").Block(
		jen.Comment("Default capabilities - all registries support keyword search"),
		jen.Id("caps").Op(":=").Id("SearchCapabilities").Values(jen.Dict{
			jen.Id("KeywordSearch"):  jen.True(),
			jen.Id("SemanticSearch"): jen.False(),
			jen.Id("TagFiltering"):   jen.True(),
			jen.Id("TypeFiltering"):  jen.True(),
		}),
		jen.Comment("Try to fetch capabilities from the registry"),
		jen.List(jen.Id("ctx"), jen.Id("cancel")).Op(":=").Qual("context", "WithTimeout").Call(jen.Qual("context", "Background").Call(), jen.Lit(5).Op("*").Qual("time", "Second")),
		jen.Defer().Id("cancel").Call(),
		jen.Id("u").Op(":=").Id("c").Dot("endpoint").Op("+").Id("pathCapabilities"),
		jen.Var().Id("remoteCaps").Id("SearchCapabilities"),
		jen.If(jen.Id("err").Op(":=").Id("c").Dot("doRequest").Call(jen.Id("ctx"), jen.Qual("net/http", "MethodGet"), jen.Id("u"), jen.Nil(), jen.Op("&").Id("remoteCaps")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Comment("If capabilities endpoint doesn't exist, return defaults"),
			jen.Return(jen.Id("caps")),
		),
		jen.Comment("Merge remote capabilities (keyword search is always true)"),
		jen.Id("remoteCaps").Dot("KeywordSearch").Op("=").True(),
		jen.Return(jen.Id("remoteCaps")),
	)
	stmt.Line()
}

func emitRegistryClientPrivateMethods(stmt *jen.Statement) {
	stmt.Comment("doRequest performs an HTTP request with retry logic.").Line()
	stmt.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("doRequest").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("method").String(),
			jen.Id("reqURL").String(),
			jen.Id("body").Index().Byte(),
			jen.Id("result").Any(),
		).
		Error().
		Block(
			jen.Var().Id("lastErr").Error(),
			jen.For(jen.Id("attempt").Op(":=").Lit(0), jen.Id("attempt").Op("<=").Id("c").Dot("retryMax"), jen.Id("attempt").Op("++")).Block(
				jen.If(jen.Id("attempt").Op(">").Lit(0)).Block(
					jen.Comment("Exponential backoff"),
					jen.Id("backoff").Op(":=").Id("c").Dot("retryBase").Op("*").Qual("time", "Duration").Call(jen.Lit(1).Op("<<").Uint().Call(jen.Id("attempt").Op("-").Lit(1))),
					jen.Select().Block(
						jen.Case(jen.Op("<-").Id("ctx").Dot("Done").Call()).Block(
							jen.Return(jen.Id("ctx").Dot("Err").Call()),
						),
						jen.Case(jen.Op("<-").Qual("time", "After").Call(jen.Id("backoff"))),
					),
				),
				jen.Id("err").Op(":=").Id("c").Dot("doSingleRequest").Call(jen.Id("ctx"), jen.Id("method"), jen.Id("reqURL"), jen.Id("body"), jen.Id("result")),
				jen.If(jen.Id("err").Op("==").Nil()).Block(
					jen.Return(jen.Nil()),
				),
				jen.Id("lastErr").Op("=").Id("err"),
				jen.Comment("Don't retry on context cancellation or client errors"),
				jen.If(jen.Id("ctx").Dot("Err").Call().Op("!=").Nil()).Block(
					jen.Return(jen.Id("ctx").Dot("Err").Call()),
				),
			),
			jen.Return(jen.Id("lastErr")),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("c").Op("*").Id("Client")).Id("doSingleRequest").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("method").String(),
			jen.Id("reqURL").String(),
			jen.Id("body").Index().Byte(),
			jen.Id("result").Any(),
		).
		Error().
		Block(
			jen.List(jen.Id("ctx"), jen.Id("cancel")).Op(":=").Qual("context", "WithTimeout").Call(jen.Id("ctx"), jen.Id("c").Dot("timeout")),
			jen.Defer().Id("cancel").Call(),
			jen.Var().Id("bodyReader").Qual("io", "Reader"),
			jen.If(jen.Id("body").Op("!=").Nil()).Block(
				jen.Id("bodyReader").Op("=").Op("&").Id("bytesReader").Values(jen.Dict{
					jen.Id("data"): jen.Id("body"),
				}),
			),
			jen.List(jen.Id("req"), jen.Id("err")).Op(":=").Qual("net/http", "NewRequestWithContext").Call(jen.Id("ctx"), jen.Id("method"), jen.Id("reqURL"), jen.Id("bodyReader")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("creating request: %w"), jen.Id("err"))),
			),
			jen.If(jen.Id("body").Op("!=").Nil()).Block(
				jen.Id("req").Dot("Header").Dot("Set").Call(jen.Lit("Content-Type"), jen.Lit("application/json")),
			),
			jen.Id("req").Dot("Header").Dot("Set").Call(jen.Lit("Accept"), jen.Lit("application/json")),
			jen.Comment("Inject trace context for distributed tracing"),
			jen.Qual("github.com/CaliLuke/loom-mcp/runtime/registry", "InjectTraceContext").Call(jen.Id("ctx"), jen.Id("req").Dot("Header")),
			jen.If(jen.Id("c").Dot("auth").Op("!=").Nil()).Block(
				jen.If(jen.Id("err").Op(":=").Id("c").Dot("auth").Dot("ApplyAuth").Call(jen.Id("req")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("applying auth: %w"), jen.Id("err"))),
				),
			),
			jen.List(jen.Id("resp"), jen.Id("err")).Op(":=").Id("c").Dot("httpClient").Dot("Do").Call(jen.Id("req")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("executing request: %w"), jen.Id("err"))),
			),
			jen.Defer().Id("resp").Dot("Body").Dot("Close").Call(),
			jen.If(jen.Id("resp").Dot("StatusCode").Op(">=").Lit(400)).Block(
				jen.List(jen.Id("respBody"), jen.Id("_")).Op(":=").Qual("io", "ReadAll").Call(jen.Id("resp").Dot("Body")),
				jen.Return(jen.Op("&").Id("RegistryError").Values(jen.Dict{
					jen.Id("StatusCode"): jen.Id("resp").Dot("StatusCode"),
					jen.Id("Message"):    jen.String().Call(jen.Id("respBody")),
				})),
			),
			jen.If(jen.Id("result").Op("!=").Nil().Op("&&").Id("resp").Dot("StatusCode").Op("!=").Qual("net/http", "StatusNoContent")).Block(
				jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "NewDecoder").Call(jen.Id("resp").Dot("Body")).Dot("Decode").Call(jen.Id("result")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("decoding response: %w"), jen.Id("err"))),
				),
			),
			jen.Return(jen.Nil()),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("e").Op("*").Id("RegistryError")).Id("Error").Params().String().Block(
		jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit("registry error (status %d): %s"), jen.Id("e").Dot("StatusCode"), jen.Id("e").Dot("Message"))),
	)
	stmt.Line()

	stmt.Func().Params(jen.Id("r").Op("*").Id("bytesReader")).Id("Read").
		Params(jen.Id("p").Index().Byte()).
		Params(jen.Id("n").Int(), jen.Err().Error()).
		Block(
			jen.If(jen.Id("r").Dot("pos").Op(">=").Len(jen.Id("r").Dot("data"))).Block(
				jen.Return(jen.Lit(0), jen.Qual("io", "EOF")),
			),
			jen.Id("n").Op("=").Copy(jen.Id("p"), jen.Id("r").Dot("data").Index(jen.Id("r").Dot("pos").Op(":"))),
			jen.Id("r").Dot("pos").Op("+=").Id("n"),
			jen.Return(jen.Id("n"), jen.Nil()),
		)
	stmt.Line()
}

func emitRegistryClientOptionTypes(stmt *jen.Statement, data *RegistryClientData) {
	codegen.Doc(stmt, "Option configures a registry client.")
	stmt.Type().Id("Option").Func().Params(jen.Op("*").Id("Client"))
	stmt.Line()

	for _, scheme := range data.SecuritySchemes {
		authTypeName := codegen.Goify(scheme.Name, true) + "Auth"
		switch scheme.Kind {
		case loomexpr.APIKeyKind:
			codegen.Doc(stmt, authTypeName+" provides API key authentication.")
			stmt.Type().Id(authTypeName).Struct(
				jen.Comment("Key is the API key value."),
				jen.Id("Key").String(),
			)
		case loomexpr.OAuth2Kind:
			codegen.Doc(stmt, authTypeName+" provides OAuth2 authentication.")
			stmt.Type().Id(authTypeName).Struct(
				jen.Comment("Token is the OAuth2 access token."),
				jen.Id("Token").String(),
			)
		case loomexpr.JWTKind:
			codegen.Doc(stmt, authTypeName+" provides JWT authentication.")
			stmt.Type().Id(authTypeName).Struct(
				jen.Comment("Token is the JWT token."),
				jen.Id("Token").String(),
			)
		case loomexpr.BasicAuthKind:
			codegen.Doc(stmt, authTypeName+" provides Basic authentication.")
			stmt.Type().Id(authTypeName).Struct(
				jen.Comment("Username is the basic auth username."),
				jen.Id("Username").String(),
				jen.Comment("Password is the basic auth password."),
				jen.Id("Password").String(),
			)
		case loomexpr.NoKind:
			continue
		default:
			continue
		}
		stmt.Line()
	}
}

func emitRegistryClientOptionFuncs(stmt *jen.Statement, data *RegistryClientData) {
	codegen.Doc(stmt, "WithHTTPClient sets a custom HTTP client.")
	stmt.Func().Id("WithHTTPClient").Params(jen.Id("client").Op("*").Qual("net/http", "Client")).Id("Option").Block(
		jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("Client")).Block(
			jen.If(jen.Id("client").Op("!=").Nil()).Block(
				jen.Id("c").Dot("httpClient").Op("=").Id("client"),
			),
		)),
	)
	stmt.Line()

	codegen.Doc(stmt, "WithAuth sets the authentication provider.")
	stmt.Func().Id("WithAuth").Params(jen.Id("auth").Id("AuthProvider")).Id("Option").Block(
		jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("Client")).Block(
			jen.Id("c").Dot("auth").Op("=").Id("auth"),
		)),
	)
	stmt.Line()

	codegen.Doc(stmt, "WithTimeout sets the request timeout.")
	stmt.Func().Id("WithTimeout").Params(jen.Id("timeout").Qual("time", "Duration")).Id("Option").Block(
		jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("Client")).Block(
			jen.If(jen.Id("timeout").Op(">").Lit(0)).Block(
				jen.Id("c").Dot("timeout").Op("=").Id("timeout"),
			),
		)),
	)
	stmt.Line()

	codegen.Doc(stmt, "WithRetry configures retry behavior.")
	stmt.Func().Id("WithRetry").Params(jen.Id("maxRetries").Int(), jen.Id("backoffBase").Qual("time", "Duration")).Id("Option").Block(
		jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("Client")).Block(
			jen.If(jen.Id("maxRetries").Op(">=").Lit(0)).Block(
				jen.Id("c").Dot("retryMax").Op("=").Id("maxRetries"),
			),
			jen.If(jen.Id("backoffBase").Op(">").Lit(0)).Block(
				jen.Id("c").Dot("retryBase").Op("=").Id("backoffBase"),
			),
		)),
	)
	stmt.Line()

	codegen.Doc(stmt, "WithEndpoint overrides the default registry endpoint.")
	stmt.Func().Id("WithEndpoint").Params(jen.Id("endpoint").String()).Id("Option").Block(
		jen.Return(jen.Func().Params(jen.Id("c").Op("*").Id("Client")).Block(
			jen.If(jen.Id("endpoint").Op("!=").Lit("")).Block(
				jen.Id("c").Dot("endpoint").Op("=").Id("endpoint"),
			),
		)),
	)
	stmt.Line()

	for _, scheme := range data.SecuritySchemes {
		emitRegistryClientWithAuthOption(stmt, scheme)
	}
}

func emitRegistryClientWithAuthOption(stmt *jen.Statement, scheme *SecuritySchemeData) {
	typeName := codegen.Goify(scheme.Name, true) + "Auth"
	funcName := "With" + codegen.Goify(scheme.Name, true)

	switch scheme.Kind {
	case loomexpr.APIKeyKind:
		codegen.Doc(stmt, funcName+" creates an auth provider with the given API key.")
		stmt.Func().Id(funcName).Params(jen.Id("key").String()).Id("Option").Block(
			jen.Return(jen.Id("WithAuth").Call(jen.Op("&").Id(typeName).Values(jen.Dict{
				jen.Id("Key"): jen.Id("key"),
			}))),
		)
	case loomexpr.OAuth2Kind:
		codegen.Doc(stmt, funcName+" creates an auth provider with the given OAuth2 token.")
		stmt.Func().Id(funcName).Params(jen.Id("token").String()).Id("Option").Block(
			jen.Return(jen.Id("WithAuth").Call(jen.Op("&").Id(typeName).Values(jen.Dict{
				jen.Id("Token"): jen.Id("token"),
			}))),
		)
	case loomexpr.JWTKind:
		codegen.Doc(stmt, funcName+" creates an auth provider with the given JWT token.")
		stmt.Func().Id(funcName).Params(jen.Id("token").String()).Id("Option").Block(
			jen.Return(jen.Id("WithAuth").Call(jen.Op("&").Id(typeName).Values(jen.Dict{
				jen.Id("Token"): jen.Id("token"),
			}))),
		)
	case loomexpr.BasicAuthKind:
		codegen.Doc(stmt, funcName+" creates an auth provider with the given credentials.")
		stmt.Func().Id(funcName).Params(jen.Id("username").String(), jen.Id("password").String()).Id("Option").Block(
			jen.Return(jen.Id("WithAuth").Call(jen.Op("&").Id(typeName).Values(jen.Dict{
				jen.Id("Username"): jen.Id("username"),
				jen.Id("Password"): jen.Id("password"),
			}))),
		)
	case loomexpr.NoKind:
		return
	default:
		return
	}
	stmt.Line()
}

func emitRegistryClientAuthMethods(stmt *jen.Statement, data *RegistryClientData) {
	for _, scheme := range data.SecuritySchemes {
		authTypeName := codegen.Goify(scheme.Name, true) + "Auth"
		switch scheme.Kind {
		case loomexpr.APIKeyKind:
			codegen.Doc(stmt, "ApplyAuth implements AuthProvider.")
			stmt.Func().Params(jen.Id("a").Op("*").Id(authTypeName)).Id("ApplyAuth").
				Params(jen.Id("req").Op("*").Qual("net/http", "Request")).
				Error().
				BlockFunc(func(g *jen.Group) {
					g.If(jen.Id("a").Dot("Key").Op("==").Lit("")).Block(
						jen.Return(jen.Nil()),
					)
					switch scheme.In {
					case authLocationHeader:
						g.Id("req").Dot("Header").Dot("Set").Call(jen.Lit(scheme.ParamName), jen.Id("a").Dot("Key"))
					case "query":
						g.Id("q").Op(":=").Id("req").Dot("URL").Dot("Query").Call()
						g.Id("q").Dot("Set").Call(jen.Lit(scheme.ParamName), jen.Id("a").Dot("Key"))
						g.Id("req").Dot("URL").Dot("RawQuery").Op("=").Id("q").Dot("Encode").Call()
					}
					g.Return(jen.Nil())
				})
		case loomexpr.OAuth2Kind, loomexpr.JWTKind:
			codegen.Doc(stmt, "ApplyAuth implements AuthProvider.")
			stmt.Func().Params(jen.Id("a").Op("*").Id(authTypeName)).Id("ApplyAuth").
				Params(jen.Id("req").Op("*").Qual("net/http", "Request")).
				Error().
				Block(
					jen.If(jen.Id("a").Dot("Token").Op("==").Lit("")).Block(
						jen.Return(jen.Nil()),
					),
					jen.Id("req").Dot("Header").Dot("Set").Call(jen.Lit("Authorization"), jen.Lit("Bearer ").Op("+").Id("a").Dot("Token")),
					jen.Return(jen.Nil()),
				)
		case loomexpr.BasicAuthKind:
			codegen.Doc(stmt, "ApplyAuth implements AuthProvider.")
			stmt.Func().Params(jen.Id("a").Op("*").Id(authTypeName)).Id("ApplyAuth").
				Params(jen.Id("req").Op("*").Qual("net/http", "Request")).
				Error().
				Block(
					jen.If(jen.Id("a").Dot("Username").Op("==").Lit("").Op("&&").Id("a").Dot("Password").Op("==").Lit("")).Block(
						jen.Return(jen.Nil()),
					),
					jen.Id("req").Dot("SetBasicAuth").Call(jen.Id("a").Dot("Username"), jen.Id("a").Dot("Password")),
					jen.Return(jen.Nil()),
				)
		case loomexpr.NoKind:
			continue
		default:
			continue
		}
		stmt.Line()
	}
}
