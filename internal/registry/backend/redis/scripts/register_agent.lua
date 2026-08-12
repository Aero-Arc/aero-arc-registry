-- KEYS: Agent hash, global Agent index, target Relay hash,
-- target Relay membership index, global Relay index.
-- ARGV: Agent ID, Relay ID, Agent TTL milliseconds, Relay key prefix,
-- Relay membership key prefix.
local relay = redis.call('HMGET', KEYS[3],
  'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
if not valid_relay(relay, ARGV[2], KEYS[3], KEYS[5], now_ms) then
  redis.call('DEL', KEYS[3])
  redis.call('ZREM', KEYS[5], ARGV[2])
  redis.call('DEL', KEYS[4])
  return 0
end
local relay_incarnation = relay[4]
local relay_ttl = redis.call('PTTL', KEYS[3])
if relay_ttl <= 0 then
  redis.call('DEL', KEYS[3])
  redis.call('ZREM', KEYS[5], ARGV[2])
  redis.call('DEL', KEYS[4])
  return 0
end
local expires_ms = tonumber(now_ms) + tonumber(ARGV[3])
local agent_type = redis.call('TYPE', KEYS[1]).ok
if agent_type ~= 'none' and agent_type ~= 'hash' then redis.call('DEL', KEYS[1]) end
local old = redis.call('HMGET', KEYS[1],
  'relay_id', 'relay_key', 'relay_incarnation', 'relay_agents_key')
if old[2] and old[2] ~= KEYS[3] then
  local canonical_old_agents_key = canonical_relay_agents_key(old[2], ARGV[4], ARGV[5])
  if canonical_old_agents_key and old[1] and old[3] then
    local old_relay = redis.pcall('HMGET', old[2], 'id', 'incarnation')
    if type(old_relay) == 'table' and not old_relay.err and
       old_relay[1] == old[1] and old_relay[2] == old[3] then
      safe_zrem(canonical_old_agents_key, ARGV[1])
    end
  end
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
local target_type = redis.call('TYPE', KEYS[4]).ok
if target_type ~= 'none' and target_type ~= 'zset' then redis.call('DEL', KEYS[4]) end
redis.call('ZADD', KEYS[4], expires_ms, ARGV[1])
redis.call('PEXPIRE', KEYS[4], relay_ttl)
return 1
