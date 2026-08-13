-- KEYS: global Agent index followed by one Agent hash per requested Agent.
-- ARGV: one Agent ID per hash, then Relay key prefix and Relay membership key prefix.
local count = #KEYS - 1
local relay_key_prefix = ARGV[count + 1]
local relay_agents_key_prefix = ARGV[count + 2]
for i = 2, #KEYS do
  local agent_id = ARGV[i - 1]
  if redis.call('TYPE', KEYS[i]).ok == 'hash' then
    local placement = redis.call('HMGET', KEYS[i],
      'relay_id', 'relay_key', 'relay_incarnation', 'relay_agents_key')
    local canonical_agents_key = canonical_relay_agents_key(
      placement[2], relay_key_prefix, relay_agents_key_prefix)
    if canonical_agents_key then
      local relay = redis.pcall('HMGET', placement[2], 'id', 'incarnation')
      if type(relay) == 'table' and not relay.err and
         relay[1] == placement[1] and relay[2] == placement[3] then
        safe_zrem(canonical_agents_key, agent_id)
      end
    end
  end
  redis.call('DEL', KEYS[i])
  redis.call('ZREM', KEYS[1], agent_id)
end
return count
