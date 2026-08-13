//go:build integration

package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	redisclient "github.com/redis/go-redis/v9"
)

// TestRedisIntegration exercises the backend against a real Redis server.
// By default the test owns a Redis container; REDIS_TEST_ADDR opts into an
// externally managed server instead.
func TestRedisIntegration(t *testing.T) {
	address := os.Getenv("REDIS_TEST_ADDR")
	if address == "" {
		address = startRedisTestContainer(t)
	} else {
		t.Logf("Using externally managed Redis test dependency: endpoint=%s", address)
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
			backend.namespace+"untrusted-membership-sentinel",
			backend.relayKey("relay-integration"),
			backend.relayKey("relay-takeover"),
			backend.relayKey("relay-corrupt"),
			backend.relayKey("relay-register-corrupt"),
			backend.relayKey("relay-heartbeat-corrupt"),
			backend.relayKey("relay-agent-heartbeat"),
			backend.relayKey("relay-ttl-expiry"),
			backend.agentKey("agent-integration"),
			backend.agentKey("agent-corrupt"),
			backend.agentKey("agent-register-rejected"),
			backend.agentKey("agent-heartbeat-corrupt"),
			backend.agentKey("agent-ttl-expiry"),
			backend.relayAgentsKey("relay-integration"),
			backend.relayAgentsKey("relay-takeover"),
			backend.relayAgentsKey("relay-corrupt"),
			backend.relayAgentsKey("relay-register-corrupt"),
			backend.relayAgentsKey("relay-heartbeat-corrupt"),
			backend.relayAgentsKey("relay-agent-heartbeat"),
			backend.relayAgentsKey("relay-ttl-expiry"),
			backend.relayIncarnationKey(),
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
	incarnation := relayIncarnation(t, backend, "relay-integration")

	if err := backend.RemoveRelay(ctx, "relay-integration"); err != nil {
		t.Fatalf("RemoveRelay(before re-registration) error = %v", err)
	}
	registerRelay(t, backend, "relay-integration")
	if current := relayIncarnation(t, backend, "relay-integration"); current == incarnation {
		t.Fatalf("relay re-registration reused incarnation %q", current)
	}
	if agents, err := backend.ListAgents(ctx); err != nil || len(agents) != 0 {
		t.Fatalf("ListAgents() after relay re-registration = %+v, error = %v", agents, err)
	}
	if _, err := backend.GetAgentPlacement(ctx, "agent-integration"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("GetAgentPlacement(old incarnation) error = %v, want ErrNotFound", err)
	}
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-integration"}, "relay-integration"); err != nil {
		t.Fatalf("RegisterAgent(current incarnation) error = %v", err)
	}
	registerRelay(t, backend, "relay-takeover")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-integration"}, "relay-takeover"); err != nil {
		t.Fatalf("RegisterAgent(takeover) error = %v", err)
	}
	placementAfterTakeover, err := backend.GetAgentPlacement(ctx, "agent-integration")
	if err != nil {
		t.Fatalf("GetAgentPlacement(takeover) error = %v", err)
	}
	ttlAfterTakeover, err := client.PTTL(ctx, backend.agentKey("agent-integration")).Result()
	if err != nil {
		t.Fatalf("agent PTTL after takeover: %v", err)
	}
	if err := backend.HeartbeatAgent(ctx, "agent-integration", "relay-integration"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("HeartbeatAgent(old owner) error = %v, want ErrNotFound", err)
	}
	if ttl, err := client.PTTL(ctx, backend.agentKey("agent-integration")).Result(); err != nil || ttl > ttlAfterTakeover {
		t.Fatalf("rejected heartbeat renewed TTL: before=%v after=%v error=%v", ttlAfterTakeover, ttl, err)
	}
	placementAfterRejected, err := backend.GetAgentPlacement(ctx, "agent-integration")
	if err != nil || *placementAfterRejected != *placementAfterTakeover {
		t.Fatalf("rejected heartbeat changed placement: before=%+v after=%+v error=%v", placementAfterTakeover, placementAfterRejected, err)
	}
	if err := backend.HeartbeatAgent(ctx, "agent-integration", "relay-takeover"); err != nil {
		t.Fatalf("HeartbeatAgent(current owner) error = %v", err)
	}
	sentinelKey := backend.namespace + "untrusted-membership-sentinel"
	if err := client.Set(ctx, sentinelKey, "keep", 0).Err(); err != nil {
		t.Fatalf("create untrusted membership sentinel: %v", err)
	}
	if err := client.HSet(ctx, backend.agentKey("agent-integration"), "relay_agents_key", sentinelKey).Err(); err != nil {
		t.Fatalf("corrupt stored membership key: %v", err)
	}
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-integration"}, "relay-integration"); err != nil {
		t.Fatalf("RegisterAgent(reassign with corrupt stored membership) error = %v", err)
	}
	if got, err := client.Get(ctx, sentinelKey).Result(); err != nil || got != "keep" {
		t.Fatalf("untrusted membership key changed: value=%q error=%v", got, err)
	}
	if err := client.ZScore(ctx, backend.relayAgentsKey("relay-takeover"), "agent-integration").Err(); !errors.Is(err, redisclient.Nil) {
		t.Fatalf("canonical old membership remains: %v", err)
	}
	assertPlacement(t, backend, "agent-integration", "relay-integration")

	registerRelay(t, backend, "relay-register-corrupt")
	if err := client.HDel(ctx, backend.relayKey("relay-register-corrupt"), "address").Err(); err != nil {
		t.Fatalf("corrupt registration target: %v", err)
	}
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-register-rejected"}, "relay-register-corrupt"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("RegisterAgent(corrupt target) error = %v, want ErrNotFound", err)
	}
	if exists := client.Exists(ctx, backend.agentKey("agent-register-rejected"), backend.relayKey("relay-register-corrupt"), backend.relayAgentsKey("relay-register-corrupt")).Val(); exists != 0 {
		t.Fatalf("rejected registration/repair left %d entity keys", exists)
	}
	if err := client.ZScore(ctx, backend.agentsKey(), "agent-register-rejected").Err(); !errors.Is(err, redisclient.Nil) {
		t.Fatalf("rejected registration wrote agent index: %v", err)
	}
	if err := client.ZScore(ctx, backend.relaysKey(), "relay-register-corrupt").Err(); !errors.Is(err, redisclient.Nil) {
		t.Fatalf("corrupt registration target remained indexed: %v", err)
	}
	registerRelay(t, backend, "relay-register-corrupt")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-register-rejected"}, "relay-register-corrupt"); err != nil {
		t.Fatalf("RegisterAgent(repaired target) error = %v", err)
	}
	assertPlacement(t, backend, "agent-register-rejected", "relay-register-corrupt")
	if err := backend.RemoveAgents(ctx, []string{"agent-register-rejected"}); err != nil {
		t.Fatalf("RemoveAgents(registration scenario) error = %v", err)
	}
	if err := backend.RemoveRelay(ctx, "relay-register-corrupt"); err != nil {
		t.Fatalf("RemoveRelay(registration scenario) error = %v", err)
	}

	registerRelay(t, backend, "relay-heartbeat-corrupt")
	if err := client.Persist(ctx, backend.relayKey("relay-heartbeat-corrupt")).Err(); err != nil {
		t.Fatalf("persist relay heartbeat target: %v", err)
	}
	if err := backend.HeartbeatRelay(ctx, "relay-heartbeat-corrupt"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("HeartbeatRelay(corrupt target) error = %v, want ErrNotFound", err)
	}
	if exists := client.Exists(ctx, backend.relayKey("relay-heartbeat-corrupt"), backend.relayAgentsKey("relay-heartbeat-corrupt")).Val(); exists != 0 {
		t.Fatalf("rejected relay heartbeat left %d keys", exists)
	}

	registerRelay(t, backend, "relay-agent-heartbeat")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-heartbeat-corrupt"}, "relay-agent-heartbeat"); err != nil {
		t.Fatalf("RegisterAgent(heartbeat scenario) error = %v", err)
	}
	if err := client.Persist(ctx, backend.agentKey("agent-heartbeat-corrupt")).Err(); err != nil {
		t.Fatalf("persist agent heartbeat target: %v", err)
	}
	if err := backend.HeartbeatAgent(ctx, "agent-heartbeat-corrupt", "relay-agent-heartbeat"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("HeartbeatAgent(corrupt target) error = %v, want ErrNotFound", err)
	}
	if exists := client.Exists(ctx, backend.agentKey("agent-heartbeat-corrupt")).Val(); exists != 0 {
		t.Fatal("rejected agent heartbeat renewed corrupt entity")
	}
	if err := backend.RemoveRelay(ctx, "relay-agent-heartbeat"); err != nil {
		t.Fatalf("RemoveRelay(agent heartbeat scenario) error = %v", err)
	}

	if err := backend.HeartbeatRelay(ctx, "relay-integration"); err != nil {
		t.Fatalf("HeartbeatRelay() error = %v", err)
	}
	if ttl, err := client.PTTL(ctx, backend.agentKey("agent-integration")).Result(); err != nil || ttl <= 0 {
		t.Fatalf("agent PTTL = %v, error = %v", ttl, err)
	}

	registerRelay(t, backend, "relay-ttl-expiry")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-ttl-expiry"}, "relay-ttl-expiry"); err != nil {
		t.Fatalf("RegisterAgent(TTL scenario) error = %v", err)
	}
	if err := client.PExpire(ctx, backend.relayKey("relay-ttl-expiry"), 50*time.Millisecond).Err(); err != nil {
		t.Fatalf("shorten relay TTL: %v", err)
	}
	expiryDeadline := time.Now().Add(2 * time.Second)
	for client.Exists(ctx, backend.relayKey("relay-ttl-expiry")).Val() != 0 && time.Now().Before(expiryDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if exists := client.Exists(ctx, backend.relayKey("relay-ttl-expiry")).Val(); exists != 0 {
		t.Fatal("Redis did not expire the short-lived relay hash")
	}
	if _, err := backend.GetAgentPlacement(ctx, "agent-ttl-expiry"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("GetAgentPlacement(expired relay) error = %v, want ErrNotFound", err)
	}
	if exists := client.Exists(ctx, backend.agentKey("agent-ttl-expiry")).Val(); exists != 0 {
		t.Fatal("placement read did not repair agent whose relay expired")
	}
	if relays, err := backend.ListRelays(ctx); err != nil {
		t.Fatalf("ListRelays(TTL scenario) error = %v", err)
	} else {
		for _, relay := range relays {
			if relay.ID == "relay-ttl-expiry" {
				t.Fatalf("ListRelays returned expired relay: %+v", relay)
			}
		}
	}
	if err := client.ZScore(ctx, backend.relaysKey(), "relay-ttl-expiry").Err(); !errors.Is(err, redisclient.Nil) {
		t.Fatalf("expired relay index was not repaired: %v", err)
	}

	registerRelay(t, backend, "relay-corrupt")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-corrupt"}, "relay-corrupt"); err != nil {
		t.Fatalf("RegisterAgent(corrupt relay) error = %v", err)
	}
	if err := client.HDel(ctx, backend.relayKey("relay-corrupt"), "address").Err(); err != nil {
		t.Fatalf("corrupt relay address: %v", err)
	}
	agents, err = backend.ListAgents(ctx)
	if err != nil || len(agents) != 1 || agents[0].ID != "agent-integration" {
		t.Fatalf("ListAgents() with corrupt relay = %+v, want healthy agent only, error = %v", agents, err)
	}
	relays, err = backend.ListRelays(ctx)
	if err != nil || len(relays) != 2 || relays[0].ID != "relay-integration" || relays[1].ID != "relay-takeover" {
		t.Fatalf("ListRelays() with corrupt neighbor = %+v, want two healthy relays, error = %v", relays, err)
	}
	if exists := client.Exists(ctx, backend.relayKey("relay-corrupt"), backend.relayAgentsKey("relay-corrupt")).Val(); exists != 0 {
		t.Fatalf("corrupt relay repair left %d keys", exists)
	}
	if err := client.ZScore(ctx, backend.relaysKey(), "relay-corrupt").Err(); !errors.Is(err, redisclient.Nil) {
		t.Fatalf("relay index still contains corrupt relay: %v", err)
	}
	if exists := client.Exists(ctx, backend.agentKey("agent-corrupt")).Val(); exists != 0 {
		t.Fatal("agent read did not repair entity on corrupt relay")
	}

	registerRelay(t, backend, "relay-corrupt")
	if err := backend.RegisterAgent(ctx, registry.Agent{ID: "agent-corrupt"}, "relay-corrupt"); err != nil {
		t.Fatalf("RegisterAgent(corrupt placement) error = %v", err)
	}
	if err := client.HSet(ctx, backend.agentKey("agent-corrupt"), "placement_updated_ms", "9223372036854775808").Err(); err != nil {
		t.Fatalf("corrupt placement timestamp: %v", err)
	}
	if _, err := backend.GetAgentPlacement(ctx, "agent-corrupt"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("GetAgentPlacement(corrupt timestamp) error = %v, want ErrNotFound", err)
	}

	if err := backend.RemoveAgents(ctx, []string{"agent-integration", "agent-corrupt"}); err != nil {
		t.Fatalf("RemoveAgents() error = %v", err)
	}
	if _, err := backend.GetAgentPlacement(ctx, "agent-integration"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("GetAgentPlacement(removed agent) error = %v, want ErrNotFound", err)
	}
	if agents, err := backend.ListRelayAgents(ctx, "relay-integration"); err != nil || len(agents) != 0 {
		t.Fatalf("ListRelayAgents() after RemoveAgents = %+v, error=%v", agents, err)
	}
	if err := backend.RemoveRelay(ctx, "relay-integration"); err != nil {
		t.Fatalf("RemoveRelay() error = %v", err)
	}
	if err := backend.HeartbeatRelay(ctx, "relay-integration"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("HeartbeatRelay(removed relay) error = %v, want ErrNotFound", err)
	}
	if err := backend.RemoveRelay(ctx, "relay-takeover"); err != nil {
		t.Fatalf("RemoveRelay(takeover) error = %v", err)
	}
}
