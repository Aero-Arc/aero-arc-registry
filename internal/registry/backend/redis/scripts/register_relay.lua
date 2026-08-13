-- KEYS: relay hash, global Relay index, Relay membership index, incarnation sequence.
-- ARGV: Relay ID, address, gRPC port, Relay TTL milliseconds.
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
