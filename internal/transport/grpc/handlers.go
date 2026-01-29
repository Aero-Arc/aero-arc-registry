package grpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) RegisterRelay(ctx context.Context, req *registryv1.RegisterRelayRequest) (*registryv1.RegisterRelayResponse, error) {
	start := time.Now()
	defer func() {
		slog.LogAttrs(ctx, slog.LevelInfo, "request completed",
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
			slog.String("method", "RegisterRelay"),
		)
	}()

	if req.Relay == nil {
		return nil, status.Error(codes.InvalidArgument, "Relay is required")
	}

	if req.Relay.RelayId == "" {
		return nil, status.Error(codes.InvalidArgument, "RelayId is required")
	}

	if req.Relay.Address == "" {
		return nil, status.Error(codes.InvalidArgument, "Address is required")
	}

	slog.LogAttrs(ctx, slog.LevelInfo, "received request",
		slog.String("method", "RegisterRelay"),
		slog.String("relay_id", req.Relay.RelayId),
		slog.String("address", req.Relay.Address),
		slog.Int64("grpc_port", int64(req.Relay.GrpcPort)),
	)

	relay := registry.Relay{
		ID:       req.Relay.RelayId,
		Address:  req.Relay.Address,
		GRPCPort: req.Relay.GrpcPort,
	}

	if err := s.registry.RegisterRelay(ctx, relay); err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "relay registration failed",
			slog.String("error", err.Error()))

		return nil, toStatusError(err)
	}

	return &registryv1.RegisterRelayResponse{}, nil
}

func (s *Server) HeartbeatRelay(ctx context.Context, req *registryv1.HeartbeatRelayRequest) (*registryv1.HeartbeatRelayResponse, error) {
	start := time.Now()
	defer func() {
		slog.LogAttrs(ctx, slog.LevelDebug, "request completed",
			slog.String("method", "HeartbeatRelay"),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
		)
	}()

	if req.RelayId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "RelayId is required")
	}

	if req.TimestampUnixMs == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "TimestampUnixMs is required")
	}

	slog.LogAttrs(ctx, slog.LevelDebug, "received request",
		slog.String("method", "HeartbeatRelay"),
		slog.String("relay_id", req.RelayId),
		slog.Time("ts", start),
	)

	resp := &registryv1.HeartbeatRelayResponse{}

	if err := s.registry.HeartbeatRelay(ctx, req.RelayId); err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "failed to track relay heartbeat",
			slog.String("error", err.Error()),
			slog.String("relay_id", req.RelayId),
		)

		return nil, toStatusError(err)
	}

	return resp, nil
}

func (s *Server) ListRelays(ctx context.Context, req *registryv1.ListRelaysRequest) (*registryv1.ListRelaysResponse, error) {
	start := time.Now()
	defer func() {
		slog.LogAttrs(ctx, slog.LevelInfo, "request completed",
			slog.String("method", "ListRelays"),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
		)
	}()

	slog.LogAttrs(ctx, slog.LevelInfo, "received request",
		slog.String("method", "ListRelays"),
	)

	resp := &registryv1.ListRelaysResponse{}

	relays, err := s.registry.ListRelays(ctx)
	if err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "failed to list relays",
			slog.String("error", err.Error()),
		)
		return nil, toStatusError(err)
	}

	resp.Relays = make([]*registryv1.Relay, len(relays))
	for i, relay := range relays {
		resp.Relays[i] = &registryv1.Relay{
			Address:             relay.Address,
			RelayId:             relay.ID,
			GrpcPort:            relay.GRPCPort,
			LastHeartbeatUnixMs: relay.LastSeen.UnixMilli(),
		}
	}

	return resp, nil
}

func (s *Server) RegisterAgent(ctx context.Context, req *registryv1.RegisterAgentRequest) (*registryv1.RegisterAgentResponse, error) {
	start := time.Now()
	defer func() {
		slog.LogAttrs(ctx, slog.LevelInfo, "request completed",
			slog.String("method", "RegisterAgent"),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
		)
	}()

	if req.RelayId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "RelayId is required")
	}

	if req.Agent == nil {
		return nil, status.Errorf(codes.InvalidArgument, "Agent is required")
	}

	if req.Agent.AgentId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "AgentId is required")
	}

	slog.LogAttrs(ctx, slog.LevelInfo, "received request",
		slog.String("method", "RegisterAgent"),
		slog.String("agent_id", req.Agent.AgentId),
		slog.String("relay_id", req.RelayId),
	)

	agent := registry.Agent{
		ID: req.Agent.AgentId,
	}

	if err := s.registry.RegisterAgent(ctx, agent, req.RelayId); err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "agent registration failed",
			slog.String("error", err.Error()),
			slog.String("agent_id", req.Agent.AgentId),
			slog.String("relay_id", req.RelayId),
		)
		return nil, toStatusError(err)
	}

	return &registryv1.RegisterAgentResponse{}, nil
}

func (s *Server) HeartbeatAgent(ctx context.Context, req *registryv1.HeartbeatAgentRequest) (*registryv1.HeartbeatAgentResponse, error) {
	start := time.Now()
	defer func() {
		slog.LogAttrs(ctx, slog.LevelDebug, "request completed",
			slog.String("method", "HeartbeatAgent"),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
		)
	}()

	if req.AgentId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "AgentId is required")
	}

	slog.LogAttrs(ctx, slog.LevelDebug, "received request",
		slog.String("method", "HeartbeatAgent"),
		slog.String("agent_id", req.AgentId),
	)

	if err := s.registry.HeartbeatAgent(ctx, req.AgentId); err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "failed to track agent heartbeat",
			slog.String("error", err.Error()),
			slog.String("agent_id", req.AgentId),
		)
		return nil, toStatusError(err)
	}

	return &registryv1.HeartbeatAgentResponse{}, nil
}

func (s *Server) GetAgentPlacement(ctx context.Context, req *registryv1.GetAgentPlacementRequest) (*registryv1.GetAgentPlacementResponse, error) {
	start := time.Now()
	defer func() {
		slog.LogAttrs(ctx, slog.LevelInfo, "request completed",
			slog.String("method", "GetAgentPlacement"),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
		)
	}()

	if req.AgentId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "AgentId is required")
	}

	slog.LogAttrs(ctx, slog.LevelInfo, "received request",
		slog.String("method", "GetAgentPlacement"),
		slog.String("agent_id", req.AgentId),
	)

	resp := &registryv1.GetAgentPlacementResponse{}

	placement, err := s.registry.GetAgentPlacement(ctx, req.AgentId)
	if err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "failed to fetch agent placement",
			slog.String("error", err.Error()),
			slog.String("agent_id", req.AgentId),
		)
		return nil, toStatusError(err)
	}

	resp.Placement = &registryv1.AgentPlacement{
		AgentId:           placement.AgentID,
		RelayId:           placement.RelayID,
		LastUpdatedUnixMs: placement.UpdatedAt.UnixMilli(),
	}
	return resp, nil
}

func (s *Server) ListAgents(ctx context.Context, req *registryv1.ListAgentsRequest) (*registryv1.ListAgentsResponse, error) {
	start := time.Now()
	defer func() {
		slog.LogAttrs(ctx, slog.LevelInfo, "request completed",
			slog.String("method", "ListAgents"),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
		)
	}()

	slog.LogAttrs(ctx, slog.LevelInfo, "received request",
		slog.String("method", "ListAgents"),
	)

	resp := &registryv1.ListAgentsResponse{}
	agents, err := s.registry.ListAgents(ctx)
	if err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "failed to list agents",
			slog.String("error", err.Error()),
		)
		return nil, toStatusError(err)
	}

	resp.Agents = make([]*registryv1.Agent, len(agents))

	for i, agent := range agents {
		resp.Agents[i] = &registryv1.Agent{
			AgentId:             agent.ID,
			LastHeartbeatUnixMs: agent.LastHeartbeat.UnixMilli(),
		}
	}

	return resp, nil
}

func toStatusError(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, registry.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, registry.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, registry.ErrConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		slog.Error("unclassified error", "err", err.Error())
		return status.Error(codes.Internal, "internal error")
	}
}
