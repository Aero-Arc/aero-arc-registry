// Package grpc implements the gRPC transport for the Aero Arc Registry.
// It adapts gRPC requests into registry domain operations and maps
// domain errors into gRPC status codes.
package grpc

import (
	"net"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	gogrpc "google.golang.org/grpc"
)

type Server struct {
	registryv1.UnimplementedAeroRegistryServer
	registry   *registry.Registry
	grpcServer *gogrpc.Server
}

var _ registryv1.AeroRegistryServer = (*Server)(nil)

func New(reg *registry.Registry, opts ...gogrpc.ServerOption) (*Server, error) {
	s := &Server{
		registry: reg,
	}

	s.grpcServer = gogrpc.NewServer(opts...)
	registryv1.RegisterAeroRegistryServer(s.grpcServer, s)

	return s, nil
}

func (s *Server) Serve(lis net.Listener) error {
	return s.grpcServer.Serve(lis)
}

func (s *Server) GracefulStop() {
	s.grpcServer.GracefulStop()
}
