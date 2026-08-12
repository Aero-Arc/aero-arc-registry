-- KEYS: Agent hash, global Agent index, expected Relay hash,
-- expected Relay membership index, global Relay index.
-- ARGV: Agent ID, expected Relay ID, Agent TTL milliseconds, Relay key prefix,
-- Relay membership key prefix.
local values = live_agent(KEYS[1], KEYS[2], KEYS[5], ARGV[1], ARGV[4], ARGV[5])
if not values then
  safe_zrem(KEYS[4], ARGV[1])
  return 0
end
if values[3] ~= ARGV[2] or values[4] ~= KEYS[3] or
   values[6] ~= KEYS[4] then
  if values[6] ~= KEYS[4] then safe_zrem(KEYS[4], ARGV[1]) end
  return 0
end
local relay_ttl = redis.call('PTTL', KEYS[3])
if relay_ttl <= 0 then
  redis.call('DEL', KEYS[3])
  redis.call('ZREM', KEYS[5], ARGV[2])
  redis.call('DEL', KEYS[4])
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  return 0
end
redis.call('HSET', KEYS[1],
  'last_heartbeat_ms', now_ms,
  'placement_updated_ms', now_ms)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
local expires_ms = tonumber(now_ms) + tonumber(ARGV[3])
redis.call('ZADD', KEYS[2], expires_ms, ARGV[1])
redis.call('ZADD', KEYS[4], expires_ms, ARGV[1])
redis.call('PEXPIRE', KEYS[4], relay_ttl)
return 1
