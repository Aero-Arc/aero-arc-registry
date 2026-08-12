//go:build integration

package redis

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	registrygrpc "github.com/Aero-Arc/aero-arc-registry/internal/transport/grpc"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// TestRegistryGRPCWithRedis exercises the complete in-repository production
// path: generated client, gRPC handlers, Registry service, and real Redis.
func TestRegistryGRPCWithRedis(t *testing.T) {
	redisAddress := startRedisTestContainer(t)
	redisHost, redisPortText, err := net.SplitHostPort(redisAddress)
	if err != nil {
		t.Fatalf("parse Redis Testcontainer address %q: %v", redisAddress, err)
	}
	redisPort, err := strconv.Atoi(redisPortText)
	if err != nil {
		t.Fatalf("parse Redis Testcontainer port %q: %v", redisPortText, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	listenPort := listener.Addr().(*net.TCPAddr).Port
	ttl := registry.TTLConfig{Relay: 2 * time.Second, Agent: 2 * time.Second}
	redisConfig := &registry.RedisConfig{Address: redisHost, Port: redisPort}
	backend, err := New(redisConfig, ttl)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close(context.Background()) })
	service, err := registry.New(&registry.Config{
		Backend: registry.BackendConfig{Type: registry.RedisRegistryBackend, Redis: redisConfig},
		GRPC:    registry.GRPCConfig{ListenAddress: "127.0.0.1", ListenPort: listenPort},
		TTL:     ttl,
	}, backend)
	if err != nil {
		t.Fatal(err)
	}
	server, err := registrygrpc.New(service)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.GracefulStop()
		if serveErr := <-serveDone; serveErr != nil && !errors.Is(serveErr, gogrpc.ErrServerStopped) {
			t.Errorf("Registry gRPC Serve() error: %v", serveErr)
		}
	})

	connection, err := gogrpc.NewClient(listener.Addr().String(), gogrpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := registryv1.NewAeroRegistryClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, relay := range []*registryv1.Relay{
		{RelayId: "relay-a", Address: "relay-a.internal", GrpcPort: 50051},
		{RelayId: "relay-b", Address: "relay-b.internal", GrpcPort: 50052},
	} {
		if _, err := client.RegisterRelay(ctx, &registryv1.RegisterRelayRequest{Relay: relay}); err != nil {
			t.Fatalf("RegisterRelay(%s) error = %v", relay.GetRelayId(), err)
		}
	}
	relays, err := client.ListRelays(ctx, &registryv1.ListRelaysRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(relays.GetRelays()) != 2 || relays.GetRelays()[0].GetRelayId() != "relay-a" || relays.GetRelays()[1].GetRelayId() != "relay-b" {
		t.Fatalf("ListRelays() = %+v, want relay-a then relay-b", relays.GetRelays())
	}

	if _, err := client.RegisterAgent(ctx, &registryv1.RegisterAgentRequest{
		Agent: &registryv1.Agent{AgentId: "agent-1"}, RelayId: "relay-a",
	}); err != nil {
		t.Fatalf("RegisterAgent(relay-a) error = %v", err)
	}
	assertGRPCPlacement(t, ctx, client, "agent-1", "relay-a")
	agents, err := client.ListAgents(ctx, &registryv1.ListAgentsRequest{})
	if err != nil || len(agents.GetAgents()) != 1 || agents.GetAgents()[0].GetAgentId() != "agent-1" || agents.GetAgents()[0].GetLastHeartbeatUnixMs() <= 0 {
		t.Fatalf("ListAgents() = %+v, error = %v", agents.GetAgents(), err)
	}

	if _, err := client.RegisterAgent(ctx, &registryv1.RegisterAgentRequest{
		Agent: &registryv1.Agent{AgentId: "agent-1"}, RelayId: "relay-b",
	}); err != nil {
		t.Fatalf("RegisterAgent(relay-b takeover) error = %v", err)
	}
	assertGRPCPlacement(t, ctx, client, "agent-1", "relay-b")
	if _, err := client.HeartbeatAgent(ctx, &registryv1.HeartbeatAgentRequest{AgentId: "agent-1", RelayId: "relay-a"}); status.Code(err) != codes.NotFound {
		t.Fatalf("HeartbeatAgent(stale relay-a) error = %v, want NotFound", err)
	}
	assertGRPCPlacement(t, ctx, client, "agent-1", "relay-b")
	if _, err := client.HeartbeatAgent(ctx, &registryv1.HeartbeatAgentRequest{AgentId: "agent-1", RelayId: "relay-b"}); err != nil {
		t.Fatalf("HeartbeatAgent(current relay-b) error = %v", err)
	}

	// Redis native expiry must be observable through the transport and trigger
	// repair of the surviving Agent placement.
	if err := backend.client.PExpire(ctx, backend.relayKey("relay-b"), 50*time.Millisecond).Err(); err != nil {
		t.Fatalf("shorten relay-b TTL: %v", err)
	}
	expiryDeadline := time.Now().Add(2 * time.Second)
	for {
		_, err := client.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: "agent-1"})
		if status.Code(err) == codes.NotFound {
			break
		}
		if err != nil {
			t.Fatalf("GetAgentPlacement(after relay expiry) error = %v", err)
		}
		if time.Now().After(expiryDeadline) {
			t.Fatal("expired relay-b placement remained visible through gRPC")
		}
		time.Sleep(10 * time.Millisecond)
	}
	agents, err = client.ListAgents(ctx, &registryv1.ListAgentsRequest{})
	if err != nil || len(agents.GetAgents()) != 0 {
		t.Fatalf("ListAgents() after relay expiry = %+v, error = %v", agents.GetAgents(), err)
	}
}

func assertGRPCPlacement(
	t *testing.T,
	ctx context.Context,
	client registryv1.AeroRegistryClient,
	agentID, relayID string,
) {
	t.Helper()
	response, err := client.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: agentID})
	if err != nil {
		t.Fatalf("GetAgentPlacement(%s) error = %v", agentID, err)
	}
	placement := response.GetPlacement()
	if placement.GetAgentId() != agentID || placement.GetRelayId() != relayID || placement.GetLastUpdatedUnixMs() <= 0 {
		t.Fatalf("GetAgentPlacement(%s) = %+v, want relay %s with timestamp", agentID, placement, relayID)
	}
}
