package grpc

import (
	"context"

	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) RegisterRelay(ctx context.Context, req *registryv1.RegisterRelayRequest) (*registryv1.RegisterRelayResponse, error) {
	return nil, status.Error(codes.Unimplemented, "RegisterRelay not implemented")
}

func (s *Server) HeartbeatRelay(ctx context.Context, req *registryv1.HeartbeatRelayRequest) (*registryv1.HeartbeatRelayResponse, error) {
	return nil, status.Error(codes.Unimplemented, "HeartbeatRelay not implemented")
}

func (s *Server) ListRelays(ctx context.Context, req *registryv1.ListRelaysRequest) (*registryv1.ListRelaysResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListRelays not implemented")
}

func (s *Server) RegisterAgent(ctx context.Context, req *registryv1.RegisterAgentRequest) (*registryv1.RegisterAgentResponse, error) {
	return nil, status.Error(codes.Unimplemented, "RegisterAgent not implemented")
}

func (s *Server) HeartbeatAgent(ctx context.Context, req *registryv1.HeartbeatAgentRequest) (*registryv1.HeartbeatAgentResponse, error) {
	return nil, status.Error(codes.Unimplemented, "HeartbeatAgent not implemented")
}

func (s *Server) GetAgentPlacement(ctx context.Context, req *registryv1.GetAgentPlacementRequest) (*registryv1.GetAgentPlacementResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetAgentPlacement not implemented")
}
