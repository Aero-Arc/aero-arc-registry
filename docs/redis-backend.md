# Redis backend

Redis is the production registry backend. It stores only current control-plane state; it is not a history store.

## Liveness and consistency

- Relay and agent hashes use Redis-native millisecond TTLs. Registration and heartbeats renew the relevant TTL.
- Mutating scripts use Redis `TIME`, so timestamps do not depend on the clock of a particular registry replica.
- A relay registration creates a monotonically increasing Redis-issued incarnation when no live hash exists. Idempotent retries and metadata updates preserve the active incarnation and agent membership. Re-creation after expiry or removal receives a new incarnation; agent registrations capture it, and reads and heartbeats reject surviving hashes from the prior relay instance.
- An agent is live only while both its agent hash and assigned relay hash exist. A relay expiry therefore removes its agents from registry reads immediately, even before the periodic registry sweep runs.
- Agent registration, reassignment, heartbeat, and removal update the entity, placement, and indexes atomically with Lua scripts.
- Sorted-set indexes carry the same expiry deadline as their entities. Reads prune expired members and ignore hashes that expired between the index and entity reads.
- Listing is deterministic by ID. The backend provides current, eventually consistent state and does not provide historical events or global ordering.

The service-level TTL sweep is disabled for Redis. Redis `TIME` and native key expiry are the sole liveness authority, avoiding incorrect deletions when a registry replica's clock differs from the Redis server. Backends without native TTL support, including the in-memory backend, continue to use the service-level sweep.

## Key model

All keys use the versioned `{aero-arc-registry}:v1:` namespace. Entity IDs are URL-safe base64 encoded in key names.

| Key | Type | Purpose |
| --- | --- | --- |
| `...:relay:<id>` | hash with TTL | Relay address, gRPC port, incarnation, and last heartbeat |
| `...:agent:<id>` | hash with TTL | Agent heartbeat, current relay placement, and relay incarnation |
| `...:relays` | sorted set | Relay IDs scored by expiry time |
| `...:agents` | sorted set | Agent IDs scored by expiry time |
| `...:relay-agents:<id>` | sorted set | Agent IDs placed on one relay, scored by agent expiry |
| `...:relay-incarnation-sequence` | integer | Monotonic source for relay incarnation tokens |

The namespace contains a Redis hash tag so related keys share a slot. The current client targets a standalone Redis deployment; Redis Cluster is not yet a supported configuration.

## Failure behavior

Redis and context errors are returned to the gRPC layer. Missing or expired entities map to `registry.ErrNotFound`. Relay and agent lists atomically omit and repair incomplete, invalid, or old-incarnation records instead of failing the entire list. Relay repair removes the corrupt hash, global index entry, and relay-membership index in one Lua operation; agent repair similarly removes its hash and index memberships.

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
