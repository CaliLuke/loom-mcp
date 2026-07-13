package progressivediscovery

import (
	"context"

	"example.com/assistant/progressive_discovery/gen/catalog"
)

type catalogService struct{}

// NewCatalog returns the minimal service used by progressive-discovery parity tests.
func NewCatalog() catalog.Service {
	return new(catalogService)
}

func (s *catalogService) Lookup(_ context.Context, payload *catalog.LookupPayload) (*catalog.LookupResult, error) {
	return &catalog.LookupResult{Value: "direct:" + payload.Query}, nil
}

func (s *catalogService) ProjectedLookup(_ context.Context, payload *catalog.ProjectedLookupPayload) (*catalog.ProjectedLookupResult, error) {
	return &catalog.ProjectedLookupResult{Value: "projected:" + payload.Query}, nil
}
