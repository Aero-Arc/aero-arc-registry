// Package redis provides a Redis-backed registry implementation.
package redis

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sort"
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
	return &Backend{
		client:    client,
		ttl:       ttl,
		namespace: namespace,
	}
}

// ManagesTTL reports that Redis atomically enforces entity expiry using Redis
// server time. The service-level sweeper must not compare these timestamps to
// its local clock.
func (b *Backend) ManagesTTL() bool { return true }

func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	if relay.ID == "" {
		return fmt.Errorf("relay id is empty: %w", registry.ErrInvalid)
	}
	if relay.Address == "" {
		return fmt.Errorf("relay address is empty: %w", registry.ErrInvalid)
	}

	_, err := registerRelayScript.Run(ctx, b.client,
		[]string{b.relayKey(relay.ID), b.relaysKey(), b.relayAgentsKey(relay.ID)},
		relay.ID, relay.Address, relay.GRPCPort, b.ttl.Relay.Milliseconds(),
	).Result()
	if err != nil {
		return fmt.Errorf("register relay %q: %w", relay.ID, err)
	}
	return nil
}

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

func (b *Backend) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	ids, err := b.activeIndexIDs(ctx, b.relaysKey())
	if err != nil {
		return nil, fmt.Errorf("list relay index: %w", err)
	}
	if len(ids) == 0 {
		return []registry.Relay{}, nil
	}

	pipe := b.client.Pipeline()
	commands := make([]*redisclient.MapStringStringCmd, len(ids))
	for i, id := range ids {
		commands[i] = pipe.HGetAll(ctx, b.relayKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("read relays: %w", err)
	}

	relays := make([]registry.Relay, 0, len(ids))
	staleIDs := make([]interface{}, 0)
	for i, command := range commands {
		fields, err := command.Result()
		if err != nil {
			return nil, fmt.Errorf("read relay %q: %w", ids[i], err)
		}
		if len(fields) == 0 {
			staleIDs = append(staleIDs, ids[i])
			continue
		}
		relay, err := decodeRelay(fields)
		if err != nil {
			return nil, fmt.Errorf("decode relay %q: %w", ids[i], err)
		}
		relays = append(relays, relay)
	}
	b.removeStaleIndexMembers(ctx, b.relaysKey(), staleIDs)

	sort.Slice(relays, func(i, j int) bool { return relays[i].ID < relays[j].ID })
	return relays, nil
}

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

func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	if agent.ID == "" {
		return fmt.Errorf("agent id is empty: %w", registry.ErrInvalid)
	}

	result, err := registerAgentScript.Run(ctx, b.client,
		[]string{
			b.agentKey(agent.ID),
			b.agentsKey(),
			b.relayKey(relayID),
			b.relayAgentsKey(relayID),
		},
		agent.ID, relayID, b.ttl.Agent.Milliseconds(),
	).Int64()
	if err != nil {
		return fmt.Errorf("register agent %q: %w", agent.ID, err)
	}
	if result == 0 {
		return errRelayNotRegistered
	}
	return nil
}

func (b *Backend) HeartbeatAgent(ctx context.Context, agentID string) error {
	result, err := heartbeatAgentScript.Run(ctx, b.client,
		[]string{b.agentKey(agentID), b.agentsKey()},
		agentID, b.ttl.Agent.Milliseconds(),
	).Int64()
	if err != nil {
		return fmt.Errorf("heartbeat agent %q: %w", agentID, err)
	}
	if result == 0 {
		return errAgentNotRegistered
	}
	return nil
}

func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	values, err := liveAgentScript.Run(ctx, b.client,
		[]string{b.agentKey(agentID), b.agentsKey()}, agentID,
	).StringSlice()
	if errors.Is(err, redisclient.Nil) || (err == nil && len(values) == 0) {
		return nil, errAgentNotRegistered
	}
	if err != nil {
		return nil, fmt.Errorf("get agent placement %q: %w", agentID, err)
	}
	if len(values) != 3 {
		return nil, fmt.Errorf("get agent placement %q: corrupt redis response", agentID)
	}

	updatedAt, err := decodeUnixMilli(values[2])
	if err != nil {
		return nil, fmt.Errorf("decode placement %q: %w", agentID, err)
	}
	return &registry.AgentPlacement{
		AgentID:   values[0],
		RelayID:   values[1],
		UpdatedAt: updatedAt,
	}, nil
}

func (b *Backend) ListAgents(ctx context.Context) ([]registry.Agent, error) {
	ids, err := b.activeIndexIDs(ctx, b.agentsKey())
	if err != nil {
		return nil, fmt.Errorf("list agent index: %w", err)
	}
	if len(ids) == 0 {
		return []registry.Agent{}, nil
	}

	pipe := b.client.Pipeline()
	commands := make([]*redisclient.SliceCmd, len(ids))
	for i, id := range ids {
		commands[i] = pipe.HMGet(ctx, b.agentKey(id), "id", "last_heartbeat_ms", "relay_key")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("read agents: %w", err)
	}

	type agentCandidate struct {
		id       string
		lastSeen string
		relayKey string
	}
	candidates := make([]agentCandidate, 0, len(ids))
	staleIDs := make([]interface{}, 0)
	for i := range commands {
		values, err := commands[i].Result()
		if err != nil {
			return nil, fmt.Errorf("read agent %q: %w", ids[i], err)
		}
		id, okID := redisString(values, 0)
		lastSeen, okLastSeen := redisString(values, 1)
		relayKey, okRelayKey := redisString(values, 2)
		if !okID && !okLastSeen && !okRelayKey {
			staleIDs = append(staleIDs, ids[i])
			continue
		}
		if !okID || !okLastSeen || !okRelayKey {
			return nil, fmt.Errorf("decode agent %q: incomplete redis record", ids[i])
		}
		candidates = append(candidates, agentCandidate{id: id, lastSeen: lastSeen, relayKey: relayKey})
	}

	relayPipe := b.client.Pipeline()
	relayCommands := make([]*redisclient.IntCmd, len(candidates))
	for i, candidate := range candidates {
		relayCommands[i] = relayPipe.Exists(ctx, candidate.relayKey)
	}
	if _, err := relayPipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("validate agent relays: %w", err)
	}

	agents := make([]registry.Agent, 0, len(candidates))
	for i, candidate := range candidates {
		exists, err := relayCommands[i].Result()
		if err != nil {
			return nil, fmt.Errorf("validate relay for agent %q: %w", candidate.id, err)
		}
		if exists == 0 {
			staleIDs = append(staleIDs, candidate.id)
			continue
		}
		lastHeartbeat, err := decodeUnixMilli(candidate.lastSeen)
		if err != nil {
			return nil, fmt.Errorf("decode agent %q: %w", candidate.id, err)
		}
		agents = append(agents, registry.Agent{ID: candidate.id, LastHeartbeat: lastHeartbeat})
	}
	b.removeStaleIndexMembers(ctx, b.agentsKey(), staleIDs)

	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents, nil
}

func (b *Backend) ListRelayAgents(ctx context.Context, relayID string) ([]*registry.Agent, error) {
	if exists, err := b.client.Exists(ctx, b.relayKey(relayID)).Result(); err != nil {
		return nil, fmt.Errorf("check relay %q: %w", relayID, err)
	} else if exists == 0 {
		return nil, errRelayNotRegistered
	}

	ids, err := b.activeIndexIDs(ctx, b.relayAgentsKey(relayID))
	if err != nil {
		return nil, fmt.Errorf("list agents for relay %q: %w", relayID, err)
	}

	pipe := b.client.Pipeline()
	commands := make([]*redisclient.SliceCmd, len(ids))
	for i, id := range ids {
		commands[i] = pipe.HMGet(ctx, b.agentKey(id), "id", "last_heartbeat_ms", "relay_id")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("read agents for relay %q: %w", relayID, err)
	}

	agents := make([]*registry.Agent, 0, len(ids))
	staleIDs := make([]interface{}, 0)
	for i := range commands {
		values, err := commands[i].Result()
		if err != nil {
			return nil, fmt.Errorf("read relay agent %q: %w", ids[i], err)
		}
		id, okID := redisString(values, 0)
		lastSeen, okLastSeen := redisString(values, 1)
		placedRelayID, okRelayID := redisString(values, 2)
		if !okID && !okLastSeen && !okRelayID {
			staleIDs = append(staleIDs, ids[i])
			continue
		}
		if !okID || !okLastSeen || !okRelayID {
			return nil, fmt.Errorf("decode relay agent %q: incomplete redis record", ids[i])
		}
		if placedRelayID != relayID {
			staleIDs = append(staleIDs, ids[i])
			continue
		}
		lastHeartbeat, err := decodeUnixMilli(lastSeen)
		if err != nil {
			return nil, fmt.Errorf("decode relay agent %q: %w", id, err)
		}
		agents = append(agents, &registry.Agent{ID: id, LastHeartbeat: lastHeartbeat})
	}
	b.removeStaleIndexMembers(ctx, b.relayAgentsKey(relayID), staleIDs)

	// Do not report a placement snapshot for a relay that expired during the read.
	if exists, err := b.client.Exists(ctx, b.relayKey(relayID)).Result(); err != nil {
		return nil, fmt.Errorf("recheck relay %q: %w", relayID, err)
	} else if exists == 0 {
		return nil, errRelayNotRegistered
	}

	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents, nil
}

func (b *Backend) RemoveAgents(ctx context.Context, agentIDs []string) error {
	if len(agentIDs) == 0 {
		return nil
	}

	keys := make([]string, 0, len(agentIDs)+1)
	keys = append(keys, b.agentsKey())
	for _, id := range agentIDs {
		keys = append(keys, b.agentKey(id))
	}
	args := make([]interface{}, 0, len(agentIDs))
	for _, id := range agentIDs {
		args = append(args, id)
	}

	if _, err := removeAgentsScript.Run(ctx, b.client, keys, args...).Result(); err != nil {
		return fmt.Errorf("remove agents: %w", err)
	}
	return nil
}

func (b *Backend) Close(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.closeOnce.Do(func() { b.closeErr = b.client.Close() })
	return b.closeErr
}

func (b *Backend) relaysKey() string { return b.namespace + "relays" }
func (b *Backend) agentsKey() string { return b.namespace + "agents" }
func (b *Backend) relayKey(id string) string {
	return b.namespace + "relay:" + encodeID(id)
}
func (b *Backend) agentKey(id string) string {
	return b.namespace + "agent:" + encodeID(id)
}
func (b *Backend) relayAgentsKey(id string) string {
	return b.namespace + "relay-agents:" + encodeID(id)
}

func encodeID(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func (b *Backend) removeStaleIndexMembers(ctx context.Context, key string, members []interface{}) {
	if len(members) == 0 || ctx.Err() != nil {
		return
	}
	// Index repair is best effort. Missing entity hashes are already excluded
	// from the response, so cleanup failure does not make the read incorrect.
	_ = b.client.ZRem(ctx, key, members...).Err()
}

func (b *Backend) activeIndexIDs(ctx context.Context, key string) ([]string, error) {
	ids, err := activeIndexScript.Run(ctx, b.client, []string{key}).StringSlice()
	if errors.Is(err, redisclient.Nil) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func decodeRelay(fields map[string]string) (registry.Relay, error) {
	if fields["id"] == "" {
		return registry.Relay{}, errors.New("missing id")
	}
	if fields["address"] == "" {
		return registry.Relay{}, errors.New("missing address")
	}
	port, err := strconv.ParseInt(fields["grpc_port"], 10, 32)
	if err != nil {
		return registry.Relay{}, fmt.Errorf("invalid grpc_port: %w", err)
	}
	lastSeen, err := decodeUnixMilli(fields["last_seen_ms"])
	if err != nil {
		return registry.Relay{}, fmt.Errorf("invalid last_seen_ms: %w", err)
	}
	return registry.Relay{
		ID:       fields["id"],
		Address:  fields["address"],
		GRPCPort: int32(port),
		LastSeen: lastSeen,
	}, nil
}

func decodeUnixMilli(value string) (time.Time, error) {
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid unix milliseconds %q: %w", value, err)
	}
	return time.UnixMilli(milliseconds), nil
}

func redisString(values []interface{}, index int) (string, bool) {
	if index >= len(values) || values[index] == nil {
		return "", false
	}
	value, ok := values[index].(string)
	return value, ok
}

// Redis TIME is used inside every mutating script so all registry replicas
// share one timestamp authority. Concatenating seconds and milliseconds avoids
// loss of epoch precision in Lua's IEEE-754 numbers.
const redisNowMilliseconds = `
local now = redis.call('TIME')
local now_ms = now[1] .. string.format('%03d', math.floor(tonumber(now[2]) / 1000))
`

var registerRelayScript = redisclient.NewScript(redisNowMilliseconds + `
local expires_ms = tonumber(now_ms) + tonumber(ARGV[4])
redis.call('HSET', KEYS[1],
  'id', ARGV[1],
  'address', ARGV[2],
  'grpc_port', ARGV[3],
  'last_seen_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[4])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
if redis.call('EXISTS', KEYS[3]) == 1 then redis.call('PEXPIRE', KEYS[3], ARGV[4]) end
return now_ms
`)

var heartbeatRelayScript = redisclient.NewScript(redisNowMilliseconds + `
if redis.call('EXISTS', KEYS[1]) == 0 then
  return 0
end
local expires_ms = tonumber(now_ms) + tonumber(ARGV[2])
redis.call('HSET', KEYS[1], 'last_seen_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[2])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
if redis.call('EXISTS', KEYS[3]) == 1 then redis.call('PEXPIRE', KEYS[3], ARGV[2]) end
return 1
`)

var removeRelayScript = redisclient.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3])
  return 0
end
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('DEL', KEYS[3])
return 1
`)

var registerAgentScript = redisclient.NewScript(redisNowMilliseconds + `
if redis.call('EXISTS', KEYS[3]) == 0 then
  return 0
end
local expires_ms = tonumber(now_ms) + tonumber(ARGV[3])
local old_relay_agents_key = redis.call('HGET', KEYS[1], 'relay_agents_key')
if old_relay_agents_key and old_relay_agents_key ~= KEYS[4] then
  redis.call('ZREM', old_relay_agents_key, ARGV[1])
end
redis.call('HSET', KEYS[1],
  'id', ARGV[1],
  'last_heartbeat_ms', now_ms,
  'relay_id', ARGV[2],
  'relay_key', KEYS[3],
  'relay_agents_key', KEYS[4],
  'placement_updated_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
redis.call('ZADD', KEYS[4], expires_ms, ARGV[1])
redis.call('PEXPIRE', KEYS[4], redis.call('PTTL', KEYS[3]))
return 1
`)

var heartbeatAgentScript = redisclient.NewScript(redisNowMilliseconds + `
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('ZREM', KEYS[2], ARGV[1])
  return 0
end
local relay_key = redis.call('HGET', KEYS[1], 'relay_key')
local relay_agents_key = redis.call('HGET', KEYS[1], 'relay_agents_key')
if not relay_key or redis.call('EXISTS', relay_key) == 0 then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  if relay_agents_key then redis.call('ZREM', relay_agents_key, ARGV[1]) end
  return 0
end
redis.call('HSET', KEYS[1],
  'last_heartbeat_ms', now_ms,
  'placement_updated_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[2])
local expires_ms = tonumber(now_ms) + tonumber(ARGV[2])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
redis.call('ZADD', relay_agents_key, expires_ms, ARGV[1])
redis.call('PEXPIRE', relay_agents_key, redis.call('PTTL', relay_key))
return 1
`)

var liveAgentScript = redisclient.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('ZREM', KEYS[2], ARGV[1])
  return nil
end
local relay_key = redis.call('HGET', KEYS[1], 'relay_key')
local relay_agents_key = redis.call('HGET', KEYS[1], 'relay_agents_key')
if not relay_key or redis.call('EXISTS', relay_key) == 0 then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  if relay_agents_key then redis.call('ZREM', relay_agents_key, ARGV[1]) end
  return nil
end
return redis.call('HMGET', KEYS[1], 'id', 'relay_id', 'placement_updated_ms')
`)

var removeAgentsScript = redisclient.NewScript(`
for i = 2, #KEYS do
  local agent_id = ARGV[i - 1]
  local relay_agents_key = redis.call('HGET', KEYS[i], 'relay_agents_key')
  if relay_agents_key then redis.call('ZREM', relay_agents_key, agent_id) end
  redis.call('DEL', KEYS[i])
  redis.call('ZREM', KEYS[1], agent_id)
end
return #KEYS - 1
`)

var activeIndexScript = redisclient.NewScript(redisNowMilliseconds + `
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
return redis.call('ZRANGEBYSCORE', KEYS[1], '(' .. now_ms, '+inf')
`)
