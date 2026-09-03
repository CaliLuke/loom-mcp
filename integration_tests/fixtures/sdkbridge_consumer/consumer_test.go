package sdkbridgeconsumer

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpconsumer "example.com/sdkbridgeconsumer/gen/mcp_consumer"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test does not regenerate the checked-in server. A same-version runtime
// update must remain compatible with that generated consumer.
func TestCheckedInConsumerUsesSameVersionRuntimeWithOfficialSDK(t *testing.T) {
	generated, err := mcpconsumer.NewSDKServer(consumerService{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(generated.Handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "external-consumer", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Timeout: 10 * time.Second},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close official SDK session: %v", err)
		}
	}()

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "lookup",
		Arguments: map[string]any{"query": "current-runtime"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool returned an error: %+v", result.Content)
	}
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(structured), "answer:current-runtime") {
		t.Fatalf("unexpected structured result: %s", structured)
	}
}
