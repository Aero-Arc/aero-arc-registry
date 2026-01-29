package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

var _ registry.Backend = (*Backend)(nil)

func TestRelayLifecycle(t *testing.T) {
	backend, err := New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	ctx := context.Background()
	relay := registry.Relay{ID: "relay-1", Address: "127.0.0.1", GRPCPort: 9000}

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
	if relays[0].ID != relay.ID {
		t.Fatalf("expected relay ID %q, got %q", relay.ID, relays[0].ID)
	}

	relayHeartbeatStart := time.Now()
	if err := backend.HeartbeatRelay(ctx, relay.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	relays, err = backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if relays[0].LastSeen.Before(relayHeartbeatStart) {
		t.Fatalf("expected LastSeen >= %v, got %v", relayHeartbeatStart, relays[0].LastSeen)
	}

	if err := backend.RemoveRelay(ctx, relay.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	relays, err = backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(relays) != 0 {
		t.Fatalf("expected 0 relays, got %d", len(relays))
	}
}

func TestAgentLifecycle(t *testing.T) {
	backend, err := New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	ctx := context.Background()
	relay := registry.Relay{ID: "relay-1", Address: "127.0.0.1", GRPCPort: 9000}
	if err := backend.RegisterRelay(ctx, relay); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	agent := registry.Agent{ID: "agent-1"}
	if err := backend.RegisterAgent(ctx, agent, relay.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	placement, err := backend.GetAgentPlacement(ctx, agent.ID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if placement.AgentID != agent.ID || placement.RelayID != relay.ID {
		t.Fatalf("unexpected placement: %#v", placement)
	}

	agentHeartbeatStart := time.Now()
	if err := backend.HeartbeatAgent(ctx, agent.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	placement, err = backend.GetAgentPlacement(ctx, agent.ID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if placement.UpdatedAt.Before(agentHeartbeatStart) {
		t.Fatalf("expected UpdatedAt >= %v, got %v", agentHeartbeatStart, placement.UpdatedAt)
	}

	if err := backend.RemoveRelay(ctx, relay.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if _, err := backend.GetAgentPlacement(ctx, agent.ID); !errors.Is(err, errAgentNotRegistered) {
		t.Fatalf("expected ErrAgentNotRegistered, got %v", err)
	}
}
