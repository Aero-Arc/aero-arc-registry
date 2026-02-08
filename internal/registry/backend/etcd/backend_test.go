package etcd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

var _ registry.Backend = (*Backend)(nil)

func TestRelayAndAgentLifecycle(t *testing.T) {
	backend, err := New(&registry.EtcdConfig{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	ctx := context.Background()
	now := time.Now()

	relay := registry.Relay{ID: "relay-1", Address: "127.0.0.1", GRPCPort: 50051, LastSeen: now}
	if err := backend.RegisterRelay(ctx, relay); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	relays, err := backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relays))
	}

	hb := now.Add(time.Second)
	if err := backend.HeartbeatRelay(ctx, relay.ID, hb); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	agent := registry.Agent{ID: "agent-1", LastHeartbeat: now}
	if err := backend.RegisterAgent(ctx, agent, relay.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if err := backend.HeartbeatAgent(ctx, agent.ID, hb); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	placement, err := backend.GetAgentPlacement(ctx, agent.ID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if placement.RelayID != relay.ID {
		t.Fatalf("expected placement relay id %s, got %s", relay.ID, placement.RelayID)
	}

	if err := backend.RemoveRelay(ctx, relay.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Persistence-only behavior: agent state is not cascade-deleted in backend.
	if _, err := backend.GetAgentPlacement(ctx, agent.ID); err != nil {
		t.Fatalf("expected placement to remain persisted, got %v", err)
	}
}

func TestValidationAndContextErrors(t *testing.T) {
	backend, err := New(&registry.EtcdConfig{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	ctx := context.Background()
	if err := backend.RegisterRelay(ctx, registry.Relay{}); !errors.Is(err, registry.ErrRelayIDEmpty) {
		t.Fatalf("expected ErrRelayIDEmpty, got %v", err)
	}
	if err := backend.HeartbeatAgent(ctx, "", time.Now()); !errors.Is(err, registry.ErrAgentIDEmpty) {
		t.Fatalf("expected ErrAgentIDEmpty, got %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.RemoveRelay(canceled, "relay-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestNotRegisteredErrors(t *testing.T) {
	backend, err := New(&registry.EtcdConfig{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	ctx := context.Background()

	if err := backend.HeartbeatRelay(ctx, "missing", time.Now()); !errors.Is(err, registry.ErrRelayNotRegistered) {
		t.Fatalf("expected ErrRelayNotRegistered, got %v", err)
	}

	if err := backend.RemoveRelay(ctx, "missing"); !errors.Is(err, registry.ErrRelayNotRegistered) {
		t.Fatalf("expected ErrRelayNotRegistered, got %v", err)
	}

	if _, err := backend.GetAgentPlacement(ctx, "missing"); !errors.Is(err, registry.ErrAgentNotRegistered) {
		t.Fatalf("expected ErrAgentNotRegistered, got %v", err)
	}
}
