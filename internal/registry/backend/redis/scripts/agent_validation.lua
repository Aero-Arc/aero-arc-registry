-- Shared Agent validation and safe-repair helpers. entity_validation.lua is
-- prepended by scripts.go before this program is loaded.
local redis_time = redis.call('TIME')
local redis_now_ms = tonumber(redis_time[1]) * 1000 +
  math.floor(tonumber(redis_time[2]) / 1000)

local function remove_agent(agent_key, agents_index, expected_id, canonical_agents_key)
  safe_zrem(canonical_agents_key, expected_id)
  redis.call('DEL', agent_key)
  redis.call('ZREM', agents_index, expected_id)
end

local function remove_relay(relay_key, relays_index, expected_id)
  redis.call('DEL', relay_key)
  redis.call('ZREM', relays_index, expected_id)
end

local function trusted_agent_membership(values, relay_key_prefix, relay_agents_key_prefix)
  local canonical_agents_key = canonical_relay_agents_key(
    values[4], relay_key_prefix, relay_agents_key_prefix)
  if not canonical_agents_key or not values[3] or not values[5] then return nil end
  local relay = redis.pcall('HMGET', values[4], 'id', 'incarnation')
  if type(relay) == 'table' and not relay.err and
     relay[1] == values[3] and relay[2] == values[5] then
    return canonical_agents_key
  end
  return nil
end

local function live_agent(agent_key, agents_index, relays_index, expected_id,
                          relay_key_prefix, relay_agents_key_prefix)
  local agent_type = redis.call('TYPE', agent_key).ok
  if agent_type == 'none' then
    redis.call('ZREM', agents_index, expected_id)
    return nil
  end
  if agent_type ~= 'hash' then
    redis.call('DEL', agent_key)
    redis.call('ZREM', agents_index, expected_id)
    return nil
  end
  local values = redis.call('HMGET', agent_key,
    'id', 'last_heartbeat_ms', 'relay_id', 'relay_key',
    'relay_incarnation', 'relay_agents_key', 'placement_updated_ms')
  local trusted_membership = trusted_agent_membership(
    values, relay_key_prefix, relay_agents_key_prefix)
  if not values[1] or values[1] ~= expected_id or
     not valid_nonnegative_int64(values[2]) or
     not values[3] or values[3] == '' or
     not values[4] or values[4] == '' or
     not valid_nonnegative_int64(values[5]) or tonumber(values[5]) < 1 or
     not values[6] or values[6] == '' or
     not valid_nonnegative_int64(values[7]) or
     redis.call('PTTL', agent_key) <= 0 then
    remove_agent(agent_key, agents_index, expected_id, trusted_membership)
    return nil
  end
  if not live_index_member(agents_index, expected_id, redis_now_ms) then
    remove_agent(agent_key, agents_index, expected_id, trusted_membership)
    return nil
  end

  local canonical_agents_key = canonical_relay_agents_key(
    values[4], relay_key_prefix, relay_agents_key_prefix)
  if not canonical_agents_key then
    remove_agent(agent_key, agents_index, expected_id, nil)
    return nil
  end
  local relay = redis.pcall('HMGET', values[4],
    'id', 'address', 'grpc_port', 'incarnation', 'last_seen_ms')
  if type(relay) ~= 'table' or relay.err or not relay[1] then
    remove_agent(agent_key, agents_index, expected_id, nil)
    return nil
  end
  -- A bad agent relay pointer must not be allowed to delete an unrelated relay.
  if relay[1] ~= values[3] then
    remove_agent(agent_key, agents_index, expected_id, nil)
    return nil
  end
  if not valid_relay(relay, values[3], values[4], relays_index, redis_now_ms) then
    remove_relay(values[4], relays_index, values[3])
    remove_agent(agent_key, agents_index, expected_id, canonical_agents_key)
    return nil
  end
  if values[6] ~= canonical_agents_key then
    remove_agent(agent_key, agents_index, expected_id, canonical_agents_key)
    return nil
  end
  if redis.call('PTTL', values[6]) <= 0 then
    redis.call('DEL', values[6])
    remove_agent(agent_key, agents_index, expected_id, nil)
    return nil
  end
  if relay[4] ~= values[5] or
     not live_index_member(values[6], expected_id, redis_now_ms) then
    remove_agent(agent_key, agents_index, expected_id, canonical_agents_key)
    return nil
  end
  return values
end
