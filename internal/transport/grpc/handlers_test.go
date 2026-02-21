package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type transportBackendStub struct {
	registerRelayFn     func(ctx context.Context, relay registry.Relay) error
	heartbeatRelayFn    func(ctx context.Context, relayID string) error
	listRelaysFn        func(ctx context.Context) ([]registry.Relay, error)
	registerAgentFn     func(ctx context.Context, agent registry.Agent, relayID string) error
	heartbeatAgentFn    func(ctx context.Context, agentID string) error
	getPlacementFn      func(ctx context.Context, agentID string) (*registry.AgentPlacement, error)
	listAgentsFn        func(ctx context.Context) ([]registry.Agent, error)
	listRelayAgentsFn   func(ctx context.Context, relayID string) ([]*registry.Agent, error)
	removeAgentsFn      func(ctx context.Context, agentIDs []string) error
	removeRelayFn       func(ctx context.Context, relayID string) error
	closeFn             func(ctx context.Context) error
	lastRegisteredRelay registry.Relay
	lastRelayHeartbeat  string
	lastRegisteredAgent registry.Agent
	lastAgentRelayID    string
	lastAgentHeartbeat  string
}

func (b *transportBackendStub) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	b.lastRegisteredRelay = relay
	if b.registerRelayFn != nil {
		return b.registerRelayFn(ctx, relay)
	}
	return nil
}

func (b *transportBackendStub) HeartbeatRelay(ctx context.Context, relayID string) error {
	b.lastRelayHeartbeat = relayID
	if b.heartbeatRelayFn != nil {
		return b.heartbeatRelayFn(ctx, relayID)
	}
	return nil
}

func (b *transportBackendStub) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	if b.listRelaysFn != nil {
		return b.listRelaysFn(ctx)
	}
	return nil, nil
}

func (b *transportBackendStub) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	b.lastRegisteredAgent = agent
	b.lastAgentRelayID = relayID
	if b.registerAgentFn != nil {
		return b.registerAgentFn(ctx, agent, relayID)
	}
	return nil
}

func (b *transportBackendStub) HeartbeatAgent(ctx context.Context, agentID string) error {
	b.lastAgentHeartbeat = agentID
	if b.heartbeatAgentFn != nil {
		return b.heartbeatAgentFn(ctx, agentID)
	}
	return nil
}

func (b *transportBackendStub) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	if b.getPlacementFn != nil {
		return b.getPlacementFn(ctx, agentID)
	}
	return nil, registry.ErrNotFound
}

func (b *transportBackendStub) ListAgents(ctx context.Context) ([]registry.Agent, error) {
	if b.listAgentsFn != nil {
		return b.listAgentsFn(ctx)
	}
	return nil, nil
}

func (b *transportBackendStub) ListRelayAgents(ctx context.Context, relayID string) ([]*registry.Agent, error) {
	if b.listRelayAgentsFn != nil {
		return b.listRelayAgentsFn(ctx, relayID)
	}
	return nil, nil
}

func (b *transportBackendStub) RemoveAgents(ctx context.Context, agentIDs []string) error {
	if b.removeAgentsFn != nil {
		return b.removeAgentsFn(ctx, agentIDs)
	}
	return nil
}

func (b *transportBackendStub) RemoveRelay(ctx context.Context, relayID string) error {
	if b.removeRelayFn != nil {
		return b.removeRelayFn(ctx, relayID)
	}
	return nil
}

func (b *transportBackendStub) Close(ctx context.Context) error {
	if b.closeFn != nil {
		return b.closeFn(ctx)
	}
	return nil
}

func newTransportTestServer(t *testing.T, b registry.Backend) *Server {
	t.Helper()

	cfg := &registry.Config{
		Backend: registry.BackendConfig{Type: registry.MemoryRegistryBackend},
		GRPC: registry.GRPCConfig{
			ListenAddress: "127.0.0.1",
			ListenPort:    50051,
		},
		TTL: registry.TTLConfig{
			Relay: 5 * time.Second,
			Agent: 5 * time.Second,
		},
	}

	reg, err := registry.New(cfg, b)
	if err != nil {
		t.Fatalf("registry.New() error = %v", err)
	}

	return &Server{registry: reg}
}

func TestRegisterRelay(t *testing.T) {
	t.Parallel()

	t.Run("validates request", func(t *testing.T) {
		t.Parallel()
		s := newTransportTestServer(t, &transportBackendStub{})

		_, err := s.RegisterRelay(context.Background(), &registryv1.RegisterRelayRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("maps conflict and forwards payload", func(t *testing.T) {
		t.Parallel()
		b := &transportBackendStub{
			registerRelayFn: func(ctx context.Context, relay registry.Relay) error {
				return registry.ErrConflict
			},
		}
		s := newTransportTestServer(t, b)

		_, err := s.RegisterRelay(context.Background(), &registryv1.RegisterRelayRequest{
			Relay: &registryv1.Relay{
				RelayId:  "relay-1",
				Address:  "127.0.0.1",
				GrpcPort: 7000,
			},
		})
		if status.Code(err) != codes.AlreadyExists {
			t.Fatalf("expected AlreadyExists, got %v", status.Code(err))
		}
		if b.lastRegisteredRelay.ID != "relay-1" || b.lastRegisteredRelay.Address != "127.0.0.1" || b.lastRegisteredRelay.GRPCPort != 7000 {
			t.Fatalf("unexpected relay payload: %+v", b.lastRegisteredRelay)
		}
	})
}

func TestHeartbeatRelay(t *testing.T) {
	t.Parallel()

	t.Run("validates empty id", func(t *testing.T) {
		t.Parallel()
		s := newTransportTestServer(t, &transportBackendStub{})
		_, err := s.HeartbeatRelay(context.Background(), &registryv1.HeartbeatRelayRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("maps not found", func(t *testing.T) {
		t.Parallel()
		b := &transportBackendStub{
			heartbeatRelayFn: func(ctx context.Context, relayID string) error {
				return registry.ErrNotFound
			},
		}
		s := newTransportTestServer(t, b)
		_, err := s.HeartbeatRelay(context.Background(), &registryv1.HeartbeatRelayRequest{RelayId: "relay-404"})
		if status.Code(err) != codes.NotFound {
			t.Fatalf("expected NotFound, got %v", status.Code(err))
		}
		if b.lastRelayHeartbeat != "relay-404" {
			t.Fatalf("expected relay heartbeat id relay-404, got %q", b.lastRelayHeartbeat)
		}
	})
}

func TestListRelays(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0)
	b := &transportBackendStub{
		listRelaysFn: func(ctx context.Context) ([]registry.Relay, error) {
			return []registry.Relay{
				{ID: "r1", Address: "10.0.0.1", GRPCPort: 5000, LastSeen: now},
			}, nil
		},
	}
	s := newTransportTestServer(t, b)

	resp, err := s.ListRelays(context.Background(), &registryv1.ListRelaysRequest{})
	if err != nil {
		t.Fatalf("ListRelays() error = %v", err)
	}
	if len(resp.Relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(resp.Relays))
	}
	if resp.Relays[0].LastHeartbeatUnixMs != now.UnixMilli() {
		t.Fatalf("unexpected heartbeat ms: got %d want %d", resp.Relays[0].LastHeartbeatUnixMs, now.UnixMilli())
	}
}

func TestRegisterAgent(t *testing.T) {
	t.Parallel()

	t.Run("validates request", func(t *testing.T) {
		t.Parallel()
		s := newTransportTestServer(t, &transportBackendStub{})
		_, err := s.RegisterAgent(context.Background(), &registryv1.RegisterAgentRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("forwards payload", func(t *testing.T) {
		t.Parallel()
		b := &transportBackendStub{}
		s := newTransportTestServer(t, b)
		_, err := s.RegisterAgent(context.Background(), &registryv1.RegisterAgentRequest{
			RelayId: "relay-1",
			Agent:   &registryv1.Agent{AgentId: "agent-1"},
		})
		if err != nil {
			t.Fatalf("RegisterAgent() error = %v", err)
		}
		if b.lastRegisteredAgent.ID != "agent-1" || b.lastAgentRelayID != "relay-1" {
			t.Fatalf("unexpected agent payload: agent=%+v relay=%s", b.lastRegisteredAgent, b.lastAgentRelayID)
		}
	})
}

func TestHeartbeatAgent(t *testing.T) {
	t.Parallel()

	b := &transportBackendStub{
		heartbeatAgentFn: func(ctx context.Context, agentID string) error {
			return registry.ErrInvalid
		},
	}
	s := newTransportTestServer(t, b)

	_, err := s.HeartbeatAgent(context.Background(), &registryv1.HeartbeatAgentRequest{AgentId: "agent-1"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
	if b.lastAgentHeartbeat != "agent-1" {
		t.Fatalf("expected agent-1 heartbeat, got %q", b.lastAgentHeartbeat)
	}
}

func TestGetAgentPlacement(t *testing.T) {
	t.Parallel()

	t.Run("maps response", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1700000000, 0)
		b := &transportBackendStub{
			getPlacementFn: func(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
				return &registry.AgentPlacement{
					AgentID:   "agent-1",
					RelayID:   "relay-1",
					UpdatedAt: now,
				}, nil
			},
		}
		s := newTransportTestServer(t, b)
		resp, err := s.GetAgentPlacement(context.Background(), &registryv1.GetAgentPlacementRequest{AgentId: "agent-1"})
		if err != nil {
			t.Fatalf("GetAgentPlacement() error = %v", err)
		}
		if resp.Placement.LastUpdatedUnixMs != now.UnixMilli() {
			t.Fatalf("unexpected updated ts: got %d want %d", resp.Placement.LastUpdatedUnixMs, now.UnixMilli())
		}
	})

	t.Run("maps not found", func(t *testing.T) {
		t.Parallel()
		s := newTransportTestServer(t, &transportBackendStub{
			getPlacementFn: func(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
				return nil, registry.ErrNotFound
			},
		})
		_, err := s.GetAgentPlacement(context.Background(), &registryv1.GetAgentPlacementRequest{AgentId: "agent-404"})
		if status.Code(err) != codes.NotFound {
			t.Fatalf("expected NotFound, got %v", status.Code(err))
		}
	})
}

func TestListAgents(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000100, 0)
	s := newTransportTestServer(t, &transportBackendStub{
		listAgentsFn: func(ctx context.Context) ([]registry.Agent, error) {
			return []registry.Agent{
				{ID: "agent-1", LastHeartbeat: now},
			}, nil
		},
	})

	resp, err := s.ListAgents(context.Background(), &registryv1.ListAgentsRequest{})
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(resp.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(resp.Agents))
	}
	if resp.Agents[0].LastHeartbeatUnixMs != now.UnixMilli() {
		t.Fatalf("unexpected heartbeat ms: got %d want %d", resp.Agents[0].LastHeartbeatUnixMs, now.UnixMilli())
	}
}

func TestToStatusError(t *testing.T) {
	t.Parallel()

	if toStatusError(nil) != nil {
		t.Fatal("expected nil error")
	}

	canceled := context.Canceled
	if got := toStatusError(canceled); !errors.Is(got, canceled) {
		t.Fatalf("expected canceled passthrough, got %v", got)
	}

	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "not found", err: registry.ErrNotFound, code: codes.NotFound},
		{name: "invalid", err: registry.ErrInvalid, code: codes.InvalidArgument},
		{name: "conflict", err: registry.ErrConflict, code: codes.AlreadyExists},
		{name: "internal fallback", err: errors.New("boom"), code: codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := status.Code(toStatusError(tc.err)); got != tc.code {
				t.Fatalf("expected %v, got %v", tc.code, got)
			}
		})
	}
}
