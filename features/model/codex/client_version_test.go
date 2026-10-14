package codex

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultCompatibilitySupportsCurrentModels(t *testing.T) {
	client := newSSETestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("version") != "0.162.1" {
			return sseResponse(http.StatusBadRequest, `{"detail":"model requires the current client revision"}`), nil
		}
		return sseResponse(http.StatusOK, sseEvents(`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"id":"message","type":"message","content":[{"type":"output_text","text":"OK"}]}]}}`)), nil
	}))
	response, err := client.Complete(t.Context(), testRequest())
	require.NoError(t, err)
	require.NotEmpty(t, response.Content)
}
