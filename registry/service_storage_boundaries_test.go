package registry

import (
	"context"
	"testing"
	"time"

	genregistry "github.com/CaliLuke/loom-mcp/v2/registry/gen/registry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceCatalogFailuresPreservePublicErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name, class string
		call        func(context.Context, *Service) error
	}{
		{"register", "service_unavailable", func(ctx context.Context, s *Service) error {
			_, err := s.Register(ctx, &genregistry.RegisterPayload{Name: "toolset", ProviderID: "provider", ProviderIncarnationID: testIncarnationA, AdmissionRevision: testAdmissionRevisionA, WireProtocolVersion: toolregistry.WireProtocolVersion})
			return err
		}},
		{"release", "service_unavailable", func(ctx context.Context, s *Service) error {
			return s.ReleaseProvider(ctx, &genregistry.ReleaseProviderPayload{Name: "toolset", ProviderID: "provider", ProviderIncarnationID: testIncarnationA, ExpectedRegistrationToken: testStaleToken})
		}},
		{"drain", "service_unavailable", func(ctx context.Context, s *Service) error {
			return s.DrainProvider(ctx, &genregistry.DrainProviderPayload{Name: "toolset", ProviderID: "provider", ProviderIncarnationID: testIncarnationA, ExpectedRegistrationToken: testStaleToken, SettlementDurationMs: 1000})
		}},
		{"unregister", "service_unavailable", func(ctx context.Context, s *Service) error {
			return s.Unregister(ctx, &genregistry.UnregisterPayload{Name: "toolset", ExpectedRegistrationToken: testStaleToken})
		}},
		{"list", "", func(ctx context.Context, s *Service) error {
			_, err := s.ListToolsets(ctx, &genregistry.ListToolsetsPayload{})
			return err
		}},
		{"get", "", func(ctx context.Context, s *Service) error {
			_, err := s.GetToolset(ctx, &genregistry.GetToolsetPayload{Name: "toolset"})
			return err
		}},
		{"search", "", func(ctx context.Context, s *Service) error {
			_, err := s.Search(ctx, &genregistry.SearchPayload{Query: "toolset"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := &failingCatalogMap{testCatalogMap: newTestCatalogMap(), readErr: assert.AnError, keysErr: assert.AnError}
			service := &Service{catalog: newToolsetCatalog(storage, newTestTimeSource(time.Unix(1, 0))), validator: newSchemaValidator(), streamManager: &unitStreamManager{}, providerLeaseDuration: DefaultProviderLeaseDuration}
			err := tc.call(t.Context(), service)
			require.Error(t, err)
			if tc.class == "" {
				require.ErrorIs(t, err, assert.AnError)
			} else {
				var public *loom.ServiceError
				require.ErrorAs(t, err, &public)
				assert.Equal(t, tc.class, public.Name)
			}
			assert.Empty(t, storage.content)
		})
	}
}
