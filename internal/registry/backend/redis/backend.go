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
			b.relaysKey(),
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

func (b *Backend) HeartbeatAgent(ctx context.Context, agentID, expectedRelayID string) error {
	result, err := heartbeatAgentScript.Run(ctx, b.client,
		[]string{
			b.agentKey(agentID),
			b.agentsKey(),
			b.relayKey(expectedRelayID),
			b.relayAgentsKey(expectedRelayID),
			b.relaysKey(),
		},
		agentID, expectedRelayID, b.ttl.Agent.Milliseconds(),
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
		[]string{b.agentKey(agentID), b.agentsKey(), b.relaysKey()}, agentID,
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
	commands := make([]*redisclient.Cmd, len(ids))
	for i, id := range ids {
		commands[i] = readLiveAgentScript.Eval(ctx, pipe,
			[]string{b.agentKey(id), b.agentsKey(), b.relaysKey()}, id,
		)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("read agents: %w", err)
	}

	agents := make([]registry.Agent, 0, len(ids))
	for i := range commands {
		values, err := commands[i].StringSlice()
		if errors.Is(err, redisclient.Nil) || (err == nil && len(values) == 0) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read agent %q: %w", ids[i], err)
		}
		if len(values) != 2 {
			return nil, fmt.Errorf("read agent %q: corrupt redis response", ids[i])
		}
		lastHeartbeat, err := decodeUnixMilli(values[1])
		if err != nil {
			return nil, fmt.Errorf("decode agent %q: %w", ids[i], err)
		}
		agents = append(agents, registry.Agent{ID: values[0], LastHeartbeat: lastHeartbeat})
	}

	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents, nil
}

func (b *Backend) ListRelayAgents(ctx context.Context, relayID string) ([]*registry.Agent, error) {
	relayValues, err := readLiveRelayScript.Run(ctx, b.client,
		[]string{b.relayKey(relayID), b.relaysKey(), b.relayAgentsKey(relayID)}, relayID,
	).StringSlice()
	if errors.Is(err, redisclient.Nil) || (err == nil && len(relayValues) == 0) {
		return nil, errRelayNotRegistered
	}
	if err != nil {
		return nil, fmt.Errorf("check relay %q: %w", relayID, err)
	}
	if len(relayValues) != 5 {
		return nil, fmt.Errorf("check relay %q: corrupt redis response", relayID)
	}
	relayIncarnation := relayValues[3]

	ids, err := b.activeIndexIDs(ctx, b.relayAgentsKey(relayID))
	if err != nil {
		return nil, fmt.Errorf("list agents for relay %q: %w", relayID, err)
	}

	pipe := b.client.Pipeline()
	commands := make([]*redisclient.Cmd, len(ids))
	for i, id := range ids {
		commands[i] = readRelayAgentScript.Eval(ctx, pipe,
			[]string{b.agentKey(id), b.agentsKey(), b.relayAgentsKey(relayID), b.relayKey(relayID), b.relaysKey()},
			id, relayID, relayIncarnation,
		)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("read agents for relay %q: %w", relayID, err)
	}

	agents := make([]*registry.Agent, 0, len(ids))
	for i := range commands {
		values, err := commands[i].StringSlice()
		if errors.Is(err, redisclient.Nil) || (err == nil && len(values) == 0) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read relay agent %q: %w", ids[i], err)
		}
		if len(values) != 2 {
			return nil, fmt.Errorf("read relay agent %q: corrupt redis response", ids[i])
		}
		lastHeartbeat, err := decodeUnixMilli(values[1])
		if err != nil {
			return nil, fmt.Errorf("decode relay agent %q: %w", ids[i], err)
		}
		agents = append(agents, &registry.Agent{ID: values[0], LastHeartbeat: lastHeartbeat})
	}

	// Do not report a placement snapshot for a relay that expired, became
	// corrupt, lost its live index entry, or was re-registered during the read.
	valid, err := validateRelayIncarnationScript.Run(ctx, b.client,
		[]string{b.relayKey(relayID), b.relaysKey(), b.relayAgentsKey(relayID)},
		relayID, relayIncarnation,
	).Int64()
	if err != nil {
		return nil, fmt.Errorf("recheck relay %q: %w", relayID, err)
	}
	if valid == 0 {
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
func (b *Backend) relayIncarnationKey() string { return b.namespace + "relay-incarnation-sequence" }

func encodeID(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
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

func decodeUnixMilli(value string) (time.Time, error) {
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid unix milliseconds %q: %w", value, err)
	}
	return time.UnixMilli(milliseconds), nil
}

// Redis TIME is used inside every mutating script so all registry replicas
// share one timestamp authority. Concatenating seconds and milliseconds avoids
// loss of epoch precision in Lua's IEEE-754 numbers.
const redisNowMilliseconds = `
local now = redis.call('TIME')
local now_ms = now[1] .. string.format('%03d', math.floor(tonumber(now[2]) / 1000))
`

const redisEntityValidationLua = `
local function valid_nonnegative_int64(value)
  if not value or not string.match(value, '^%d+$') then return false end
  if string.len(value) > 19 or
     (string.len(value) == 19 and value > '9223372036854775807') then
    return false
  end
  return true
end

local function live_index_member(index_key, member, current_time_ms)
  local score = redis.call('ZSCORE', index_key, member)
  return valid_nonnegative_int64(score) and
    tonumber(score) > tonumber(current_time_ms)
end

local function valid_relay(values, expected_id, relays_index, current_time_ms)
  local port = values[3] and tonumber(values[3])
  return values[1] and values[1] == expected_id and
    values[2] and values[2] ~= '' and
    values[3] and string.match(values[3], '^%d+$') and
    port and port >= 1 and port <= 65535 and
    valid_nonnegative_int64(values[4]) and tonumber(values[4]) >= 1 and
    valid_nonnegative_int64(values[5]) and
    live_index_member(relays_index, expected_id, current_time_ms)
end
`

var registerRelayScript = redisclient.NewScript(redisNowMilliseconds + redisEntityValidationLua + `
local expires_ms = tonumber(now_ms) + tonumber(ARGV[4])
local current = redis.call('HMGET', KEYS[1], 'id', 'incarnation')
local incarnation = current[2]
if current[1] ~= ARGV[1] or
   not valid_nonnegative_int64(incarnation) or tonumber(incarnation) < 1 then
  incarnation = tostring(redis.call('INCR', KEYS[4]))
  redis.call('DEL', KEYS[3])
end
redis.call('HSET', KEYS[1],
  'id', ARGV[1],
  'address', ARGV[2],
  'grpc_port', ARGV[3],
  'incarnation', incarnation,
  'last_seen_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[4])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
if redis.call('EXISTS', KEYS[3]) == 1 then redis.call('PEXPIRE', KEYS[3], ARGV[4]) end
return incarnation
`)

var heartbeatRelayScript = redisclient.NewScript(redisNowMilliseconds + redisEntityValidationLua + `
local relay = redis.call('HMGET', KEYS[1],
  'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
if not valid_relay(relay, ARGV[1], KEYS[2], now_ms) then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3])
  return 0
end
local expires_ms = tonumber(now_ms) + tonumber(ARGV[2])
redis.call('HSET', KEYS[1], 'last_seen_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[2])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
if redis.call('EXISTS', KEYS[3]) == 1 then redis.call('PEXPIRE', KEYS[3], ARGV[2]) end
return 1
`)

var readLiveRelayScript = redisclient.NewScript(redisNowMilliseconds + redisEntityValidationLua + `
local function remove_relay()
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3])
end
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3])
  return nil
end
local values = redis.call('HMGET', KEYS[1],
  'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
if not valid_relay(values, ARGV[1], KEYS[2], now_ms) then
  remove_relay()
  return nil
end
return values
`)

var validateRelayIncarnationScript = redisclient.NewScript(redisNowMilliseconds + redisEntityValidationLua + `
local relay = redis.call('HMGET', KEYS[1],
  'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
if not valid_relay(relay, ARGV[1], KEYS[2], now_ms) then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3])
  return 0
end
if relay[4] ~= ARGV[2] then return 0 end
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

var registerAgentScript = redisclient.NewScript(redisNowMilliseconds + redisEntityValidationLua + `
local relay = redis.call('HMGET', KEYS[3],
  'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
if not valid_relay(relay, ARGV[2], KEYS[5], now_ms) then
  redis.call('DEL', KEYS[3])
  redis.call('ZREM', KEYS[5], ARGV[2])
  redis.call('DEL', KEYS[4])
  return 0
end
local relay_incarnation = relay[4]
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
  'relay_incarnation', relay_incarnation,
  'relay_agents_key', KEYS[4],
  'placement_updated_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
redis.call('ZADD', KEYS[4], expires_ms, ARGV[1])
redis.call('PEXPIRE', KEYS[4], redis.call('PTTL', KEYS[3]))
return 1
`)

const agentReadValidationLua = redisEntityValidationLua + `
local redis_time = redis.call('TIME')
local redis_now_ms = tonumber(redis_time[1]) * 1000 +
  math.floor(tonumber(redis_time[2]) / 1000)

local function remove_agent(agent_key, agents_index, expected_id, relay_agents_key)
  if relay_agents_key then redis.call('ZREM', relay_agents_key, expected_id) end
  redis.call('DEL', agent_key)
  redis.call('ZREM', agents_index, expected_id)
end

local function remove_relay(relay_key, relays_index, expected_id)
  redis.call('DEL', relay_key)
  redis.call('ZREM', relays_index, expected_id)
end

local function live_agent(agent_key, agents_index, relays_index, expected_id)
  if redis.call('EXISTS', agent_key) == 0 then
    redis.call('ZREM', agents_index, expected_id)
    return nil
  end
  local values = redis.call('HMGET', agent_key,
    'id', 'last_heartbeat_ms', 'relay_id', 'relay_key',
    'relay_incarnation', 'relay_agents_key', 'placement_updated_ms')
  if not values[1] or values[1] ~= expected_id or
     not valid_nonnegative_int64(values[2]) or
     not values[3] or values[3] == '' or
     not values[4] or values[4] == '' or
     not valid_nonnegative_int64(values[5]) or tonumber(values[5]) < 1 or
     not values[6] or values[6] == '' or
     not valid_nonnegative_int64(values[7]) then
    remove_agent(agent_key, agents_index, expected_id, values[6])
    return nil
  end
  if not live_index_member(agents_index, expected_id, redis_now_ms) then
    remove_agent(agent_key, agents_index, expected_id, values[6])
    return nil
  end

  local relay = redis.call('HMGET', values[4],
    'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
  if not relay[1] then
    remove_agent(agent_key, agents_index, expected_id, values[6])
    return nil
  end
  -- A bad agent relay pointer must not be allowed to delete an unrelated relay.
  if relay[1] ~= values[3] then
    remove_agent(agent_key, agents_index, expected_id, values[6])
    return nil
  end
  if not valid_relay(relay, values[3], relays_index, redis_now_ms) then
    remove_relay(values[4], relays_index, values[3])
    remove_agent(agent_key, agents_index, expected_id, values[6])
    return nil
  end
  if relay[4] ~= values[5] or
     not live_index_member(values[6], expected_id, redis_now_ms) then
    remove_agent(agent_key, agents_index, expected_id, values[6])
    return nil
  end
  return values
end
`

var heartbeatAgentScript = redisclient.NewScript(redisNowMilliseconds + agentReadValidationLua + `
local values = live_agent(KEYS[1], KEYS[2], KEYS[5], ARGV[1])
if not values then
  redis.call('ZREM', KEYS[4], ARGV[1])
  return 0
end
if values[3] ~= ARGV[2] or values[4] ~= KEYS[3] or
   values[6] ~= KEYS[4] then
  if values[6] ~= KEYS[4] then redis.call('ZREM', KEYS[4], ARGV[1]) end
  return 0
end
redis.call('HSET', KEYS[1],
  'last_heartbeat_ms', now_ms,
  'placement_updated_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
local expires_ms = tonumber(now_ms) + tonumber(ARGV[3])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
redis.call('ZADD', KEYS[4], expires_ms, ARGV[1])
redis.call('PEXPIRE', KEYS[4], redis.call('PTTL', KEYS[3]))
return 1
`)

var liveAgentScript = redisclient.NewScript(agentReadValidationLua + `
local values = live_agent(KEYS[1], KEYS[2], KEYS[3], ARGV[1])
if not values then return nil end
return {values[1], values[3], values[7]}
`)

var readLiveAgentScript = redisclient.NewScript(agentReadValidationLua + `
local values = live_agent(KEYS[1], KEYS[2], KEYS[3], ARGV[1])
if not values then return nil end
return {values[1], values[2]}
`)

var readRelayAgentScript = redisclient.NewScript(agentReadValidationLua + `
local values = live_agent(KEYS[1], KEYS[2], KEYS[5], ARGV[1])
if not values then return nil end
if values[3] ~= ARGV[2] or values[4] ~= KEYS[4] or
   values[5] ~= ARGV[3] or values[6] ~= KEYS[3] then
  -- live_agent proved that the entity belongs to a valid current placement.
  -- A stale relay-list snapshot must never follow that placement and delete it.
  -- It may only repair a membership key that is distinct from the current one.
  if KEYS[3] ~= values[6] then redis.call('ZREM', KEYS[3], ARGV[1]) end
  return nil
end
return {values[1], values[2]}
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
