// Package redis provides a Redis-backed registry implementation.
package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	"github.com/redis/go-redis/v9"
)

type Backend struct {
	cfg *registry.RedisConfig
	ttl registry.TTLConfig
	rdb *redis.Client
}

const (
	relaysIndexKey  = "aeroarc:registry:relays"
	agentsIndexKey  = "aeroarc:registry:agents"
	relayKeyPrefix  = "aeroarc:registry:relay:"
	agentKeyPrefix  = "aeroarc:registry:agent:"
	placementPrefix = "aeroarc:registry:placement:"
)

func New(cfg *registry.RedisConfig, ttl registry.TTLConfig) (*Backend, error) {
	if cfg == nil {
		return nil, registry.ErrRedisConfigNil
	}

	addr := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)
	opts := &redis.Options{
		Addr:     addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	}

	rdb := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, err
	}

	return &Backend{cfg: cfg, ttl: ttl, rdb: rdb}, nil
}

func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	if b.rdb == nil {
		return ErrClientNotInitialized
	}

	now := time.Now().UTC()
	key := relayKey(relay.ID)
	pipe := b.rdb.Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"ID":             relay.ID,
		"Address":        relay.Address,
		"GRPCPort":       relay.GRPCPort,
		"LastSeenUnixMs": now.UnixMilli(),
	})
	pipe.PExpire(ctx, key, b.ttl.Relay)
	pipe.SAdd(ctx, relaysIndexKey, relay.ID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) HeartbeatRelay(ctx context.Context, relayID string, ts time.Time) error {
	if b.rdb == nil {
		return ErrClientNotInitialized
	}

	if ts.IsZero() {
		return registry.ErrHeartbeatTimestampMissing
	}

	key := relayKey(relayID)
	pipe := b.rdb.Pipeline()
	pipe.HSet(ctx, key, "LastSeenUnixMs", ts.UTC().UnixMilli())
	pipe.PExpire(ctx, key, b.ttl.Relay)
	pipe.SAdd(ctx, relaysIndexKey, relayID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	if b.rdb == nil {
		return nil, ErrClientNotInitialized
	}

	ids, err := b.rdb.SMembers(ctx, relaysIndexKey).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []registry.Relay{}, nil
	}

	pipe := b.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, 0, len(ids))
	for _, id := range ids {
		cmds = append(cmds, pipe.HGetAll(ctx, relayKey(id)))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	now := time.Now().UTC()
	staleIDs := make([]string, 0)
	relays := make([]registry.Relay, 0, len(ids))
	for i, cmd := range cmds {
		id := ids[i]
		data, err := cmd.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if len(data) == 0 {
			staleIDs = append(staleIDs, id)
			continue
		}

		relay, err := parseRelay(data)
		if err != nil {
			return nil, err
		}
		if relay.LastSeen.IsZero() || now.Sub(relay.LastSeen) > b.ttl.Relay {
			staleIDs = append(staleIDs, relay.ID)
			continue
		}
		relays = append(relays, relay)
	}

	if len(staleIDs) > 0 {
		staleArgs := make([]any, 0, len(staleIDs))
		for _, id := range staleIDs {
			staleArgs = append(staleArgs, id)
		}
		pipe := b.rdb.Pipeline()
		for _, id := range staleIDs {
			pipe.Del(ctx, relayKey(id))
		}
		pipe.SRem(ctx, relaysIndexKey, staleArgs...)
		_, _ = pipe.Exec(ctx)
	}

	return relays, nil
}

func (b *Backend) RemoveRelay(ctx context.Context, relayID string) error {
	if b.rdb == nil {
		return ErrClientNotInitialized
	}

	pipe := b.rdb.Pipeline()
	pipe.Del(ctx, relayKey(relayID))
	pipe.SRem(ctx, relaysIndexKey, relayID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	if b.rdb == nil {
		return ErrClientNotInitialized
	}

	now := time.Now().UTC()
	placement := registry.AgentPlacement{
		AgentID:   agent.ID,
		RelayID:   relayID,
		UpdatedAt: now,
	}

	pipe := b.rdb.Pipeline()
	pipe.HSet(ctx, agentKey(agent.ID), map[string]any{
		"ID":                  agent.ID,
		"LastHeartbeatUnixMs": now.UnixMilli(),
	})
	pipe.PExpire(ctx, agentKey(agent.ID), b.ttl.Agent)
	pipe.HSet(ctx, placementKey(agent.ID), map[string]any{
		"AgentID":         placement.AgentID,
		"RelayID":         placement.RelayID,
		"UpdatedAtUnixMs": placement.UpdatedAt.UnixMilli(),
	})
	pipe.PExpire(ctx, placementKey(agent.ID), b.ttl.Agent)
	pipe.SAdd(ctx, agentsIndexKey, agent.ID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) HeartbeatAgent(ctx context.Context, agentID string, ts time.Time) error {
	if b.rdb == nil {
		return ErrClientNotInitialized
	}

	if ts.IsZero() {
		return registry.ErrHeartbeatTimestampMissing
	}

	placementData, err := b.rdb.HGetAll(ctx, placementKey(agentID)).Result()
	if err != nil {
		return err
	}
	if len(placementData) == 0 {
		return registry.ErrAgentNotPlaced
	}

	placementAgentID := placementData["AgentID"]
	placementRelayID := placementData["RelayID"]
	if placementAgentID == "" || placementRelayID == "" {
		return registry.ErrAgentNotPlaced
	}

	now := time.Now().UTC()
	pipe := b.rdb.Pipeline()
	pipe.HSet(ctx, agentKey(agentID), "LastHeartbeatUnixMs", ts.UTC().UnixMilli())
	pipe.PExpire(ctx, agentKey(agentID), b.ttl.Agent)
	pipe.HSet(ctx, placementKey(agentID), map[string]any{
		"AgentID":         placementAgentID,
		"RelayID":         placementRelayID,
		"UpdatedAtUnixMs": now.UnixMilli(),
	})
	pipe.PExpire(ctx, placementKey(agentID), b.ttl.Agent)
	pipe.SAdd(ctx, agentsIndexKey, agentID)
	_, err = pipe.Exec(ctx)
	return err
}

func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	if b.rdb == nil {
		return nil, ErrClientNotInitialized
	}

	data, err := b.rdb.HGetAll(ctx, placementKey(agentID)).Result()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, registry.ErrAgentNotPlaced
	}

	placement, err := parsePlacement(data)
	if err != nil {
		return nil, err
	}
	if placement.UpdatedAt.IsZero() || time.Since(placement.UpdatedAt) > b.ttl.Agent {
		pipe := b.rdb.Pipeline()
		pipe.Del(ctx, agentKey(agentID))
		pipe.Del(ctx, placementKey(agentID))
		pipe.SRem(ctx, agentsIndexKey, agentID)
		_, _ = pipe.Exec(ctx)
		return nil, registry.ErrAgentNotPlaced
	}
	return &placement, nil
}

func (b *Backend) Close(ctx context.Context) error {
	if b.rdb == nil {
		return nil
	}
	return b.rdb.Close()
}

func relayKey(relayID string) string {
	return relayKeyPrefix + relayID
}

func agentKey(agentID string) string {
	return agentKeyPrefix + agentID
}

func placementKey(agentID string) string {
	return placementPrefix + agentID
}

func parseRelay(data map[string]string) (registry.Relay, error) {
	var relay registry.Relay
	relay.ID = data["ID"]
	relay.Address = data["Address"]

	if grpcPort := data["GRPCPort"]; grpcPort != "" {
		parsed, err := strconv.Atoi(grpcPort)
		if err != nil {
			return relay, fmt.Errorf("invalid relay grpc port: %w", err)
		}
		relay.GRPCPort = parsed
	}

	if lastSeen := data["LastSeenUnixMs"]; lastSeen != "" {
		parsed, err := strconv.ParseInt(lastSeen, 10, 64)
		if err != nil {
			return relay, fmt.Errorf("invalid relay last seen: %w", err)
		}
		relay.LastSeen = time.UnixMilli(parsed).UTC()
	}

	return relay, nil
}

func parsePlacement(data map[string]string) (registry.AgentPlacement, error) {
	var placement registry.AgentPlacement
	placement.AgentID = data["AgentID"]
	placement.RelayID = data["RelayID"]

	if updatedAt := data["UpdatedAtUnixMs"]; updatedAt != "" {
		parsed, err := strconv.ParseInt(updatedAt, 10, 64)
		if err != nil {
			return placement, fmt.Errorf("invalid placement updated at: %w", err)
		}
		placement.UpdatedAt = time.UnixMilli(parsed).UTC()
	}

	return placement, nil
}
