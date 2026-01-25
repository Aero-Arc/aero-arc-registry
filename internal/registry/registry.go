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
	"time"
)

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

func (r *Registry) RegisterRelay(ctx context.Context, relay Relay) error {
	return r.backend.RegisterRelay(ctx, relay)
}

func (r *Registry) HeartbeatRelay(ctx context.Context, relayID string, ts time.Time) error {
	return r.backend.HeartbeatRelay(ctx, relayID, ts)
}

func (r *Registry) ListRelays(ctx context.Context) ([]Relay, error) {
	return r.backend.ListRelays(ctx)
}

func (r *Registry) RemoveRelay(ctx context.Context, relayID string) error {
	return r.backend.RemoveRelay(ctx, relayID)
}

func (r *Registry) RegisterAgent(ctx context.Context, agent Agent, relayID string) error {
	return r.backend.RegisterAgent(ctx, agent, relayID)
}

func (r *Registry) HeartbeatAgent(ctx context.Context, agentID string, ts time.Time) error {
	return r.backend.HeartbeatAgent(ctx, agentID, ts)
}

func (r *Registry) GetAgentPlacement(ctx context.Context, agentID string) (*AgentPlacement, error) {
	return r.backend.GetAgentPlacement(ctx, agentID)
}

func (r *Registry) ListAgents(ctx context.Context) ([]Agent, error) {
	return r.backend.ListAgents(ctx)
}

func (r *Registry) Close(ctx context.Context) error {
	return r.backend.Close(ctx)
}
