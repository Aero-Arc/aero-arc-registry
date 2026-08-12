-- KEYS: Agent hash, global Agent index, Relay membership snapshot,
-- Relay hash, global Relay index.
-- ARGV: Agent ID, Relay ID, Relay incarnation, Relay key prefix,
-- Relay membership key prefix.
local values = live_agent(KEYS[1], KEYS[2], KEYS[5], ARGV[1], ARGV[4], ARGV[5])
if not values then return nil end
if values[3] ~= ARGV[2] or values[4] ~= KEYS[4] or
   values[5] ~= ARGV[3] or values[6] ~= KEYS[3] then
  -- live_agent proved that the entity belongs to a valid current placement.
  -- A stale relay-list snapshot must never follow that placement and delete it.
  -- It may only repair a membership key that is distinct from the current one.
  if KEYS[3] ~= values[6] then safe_zrem(KEYS[3], ARGV[1]) end
  return nil
end
return {values[1], values[2]}
