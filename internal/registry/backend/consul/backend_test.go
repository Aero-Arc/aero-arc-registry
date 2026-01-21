package consul

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

var _ registry.Backend = (*Backend)(nil)

func TestBackendMethodsReturnNotImplemented(t *testing.T) {
	backend, err := New(&registry.ConsulConfig{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	ctx := context.Background()

	if err := backend.RegisterRelay(ctx, registry.Relay{}); !errors.Is(err, registry.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}

	if err := backend.HeartbeatRelay(ctx, "relay", time.Now()); !errors.Is(err, registry.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}

	if _, err := backend.ListRelays(ctx); !errors.Is(err, registry.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}

	if err := backend.RemoveRelay(ctx, "relay"); !errors.Is(err, registry.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}

	if err := backend.RegisterAgent(ctx, registry.Agent{}, "relay"); !errors.Is(err, registry.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}

	if err := backend.HeartbeatAgent(ctx, "agent", time.Now()); !errors.Is(err, registry.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}

	if _, err := backend.GetAgentPlacement(ctx, "agent"); !errors.Is(err, registry.ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}

	if err := backend.Close(ctx); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}
