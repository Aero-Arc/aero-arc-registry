package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	"github.com/alicebob/miniredis/v2"
	redisclient "github.com/redis/go-redis/v9"
)

var _ registry.Backend = (*Backend)(nil)

func TestNewValidatesConfiguration(t *testing.T) {
	t.Parallel()

	validTTL := registry.TTLConfig{Relay: time.Second, Agent: time.Second}
	validConfig := &registry.RedisConfig{Address: "localhost", Port: 6379}

	if _, err := New(nil, validTTL); !errors.Is(err, registry.ErrRedisConfigNil) {
		t.Fatalf("New(nil) error = %v, want ErrRedisConfigNil", err)
	}
	if _, err := New(&registry.RedisConfig{}, validTTL); !errors.Is(err, registry.ErrRedisAddrEmpty) {
		t.Fatalf("New(invalid config) error = %v, want ErrRedisAddrEmpty", err)
	}
	if _, err := New(validConfig, registry.TTLConfig{}); !errors.Is(err, registry.ErrTTLAgentInvalid) {
		t.Fatalf("New(invalid TTL) error = %v, want ErrTTLAgentInvalid", err)
	}
}

func TestRelayLifecycle(t *testing.T) {
	backend, server := newTestBackend(t, 5*time.Second, 5*time.Second)
	ctx := context.Background()

	start := time.Now().Add(-time.Second)
	if err := backend.RegisterRelay(ctx, registry.Relay{
		ID: "relay-1", Address: "10.0.0.1", GRPCPort: 50051,
	}); err != nil {
		t.Fatalf("RegisterRelay() error = %v", err)
	}

	relays, err := backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("ListRelays() error = %v", err)
	}
	if len(relays) != 1 {
		t.Fatalf("ListRelays() count = %d, want 1", len(relays))
	}
	if got := relays[0]; got.ID != "relay-1" || got.Address != "10.0.0.1" || got.GRPCPort != 50051 || got.LastSeen.Before(start) {
		t.Fatalf("ListRelays() relay = %+v", got)
	}

	server.FastForward(time.Second)
	if err := backend.HeartbeatRelay(ctx, "relay-1"); err != nil {
		t.Fatalf("HeartbeatRelay() error = %v", err)
	}
	if ttl := server.TTL(backend.relayKey("relay-1")); ttl != 5*time.Second {
		t.Fatalf("relay TTL after heartbeat = %v, want 5s", ttl)
	}

	if err := backend.RegisterRelay(ctx, registry.Relay{
		ID: "relay-1", Address: "10.0.0.2", GRPCPort: 50052,
	}); err != nil {
		t.Fatalf("RegisterRelay(update) error = %v", err)
	}
	relays, err = backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("ListRelays() after update error = %v", err)
	}
	if got := relays[0]; got.Address != "10.0.0.2" || got.GRPCPort != 50052 {
		t.Fatalf("updated relay = %+v", got)
	}

	if err := backend.RemoveRelay(ctx, "relay-1"); err != nil {
		t.Fatalf("RemoveRelay() error = %v", err)
	}
	if err := backend.HeartbeatRelay(ctx, "relay-1"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("HeartbeatRelay(removed) error = %v, want ErrNotFound", err)
	}
	if err := backend.RemoveRelay(ctx, "relay-1"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("RemoveRelay(removed) error = %v, want ErrNotFound", err)
	}
}

func TestAgentLifecycleAndAtomicReassignment(t *testing.T) {
	backend, _ := newTestBackend(t, 10*time.Second, 5*time.Second)
	ctx := context.Background()

	registerRelay(t, backend, "relay-1")
	registerRelay(t, backend, "relay-2")

	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-1"}, "missing"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("RegisterAgent(missing relay) error = %v, want ErrNotFound", err)
	}
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-1"}, "relay-1"); err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	assertPlacement(t, backend, "agent-1", "relay-1")
	assertRelayAgentIDs(t, backend, "relay-1", []string{"agent-1"})

	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-1"}, "relay-2"); err != nil {
		t.Fatalf("RegisterAgent(reassign) error = %v", err)
	}
	assertPlacement(t, backend, "agent-1", "relay-2")
	assertRelayAgentIDs(t, backend, "relay-1", nil)
	assertRelayAgentIDs(t, backend, "relay-2", []string{"agent-1"})

	beforeHeartbeat, err := backend.GetAgentPlacement(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetAgentPlacement() error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := backend.HeartbeatAgent(ctx, "agent-1"); err != nil {
		t.Fatalf("HeartbeatAgent() error = %v", err)
	}
	afterHeartbeat, err := backend.GetAgentPlacement(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetAgentPlacement() after heartbeat error = %v", err)
	}
	if afterHeartbeat.UpdatedAt.Before(beforeHeartbeat.UpdatedAt) {
		t.Fatalf("heartbeat moved placement timestamp backwards: before=%v after=%v", beforeHeartbeat.UpdatedAt, afterHeartbeat.UpdatedAt)
	}

	if err := backend.RemoveAgents(ctx, []string{"agent-1", "does-not-exist"}); err != nil {
		t.Fatalf("RemoveAgents() error = %v", err)
	}
	if _, err := backend.GetAgentPlacement(ctx, "agent-1"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("GetAgentPlacement(removed) error = %v, want ErrNotFound", err)
	}
	assertRelayAgentIDs(t, backend, "relay-2", nil)
}

func TestNativeTTLExcludesExpiredEntitiesAndRepairsIndexes(t *testing.T) {
	backend, server := newTestBackend(t, 2*time.Second, time.Second)
	ctx := context.Background()

	registerRelay(t, backend, "relay-agent-expiry")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-expiring"}, "relay-agent-expiry"); err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	server.FastForward(1100 * time.Millisecond)

	agents, err := backend.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("ListAgents() = %+v, want expired agent excluded", agents)
	}
	_, err = server.ZScore(backend.agentsKey(), "agent-expiring")
	if err != nil && !strings.Contains(err.Error(), "no such key") {
		t.Fatalf("ZScore(agent index) error = %v", err)
	}
	if err == nil {
		t.Fatal("expired agent remained in global index after read repair")
	}
	if _, err := backend.GetAgentPlacement(ctx, "agent-expiring"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("GetAgentPlacement(expired) error = %v, want ErrNotFound", err)
	}

	registerRelay(t, backend, "relay-expiring")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-on-dead-relay"}, "relay-expiring"); err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	// Keep the agent key alive beyond its relay to exercise relay-cascade reads.
	if err := backend.client.PExpire(ctx, backend.agentKey("agent-on-dead-relay"), 10*time.Second).Err(); err != nil {
		t.Fatalf("PExpire(agent) error = %v", err)
	}
	server.FastForward(2100 * time.Millisecond)

	agents, err = backend.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() after relay expiry error = %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("ListAgents() = %+v, want agents on expired relay excluded", agents)
	}
	if _, err := backend.GetAgentPlacement(ctx, "agent-on-dead-relay"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("GetAgentPlacement(dead relay) error = %v, want ErrNotFound", err)
	}
	if err := backend.HeartbeatAgent(ctx, "agent-on-dead-relay"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("HeartbeatAgent(dead relay) error = %v, want ErrNotFound", err)
	}
}

func TestConcurrentAgentReassignmentPreservesSinglePlacement(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	registerRelay(t, backend, "relay-a")
	registerRelay(t, backend, "relay-b")

	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			relayID := "relay-a"
			if i%2 == 0 {
				relayID = "relay-b"
			}
			if err := backend.RegisterAgent(context.Background(), registry.Agent{ID: "agent-racing"}, relayID); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent RegisterAgent() error = %v", err)
	}

	// Establish a deterministic final write, then assert no stale membership.
	if err := backend.RegisterAgent(context.Background(), registry.Agent{ID: "agent-racing"}, "relay-b"); err != nil {
		t.Fatalf("final RegisterAgent() error = %v", err)
	}
	assertPlacement(t, backend, "agent-racing", "relay-b")
	assertRelayAgentIDs(t, backend, "relay-a", nil)
	assertRelayAgentIDs(t, backend, "relay-b", []string{"agent-racing"})
}

func TestIDsCannotCollideWithRedisKeyStructure(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	registerRelay(t, backend, "x")
	registerRelay(t, backend, "x:agents")
	if backend.relayKey("x:agents") == backend.relayAgentsKey("x") {
		t.Fatal("encoded entity ID collided with relay-agent index key")
	}
}

func TestCanceledContextAndClosedClientErrors(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.ListRelays(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRelays(canceled) error = %v, want context.Canceled", err)
	}

	if err := backend.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := backend.Close(context.Background()); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}
	if _, err := backend.ListRelays(context.Background()); err == nil {
		t.Fatal("ListRelays(closed) error = nil, want client error")
	}
}

func newTestBackend(t *testing.T, relayTTL, agentTTL time.Duration) (*Backend, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redisclient.NewClient(&redisclient.Options{Addr: server.Addr()})
	backend := newBackend(client, registry.TTLConfig{Relay: relayTTL, Agent: agentTTL}, defaultNamespace)
	t.Cleanup(func() { _ = backend.Close(context.Background()) })
	return backend, server
}

func registerRelay(t *testing.T, backend *Backend, relayID string) {
	t.Helper()
	if err := backend.RegisterRelay(context.Background(), registry.Relay{
		ID: relayID, Address: "127.0.0.1", GRPCPort: 50051,
	}); err != nil {
		t.Fatalf("RegisterRelay(%q) error = %v", relayID, err)
	}
}

func assertPlacement(t *testing.T, backend *Backend, agentID, relayID string) {
	t.Helper()
	placement, err := backend.GetAgentPlacement(context.Background(), agentID)
	if err != nil {
		t.Fatalf("GetAgentPlacement(%q) error = %v", agentID, err)
	}
	if placement.AgentID != agentID || placement.RelayID != relayID || placement.UpdatedAt.IsZero() {
		t.Fatalf("GetAgentPlacement(%q) = %+v, want relay %q", agentID, placement, relayID)
	}
}

func assertRelayAgentIDs(t *testing.T, backend *Backend, relayID string, want []string) {
	t.Helper()
	agents, err := backend.ListRelayAgents(context.Background(), relayID)
	if err != nil {
		t.Fatalf("ListRelayAgents(%q) error = %v", relayID, err)
	}
	got := make([]string, 0, len(agents))
	for _, agent := range agents {
		got = append(got, agent.ID)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ListRelayAgents(%q) = %v, want %v", relayID, got, want)
	}
}
