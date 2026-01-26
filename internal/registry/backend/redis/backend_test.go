package redis

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	"github.com/alicebob/miniredis/v2"
)

var _ registry.Backend = (*Backend)(nil)

func newTestBackend(t *testing.T) (*Backend, *miniredis.Miniredis, func()) {
	t.Helper()

	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}

	host, portStr, err := net.SplitHostPort(s.Addr())
	if err != nil {
		s.Close()
		t.Fatalf("split host/port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		s.Close()
		t.Fatalf("parse port: %v", err)
	}

	ttl := registry.TTLConfig{
		Relay: 2 * time.Second,
		Agent: 2 * time.Second,
	}

	backend, err := New(&registry.RedisConfig{Address: host, Port: port}, ttl)
	if err != nil {
		s.Close()
		t.Fatalf("new backend: %v", err)
	}

	cleanup := func() {
		_ = backend.Close(context.Background())
		s.Close()
	}

	return backend, s, cleanup
}

func TestBackendRelayLifecycle(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer cleanup()

	ctx := context.Background()
	relay := registry.Relay{ID: "relay-1", Address: "10.0.0.1", GRPCPort: 50051}

	if err := backend.RegisterRelay(ctx, relay); err != nil {
		t.Fatalf("RegisterRelay: %v", err)
	}

	relays, err := backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("ListRelays: %v", err)
	}
	if len(relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relays))
	}
	if relays[0].ID != relay.ID || relays[0].Address != relay.Address || relays[0].GRPCPort != relay.GRPCPort {
		t.Fatalf("relay mismatch: %#v", relays[0])
	}

	ts := time.Now().UTC().Add(10 * time.Second)
	if err := backend.HeartbeatRelay(ctx, relay.ID, ts); err != nil {
		t.Fatalf("HeartbeatRelay: %v", err)
	}

	relays, err = backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("ListRelays: %v", err)
	}
	if len(relays) != 1 {
		t.Fatalf("expected 1 relay after heartbeat, got %d", len(relays))
	}
	if relays[0].LastSeen.UnixMilli() != ts.UnixMilli() {
		t.Fatalf("expected last seen %v, got %v", ts, relays[0].LastSeen)
	}

	if err := backend.RemoveRelay(ctx, relay.ID); err != nil {
		t.Fatalf("RemoveRelay: %v", err)
	}

	relays, err = backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("ListRelays after remove: %v", err)
	}
	if len(relays) != 0 {
		t.Fatalf("expected 0 relays after remove, got %d", len(relays))
	}
}

func TestBackendAgentPlacement(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer cleanup()

	ctx := context.Background()
	agent := registry.Agent{ID: "agent-1"}
	relayID := "relay-1"

	placement, err := backend.GetAgentPlacement(ctx, agent.ID)
	if err == nil || !errors.Is(err, registry.ErrAgentNotPlaced) {
		t.Fatalf("expected ErrAgentNotPlaced, got %v", err)
	}
	if placement != nil {
		t.Fatalf("expected nil placement, got %#v", placement)
	}

	if err := backend.RegisterAgent(ctx, agent, relayID); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	placement, err = backend.GetAgentPlacement(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentPlacement: %v", err)
	}
	if placement == nil {
		t.Fatalf("expected placement to be stored")
	}
	if placement.AgentID != agent.ID || placement.RelayID != relayID {
		t.Fatalf("placement mismatch: %#v", placement)
	}

	updatedAt := time.Now().UTC().Add(30 * time.Second)
	before := time.Now().UTC()
	if err := backend.HeartbeatAgent(ctx, agent.ID, updatedAt); err != nil {
		t.Fatalf("HeartbeatAgent: %v", err)
	}
	after := time.Now().UTC()

	placement, err = backend.GetAgentPlacement(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentPlacement: %v", err)
	}
	updatedAtMs := placement.UpdatedAt.UnixMilli()
	if updatedAtMs < before.UnixMilli() || updatedAtMs > after.UnixMilli() {
		t.Fatalf("expected updated at between %v and %v, got %v", before, after, placement.UpdatedAt)
	}
}

func TestBackendRelayStaleFiltered(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer cleanup()

	ctx := context.Background()
	relay := registry.Relay{
		ID:       "relay-stale",
		Address:  "10.0.0.2",
		GRPCPort: 60000,
	}

	if err := backend.RegisterRelay(ctx, relay); err != nil {
		t.Fatalf("RegisterRelay: %v", err)
	}

	staleTS := time.Now().UTC().Add(-time.Minute)
	if err := backend.HeartbeatRelay(ctx, relay.ID, staleTS); err != nil {
		t.Fatalf("HeartbeatRelay: %v", err)
	}

	relays, err := backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("ListRelays: %v", err)
	}
	if len(relays) != 0 {
		t.Fatalf("expected stale relay to be filtered, got %d", len(relays))
	}
}

func TestBackendRelayTTLExpiry(t *testing.T) {
	backend, server, cleanup := newTestBackend(t)
	defer cleanup()

	ctx := context.Background()
	relay := registry.Relay{ID: "relay-ttl", Address: "10.0.0.3", GRPCPort: 60001}
	if err := backend.RegisterRelay(ctx, relay); err != nil {
		t.Fatalf("RegisterRelay: %v", err)
	}

	server.FastForward(3 * time.Second)

	relays, err := backend.ListRelays(ctx)
	if err != nil {
		t.Fatalf("ListRelays: %v", err)
	}
	if len(relays) != 0 {
		t.Fatalf("expected relay to expire, got %d", len(relays))
	}
}

func TestBackendAgentStaleFiltered(t *testing.T) {
	backend, _, cleanup := newTestBackend(t)
	defer cleanup()

	ctx := context.Background()
	agent := registry.Agent{ID: "agent-stale"}
	if err := backend.RegisterAgent(ctx, agent, "relay-9"); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	staleUpdatedAt := time.Now().UTC().Add(-time.Minute).UnixMilli()
	if err := backend.rdb.HSet(ctx, placementKey(agent.ID), map[string]any{
		"AgentID":         agent.ID,
		"RelayID":         "relay-9",
		"UpdatedAtUnixMs": staleUpdatedAt,
	}).Err(); err != nil {
		t.Fatalf("seed stale placement: %v", err)
	}

	placement, err := backend.GetAgentPlacement(ctx, agent.ID)
	if err == nil || !errors.Is(err, registry.ErrAgentNotPlaced) {
		t.Fatalf("expected ErrAgentNotPlaced for stale placement, got %v", err)
	}
	if placement != nil {
		t.Fatalf("expected stale placement to be filtered, got %#v", placement)
	}
}

func TestBackendAgentTTLExpiry(t *testing.T) {
	backend, server, cleanup := newTestBackend(t)
	defer cleanup()

	ctx := context.Background()
	agent := registry.Agent{ID: "agent-ttl"}
	if err := backend.RegisterAgent(ctx, agent, "relay-7"); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	server.FastForward(3 * time.Second)

	placement, err := backend.GetAgentPlacement(ctx, agent.ID)
	if err == nil || !errors.Is(err, registry.ErrAgentNotPlaced) {
		t.Fatalf("expected ErrAgentNotPlaced after expiry, got %v", err)
	}
	if placement != nil {
		t.Fatalf("expected placement to expire, got %#v", placement)
	}
}
