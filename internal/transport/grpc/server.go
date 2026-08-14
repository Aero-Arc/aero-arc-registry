// Package grpc implements the gRPC transport for the Aero Arc Registry.
// It adapts gRPC requests into registry domain operations and maps
// domain errors into gRPC status codes.
package grpc

import (
	"net"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type Server struct {
	registryv1.UnimplementedAeroRegistryServer
	registry   *registry.Registry
	grpcServer *gogrpc.Server
}

var _ registryv1.AeroRegistryServer = (*Server)(nil)

// New constructs grpc from the supplied configuration and dependencies.
//
// Parameters:
//   - reg: is the *registry.Registry value supplied to New.
//   - opts: provides the configuration values used to initialize or execute the operation.
//
// Returns:
//   - result: is the *Server value produced by New.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func New(reg *registry.Registry, opts ...gogrpc.ServerOption) (*Server, error) {
	s := &Server{
		registry: reg,
	}

	s.grpcServer = gogrpc.NewServer(opts...)
	registryv1.RegisterAeroRegistryServer(s.grpcServer, s)
	reflection.Register(s.grpcServer)

	return s, nil
}

// Serve serves Server until the server stops or returns an error.
//
// Parameters:
//   - lis: is the net.Listener value supplied to Serve.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Server) Serve(lis net.Listener) error {
	return s.grpcServer.Serve(lis)
}

// GracefulStop stops accepting new Registry RPCs and waits for active RPCs to finish.
func (s *Server) GracefulStop() {
	s.grpcServer.GracefulStop()
}
