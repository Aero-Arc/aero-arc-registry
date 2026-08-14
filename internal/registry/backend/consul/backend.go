// Package is not implemented by the consul backend stub.
package consul

import (
	"context"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

type Backend struct {
	cfg *registry.ConsulConfig
}

// New constructs the consul backend stub without opening a connection.
//
// Parameters:
//   - cfg: provides the configuration values used to initialize or execute the operation.
//
// Returns:
//   - result: is the *Backend value produced by New.
//   - error: is always nil for the backend stub.
func New(cfg *registry.ConsulConfig) (*Backend, error) {
	return &Backend{cfg: cfg}, nil
}

// RegisterRelay is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relay: is the registry.Relay value supplied to RegisterRelay.
//
// Returns:
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	return registry.ErrNotImplemented
}

// HeartbeatRelay is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) HeartbeatRelay(ctx context.Context, relayID string) error {
	return registry.ErrNotImplemented
}

// ListRelays is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the []registry.Relay value produced by ListRelays.
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	return nil, registry.ErrNotImplemented
}

// RemoveRelay is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) RemoveRelay(ctx context.Context, relayID string) error {
	return registry.ErrNotImplemented
}

// RegisterAgent is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agent: is the registry.Agent value supplied to RegisterAgent.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	return registry.ErrNotImplemented
}

// HeartbeatAgent is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentID: identifies the target agent.
//   - expectedRelayID: identifies the target expectedrelay.
//
// Returns:
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) HeartbeatAgent(ctx context.Context, agentID, expectedRelayID string) error {
	return registry.ErrNotImplemented
}

// GetAgentPlacement is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentID: identifies the target agent.
//
// Returns:
//   - result: is the *registry.AgentPlacement value produced by GetAgentPlacement.
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	return nil, registry.ErrNotImplemented
}

// ListAgents is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the []registry.Agent value produced by ListAgents.
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) ListAgents(ctx context.Context) ([]registry.Agent, error) {
	return nil, registry.ErrNotImplemented
}

// ListRelayAgents is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - result: is the []*registry.Agent value produced by ListRelayAgents.
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) ListRelayAgents(ctx context.Context, relayID string) ([]*registry.Agent, error) {
	return nil, registry.ErrNotImplemented
}

// RemoveAgents is not implemented by the consul backend stub.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentIDs: identifies the target agent.
//
// Returns:
//   - error: is always registry.ErrNotImplemented.
func (b *Backend) RemoveAgents(ctx context.Context, agentIDs []string) error {
	return registry.ErrNotImplemented
}

// Close is a no-op because the consul backend stub owns no resources.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - error: is always nil for the backend stub.
func (b *Backend) Close(ctx context.Context) error {
	return nil
}
