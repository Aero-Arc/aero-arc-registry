//go:build integration

package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	redisclient "github.com/redis/go-redis/v9"
)

// TestRedisIntegration exercises the backend against a real Redis server.
// CI provides one at localhost:6379; local runs can override REDIS_TEST_ADDR.
func TestRedisIntegration(t *testing.T) {
	address := os.Getenv("REDIS_TEST_ADDR")
	if address == "" {
		address = "127.0.0.1:6379"
	}

	client := redisclient.NewClient(&redisclient.Options{Addr: address})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis integration server %q unavailable: %v", address, err)
	}

	namespace := fmt.Sprintf("{aero-arc-registry-test}:v1:%d:", time.Now().UnixNano())
	backend := newBackend(client, registry.TTLConfig{
		Relay: 2 * time.Second,
		Agent: 2 * time.Second,
	}, namespace)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = client.Del(cleanupCtx,
			backend.relayKey("relay-integration"),
			backend.agentKey("agent-integration"),
			backend.relayAgentsKey("relay-integration"),
			backend.relaysKey(),
			backend.agentsKey(),
		).Err()
		_ = backend.Close(cleanupCtx)
	})

	registerRelay(t, backend, "relay-integration")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-integration"}, "relay-integration"); err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	relays, err := backend.ListRelays(ctx)
	if err != nil || len(relays) != 1 || relays[0].ID != "relay-integration" {
		t.Fatalf("ListRelays() = %+v, error = %v", relays, err)
	}
	agents, err := backend.ListAgents(ctx)
	if err != nil || len(agents) != 1 || agents[0].ID != "agent-integration" {
		t.Fatalf("ListAgents() = %+v, error = %v", agents, err)
	}
	assertPlacement(t, backend, "agent-integration", "relay-integration")
	assertRelayAgentIDs(t, backend, "relay-integration", []string{"agent-integration"})

	if err := backend.HeartbeatRelay(ctx, "relay-integration"); err != nil {
		t.Fatalf("HeartbeatRelay() error = %v", err)
	}
	if err := backend.HeartbeatAgent(ctx, "agent-integration"); err != nil {
		t.Fatalf("HeartbeatAgent() error = %v", err)
	}
	if ttl, err := client.PTTL(ctx, backend.agentKey("agent-integration")).Result(); err != nil || ttl <= 0 {
		t.Fatalf("agent PTTL = %v, error = %v", ttl, err)
	}

	if err := backend.RemoveAgents(ctx, []string{"agent-integration"}); err != nil {
		t.Fatalf("RemoveAgents() error = %v", err)
	}
	if err := backend.RemoveRelay(ctx, "relay-integration"); err != nil {
		t.Fatalf("RemoveRelay() error = %v", err)
	}
}
