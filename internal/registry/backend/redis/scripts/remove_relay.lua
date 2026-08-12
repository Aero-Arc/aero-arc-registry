-- KEYS: relay hash, global Relay index, Relay membership index.
-- ARGV: Relay ID.
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('ZREM', KEYS[2], ARGV[1])
  redis.call('DEL', KEYS[3])
  return 0
end
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('DEL', KEYS[3])
return 1
