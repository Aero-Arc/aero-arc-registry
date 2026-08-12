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

	tests := []struct {
		name string
		ttl  registry.TTLConfig
		want string
	}{
		{
			name: "agent below Redis precision",
			ttl:  registry.TTLConfig{Relay: time.Millisecond, Agent: time.Millisecond - time.Nanosecond},
			want: "redis agent ttl must be at least 1ms",
		},
		{
			name: "relay below Redis precision",
			ttl:  registry.TTLConfig{Relay: time.Millisecond - time.Nanosecond, Agent: time.Millisecond},
			want: "redis relay ttl must be at least 1ms",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(validConfig, tt.ttl); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New(%+v) error = %v, want error containing %q", tt.ttl, err, tt.want)
			}
		})
	}

	backend, err := New(validConfig, registry.TTLConfig{Relay: time.Millisecond, Agent: time.Millisecond})
	if err != nil {
		t.Fatalf("New(1ms TTLs) error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close(context.Background()) })
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
	incarnation := relayIncarnation(t, backend, "relay-1")
	registerAgent(t, backend, "agent-preserved", "relay-1")

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
	if current := relayIncarnation(t, backend, "relay-1"); current != incarnation {
		t.Fatalf("idempotent relay update changed incarnation: got %q want %q", current, incarnation)
	}
	assertRelayAgentIDs(t, backend, "relay-1", []string{"agent-preserved"})

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

func TestListRelaysOmitsAndAtomicallyRepairsCorruptRecords(t *testing.T) {
	backend, _ := newTestBackend(t, 5*time.Second, 5*time.Second)
	ctx := context.Background()

	if err := backend.RegisterRelay(ctx, registry.Relay{ID: "missing-address", GRPCPort: 50051}); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("RegisterRelay(empty address) error = %v, want ErrInvalid", err)
	}
	for _, port := range []int32{0, 65536} {
		if err := backend.RegisterRelay(ctx, registry.Relay{ID: "invalid-port", Address: "127.0.0.1", GRPCPort: port}); !errors.Is(err, registry.ErrInvalid) {
			t.Fatalf("RegisterRelay(port %d) error = %v, want ErrInvalid", port, err)
		}
	}

	registerRelay(t, backend, "relay-healthy-a")
	registerRelay(t, backend, "relay-healthy-b")

	corruptions := map[string]func() error{
		"relay-missing-address": func() error {
			return backend.client.HDel(ctx, backend.relayKey("relay-missing-address"), "address").Err()
		},
		"relay-invalid-port": func() error {
			return backend.client.HSet(ctx, backend.relayKey("relay-invalid-port"), "grpc_port", "not-a-port").Err()
		},
		"relay-missing-port": func() error {
			return backend.client.HDel(ctx, backend.relayKey("relay-missing-port"), "grpc_port").Err()
		},
		"relay-out-of-range-port": func() error {
			return backend.client.HSet(ctx, backend.relayKey("relay-out-of-range-port"), "grpc_port", "65536").Err()
		},
		"relay-missing-incarnation": func() error {
			return backend.client.HDel(ctx, backend.relayKey("relay-missing-incarnation"), "incarnation").Err()
		},
		"relay-invalid-incarnation": func() error {
			return backend.client.HSet(ctx, backend.relayKey("relay-invalid-incarnation"), "incarnation", "not-an-incarnation").Err()
		},
		"relay-missing-timestamp": func() error {
			return backend.client.HDel(ctx, backend.relayKey("relay-missing-timestamp"), "last_seen_ms").Err()
		},
		"relay-invalid-timestamp": func() error {
			return backend.client.HSet(ctx, backend.relayKey("relay-invalid-timestamp"), "last_seen_ms", "9223372036854775808").Err()
		},
		"relay-wrong-id": func() error {
			return backend.client.HSet(ctx, backend.relayKey("relay-wrong-id"), "id", "another-relay").Err()
		},
	}
	corruptIDs := make([]string, 0, len(corruptions))
	for relayID, corrupt := range corruptions {
		corruptIDs = append(corruptIDs, relayID)
		registerRelay(t, backend, relayID)
		registerAgent(t, backend, "agent-for-"+relayID, relayID)
		if err := corrupt(); err != nil {
			t.Fatalf("corrupt %q: %v", relayID, err)
		}
	}

	relays, err := backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("ListRelays() error = %v", err)
	}
	if len(relays) != 2 || relays[0].ID != "relay-healthy-a" || relays[1].ID != "relay-healthy-b" {
		t.Fatalf("ListRelays() = %+v, want two healthy relays", relays)
	}

	for _, relayID := range corruptIDs {
		if exists := backend.client.Exists(ctx, backend.relayKey(relayID)).Val(); exists != 0 {
			t.Fatalf("corrupt relay hash %q was not removed", relayID)
		}
		if err := backend.client.ZScore(ctx, backend.relaysKey(), relayID).Err(); !errors.Is(err, redisclient.Nil) {
			t.Fatalf("relay index still contains %q: %v", relayID, err)
		}
		if exists := backend.client.Exists(ctx, backend.relayAgentsKey(relayID)).Val(); exists != 0 {
			t.Fatalf("relay membership index %q was not removed", relayID)
		}
	}
}

func TestListRelaysRepairDoesNotDeleteConcurrentRegistration(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	ctx := context.Background()

	for i := 0; i < 64; i++ {
		relayID := fmt.Sprintf("relay-race-%d", i)
		registerRelay(t, backend, relayID)
		if err := backend.client.HDel(ctx, backend.relayKey(relayID), "address").Err(); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			_, err := backend.ListRelays(ctx)
			errs <- err
		}()
		go func() {
			<-start
			errs <- backend.RegisterRelay(ctx, registry.Relay{
				ID: relayID, Address: "127.0.0.1", GRPCPort: 50051,
			})
		}()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("concurrent relay repair/register: %v", err)
			}
		}

		relays, err := backend.ListRelays(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, relay := range relays {
			if relay.ID == relayID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("concurrent repair deleted fresh registration %q", relayID)
		}
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
	if err := backend.HeartbeatAgent(ctx, "agent-1", "relay-2"); err != nil {
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

func TestAgentHeartbeatRequiresCurrentRelayOwnership(t *testing.T) {
	backend, server := newTestBackend(t, time.Minute, 5*time.Second)
	ctx := context.Background()
	registerRelay(t, backend, "relay-a")
	registerRelay(t, backend, "relay-b")
	registerAgent(t, backend, "agent-1", "relay-a")
	registerAgent(t, backend, "agent-1", "relay-b")

	placementAfterTakeover, err := backend.GetAgentPlacement(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	server.FastForward(time.Second)
	ttlBeforeRejected := server.TTL(backend.agentKey("agent-1"))
	if err := backend.HeartbeatAgent(ctx, "agent-1", "relay-a"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("old relay heartbeat error = %v, want ErrNotFound", err)
	}
	if ttl := server.TTL(backend.agentKey("agent-1")); ttl != ttlBeforeRejected {
		t.Fatalf("rejected heartbeat renewed agent TTL: before=%v after=%v", ttlBeforeRejected, ttl)
	}
	placementAfterRejected, err := backend.GetAgentPlacement(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if *placementAfterRejected != *placementAfterTakeover {
		t.Fatalf("rejected heartbeat mutated placement: before=%+v after=%+v", placementAfterTakeover, placementAfterRejected)
	}

	if err := backend.HeartbeatAgent(ctx, "agent-1", "relay-b"); err != nil {
		t.Fatalf("current relay heartbeat error = %v", err)
	}
	if ttl := server.TTL(backend.agentKey("agent-1")); ttl != 5*time.Second {
		t.Fatalf("accepted heartbeat TTL = %v, want 5s", ttl)
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

func TestRelayReregistrationFencesPreviousAgentIncarnation(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		backend, server := newTestBackend(t, time.Second, 10*time.Second)
		registerRelay(t, backend, "relay-reused")
		registerAgent(t, backend, "agent-old", "relay-reused")
		oldIncarnation := relayIncarnation(t, backend, "relay-reused")

		server.FastForward(1100 * time.Millisecond)
		if exists := backend.client.Exists(context.Background(), backend.agentKey("agent-old")).Val(); exists != 1 {
			t.Fatal("agent did not survive long enough to exercise relay recreation")
		}
		registerRelay(t, backend, "relay-reused")
		if current := relayIncarnation(t, backend, "relay-reused"); current == oldIncarnation {
			t.Fatalf("relay recreation after expiry reused incarnation %q", current)
		}
		agents, err := backend.ListAgents(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(agents) != 0 {
			t.Fatalf("ListAgents() = %+v, want old incarnation fenced", agents)
		}
	})

	t.Run("placement", func(t *testing.T) {
		backend, _ := newTestBackend(t, time.Minute, time.Minute)
		registerRelay(t, backend, "relay-reused")
		registerAgent(t, backend, "agent-old", "relay-reused")
		oldIncarnation := relayIncarnation(t, backend, "relay-reused")

		if err := backend.RemoveRelay(context.Background(), "relay-reused"); err != nil {
			t.Fatal(err)
		}
		registerRelay(t, backend, "relay-reused")
		if current := relayIncarnation(t, backend, "relay-reused"); current == oldIncarnation {
			t.Fatalf("relay incarnation was reused: %q", current)
		}
		if _, err := backend.GetAgentPlacement(context.Background(), "agent-old"); !errors.Is(err, registry.ErrNotFound) {
			t.Fatalf("GetAgentPlacement(old incarnation) error = %v, want ErrNotFound", err)
		}
		if exists := backend.client.Exists(context.Background(), backend.agentKey("agent-old")).Val(); exists != 0 {
			t.Fatal("placement lookup did not repair old-incarnation agent hash")
		}
	})

	t.Run("heartbeat", func(t *testing.T) {
		backend, _ := newTestBackend(t, time.Minute, time.Minute)
		registerRelay(t, backend, "relay-reused")
		registerAgent(t, backend, "agent-old", "relay-reused")
		if err := backend.RemoveRelay(context.Background(), "relay-reused"); err != nil {
			t.Fatal(err)
		}
		registerRelay(t, backend, "relay-reused")

		if err := backend.HeartbeatAgent(context.Background(), "agent-old", "relay-reused"); !errors.Is(err, registry.ErrNotFound) {
			t.Fatalf("HeartbeatAgent(old incarnation) error = %v, want ErrNotFound", err)
		}
		if exists := backend.client.Exists(context.Background(), backend.agentKey("agent-old")).Val(); exists != 1 {
			t.Fatal("rejected heartbeat deleted the existing agent instead of leaving ownership unchanged")
		}
	})

	t.Run("lists", func(t *testing.T) {
		backend, _ := newTestBackend(t, time.Minute, time.Minute)
		registerRelay(t, backend, "relay-reused")
		registerAgent(t, backend, "agent-old", "relay-reused")
		if err := backend.RemoveRelay(context.Background(), "relay-reused"); err != nil {
			t.Fatal(err)
		}
		registerRelay(t, backend, "relay-reused")
		registerAgent(t, backend, "agent-current", "relay-reused")

		agents, err := backend.ListAgents(context.Background())
		if err != nil {
			t.Fatalf("ListAgents() error = %v", err)
		}
		if len(agents) != 1 || agents[0].ID != "agent-current" {
			t.Fatalf("ListAgents() = %+v, want only current incarnation", agents)
		}
		assertRelayAgentIDs(t, backend, "relay-reused", []string{"agent-current"})
	})
}

func TestListAgentsOmitsAndRepairsCorruptRecords(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	ctx := context.Background()
	registerRelay(t, backend, "relay-1")
	for _, agentID := range []string{"agent-valid", "agent-missing-field", "agent-invalid-time", "agent-overflow-time", "agent-wrong-id"} {
		registerAgent(t, backend, agentID, "relay-1")
	}

	if err := backend.client.HDel(ctx, backend.agentKey("agent-missing-field"), "last_heartbeat_ms").Err(); err != nil {
		t.Fatal(err)
	}
	if err := backend.client.HSet(ctx, backend.agentKey("agent-invalid-time"), "last_heartbeat_ms", "not-a-time").Err(); err != nil {
		t.Fatal(err)
	}
	if err := backend.client.HSet(ctx, backend.agentKey("agent-overflow-time"), "last_heartbeat_ms", "9223372036854775808").Err(); err != nil {
		t.Fatal(err)
	}
	if err := backend.client.HSet(ctx, backend.agentKey("agent-wrong-id"), "id", "another-agent").Err(); err != nil {
		t.Fatal(err)
	}

	agents, err := backend.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "agent-valid" {
		t.Fatalf("ListAgents() = %+v, want only valid record", agents)
	}
	for _, agentID := range []string{"agent-missing-field", "agent-invalid-time", "agent-overflow-time", "agent-wrong-id"} {
		if exists := backend.client.Exists(ctx, backend.agentKey(agentID)).Val(); exists != 0 {
			t.Fatalf("corrupt agent hash %q was not removed", agentID)
		}
		if err := backend.client.ZScore(ctx, backend.agentsKey(), agentID).Err(); !errors.Is(err, redisclient.Nil) {
			t.Fatalf("global index still contains %q: %v", agentID, err)
		}
		if err := backend.client.ZScore(ctx, backend.relayAgentsKey("relay-1"), agentID).Err(); !errors.Is(err, redisclient.Nil) {
			t.Fatalf("relay index still contains %q: %v", agentID, err)
		}
	}
}

func TestAgentReadsRequireFullyValidRelay(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(context.Context, *Backend, string) error
	}{
		{name: "missing address", corrupt: func(ctx context.Context, backend *Backend, relayID string) error {
			return backend.client.HDel(ctx, backend.relayKey(relayID), "address").Err()
		}},
		{name: "invalid port", corrupt: func(ctx context.Context, backend *Backend, relayID string) error {
			return backend.client.HSet(ctx, backend.relayKey(relayID), "grpc_port", "65536").Err()
		}},
		{name: "overflow timestamp", corrupt: func(ctx context.Context, backend *Backend, relayID string) error {
			return backend.client.HSet(ctx, backend.relayKey(relayID), "last_seen_ms", "9223372036854775808").Err()
		}},
		{name: "missing global index", corrupt: func(ctx context.Context, backend *Backend, relayID string) error {
			return backend.client.ZRem(ctx, backend.relaysKey(), relayID).Err()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, _ := newTestBackend(t, time.Minute, time.Minute)
			ctx := context.Background()
			registerRelay(t, backend, "relay-healthy")
			registerAgent(t, backend, "agent-healthy", "relay-healthy")
			registerRelay(t, backend, "relay-corrupt")
			registerAgent(t, backend, "agent-corrupt", "relay-corrupt")
			if err := tt.corrupt(ctx, backend, "relay-corrupt"); err != nil {
				t.Fatal(err)
			}

			agents, err := backend.ListAgents(ctx)
			if err != nil {
				t.Fatalf("ListAgents() error = %v", err)
			}
			if len(agents) != 1 || agents[0].ID != "agent-healthy" {
				t.Fatalf("ListAgents() = %+v, want healthy neighbor only", agents)
			}
			if _, err := backend.GetAgentPlacement(ctx, "agent-corrupt"); !errors.Is(err, registry.ErrNotFound) {
				t.Fatalf("GetAgentPlacement(corrupt relay) error = %v, want ErrNotFound", err)
			}
			assertPlacement(t, backend, "agent-healthy", "relay-healthy")
			if exists := backend.client.Exists(ctx, backend.relayKey("relay-corrupt"), backend.agentKey("agent-corrupt")).Val(); exists != 0 {
				t.Fatalf("repair left %d corrupt entity keys", exists)
			}
		})
	}
}

func TestPlacementAndRelayAgentListRejectMalformedRelay(t *testing.T) {
	for _, read := range []struct {
		name string
		run  func(context.Context, *Backend) error
	}{
		{name: "placement", run: func(ctx context.Context, backend *Backend) error {
			_, err := backend.GetAgentPlacement(ctx, "agent-corrupt")
			return err
		}},
		{name: "relay agents", run: func(ctx context.Context, backend *Backend) error {
			_, err := backend.ListRelayAgents(ctx, "relay-corrupt")
			return err
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			backend, _ := newTestBackend(t, time.Minute, time.Minute)
			ctx := context.Background()
			registerRelay(t, backend, "relay-corrupt")
			registerAgent(t, backend, "agent-corrupt", "relay-corrupt")
			if err := backend.client.HSet(ctx, backend.relayKey("relay-corrupt"), "grpc_port", "not-a-port").Err(); err != nil {
				t.Fatal(err)
			}

			if err := read.run(ctx, backend); !errors.Is(err, registry.ErrNotFound) {
				t.Fatalf("read with malformed relay error = %v, want ErrNotFound", err)
			}
			if exists := backend.client.Exists(ctx, backend.relayKey("relay-corrupt")).Val(); exists != 0 {
				t.Fatal("malformed relay hash was not repaired")
			}
		})
	}
}

func TestListRelayAgentsRejectsOverflowTimestampAndPreservesHealthyAgent(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	ctx := context.Background()
	registerRelay(t, backend, "relay-1")
	registerAgent(t, backend, "agent-healthy", "relay-1")
	registerAgent(t, backend, "agent-overflow", "relay-1")
	if err := backend.client.HSet(ctx, backend.agentKey("agent-overflow"), "last_heartbeat_ms", "9223372036854775808").Err(); err != nil {
		t.Fatal(err)
	}

	assertRelayAgentIDs(t, backend, "relay-1", []string{"agent-healthy"})
	if exists := backend.client.Exists(ctx, backend.agentKey("agent-overflow")).Val(); exists != 0 {
		t.Fatal("ListRelayAgents did not repair overflow timestamp")
	}
}

func TestGetAgentPlacementValidatesAndRepairsEntireRecord(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(context.Context, *Backend) error
	}{
		{name: "stored id", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.HSet(ctx, backend.agentKey("agent-corrupt"), "id", "other-agent").Err()
		}},
		{name: "last heartbeat", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.HSet(ctx, backend.agentKey("agent-corrupt"), "last_heartbeat_ms", "9223372036854775808").Err()
		}},
		{name: "relay id", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.HSet(ctx, backend.agentKey("agent-corrupt"), "relay_id", "relay-other").Err()
		}},
		{name: "relay key", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.HSet(ctx, backend.agentKey("agent-corrupt"), "relay_key", backend.relayKey("relay-other")).Err()
		}},
		{name: "relay incarnation", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.HSet(ctx, backend.agentKey("agent-corrupt"), "relay_incarnation", "9223372036854775808").Err()
		}},
		{name: "relay membership key", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.HSet(ctx, backend.agentKey("agent-corrupt"), "relay_agents_key", backend.relayAgentsKey("relay-other")).Err()
		}},
		{name: "placement timestamp", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.HSet(ctx, backend.agentKey("agent-corrupt"), "placement_updated_ms", "not-a-time").Err()
		}},
		{name: "global index", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.ZRem(ctx, backend.agentsKey(), "agent-corrupt").Err()
		}},
		{name: "relay index", corrupt: func(ctx context.Context, backend *Backend) error {
			return backend.client.ZRem(ctx, backend.relayAgentsKey("relay-1"), "agent-corrupt").Err()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, _ := newTestBackend(t, time.Minute, time.Minute)
			ctx := context.Background()
			registerRelay(t, backend, "relay-1")
			registerRelay(t, backend, "relay-other")
			registerAgent(t, backend, "agent-healthy", "relay-1")
			registerAgent(t, backend, "agent-corrupt", "relay-1")
			if err := tt.corrupt(ctx, backend); err != nil {
				t.Fatal(err)
			}

			if _, err := backend.GetAgentPlacement(ctx, "agent-corrupt"); !errors.Is(err, registry.ErrNotFound) {
				t.Fatalf("GetAgentPlacement(corrupt) error = %v, want ErrNotFound", err)
			}
			if exists := backend.client.Exists(ctx, backend.agentKey("agent-corrupt")).Val(); exists != 0 {
				t.Fatal("corrupt agent hash was not removed")
			}
			if err := backend.client.ZScore(ctx, backend.agentsKey(), "agent-corrupt").Err(); !errors.Is(err, redisclient.Nil) {
				t.Fatalf("global index still contains corrupt agent: %v", err)
			}
			assertPlacement(t, backend, "agent-healthy", "relay-1")
		})
	}
}

func TestAgentPlacementRepairDoesNotDeleteConcurrentRegistration(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	ctx := context.Background()
	registerRelay(t, backend, "relay-1")

	for i := 0; i < 64; i++ {
		agentID := fmt.Sprintf("agent-race-%d", i)
		registerAgent(t, backend, agentID, "relay-1")
		if err := backend.client.HDel(ctx, backend.agentKey(agentID), "placement_updated_ms").Err(); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			_, err := backend.GetAgentPlacement(ctx, agentID)
			if errors.Is(err, registry.ErrNotFound) {
				err = nil
			}
			errs <- err
		}()
		go func() {
			<-start
			errs <- backend.RegisterAgent(ctx, registry.Agent{ID: agentID}, "relay-1")
		}()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("concurrent placement repair/register: %v", err)
			}
		}
		assertPlacement(t, backend, agentID, "relay-1")
	}
}

func TestStaleRelayListCannotDeleteReassignedAgent(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	ctx := context.Background()
	registerRelay(t, backend, "relay-a")
	registerRelay(t, backend, "relay-b")
	registerAgent(t, backend, "agent-1", "relay-a")
	staleIncarnation := relayIncarnation(t, backend, "relay-a")

	registerAgent(t, backend, "agent-1", "relay-b")
	if err := backend.client.ZAdd(ctx, backend.relayAgentsKey("relay-a"), redisclient.Z{
		Score: float64(time.Now().Add(time.Minute).UnixMilli()), Member: "agent-1",
	}).Err(); err != nil {
		t.Fatal(err)
	}
	values, err := readRelayAgentScript.Run(ctx, backend.client, []string{
		backend.agentKey("agent-1"), backend.agentsKey(), backend.relayAgentsKey("relay-a"),
		backend.relayKey("relay-a"), backend.relaysKey(),
	}, "agent-1", "relay-a", staleIncarnation).StringSlice()
	if err != nil && !errors.Is(err, redisclient.Nil) {
		t.Fatalf("stale relay read error = %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("stale relay read returned %+v", values)
	}
	assertPlacement(t, backend, "agent-1", "relay-b")
	assertRelayAgentIDs(t, backend, "relay-a", nil)
	assertRelayAgentIDs(t, backend, "relay-b", []string{"agent-1"})
}

func TestConcurrentStaleRelayListsPreserveReassignment(t *testing.T) {
	backend, _ := newTestBackend(t, time.Minute, time.Minute)
	ctx := context.Background()
	registerRelay(t, backend, "relay-a")
	registerRelay(t, backend, "relay-b")

	for i := 0; i < 128; i++ {
		agentID := fmt.Sprintf("agent-reassign-%d", i)
		registerAgent(t, backend, agentID, "relay-a")
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			_, err := backend.ListRelayAgents(ctx, "relay-a")
			errs <- err
		}()
		go func() {
			<-start
			errs <- backend.RegisterAgent(ctx, registry.Agent{ID: agentID}, "relay-b")
		}()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("concurrent list/reassignment: %v", err)
			}
		}
		assertPlacement(t, backend, agentID, "relay-b")
	}
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
	if err := backend.HeartbeatAgent(ctx, "agent-on-dead-relay", "relay-expiring"); !errors.Is(err, registry.ErrNotFound) {
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

func registerAgent(t *testing.T, backend *Backend, agentID, relayID string) {
	t.Helper()
	if err := backend.RegisterAgent(context.Background(), registry.Agent{ID: agentID}, relayID); err != nil {
		t.Fatalf("RegisterAgent(%q, %q) error = %v", agentID, relayID, err)
	}
}

func relayIncarnation(t *testing.T, backend *Backend, relayID string) string {
	t.Helper()
	incarnation, err := backend.client.HGet(context.Background(), backend.relayKey(relayID), "incarnation").Result()
	if err != nil {
		t.Fatalf("read relay incarnation: %v", err)
	}
	return incarnation
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
