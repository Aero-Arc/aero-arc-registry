-- Shared Redis time authority for mutating/index scripts.
-- Concatenating seconds and milliseconds avoids loss of epoch precision in
-- Lua's IEEE-754 numbers.
local now = redis.call('TIME')
local now_ms = now[1] .. string.format('%03d', math.floor(tonumber(now[2]) / 1000))
