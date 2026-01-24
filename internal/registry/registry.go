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

type Registry struct {
	cfg     *Config
	backend Backend
}

func New(cfg *Config, backend Backend) (*Registry, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	aeroRegistry := &Registry{
		cfg:     cfg,
		backend: backend,
	}

	return aeroRegistry, nil
}
