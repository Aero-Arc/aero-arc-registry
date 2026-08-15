# Aero Arc Relay Registry

## Purpose
The Relay Registry is the control-plane service for Aero Arc. It tracks live relays, agent ownership, routing metadata, and expiring current Conformance projections that other control-plane components use to make decisions. It does not handle data-plane traffic or incident history.

## Control-Plane vs Data-Plane
- Control-plane: metadata, coordination, liveness, and ownership used for routing decisions.
- Data-plane: actual traffic forwarding through relays and agents.
The registry only manages control-plane metadata and never participates in data-plane traffic.

## Expected Invariants
- **Relay liveness** is maintained via TTL-based heartbeats. A relay is considered live only while its TTL is valid.
- **Agent ownership** is TTL-based and must expire automatically if not renewed.
- **Eventual consistency** is acceptable; the registry is advisory and not authoritative.
- **Conformance projections** are TTL scoped and fenced first by assignment
  generation and then by evaluation revision. Their longer cursor fence must
  survive projection expiry.
- **gRPC-only**: all external interaction happens over gRPC.
- **Backend-agnostic**: storage backends must be pluggable via a Go interface.

## Non-Goals
- No data-plane routing or packet/stream forwarding.
- No strong consensus or global ordering guarantees.
- No backend-specific features or guarantees exposed in the public API.
- No durable historical analytics or incident evidence; only current, TTL-scoped state.

## Adding New Storage Backends
- Implement the storage interface without leaking backend-specific concepts into the API or protobufs.
- Preserve TTL semantics for relay liveness and agent ownership.
- Ensure graceful degradation: treat backend failures as expected and return best-effort results.
- Keep logic explicit and readable; avoid clever caching or hidden coupling.
- Provide tests that validate TTL behavior and basic CRUD semantics across the interface.

## Versioning and Backward Compatibility
- Keep the gRPC service thin and stable; avoid breaking changes to protobufs.
- Add fields and methods in a backward-compatible manner (e.g., new optional fields, new RPCs).
- Avoid semantic changes that alter TTL or liveness guarantees without a versioned API change.
- Document any required migration behavior for new backends or interface changes.
