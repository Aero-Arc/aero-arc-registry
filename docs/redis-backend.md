# Redis backend

Redis is the production registry backend. It stores only current control-plane state; it is not a history store.

## Liveness and consistency

- Relay and agent hashes use Redis-native millisecond TTLs. A positive native TTL is part of entity validity; persistent hashes or relay-membership indexes are corrupt state. Registration and heartbeats renew the relevant TTL only after atomically validating the complete entity, ownership relationship, live indexes, and native expiries; corrupt records are rejected and repaired without partial renewal.
- Mutating scripts use Redis `TIME`, so timestamps do not depend on the clock of a particular registry replica.
- A relay registration creates a monotonically increasing Redis-issued incarnation when no live hash exists. Idempotent retries and metadata updates preserve the active incarnation and agent membership. Re-creation after expiry or removal receives a new incarnation; agent registrations capture it, and reads and heartbeats reject surviving hashes from the prior relay instance.
- An agent is live only while both its agent hash and assigned relay hash exist. A relay expiry therefore removes its agents from registry reads immediately, even before the periodic registry sweep runs.
- Agent registration, reassignment, heartbeat, and removal update the entity, placement, and indexes atomically with Lua scripts. Registration validates the relay's complete hash and unexpired global-index membership before writing any agent state; an invalid target is rejected without disturbing an agent's existing placement.
- Cleanup never executes Redis commands against a membership key merely because an agent hash names it. Scripts derive the canonical registry-owned membership key from a validated relay key and ownership incarnation, and safely repair wrong-type canonical keys without touching arbitrary stored keys.
- Relay-scoped reads repair only the membership belonging to the relay being listed when they encounter an agent that has moved. They never follow the agent's new placement to delete current state, so a stale relay-list snapshot cannot erase a concurrent reassignment.
- Agent heartbeats are ownership-scoped: the caller supplies its relay ID, and Redis renews the agent only when that relay is live and still owns the current placement. A stale relay heartbeat cannot move or extend an agent taken over by another relay.
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

Redis and context errors are returned to the gRPC layer. Missing or expired entities map to `registry.ErrNotFound`. Relay, agent, placement, and relay-membership reads validate the complete current entity and ownership relationship in one Lua operation. Incomplete, invalid, unindexed, or old-incarnation records are omitted and repaired instead of failing healthy neighbors. Relay repair removes the corrupt hash, global index entry, and relay-membership index; agent repair removes its hash and index memberships. Repairs and registrations are serialized by Redis, so a repair based on stale state cannot delete a fresh valid write.

## Ownership heartbeat rollout

Deploy the relay-owned heartbeat contract in this order:

1. Merge and publish the protobuf release that adds `relay_id` to `HeartbeatAgentRequest`.
2. Deploy Relay with that protobuf version so every agent heartbeat supplies its relay ID. The older Registry safely ignores the new field.
3. Deploy the strict Registry version, which requires `relay_id` and rejects a heartbeat unless it matches the current live placement.

After step 3, do not roll Relay back independently: an older Relay omits `relay_id`, so the strict Registry rejects its agent heartbeats until Relay is restored or Registry is rolled back as well. Rollbacks must therefore restore Relay first, or roll Relay and Registry back together.

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
