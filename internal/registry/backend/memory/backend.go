// Package memory provides a stub in-memory backend implementation.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

type Backend struct {
	cfg    *registry.MemoryConfig
	relays map[string]*relayEntry
	mu     sync.RWMutex
}

type relayEntry struct {
	mu    sync.Mutex
	relay *registry.Relay
}

func New(cfg *registry.MemoryConfig) (*Backend, error) {
	return &Backend{
		cfg:    cfg,
		relays: make(map[string]*relayEntry),
	}, nil
}

func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.mu.RLock()
	entry, exists := b.relays[relay.ID]
	b.mu.RUnlock()

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

	b.mu.Lock()
	if existing, ok := b.relays[relay.ID]; ok {
		b.mu.Unlock()

		existing.mu.Lock()
		defer existing.mu.Unlock()
		existing.relay.LastSeen = time.Now()

		return nil
	}

	b.relays[relay.ID] = newEntry
	b.mu.Unlock()

	return nil
}

func (b *Backend) HeartbeatRelay(ctx context.Context, relayID string, ts time.Time) error {
	b.mu.RLock()
	relayEntry, exists := b.relays[relayID]
	b.mu.RUnlock()

	if !exists {
		return ErrRelayNotRegistered
	}
	relayEntry.mu.Lock()
	relayEntry.relay.LastSeen = ts
	relayEntry.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return nil
}

func (b *Backend) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	return nil, registry.ErrNotImplemented
}

func (b *Backend) RemoveRelay(ctx context.Context, relayID string) error {
	return registry.ErrNotImplemented
}

func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	return registry.ErrNotImplemented
}

func (b *Backend) HeartbeatAgent(ctx context.Context, agentID string, ts time.Time) error {
	return registry.ErrNotImplemented
}

func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	return nil, registry.ErrNotImplemented
}

func (b *Backend) Close(ctx context.Context) error {
	return nil
}
