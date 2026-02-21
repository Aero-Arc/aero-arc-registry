package grpc

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

func TestNewServeAndGracefulStop(t *testing.T) {
	t.Parallel()

	reg := newTransportTestRegistryForServer(t)
	s, err := New(reg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	listenErr := errors.New("listener failed")
	lis := &listenerStub{acceptErr: listenErr}
	err = s.Serve(lis)
	if !errors.Is(err, listenErr) {
		t.Fatalf("Serve() error = %v, want %v", err, listenErr)
	}

	s.GracefulStop()
}

func newTransportTestRegistryForServer(t *testing.T) *registry.Registry {
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

	reg, err := registry.New(cfg, &transportBackendStub{})
	if err != nil {
		t.Fatalf("registry.New() error = %v", err)
	}

	return reg
}

type listenerStub struct {
	acceptErr error
}

func (l *listenerStub) Accept() (net.Conn, error) {
	return nil, l.acceptErr
}

func (l *listenerStub) Close() error {
	return nil
}

func (l *listenerStub) Addr() net.Addr {
	return &addrStub{}
}

type addrStub struct{}

func (a *addrStub) Network() string {
	return "tcp"
}

func (a *addrStub) String() string {
	return "127.0.0.1:0"
}
