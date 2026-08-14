// Package memory provides a stub in-memory backend implementation.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

type Backend struct {
	cfg         *registry.MemoryConfig
	relays      map[string]*relayEntry
	agents      map[string]*agentEntry
	placements  map[string]*registry.AgentPlacement
	relayAgents map[string]map[string]*agentEntry

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

// New constructs memory from the supplied configuration and dependencies.
//
// Parameters:
//   - cfg: provides the configuration values used to initialize or execute the operation.
//
// Returns:
//   - result: is the *Backend value produced by New.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func New(cfg *registry.MemoryConfig) (*Backend, error) {
	return &Backend{
		cfg:         cfg,
		relays:      make(map[string]*relayEntry),
		agents:      make(map[string]*agentEntry),
		placements:  make(map[string]*registry.AgentPlacement),
		relayAgents: make(map[string]map[string]*agentEntry),
	}, nil
}

// RegisterRelay registers the supplied Backend identity or handler.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relay: is the registry.Relay value supplied to RegisterRelay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// HeartbeatRelay renews liveness for the supplied Backend identity without changing ownership.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListRelays returns Backend records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the []registry.Relay value produced by ListRelays.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
	sort.Slice(relays, func(i, j int) bool {
		return relays[i].ID < relays[j].ID
	})

	return relays, nil
}

// RemoveRelay removes the selected Backend records and associated live indexes.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
	for agentID := range b.relayAgents[relayID] {
		placement, placed := b.placements[agentID]
		if !placed || placement.RelayID != relayID {
			continue
		}
		delete(b.placements, agentID)
		delete(b.agents, agentID)
	}
	delete(b.relays, relayID)
	delete(b.relayAgents, relayID)

	return nil
}

// RegisterAgent registers the supplied Backend identity or handler.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agent: is the registry.Agent value supplied to RegisterAgent.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
		b.setPlacementLocked(agent.ID, relayID, entry, now)
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
		b.setPlacementLocked(agent.ID, relayID, existing, now)
		b.agentMu.Unlock()

		return nil
	}

	b.agents[agent.ID] = newEntry
	b.setPlacementLocked(agent.ID, relayID, newEntry, now)
	b.agentMu.Unlock()

	return nil
}

// HeartbeatAgent renews liveness for the supplied Backend identity without changing ownership.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentID: identifies the target agent.
//   - expectedRelayID: identifies the target expectedrelay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) HeartbeatAgent(ctx context.Context, agentID, expectedRelayID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Hold relay and placement ownership through renewal so a concurrent
	// RegisterAgent takeover cannot be overwritten or extended by the old relay.
	b.relayMu.RLock()
	defer b.relayMu.RUnlock()
	b.agentMu.Lock()
	defer b.agentMu.Unlock()

	if _, exists := b.relays[expectedRelayID]; !exists {
		return errRelayNotRegistered
	}
	entry, exists := b.agents[agentID]
	placement, placed := b.placements[agentID]
	if !exists || !placed || placement.RelayID != expectedRelayID {
		return errAgentNotRegistered
	}

	now := time.Now()
	entry.mu.Lock()
	entry.agent.LastHeartbeat = now
	entry.mu.Unlock()
	placement.UpdatedAt = now

	return nil
}

// GetAgentPlacement returns an Agent's live in-memory Relay placement.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentID: identifies the target agent.
//
// Returns:
//   - result: is the *registry.AgentPlacement value produced by GetAgentPlacement.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListAgents returns Backend records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the []registry.Agent value produced by ListAgents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})

	return agents, nil
}

// ListRelayAgents returns Backend records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - result: is the []*registry.Agent value produced by ListRelayAgents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) ListRelayAgents(ctx context.Context, relayID string) ([]*registry.Agent, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	b.relayMu.RLock()
	_, relayExists := b.relays[relayID]
	b.relayMu.RUnlock()
	if !relayExists {
		return nil, errRelayNotRegistered
	}

	b.agentMu.RLock()
	relayAgentEntries := b.relayAgents[relayID]
	entries := make([]*agentEntry, 0, len(relayAgentEntries))
	for _, entry := range relayAgentEntries {
		entries = append(entries, entry)
	}
	b.agentMu.RUnlock()

	agents := make([]*registry.Agent, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		agentCopy := *entry.agent
		entry.mu.Unlock()

		agents = append(agents, &agentCopy)
	}
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})

	return agents, nil
}

// RemoveAgents removes the selected Backend records and associated live indexes.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentIDs: identifies the target agent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) RemoveAgents(ctx context.Context, agentIDs []string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.agentMu.Lock()
	defer b.agentMu.Unlock()

	for _, agentID := range agentIDs {
		placement, hasPlacement := b.placements[agentID]
		if hasPlacement {
			if relayEntries, ok := b.relayAgents[placement.RelayID]; ok {
				delete(relayEntries, agentID)
				if len(relayEntries) == 0 {
					delete(b.relayAgents, placement.RelayID)
				}
			}
		}

		delete(b.placements, agentID)
		delete(b.agents, agentID)
	}

	return nil
}

func (b *Backend) setPlacementLocked(agentID, relayID string, entry *agentEntry, now time.Time) {
	if oldPlacement, ok := b.placements[agentID]; ok {
		if oldRelayEntries, ok := b.relayAgents[oldPlacement.RelayID]; ok {
			delete(oldRelayEntries, agentID)
			if len(oldRelayEntries) == 0 {
				delete(b.relayAgents, oldPlacement.RelayID)
			}
		}
	}

	b.placements[agentID] = &registry.AgentPlacement{
		AgentID:   agentID,
		RelayID:   relayID,
		UpdatedAt: now,
	}

	relayEntries, exists := b.relayAgents[relayID]
	if !exists {
		relayEntries = make(map[string]*agentEntry)
		b.relayAgents[relayID] = relayEntries
	}

	relayEntries[agentID] = entry
}

// Close releases resources owned by Backend and completes any required shutdown work.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) Close(ctx context.Context) error {
	return nil
}
