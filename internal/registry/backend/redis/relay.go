package redis

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	redisclient "github.com/redis/go-redis/v9"
)

// RegisterRelay registers the supplied Backend identity or handler.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relay: is the registry.Relay value supplied to RegisterRelay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	if relay.ID == "" {
		return fmt.Errorf("relay id is empty: %w", registry.ErrInvalid)
	}
	if relay.Address == "" {
		return fmt.Errorf("relay address is empty: %w", registry.ErrInvalid)
	}
	if relay.GRPCPort <= 0 || relay.GRPCPort > 65535 {
		return fmt.Errorf("relay grpc port %d is outside 1-65535: %w", relay.GRPCPort, registry.ErrInvalid)
	}

	_, err := registerRelayScript.Run(ctx, b.client,
		[]string{b.relayKey(relay.ID), b.relaysKey(), b.relayAgentsKey(relay.ID), b.relayIncarnationKey()},
		relay.ID, relay.Address, relay.GRPCPort, b.ttl.Relay.Milliseconds(),
	).Result()
	if err != nil {
		return fmt.Errorf("register relay %q: %w", relay.ID, err)
	}
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
	result, err := heartbeatRelayScript.Run(ctx, b.client,
		[]string{b.relayKey(relayID), b.relaysKey(), b.relayAgentsKey(relayID)},
		relayID, b.ttl.Relay.Milliseconds(),
	).Int64()
	if err != nil {
		return fmt.Errorf("heartbeat relay %q: %w", relayID, err)
	}
	if result == 0 {
		return errRelayNotRegistered
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
	ids, err := b.activeIndexIDs(ctx, b.relaysKey())
	if err != nil {
		return nil, fmt.Errorf("list relay index: %w", err)
	}
	if len(ids) == 0 {
		return []registry.Relay{}, nil
	}

	pipe := b.client.Pipeline()
	commands := make([]*redisclient.Cmd, len(ids))
	for i, id := range ids {
		commands[i] = readLiveRelayScript.Eval(ctx, pipe,
			[]string{b.relayKey(id), b.relaysKey(), b.relayAgentsKey(id)}, id,
		)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("read relays: %w", err)
	}

	relays := make([]registry.Relay, 0, len(ids))
	for i, command := range commands {
		fields, err := command.StringSlice()
		if errors.Is(err, redisclient.Nil) || (err == nil && len(fields) == 0) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read relay %q: %w", ids[i], err)
		}
		if len(fields) != 5 {
			return nil, fmt.Errorf("read relay %q: corrupt redis response", ids[i])
		}
		port, err := strconv.ParseInt(fields[2], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("decode relay %q grpc port: %w", ids[i], err)
		}
		lastSeen, err := decodeUnixMilli(fields[4])
		if err != nil {
			return nil, fmt.Errorf("decode relay %q last seen: %w", ids[i], err)
		}
		relays = append(relays, registry.Relay{
			ID: fields[0], Address: fields[1], GRPCPort: int32(port), LastSeen: lastSeen,
		})
	}

	sort.Slice(relays, func(i, j int) bool { return relays[i].ID < relays[j].ID })
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
	result, err := removeRelayScript.Run(ctx, b.client,
		[]string{b.relayKey(relayID), b.relaysKey(), b.relayAgentsKey(relayID)}, relayID,
	).Int64()
	if err != nil {
		return fmt.Errorf("remove relay %q: %w", relayID, err)
	}
	if result == 0 {
		return errRelayNotRegistered
	}
	return nil
}
