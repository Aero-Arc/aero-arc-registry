-- KEYS: relay hash, global Relay index, Relay membership index.
-- ARGV: Relay ID, Relay TTL milliseconds.
local relay = redis.call('HMGET', KEYS[1],
  'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
if not valid_relay(relay, ARGV[1], KEYS[1], KEYS[2], now_ms) then
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
