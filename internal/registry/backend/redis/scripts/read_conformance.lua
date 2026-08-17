-- Read one live Conformance projection and reject persistent or malformed
-- hashes so backend TTL semantics cannot be bypassed.
--
-- KEYS:
--   1 summary hash

local ttl = redis.call('PTTL', KEYS[1])
if ttl <= 0 then
  if ttl == -1 then
    redis.call('DEL', KEYS[1])
  end
  return {}
end

local result = redis.call('HMGET', KEYS[1], 'payload', 'stored_at_ms', 'expires_at_ms')
if not result[1] or not result[2] or not result[3] then
  redis.call('DEL', KEYS[1])
  return {}
end
return result
