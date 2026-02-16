package registry

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunTTLCleanupRelayFirstThenLeftoverAgents(t *testing.T) {
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

	backend := newTTLCleanupBackend()
	backend.relays["relay-stale"] = Relay{ID: "relay-stale", LastSeen: now.Add(-45 * time.Second)}
	backend.relays["relay-fresh"] = Relay{ID: "relay-fresh", LastSeen: now.Add(-5 * time.Second)}

	backend.agents["agent-under-stale-relay"] = Agent{ID: "agent-under-stale-relay", LastHeartbeat: now.Add(-2 * time.Second)}
	backend.agents["agent-leftover-stale"] = Agent{ID: "agent-leftover-stale", LastHeartbeat: now.Add(-40 * time.Second)}
	backend.agents["agent-fresh"] = Agent{ID: "agent-fresh", LastHeartbeat: now.Add(-1 * time.Second)}

	backend.relayAgents["relay-stale"] = map[string]struct{}{
		"agent-under-stale-relay": {},
	}
	backend.relayAgents["relay-fresh"] = map[string]struct{}{
		"agent-fresh": {},
	}

	reg := &Registry{
		cfg: &Config{
			TTL: TTLConfig{
				Relay: 30 * time.Second,
				Agent: 30 * time.Second,
			},
		},
		backend: backend,
	}

	if err := reg.runTTLCleanup(context.Background(), now); err != nil {
		t.Fatalf("runTTLCleanup returned error: %v", err)
	}

	got := backend.calls()
	want := []string{
		"ListRelays",
		"ListRelayAgents:relay-fresh",
		"ListRelays",
		"ListRelayAgents:relay-stale",
		"RemoveAgents:agent-under-stale-relay",
		"RemoveRelay:relay-stale",
		"ListAgents",
		"ListAgents",
		"RemoveAgents:agent-leftover-stale",
	}

	if len(got) != len(want) {
		t.Fatalf("unexpected call count: got %d want %d calls=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected call at index %d: got %q want %q full=%v", i, got[i], want[i], got)
		}
	}
}

func TestRunTTLCleanupSkipsWhenCleanupAlreadyInProgress(t *testing.T) {
	backend := newTTLCleanupBackend()
	reg := &Registry{
		cfg: &Config{
			TTL: TTLConfig{
				Relay: 30 * time.Second,
				Agent: 30 * time.Second,
			},
		},
		backend: backend,
	}
	reg.ttlCleanupInProgress.Store(true)

	if err := reg.runTTLCleanup(context.Background(), time.Now()); err != nil {
		t.Fatalf("runTTLCleanup returned error: %v", err)
	}

	if got := backend.calls(); len(got) != 0 {
		t.Fatalf("expected no backend calls when cleanup is in progress, got %v", got)
	}
}

func TestRunTTLCleanupDoesNotRemoveRelayRefreshedDuringCleanup(t *testing.T) {
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

	backend := newTTLCleanupBackend()
	backend.relays["relay-race"] = Relay{
		ID:       "relay-race",
		LastSeen: now.Add(-45 * time.Second),
	}
	backend.onListRelays = func(b *ttlCleanupBackend, callNum int) {
		if callNum != 2 {
			return
		}
		relay := b.relays["relay-race"]
		relay.LastSeen = time.Now()
		b.relays["relay-race"] = relay
	}

	reg := &Registry{
		cfg: &Config{
			TTL: TTLConfig{
				Relay: 30 * time.Second,
				Agent: 30 * time.Second,
			},
		},
		backend: backend,
	}

	if err := reg.runTTLCleanup(context.Background(), now); err != nil {
		t.Fatalf("runTTLCleanup returned error: %v", err)
	}

	for _, call := range backend.calls() {
		if call == "RemoveRelay:relay-race" {
			t.Fatalf("expected relay-race not to be removed after refresh, calls=%v", backend.calls())
		}
	}
}

type ttlCleanupBackend struct {
	mu          sync.Mutex
	relays      map[string]Relay
	agents      map[string]Agent
	relayAgents map[string]map[string]struct{}
	callLog     []string

	listRelaysCalls int
	listAgentsCalls int
	onListRelays    func(b *ttlCleanupBackend, callNum int)
	onListAgents    func(b *ttlCleanupBackend, callNum int)
}

func newTTLCleanupBackend() *ttlCleanupBackend {
	return &ttlCleanupBackend{
		relays:      make(map[string]Relay),
		agents:      make(map[string]Agent),
		relayAgents: make(map[string]map[string]struct{}),
	}
}

func (b *ttlCleanupBackend) RegisterRelay(ctx context.Context, relay Relay) error {
	return nil
}

func (b *ttlCleanupBackend) HeartbeatRelay(ctx context.Context, relayID string) error {
	return nil
}

func (b *ttlCleanupBackend) ListRelays(ctx context.Context) ([]Relay, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.listRelaysCalls++
	if b.onListRelays != nil {
		b.onListRelays(b, b.listRelaysCalls)
	}

	b.callLog = append(b.callLog, "ListRelays")

	ids := make([]string, 0, len(b.relays))
	for id := range b.relays {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]Relay, 0, len(ids))
	for _, id := range ids {
		out = append(out, b.relays[id])
	}
	return out, nil
}

func (b *ttlCleanupBackend) RegisterAgent(ctx context.Context, agent Agent, relayID string) error {
	return nil
}

func (b *ttlCleanupBackend) HeartbeatAgent(ctx context.Context, agentID string) error {
	return nil
}

func (b *ttlCleanupBackend) GetAgentPlacement(ctx context.Context, agentID string) (*AgentPlacement, error) {
	return nil, nil
}

func (b *ttlCleanupBackend) ListAgents(ctx context.Context) ([]Agent, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.listAgentsCalls++
	if b.onListAgents != nil {
		b.onListAgents(b, b.listAgentsCalls)
	}

	b.callLog = append(b.callLog, "ListAgents")

	ids := make([]string, 0, len(b.agents))
	for id := range b.agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]Agent, 0, len(ids))
	for _, id := range ids {
		out = append(out, b.agents[id])
	}
	return out, nil
}

func (b *ttlCleanupBackend) ListRelayAgents(ctx context.Context, relayID string) ([]*Agent, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.callLog = append(b.callLog, "ListRelayAgents:"+relayID)

	entries := b.relayAgents[relayID]
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]*Agent, 0, len(ids))
	for _, id := range ids {
		agent := b.agents[id]
		agentCopy := agent
		out = append(out, &agentCopy)
	}
	return out, nil
}

func (b *ttlCleanupBackend) RemoveAgents(ctx context.Context, agentIDs []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	ids := append([]string(nil), agentIDs...)
	sort.Strings(ids)
	b.callLog = append(b.callLog, "RemoveAgents:"+strings.Join(ids, ","))

	for _, agentID := range ids {
		delete(b.agents, agentID)
		for relayID, relayEntries := range b.relayAgents {
			delete(relayEntries, agentID)
			if len(relayEntries) == 0 {
				delete(b.relayAgents, relayID)
			}
		}
	}

	return nil
}

func (b *ttlCleanupBackend) RemoveRelay(ctx context.Context, relayID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.callLog = append(b.callLog, "RemoveRelay:"+relayID)
	delete(b.relays, relayID)
	delete(b.relayAgents, relayID)
	return nil
}

func (b *ttlCleanupBackend) Close(ctx context.Context) error {
	return nil
}

func (b *ttlCleanupBackend) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.callLog))
	copy(out, b.callLog)
	return out
}
