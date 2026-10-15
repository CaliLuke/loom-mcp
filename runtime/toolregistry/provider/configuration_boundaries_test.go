package provider

import (
	"context"
	"testing"
	"time"

	mockpulse "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse/mocks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/stretchr/testify/require"
)

func TestServeRejectsInvalidRegistrationBeforeOpeningStream(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Registration)
		want string
	}{
		{"register", func(r *Registration) {
			r.Register = nil
		}, "registration callback"},
		{"release", func(r *Registration) {
			r.Release = nil
		}, "release callback"},
		{"drain", func(r *Registration) {
			r.Drain = nil
		}, "drain callback"},
		{"complete", func(r *Registration) {
			r.Complete = nil
		}, "complete callback"},
		{"output delta", func(r *Registration) {
			r.PublishOutputDelta = nil
		}, "publish output delta callback"},
		{"overload", func(r *Registration) {
			r.ReportOverload = nil
		}, "report overload callback"},
		{"claim", func(r *Registration) {
			r.Claim = nil
		}, "claim callback"},
		{"revision", func(r *Registration) {
			r.AdmissionRevision = ""
		}, "admission revision"},
		{"negative initial", func(r *Registration) {
			r.RetryInitialInterval = -time.Second
		}, "initial interval"},
		{"negative maximum", func(r *Registration) {
			r.RetryMaxInterval = -time.Second
		}, "max interval"},
		{"negative attempt", func(r *Registration) {
			r.AttemptTimeout = -time.Second
		}, "attempt timeout"},
		{"negative shutdown", func(r *Registration) {
			r.ShutdownMargin = -time.Second
		}, "shutdown margin"},
		{"negative release", func(r *Registration) {
			r.ReleaseTimeout = -time.Second
		}, "release timeout"},
		{"inverted backoff", func(r *Registration) {
			r.RetryInitialInterval = time.Minute
			r.RetryMaxInterval = time.Second
		}, "initial interval"},
		{"excessive maximum", func(r *Registration) {
			r.RetryMaxInterval = toolregistry.MaxProviderLeaseDuration + time.Second
		}, "must not exceed"},
		{"excessive attempt", func(r *Registration) {
			r.AttemptTimeout = toolregistry.MaxProviderLeaseDuration + time.Second
		}, "must not exceed"},
		{"excessive shutdown", func(r *Registration) {
			r.ShutdownMargin = toolregistry.MaxProviderLeaseDuration + time.Second
		}, "must not exceed"},
		{"excessive release", func(r *Registration) {
			r.ReleaseTimeout = toolregistry.MaxProviderLeaseDuration + time.Second
		}, "must not exceed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registration := successfulRegistration()
			tc.edit(&registration)
			// No mock expectations: opening a stream would fail the test.
			err := Serve(t.Context(), mockpulse.NewClient(t), "test.toolset", &recordingHandler{}, registration, Options{ProviderID: testProviderID, Pong: func(context.Context, string, string, string) error {
				return nil
			}})
			require.ErrorContains(t, err, tc.want)
		})
	}
}
