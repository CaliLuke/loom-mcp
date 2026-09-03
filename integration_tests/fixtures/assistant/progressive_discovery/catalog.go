package progressivediscovery

import (
	"context"

	"example.com/assistant/progressive_discovery/gen/catalog"
)

type catalogService struct {
	cancelStarted chan struct{}
	cancelSeen    chan struct{}
}

// NewCatalog returns the minimal service used by progressive-discovery parity tests.
func NewCatalog() catalog.Service {
	return new(catalogService)
}

func (s *catalogService) Lookup(_ context.Context, payload *catalog.LookupPayload) (*catalog.LookupResult, error) {
	return &catalog.LookupResult{Value: "direct:" + payload.Query}, nil
}

func (s *catalogService) Status(context.Context, *catalog.StatusPayload) (*catalog.StatusResult, error) {
	return &catalog.StatusResult{State: "ready"}, nil
}
func (s *catalogService) Health(context.Context) (*catalog.HealthResult, error) {
	return &catalog.HealthResult{State: "ready"}, nil
}

func (s *catalogService) StreamChunks(ctx context.Context, stream catalog.StreamChunksServerStream) error {
	first := "first"
	if err := stream.SendWithContext(ctx, &catalog.StreamChunksResult{Chunk: first}); err != nil {
		return err
	}
	final := "second"
	if err := stream.SendWithContext(ctx, &catalog.StreamChunksResult{Chunk: final}); err != nil {
		return err
	}
	return stream.Close()
}

func (s *catalogService) WaitForCancel(ctx context.Context) (*catalog.WaitForCancelResult, error) {
	if s.cancelStarted != nil {
		close(s.cancelStarted)
	}
	<-ctx.Done()
	if s.cancelSeen != nil {
		close(s.cancelSeen)
	}
	return nil, ctx.Err()
}

func (s *catalogService) ProjectedLookup(_ context.Context, payload *catalog.ProjectedLookupPayload) (*catalog.ProjectedLookupResult, error) {
	return &catalog.ProjectedLookupResult{Value: "projected:" + payload.Query}, nil
}
