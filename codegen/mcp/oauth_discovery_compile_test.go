package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

const oauthDerivedTest = `package oauth_test
import (
 "net/http/httptest"
 "testing"
 oauth "example.com/oauth/gen/mcp_oauth"
 "github.com/stretchr/testify/assert"
)
func TestDerivedDiscovery(t *testing.T) {
 request:=httptest.NewRequest("GET","https://internal.example/mcp",nil)
 request.Header.Set("Forwarded","proto=https;host=public.example")
 response:=httptest.NewRecorder()
 oauth.HandleProtectedResourceMetadata(response,request)
 assert.Equal(t,200,response.Code)
 assert.JSONEq(t,"{\"resource\":\"https://public.example/mcp\",\"authorization_servers\":[\"https://auth.example.com\"],\"bearer_methods_supported\":[\"header\"]}",response.Body.String())
 assert.Equal(t,"application/json",response.Header().Get("Content-Type"))
 assert.Equal(t,"max-age=3600",response.Header().Get("Cache-Control"))
 assert.Equal(t,"/.well-known/oauth-protected-resource/mcp",oauth.OAuthMetadataPath("/mcp"))
 assert.Contains(t,oauth.OAuthChallengeHeader(request,"/mcp"),"https://public.example/.well-known/oauth-protected-resource/mcp")
 assert.Empty(t,oauth.ExpectedResourceIdentifier())
 request.Header.Set("Forwarded","proto=invalid;host=public.example")
 response=httptest.NewRecorder()
 oauth.HandleProtectedResourceMetadata(response,request)
 assert.Equal(t,400,response.Code)
 assert.NotContains(t,response.Body.String(),"authorization_servers")
 assert.Contains(t,oauth.OAuthInvalidTokenChallengeHeader(request,"/mcp","expired"),"invalid_token")
}
`

const oauthPinnedTest = `package oauth_test
import (
 "context"
 "errors"
 "net/http"
 "net/http/httptest"
 "testing"
 oauth "example.com/oauth/gen/mcp_oauth"
 "github.com/modelcontextprotocol/go-sdk/auth"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)
func TestPinnedDiscovery(t *testing.T) {
 request:=httptest.NewRequest("GET","https://untrusted.example/mcp",nil)
 response:=httptest.NewRecorder()
 oauth.HandleProtectedResourceMetadata(response,request)
 assert.Equal(t,200,response.Code)
 assert.JSONEq(t,"{\"resource\":\"https://api.example.com/mcp\",\"authorization_servers\":[\"https://auth.example.com\"],\"scopes_supported\":[\"read\",\"write\"],\"bearer_methods_supported\":[\"header\"],\"resource_documentation\":\"https://api.example.com/docs\"}",response.Body.String())
 assert.Contains(t,oauth.OAuthChallengeHeader(request,"/mcp"),"scope=\"read write\"")
}
func TestAudienceBinding(t *testing.T) {
 resource:="https://api.example.com/mcp"
 for _,tc:=range []struct{claim any; valid bool}{
  {resource,true},{[]string{"other",resource},true},{[]any{1,resource},true},
  {"other",false},{[]string{"other"},false},{[]any{1,"other"},false},{nil,false},{1,false},
 } {
  verifier:=oauth.EnforceAudience(func(context.Context,string,*http.Request)(*auth.TokenInfo,error){return &auth.TokenInfo{Extra:map[string]any{"aud":tc.claim}},nil})
  info,err:=verifier(t.Context(),"token",httptest.NewRequest("GET",resource,nil))
  if tc.valid {require.NoError(t,err);require.NotNil(t,info)} else {require.ErrorIs(t,err,auth.ErrInvalidToken);assert.Nil(t,info)}
 }
 denied:=errors.New("revoked")
 verifier:=oauth.EnforceAudience(func(context.Context,string,*http.Request)(*auth.TokenInfo,error){return nil,denied})
 _,err:=verifier(t.Context(),"token",nil)
 require.ErrorIs(t,err,denied)
 verifier=oauth.EnforceAudience(func(context.Context,string,*http.Request)(*auth.TokenInfo,error){return nil,nil})
 _,err=verifier(t.Context(),"token",nil)
 require.ErrorIs(t,err,auth.ErrInvalidToken)
}
`

func TestGeneratedOAuthDiscoveryBehavior(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		name := "derived"
		oauth := &mcpexpr.OAuthExpr{AuthorizationServers: []string{"https://auth.example.com"}, TrustProxyHeaders: true}
		testSource := oauthDerivedTest
		if pinned {
			name = "pinned"
			oauth.ResourceIdentifier = "https://api.example.com/mcp"
			oauth.ResourceDocumentationURL = "https://api.example.com/docs"
			oauth.Scopes = []*mcpexpr.ScopeExpr{{Name: "read", Description: "Read records"}, {Name: "write"}}
			oauth.BearerMethodsSupported = []string{"header"}
			testSource = oauthPinnedTest
		}
		t.Run(name, func(t *testing.T) {
			restore := resetMCPCodegenState(t)
			defer restore()
			svc, methods := testService("oauth", "ping")
			root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
			mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{Name: "oauth", Version: "1.0.0", OAuth: oauth, Tools: []*mcpexpr.ToolExpr{{Name: "ping", Method: methods["ping"]}}})
			files, err := Generate("example.com/oauth/gen", []eval.Root{root}, nil)
			require.NoError(t, err)
			discovery := findGeneratedFile(t, files, "gen/mcp_oauth/oauth_discovery.go")
			dir := t.TempDir()
			_, err = discovery.Render(dir)
			require.NoError(t, err)
			repo, err := filepath.Abs("../..")
			require.NoError(t, err)
			source, err := os.OpenRoot(repo)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, source.Close())
			})
			outputRoot, err := os.OpenRoot(dir)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, outputRoot.Close())
			})
			mod, err := source.ReadFile("go.mod")
			require.NoError(t, err)
			modText := strings.Replace(string(mod), "module github.com/CaliLuke/loom-mcp/v2", "module example.com/oauth", 1) + "\nrequire github.com/CaliLuke/loom-mcp/v2 v2.0.0\nreplace github.com/CaliLuke/loom-mcp/v2 => " + repo + "\n"
			require.NoError(t, outputRoot.WriteFile("go.mod", []byte(modText), 0600))
			sum, err := source.ReadFile("go.sum")
			require.NoError(t, err)
			require.NoError(t, outputRoot.WriteFile("go.sum", sum, 0600))
			require.NoError(t, outputRoot.WriteFile("oauth_test.go", []byte(testSource), 0600))
			command := exec.CommandContext(t.Context(), "go", "test", "./...")
			command.Dir = dir
			command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod -p=2")
			output, err := command.CombinedOutput()
			require.NoError(t, err, "generated OAuth behavior: %s", output)
		})
	}
}
