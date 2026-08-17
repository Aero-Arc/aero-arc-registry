-- Atomically publish one current Conformance projection behind its durable-ish
-- cursor fence. Generation and revision are zero-padded unsigned decimal
-- strings so lexicographic comparison remains exact beyond Lua's 53-bit number
-- precision.
--
-- KEYS:
--   1 summary hash
--   2 assignment cursor fence hash
-- ARGV:
--   1 padded assignment generation
--   2 padded evaluation revision
--   3 deterministic content digest
--   4 JSON summary payload
--   5 projection TTL milliseconds
--   6 fence TTL milliseconds

local summary_key = KEYS[1]
local fence_key = KEYS[2]
local generation = ARGV[1]
local revision = ARGV[2]
local digest = ARGV[3]

local fence_ttl = redis.call('PTTL', fence_key)
if fence_ttl == -1 then
  redis.call('DEL', fence_key)
elseif fence_ttl == -2 then
  fence_ttl = 0
end

local disposition = 1
if fence_ttl > 0 then
  local current = redis.call('HMGET', fence_key, 'generation', 'revision', 'digest')
  if not current[1] or not current[2] or not current[3] then
    redis.call('DEL', fence_key)
  elseif generation < current[1] or (generation == current[1] and revision < current[2]) then
    return {-1}
  elseif generation == current[1] and revision == current[2] then
    if digest ~= current[3] then
      return {0}
    end
    disposition = 2
  end
end

local expires_at_ms = tostring(tonumber(now_ms) + tonumber(ARGV[5]))
redis.call('HSET', summary_key,
  'payload', ARGV[4],
  'stored_at_ms', now_ms,
  'expires_at_ms', expires_at_ms)
redis.call('PEXPIRE', summary_key, ARGV[5])
redis.call('HSET', fence_key,
  'generation', generation,
  'revision', revision,
  'digest', digest)
redis.call('PEXPIRE', fence_key, ARGV[6])

return {disposition, now_ms, expires_at_ms}
