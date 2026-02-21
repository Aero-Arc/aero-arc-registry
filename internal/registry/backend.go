package registry

import (
	"context"
	"time"
)

// Backend defines the persistence and coordination contract
// required by the registry control plane.
type Backend interface {
	// Relay lifecycle
	RegisterRelay(ctx context.Context, relay Relay) error
	HeartbeatRelay(ctx context.Context, relayID string) error
	ListRelays(ctx context.Context) ([]Relay, error)
	// TODO(registry-ttl): add indexed stale query APIs for scale:
	// ListStaleRelays(ctx context.Context, before time.Time) ([]Relay, error)

	// Agent lifecycle
	RegisterAgent(ctx context.Context, agent Agent, relayID string) error
	HeartbeatAgent(ctx context.Context, agentID string) error
	GetAgentPlacement(ctx context.Context, agentID string) (*AgentPlacement, error)
	ListAgents(ctx context.Context) ([]Agent, error)
	// TODO(registry-ttl): add indexed stale query + batch placement APIs:
	// ListStaleAgents(ctx context.Context, before time.Time) ([]Agent, error)
	// GetAgentPlacements(ctx context.Context, agentIDs []string) (map[string]*AgentPlacement, error)

	// Control Plane Helpers
	ListRelayAgents(ctx context.Context, relayID string) ([]*Agent, error)
	RemoveAgents(ctx context.Context, agentIDs []string) error
	RemoveRelay(ctx context.Context, relayID string) error

	// Shutdown
	Close(ctx context.Context) error
}

// Relay represents a relay instance registered with the registry.
type Relay struct {
	ID       string
	Address  string
	GRPCPort int32
	LastSeen time.Time
}

// Agent represents an agent (e.g. drone or edge process)
type Agent struct {
	ID            string
	LastHeartbeat time.Time
}

// AgentPlacement represents the association between an agent and a relay.
type AgentPlacement struct {
	AgentID   string
	RelayID   string
	UpdatedAt time.Time
}
