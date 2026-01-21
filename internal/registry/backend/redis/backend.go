// Package redis provides a stub Redis backend implementation.
package redis

import (
	"context"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

type Backend struct {
	cfg *registry.RedisConfig
}

func New(cfg *registry.RedisConfig) (*Backend, error) {
	return &Backend{cfg: cfg}, nil
}

func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	return registry.ErrNotImplemented
}

func (b *Backend) HeartbeatRelay(ctx context.Context, relayID string, ts time.Time) error {
	return registry.ErrNotImplemented
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
