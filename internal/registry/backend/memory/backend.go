// Package memory provides a stub in-memory backend implementation.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

type Backend struct {
	cfg        *registry.MemoryConfig
	relays     map[string]*relayEntry
	agents     map[string]*agentEntry
	placements map[string]*registry.AgentPlacement

	// Lock order: relayMu -> agentMu -> entry.mu
	// - relays map guarded by relayMu
	// - agents/placements guarded by agentMu
	// - individual relay/agent fields guarded by entry.mu
	relayMu sync.RWMutex
	agentMu sync.RWMutex
}

type relayEntry struct {
	mu    sync.Mutex
	relay *registry.Relay
}

type agentEntry struct {
	mu    sync.Mutex
	agent *registry.Agent
}

func New(cfg *registry.MemoryConfig) (*Backend, error) {
	return &Backend{
		cfg:        cfg,
		relays:     make(map[string]*relayEntry),
		agents:     make(map[string]*agentEntry),
		placements: make(map[string]*registry.AgentPlacement),
	}, nil
}

func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.relayMu.RLock()
	entry, exists := b.relays[relay.ID]
	b.relayMu.RUnlock()

	if exists {
		entry.mu.Lock()
		defer entry.mu.Unlock()

		// Idempotent Update
		entry.relay.ID = relay.ID
		entry.relay.Address = relay.Address
		entry.relay.GRPCPort = relay.GRPCPort
		entry.relay.LastSeen = time.Now()

		return nil
	}

	newEntry := &relayEntry{
		relay: &registry.Relay{
			ID:       relay.ID,
			Address:  relay.Address,
			GRPCPort: relay.GRPCPort,
			LastSeen: time.Now(),
		},
	}

	b.relayMu.Lock()
	if existing, ok := b.relays[relay.ID]; ok {
		b.relayMu.Unlock()

		existing.mu.Lock()
		defer existing.mu.Unlock()
		existing.relay.LastSeen = time.Now()

		return nil
	}

	b.relays[relay.ID] = newEntry
	b.relayMu.Unlock()

	return nil
}

func (b *Backend) HeartbeatRelay(ctx context.Context, relayID string) error {
	b.relayMu.RLock()
	relayEntry, exists := b.relays[relayID]
	b.relayMu.RUnlock()

	if !exists {
		return errRelayNotRegistered
	}
	relayEntry.mu.Lock()
	relayEntry.relay.LastSeen = time.Now()
	relayEntry.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return nil
}

func (b *Backend) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	b.relayMu.RLock()
	entries := make([]*relayEntry, 0, len(b.relays))
	for _, entry := range b.relays {
		entries = append(entries, entry)
	}
	b.relayMu.RUnlock()

	relays := make([]registry.Relay, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		relay := *entry.relay
		entry.mu.Unlock()
		relays = append(relays, relay)
	}

	return relays, nil
}

func (b *Backend) RemoveRelay(ctx context.Context, relayID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.relayMu.Lock()
	b.agentMu.Lock()

	defer b.relayMu.Unlock()
	defer b.agentMu.Unlock()

	if _, exists := b.relays[relayID]; !exists {
		return errRelayNotRegistered
	}
	delete(b.relays, relayID)

	for agentID, placement := range b.placements {
		if placement.RelayID == relayID {
			delete(b.placements, agentID)
			delete(b.agents, agentID)
		}
	}

	return nil
}

func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.relayMu.RLock()
	_, relayExists := b.relays[relayID]
	b.relayMu.RUnlock()
	if !relayExists {
		return errRelayNotRegistered
	}

	b.agentMu.RLock()
	entry, exists := b.agents[agent.ID]
	b.agentMu.RUnlock()

	now := time.Now()
	if exists {
		entry.mu.Lock()
		entry.agent.LastHeartbeat = now
		entry.mu.Unlock()

		b.agentMu.Lock()
		b.placements[agent.ID] = &registry.AgentPlacement{
			AgentID:   agent.ID,
			RelayID:   relayID,
			UpdatedAt: now,
		}
		b.agentMu.Unlock()

		return nil
	}

	newEntry := &agentEntry{
		agent: &registry.Agent{
			ID:            agent.ID,
			LastHeartbeat: now,
		},
	}

	b.agentMu.Lock()
	if existing, ok := b.agents[agent.ID]; ok {
		b.agentMu.Unlock()

		existing.mu.Lock()
		existing.agent.LastHeartbeat = now
		existing.mu.Unlock()

		b.agentMu.Lock()
		b.placements[agent.ID] = &registry.AgentPlacement{
			AgentID:   agent.ID,
			RelayID:   relayID,
			UpdatedAt: now,
		}
		b.agentMu.Unlock()

		return nil
	}

	b.agents[agent.ID] = newEntry
	b.placements[agent.ID] = &registry.AgentPlacement{
		AgentID:   agent.ID,
		RelayID:   relayID,
		UpdatedAt: now,
	}
	b.agentMu.Unlock()

	return nil
}

func (b *Backend) HeartbeatAgent(ctx context.Context, agentID string) error {
	b.agentMu.RLock()
	entry, exists := b.agents[agentID]
	b.agentMu.RUnlock()
	if !exists {
		return errAgentNotRegistered
	}

	entry.mu.Lock()
	entry.agent.LastHeartbeat = time.Now()
	entry.mu.Unlock()

	b.agentMu.Lock()
	if placement, ok := b.placements[agentID]; ok {
		placement.UpdatedAt = time.Now()
	}
	b.agentMu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return nil
}

func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	b.agentMu.RLock()
	defer b.agentMu.RUnlock()

	placement, exists := b.placements[agentID]
	if !exists {
		return nil, errAgentNotRegistered
	}

	result := *placement

	return &result, nil
}

func (b *Backend) ListAgents(ctx context.Context) ([]registry.Agent, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	b.agentMu.RLock()
	entries := make([]*agentEntry, 0, len(b.agents))
	for _, agentEntry := range b.agents {
		entries = append(entries, agentEntry)
	}
	b.agentMu.RUnlock()

	agents := make([]registry.Agent, len(entries))
	for i, entry := range entries {
		entry.mu.Lock()
		agent := *entry.agent
		entry.mu.Unlock()

		agents[i] = agent
	}

	return agents, nil
}

func (b *Backend) Close(ctx context.Context) error {
	return nil
}
