-- Shared validation and safe-repair helpers for Relay and Agent scripts.
local function valid_nonnegative_int64(value)
  if type(value) ~= 'string' or not string.match(value, '^%d+$') then return false end
  if string.len(value) > 19 or
     (string.len(value) == 19 and value > '9223372036854775807') then
    return false
  end
  return true
end

local function live_index_member(index_key, member, current_time_ms)
  local score = redis.pcall('ZSCORE', index_key, member)
  return valid_nonnegative_int64(score) and
    tonumber(score) > tonumber(current_time_ms)
end

local function valid_relay(values, expected_id, relay_key, relays_index, current_time_ms)
  local port = values[3] and tonumber(values[3])
  return values[1] and values[1] == expected_id and
    values[2] and values[2] ~= '' and
    values[3] and string.match(values[3], '^%d+$') and
    port and port >= 1 and port <= 65535 and
    valid_nonnegative_int64(values[4]) and tonumber(values[4]) >= 1 and
    valid_nonnegative_int64(values[5]) and
    redis.call('PTTL', relay_key) > 0 and
    live_index_member(relays_index, expected_id, current_time_ms)
end

local function canonical_relay_agents_key(relay_key, relay_key_prefix, relay_agents_key_prefix)
  if not relay_key or string.sub(relay_key, 1, string.len(relay_key_prefix)) ~= relay_key_prefix then
    return nil
  end
  local suffix = string.sub(relay_key, string.len(relay_key_prefix) + 1)
  if suffix == '' then return nil end
  return relay_agents_key_prefix .. suffix
end

local function safe_zrem(key, member)
  if not key then return end
  local key_type = redis.call('TYPE', key).ok
  if key_type == 'zset' then
    redis.call('ZREM', key, member)
  elseif key_type ~= 'none' then
    redis.call('DEL', key)
  end
end
