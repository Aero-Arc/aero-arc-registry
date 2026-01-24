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
	rdb *redis.Client
}

func New(cfg *registry.RedisConfig) (*Backend, error) {
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

	return &Backend{cfg: cfg, rdb: rdb}, nil
}

func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	if b.rdb == nil {
		return errors.New("redis client not initialized")
	}

	if relay.LastSeen.IsZero() {
		relay.LastSeen = time.Now().UTC()
	}

	key := relayKey(relay.ID)
	pipe := b.rdb.Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"ID":           relay.ID,
		"Address":      relay.Address,
		"GRPCPort":     relay.GRPCPort,
		"LastSeenUnix": relay.LastSeen.UnixNano(),
	})
	pipe.SAdd(ctx, relaysIndexKey, relay.ID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) HeartbeatRelay(ctx context.Context, relayID string, ts time.Time) error {
	if b.rdb == nil {
		return errors.New("redis client not initialized")
	}

	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	key := relayKey(relayID)
	pipe := b.rdb.Pipeline()
	pipe.HSet(ctx, key, "LastSeenUnix", ts.UnixNano())
	pipe.SAdd(ctx, relaysIndexKey, relayID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	if b.rdb == nil {
		return nil, errors.New("redis client not initialized")
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

	relays := make([]registry.Relay, 0, len(ids))
	for _, cmd := range cmds {
		data, err := cmd.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}

		relay, err := parseRelay(data)
		if err != nil {
			return nil, err
		}
		relays = append(relays, relay)
	}

	return relays, nil
}

func (b *Backend) RemoveRelay(ctx context.Context, relayID string) error {
	if b.rdb == nil {
		return errors.New("redis client not initialized")
	}

	pipe := b.rdb.Pipeline()
	pipe.Del(ctx, relayKey(relayID))
	pipe.SRem(ctx, relaysIndexKey, relayID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	if b.rdb == nil {
		return errors.New("redis client not initialized")
	}

	if agent.LastHeartbeat.IsZero() {
		agent.LastHeartbeat = time.Now().UTC()
	}

	placement := registry.AgentPlacement{
		AgentID:   agent.ID,
		RelayID:   relayID,
		UpdatedAt: agent.LastHeartbeat,
	}

	pipe := b.rdb.Pipeline()
	pipe.HSet(ctx, agentKey(agent.ID), map[string]any{
		"ID":                agent.ID,
		"LastHeartbeatUnix": agent.LastHeartbeat.UnixNano(),
	})
	pipe.HSet(ctx, placementKey(agent.ID), map[string]any{
		"AgentID":      placement.AgentID,
		"RelayID":      placement.RelayID,
		"UpdatedAtUnix": placement.UpdatedAt.UnixNano(),
	})
	pipe.SAdd(ctx, agentsIndexKey, agent.ID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) HeartbeatAgent(ctx context.Context, agentID string, ts time.Time) error {
	if b.rdb == nil {
		return errors.New("redis client not initialized")
	}

	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	pipe := b.rdb.Pipeline()
	pipe.HSet(ctx, agentKey(agentID), "LastHeartbeatUnix", ts.UnixNano())
	pipe.HSet(ctx, placementKey(agentID), "UpdatedAtUnix", ts.UnixNano())
	pipe.SAdd(ctx, agentsIndexKey, agentID)
	_, err := pipe.Exec(ctx)
	return err
}

func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	if b.rdb == nil {
		return nil, errors.New("redis client not initialized")
	}

	data, err := b.rdb.HGetAll(ctx, placementKey(agentID)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	placement, err := parsePlacement(data)
	if err != nil {
		return nil, err
	}
	return &placement, nil
}

func (b *Backend) Close(ctx context.Context) error {
	if b.rdb == nil {
		return nil
	}
	return b.rdb.Close()
}

const (
	relaysIndexKey  = "aeroarc:registry:relays"
	agentsIndexKey  = "aeroarc:registry:agents"
	relayKeyPrefix  = "aeroarc:registry:relay:"
	agentKeyPrefix  = "aeroarc:registry:agent:"
	placementPrefix = "aeroarc:registry:placement:"
)

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

	if lastSeen := data["LastSeenUnix"]; lastSeen != "" {
		parsed, err := strconv.ParseInt(lastSeen, 10, 64)
		if err != nil {
			return relay, fmt.Errorf("invalid relay last seen: %w", err)
		}
		relay.LastSeen = time.Unix(0, parsed).UTC()
	}

	return relay, nil
}

func parsePlacement(data map[string]string) (registry.AgentPlacement, error) {
	var placement registry.AgentPlacement
	placement.AgentID = data["AgentID"]
	placement.RelayID = data["RelayID"]

	if updatedAt := data["UpdatedAtUnix"]; updatedAt != "" {
		parsed, err := strconv.ParseInt(updatedAt, 10, 64)
		if err != nil {
			return placement, fmt.Errorf("invalid placement updated at: %w", err)
		}
		placement.UpdatedAt = time.Unix(0, parsed).UTC()
	}

	return placement, nil
}
