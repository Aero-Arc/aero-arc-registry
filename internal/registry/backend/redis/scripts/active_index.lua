-- KEYS: expiry-scored sorted-set index.
-- ARGV: none.
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
return redis.call('ZRANGEBYSCORE', KEYS[1], '(' .. now_ms, '+inf')
