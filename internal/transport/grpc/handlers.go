package grpc

import (
	"context"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
)

func (s *Server) RegisterRelay(ctx context.Context, req *registryv1.RegisterRelayRequest) (*registryv1.RegisterRelayResponse, error) {
	relay := registry.Relay{
		ID:       req.Relay.RelayId,
		Address:  req.Relay.Address,
		GRPCPort: req.Relay.GrpcPort,
		LastSeen: time.Now(),
	}

	return &registryv1.RegisterRelayResponse{}, s.registry.RegisterRelay(ctx, relay)
}

func (s *Server) HeartbeatRelay(ctx context.Context, req *registryv1.HeartbeatRelayRequest) (*registryv1.HeartbeatRelayResponse, error) {
	return &registryv1.HeartbeatRelayResponse{}, s.registry.HeartbeatRelay(ctx, req.RelayId, time.Now())
}

func (s *Server) ListRelays(ctx context.Context, req *registryv1.ListRelaysRequest) (*registryv1.ListRelaysResponse, error) {
	resp := &registryv1.ListRelaysResponse{}

	relays, err := s.registry.ListRelays(ctx)
	if err != nil {
		return resp, err
	}

	resp.Relays = []*registryv1.Relay{}
	for _, relay := range relays {
		resp.Relays = append(resp.Relays, &registryv1.Relay{
			Address:             relay.Address,
			RelayId:             relay.ID,
			GrpcPort:            relay.GRPCPort,
			LastHeartbeatUnixMs: relay.LastSeen.UnixMilli(),
		})
	}

	return resp, nil
}

func (s *Server) RegisterAgent(ctx context.Context, req *registryv1.RegisterAgentRequest) (*registryv1.RegisterAgentResponse, error) {
	agent := registry.Agent{
		ID:            req.Agent.AgentId,
		LastHeartbeat: time.Now(),
	}

	return &registryv1.RegisterAgentResponse{}, s.registry.RegisterAgent(ctx, agent, req.RelayId)
}

func (s *Server) HeartbeatAgent(ctx context.Context, req *registryv1.HeartbeatAgentRequest) (*registryv1.HeartbeatAgentResponse, error) {
	return &registryv1.HeartbeatAgentResponse{}, s.registry.HeartbeatAgent(ctx, req.AgentId, time.Now())
}

func (s *Server) GetAgentPlacement(ctx context.Context, req *registryv1.GetAgentPlacementRequest) (*registryv1.GetAgentPlacementResponse, error) {
	resp := &registryv1.GetAgentPlacementResponse{}

	placement, err := s.registry.GetAgentPlacement(ctx, req.AgentId)
	if err != nil {
		return resp, err
	}

	resp.Placement = &registryv1.AgentPlacement{
		AgentId:           placement.AgentID,
		RelayId:           placement.RelayID,
		LastUpdatedUnixMs: placement.UpdatedAt.UnixMilli(),
	}
	return resp, nil
}

func (s *Server) ListAgents(ctx context.Context, req *registryv1.ListAgentsRequest) (*registryv1.ListAgentsResponse, error) {
	resp := &registryv1.ListAgentsResponse{}
	agents, err := s.registry.ListAgents(ctx)
	if err != nil {
		return resp, err
	}

	resp.Agents = []*registryv1.Agent{}

	for _, agent := range agents {
		respAgent := &registryv1.Agent{
			AgentId:             agent.ID,
			LastHeartbeatUnixMs: agent.LastHeartbeat.UnixMilli(),
		}

		resp.Agents = append(resp.Agents, respAgent)
	}

	return resp, nil
}
