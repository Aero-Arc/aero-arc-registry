// Package registry implements the Aero Arc Registry control plane.
//
// The registry is responsible for tracking the liveness, identity, and
// placement of Aero Arc relays and agents in a distributed system. It acts
// as a coordination layer between stateless relay instances and higher-level
// control plane components such as APIs, operator dashboards, and fleet-wide
// management services.
//
// The registry is designed to be backend-agnostic. It defines a stable,
// backend-independent contract while allowing multiple implementations
// (e.g. in-memory, Redis, etcd, Consul) to be plugged in via configuration.
// This enables Aero Arc to integrate cleanly with existing infrastructure
// and service discovery systems without coupling core logic to a specific
// datastore or coordination mechanism.
//
// Liveness semantics such as heartbeats and time-to-live (TTL) enforcement
// are implemented at the registry layer, ensuring consistent behavior across
// all backend implementations.
//
// The registry exposes its functionality over gRPC and is intended to be
// deployed as a standalone, horizontally scalable control plane service.
package registry

import (
	"context"
	"errors"
	"time"
)

type Registry struct {
	cfg     *Config
	RunFunc func(ctx context.Context, shutdownTimeout time.Duration) error
}

func New(cfg *Config) (*Registry, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	aeroRegistry := &Registry{
		cfg: cfg,
	}

	aeroRegistry.RunFunc = aeroRegistry.Run

	return aeroRegistry, nil
}

func (r *Registry) Run(ctx context.Context, shutdownTimeout time.Duration) error {
	runErrCh := make(chan error, 1)

	go func() {
		// TODO: replace with gRPC server startup and Serve.
		<-ctx.Done()
		runErrCh <- ctx.Err()
	}()

	shouldShutdown := false
	select {
	case err := <-runErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if ctx.Err() == nil {
			return nil
		}
		shouldShutdown = true
	case <-ctx.Done():
		shouldShutdown = true
	}

	if !shouldShutdown {
		return nil
	}

	shutdownCtx := context.Background()
	var cancel context.CancelFunc
	if shutdownTimeout > 0 {
		shutdownCtx, cancel = context.WithTimeout(context.Background(), shutdownTimeout)
	} else {
		shutdownCtx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	shutdownErrCh := make(chan error, 1)
	go func() {
		// TODO: replace with gRPC GracefulStop and backend cleanup.
		shutdownErrCh <- nil
	}()

	select {
	case err := <-shutdownErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	case <-shutdownCtx.Done():
		return shutdownCtx.Err()
	}
}
