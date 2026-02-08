// Package etcd provides an etcd-shaped backend implementation.
package etcd

import (
	"context"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

type Backend struct {
	cfg *registry.EtcdConfig

	mu         sync.RWMutex
	relays     map[string]registry.Relay
	agents     map[string]registry.Agent
	placements map[string]registry.AgentPlacement
}

func New(cfg *registry.EtcdConfig) (*Backend, error) {
	return &Backend{
		cfg:        cfg,
		relays:     make(map[string]registry.Relay),
		agents:     make(map[string]registry.Agent),
		placements: make(map[string]registry.AgentPlacement),
	}, nil
}

func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relay.ID == "" {
		return registry.ErrRelayIDEmpty
	}
	if relay.LastSeen.IsZero() {
		relay.LastSeen = time.Now()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.relays[relay.ID] = relay
	return nil
}

func (b *Backend) HeartbeatRelay(ctx context.Context, relayID string, ts time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relayID == "" {
		return registry.ErrRelayIDEmpty
	}
	if ts.IsZero() {
		ts = time.Now()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	relay, ok := b.relays[relayID]
	if !ok {
		return registry.ErrRelayNotRegistered
	}
	relay.LastSeen = ts
	b.relays[relayID] = relay
	return nil
}

func (b *Backend) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	relays := make([]registry.Relay, 0, len(b.relays))
	for _, relay := range b.relays {
		relays = append(relays, relay)
	}
	return relays, nil
}

func (b *Backend) RemoveRelay(ctx context.Context, relayID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relayID == "" {
		return registry.ErrRelayIDEmpty
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.relays[relayID]; !ok {
		return registry.ErrRelayNotRegistered
	}
	delete(b.relays, relayID)
	return nil
}

func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relayID == "" {
		return registry.ErrRelayIDEmpty
	}
	if agent.ID == "" {
		return registry.ErrAgentIDEmpty
	}
	if agent.LastHeartbeat.IsZero() {
		agent.LastHeartbeat = time.Now()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.relays[relayID]; !ok {
		return registry.ErrRelayNotRegistered
	}
	b.agents[agent.ID] = agent
	b.placements[agent.ID] = registry.AgentPlacement{
		AgentID:   agent.ID,
		RelayID:   relayID,
		UpdatedAt: agent.LastHeartbeat,
	}
	return nil
}

func (b *Backend) HeartbeatAgent(ctx context.Context, agentID string, ts time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if agentID == "" {
		return registry.ErrAgentIDEmpty
	}
	if ts.IsZero() {
		ts = time.Now()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	agent, ok := b.agents[agentID]
	if !ok {
		return registry.ErrAgentNotRegistered
	}
	placement, ok := b.placements[agentID]
	if !ok {
		return registry.ErrAgentNotRegistered
	}

	agent.LastHeartbeat = ts
	placement.UpdatedAt = ts
	b.agents[agentID] = agent
	b.placements[agentID] = placement
	return nil
}

func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if agentID == "" {
		return nil, registry.ErrAgentIDEmpty
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	placement, ok := b.placements[agentID]
	if !ok {
		return nil, registry.ErrAgentNotRegistered
	}
	out := placement
	return &out, nil
}

func (b *Backend) Close(ctx context.Context) error {
	return nil
}
