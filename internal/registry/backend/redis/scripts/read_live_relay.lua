-- KEYS: relay hash, global Relay index, Relay membership index.
-- ARGV: expected Relay ID.
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
if not valid_relay(values, ARGV[1], KEYS[1], KEYS[2], now_ms) then
  remove_relay()
  return nil
end
return values
