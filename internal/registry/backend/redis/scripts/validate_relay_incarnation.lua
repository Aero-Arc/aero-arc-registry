-- KEYS: relay hash, global Relay index, Relay membership index.
-- ARGV: expected Relay ID, expected incarnation.
local relay = redis.call('HMGET', KEYS[1],
  'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
if not valid_relay(relay, ARGV[1], KEYS[1], KEYS[2], now_ms) then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3])
  return 0
end
if relay[4] ~= ARGV[2] then return 0 end
return 1
