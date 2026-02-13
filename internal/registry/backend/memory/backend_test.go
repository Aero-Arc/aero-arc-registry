package memory

import (
	"context"
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

	placement, err = backend.GetAgentPlacement(ctx, agent.ID)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if placement.AgentID != agent.ID || placement.RelayID != relay.ID {
		t.Fatalf("unexpected placement after relay removal: %#v", placement)
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
