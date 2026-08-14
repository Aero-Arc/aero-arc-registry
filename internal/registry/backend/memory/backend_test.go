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
	if err := backend.HeartbeatAgent(ctx, agent.ID, relay.ID); err != nil {
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

	placement, err = backend.GetAgentPlacement(ctx, agent.ID)
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("placement error after relay removal = %v, want ErrNotFound", err)
	}
	if placement != nil {
		t.Fatalf("placement after relay removal = %#v, want nil", placement)
	}

	agents, err := backend.ListAgents(ctx)
	if err != nil {
		t.Fatalf("list agents after relay removal: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("agents after relay removal = %#v, want empty", agents)
	}
}

func TestListsAreDeterministicByID(t *testing.T) {
	backend, err := New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	for _, relayID := range []string{"relay-z", "relay-a", "relay-m"} {
		if err := backend.RegisterRelay(ctx, registry.Relay{ID: relayID, Address: "127.0.0.1", GRPCPort: 9000}); err != nil {
			t.Fatal(err)
		}
	}
	relays, err := backend.ListRelays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"relay-a", "relay-m", "relay-z"} {
		if relays[i].ID != want {
			t.Fatalf("relay[%d].ID = %q, want %q", i, relays[i].ID, want)
		}
	}

	for _, agentID := range []string{"agent-z", "agent-a", "agent-m"} {
		if err := backend.RegisterAgent(ctx, registry.Agent{ID: agentID}, "relay-a"); err != nil {
			t.Fatal(err)
		}
	}
	agents, err := backend.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	relayAgents, err := backend.ListRelayAgents(ctx, "relay-a")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"agent-a", "agent-m", "agent-z"} {
		if agents[i].ID != want {
			t.Fatalf("agent[%d].ID = %q, want %q", i, agents[i].ID, want)
		}
		if relayAgents[i].ID != want {
			t.Fatalf("relay agent[%d].ID = %q, want %q", i, relayAgents[i].ID, want)
		}
	}
}

func TestAgentHeartbeatRequiresCurrentRelayOwnership(t *testing.T) {
	backend, err := New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, relayID := range []string{"relay-a", "relay-b"} {
		if err := backend.RegisterRelay(ctx, registry.Relay{ID: relayID, Address: "127.0.0.1", GRPCPort: 9000}); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-1"}, "relay-a"); err != nil {
		t.Fatal(err)
	}
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-1"}, "relay-b"); err != nil {
		t.Fatal(err)
	}
	placementAfterTakeover, err := backend.GetAgentPlacement(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}

	if err := backend.HeartbeatAgent(ctx, "agent-1", "relay-a"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("old relay heartbeat error = %v, want ErrNotFound", err)
	}
	placementAfterRejected, err := backend.GetAgentPlacement(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if *placementAfterRejected != *placementAfterTakeover {
		t.Fatalf("rejected heartbeat mutated placement: before=%+v after=%+v", placementAfterTakeover, placementAfterRejected)
	}

	time.Sleep(time.Millisecond)
	if err := backend.HeartbeatAgent(ctx, "agent-1", "relay-b"); err != nil {
		t.Fatalf("current relay heartbeat error = %v", err)
	}
	placementAfterAccepted, err := backend.GetAgentPlacement(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if !placementAfterAccepted.UpdatedAt.After(placementAfterRejected.UpdatedAt) {
		t.Fatalf("accepted heartbeat did not renew placement: before=%v after=%v", placementAfterRejected.UpdatedAt, placementAfterAccepted.UpdatedAt)
	}

	if err := backend.HeartbeatAgent(ctx, "agent-1", "relay-missing"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("missing relay heartbeat error = %v, want ErrNotFound", err)
	}
}

func TestListRelayAgents(t *testing.T) {
	backend, err := New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	ctx := context.Background()
	relay1 := registry.Relay{ID: "relay-1", Address: "127.0.0.1", GRPCPort: 9000}
	relay2 := registry.Relay{ID: "relay-2", Address: "127.0.0.1", GRPCPort: 9001}

	if err := backend.RegisterRelay(ctx, relay1); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if err := backend.RegisterRelay(ctx, relay2); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-1"}, relay1.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-2"}, relay1.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	relay1Agents, err := backend.ListRelayAgents(ctx, relay1.ID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(relay1Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(relay1Agents))
	}

	seen := map[string]bool{}
	for _, agent := range relay1Agents {
		seen[agent.ID] = true
	}
	if !seen["agent-1"] || !seen["agent-2"] {
		t.Fatalf("unexpected relay-1 agents: %#v", relay1Agents)
	}

	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-1"}, relay2.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	relay1Agents, err = backend.ListRelayAgents(ctx, relay1.ID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(relay1Agents) != 1 || relay1Agents[0].ID != "agent-2" {
		t.Fatalf("unexpected relay-1 agents after reassignment: %#v", relay1Agents)
	}

	relay2Agents, err := backend.ListRelayAgents(ctx, relay2.ID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(relay2Agents) != 1 || relay2Agents[0].ID != "agent-1" {
		t.Fatalf("unexpected relay-2 agents after reassignment: %#v", relay2Agents)
	}
}

func TestRemoveAgents(t *testing.T) {
	backend, err := New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	ctx := context.Background()
	relay := registry.Relay{ID: "relay-1", Address: "127.0.0.1", GRPCPort: 9000}
	if err := backend.RegisterRelay(ctx, relay); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-1"}, relay.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-2"}, relay.ID); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if err := backend.RemoveAgents(ctx, []string{"agent-1"}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	agents, err := backend.ListAgents(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "agent-2" {
		t.Fatalf("unexpected agents after removal: %#v", agents)
	}

	if _, err := backend.GetAgentPlacement(ctx, "agent-1"); err == nil {
		t.Fatal("expected error for removed agent placement")
	}

	relayAgents, err := backend.ListRelayAgents(ctx, relay.ID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(relayAgents) != 1 || relayAgents[0].ID != "agent-2" {
		t.Fatalf("unexpected relay agents after removal: %#v", relayAgents)
	}
}
