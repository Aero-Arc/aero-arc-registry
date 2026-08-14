// Package redis provides a Redis-backed registry implementation.
package redis

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	redisclient "github.com/redis/go-redis/v9"
)

const defaultNamespace = "{aero-arc-registry}:v1:"

var (
	errRelayNotRegistered = fmt.Errorf("relay not registered: %w", registry.ErrNotFound)
	errAgentNotRegistered = fmt.Errorf("agent not registered: %w", registry.ErrNotFound)
)

// Backend persists current registry state in Redis. Redis TIME and native key
// expirations are the liveness authority; the registry's local-clock TTL
// sweeper is disabled for this backend. Secondary indexes are advisory; reads
// ignore and opportunistically remove index members whose entity hash has
// expired.
type Backend struct {
	client    *redisclient.Client
	ttl       registry.TTLConfig
	namespace string

	closeOnce sync.Once
	closeErr  error
}

var _ registry.TTLManagedBackend = (*Backend)(nil)

// New constructs a Redis backend. The client establishes its network
// connection lazily on the first operation.
func New(cfg *registry.RedisConfig, ttl registry.TTLConfig) (*Backend, error) {
	if cfg == nil {
		return nil, registry.ErrRedisConfigNil
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("redis config invalid: %w", err)
	}
	if err := ttl.Validate(); err != nil {
		return nil, fmt.Errorf("redis ttl config invalid: %w", err)
	}
	if ttl.Agent < time.Millisecond {
		return nil, fmt.Errorf("redis agent ttl must be at least %s (got %s)", time.Millisecond, ttl.Agent)
	}
	if ttl.Relay < time.Millisecond {
		return nil, fmt.Errorf("redis relay ttl must be at least %s (got %s)", time.Millisecond, ttl.Relay)
	}

	client := redisclient.NewClient(&redisclient.Options{
		Addr:     net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port)),
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	return newBackend(client, ttl, defaultNamespace), nil
}

func newBackend(client *redisclient.Client, ttl registry.TTLConfig, namespace string) *Backend {
	return &Backend{client: client, ttl: ttl, namespace: namespace}
}

// ManagesTTL reports that Redis atomically enforces entity expiry using Redis
// server time. The service-level sweeper must not compare these timestamps to
// its local clock.
func (b *Backend) ManagesTTL() bool { return true }

// Close closes the Redis client once. Subsequent calls return the first close
// result, making backend shutdown idempotent.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - error: reports prior context cancellation or the Redis client's close failure.
func (b *Backend) Close(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.closeOnce.Do(func() { b.closeErr = b.client.Close() })
	return b.closeErr
}
