-- KEYS: Agent hash, global Agent index, global Relay index.
-- ARGV: expected Agent ID, Relay key prefix, Relay membership key prefix.
local values = live_agent(KEYS[1], KEYS[2], KEYS[3], ARGV[1], ARGV[2], ARGV[3])
if not values then return nil end
return {values[1], values[3], values[7]}
