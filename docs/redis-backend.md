# Redis backend

Redis is the production registry backend. It stores only current control-plane state; it is not a history store.

## Liveness and consistency

- Relay and agent hashes use Redis-native millisecond TTLs. Registration and heartbeats renew the relevant TTL.
- Mutating scripts use Redis `TIME`, so timestamps do not depend on the clock of a particular registry replica.
- An agent is live only while both its agent hash and assigned relay hash exist. A relay expiry therefore removes its agents from registry reads immediately, even before the periodic registry sweep runs.
- Agent registration, reassignment, heartbeat, and removal update the entity, placement, and indexes atomically with Lua scripts.
- Sorted-set indexes carry the same expiry deadline as their entities. Reads prune expired members and ignore hashes that expired between the index and entity reads.
- Listing is deterministic by ID. The backend provides current, eventually consistent state and does not provide historical events or global ordering.

The service-level TTL sweep is disabled for Redis. Redis `TIME` and native key expiry are the sole liveness authority, avoiding incorrect deletions when a registry replica's clock differs from the Redis server. Backends without native TTL support, including the in-memory backend, continue to use the service-level sweep.

## Key model

All keys use the versioned `{aero-arc-registry}:v1:` namespace. Entity IDs are URL-safe base64 encoded in key names.

| Key | Type | Purpose |
| --- | --- | --- |
| `...:relay:<id>` | hash with TTL | Relay address, gRPC port, and last heartbeat |
| `...:agent:<id>` | hash with TTL | Agent heartbeat and current relay placement |
| `...:relays` | sorted set | Relay IDs scored by expiry time |
| `...:agents` | sorted set | Agent IDs scored by expiry time |
| `...:relay-agents:<id>` | sorted set | Agent IDs placed on one relay, scored by agent expiry |

The namespace contains a Redis hash tag so related keys share a slot. The current client targets a standalone Redis deployment; Redis Cluster is not yet a supported configuration.

## Failure behavior

Redis and context errors are returned to the gRPC layer. Missing or expired entities map to `registry.ErrNotFound`. Corrupt records fail the list/read operation instead of returning partially decoded state. Secondary-index cleanup is best effort because stale members are already excluded from responses.

## Tests

Fast unit and concurrency tests use an in-process Redis-compatible server:

```sh
go test ./...
go test -race ./...
```

Run the real Redis integration test against `127.0.0.1:6379`, or override the address:

```sh
go test -tags=integration ./internal/registry/backend/redis
REDIS_TEST_ADDR=127.0.0.1:6380 go test -tags=integration ./internal/registry/backend/redis
```

The integration test uses a unique namespace and removes its keys when complete. It never flushes the selected Redis database.
