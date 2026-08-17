# Repository guidance

## Scope

This service is the gRPC-only control plane for current relay liveness,
agent-to-relay placement, and expiring live Conformance projections. It never
forwards telemetry or other data-plane traffic and does not store history.

## Invariants

- Relay and agent liveness are TTL scoped; an agent on an expired relay is not live.
- Registration is idempotent. Re-registering an agent may atomically move it to another live relay. Agent heartbeats must include the expected relay ID and only renew the current matching placement; heartbeats never move ownership.
- Heartbeats update registry-owned timestamps. Unknown or expired entities return an error wrapping `registry.ErrNotFound`.
- Backends implement `internal/registry.Backend` without exposing backend details in protobufs.
- Redis is the production backend; memory is for development/tests. Consul and etcd are stubs.
- Redis mutations that span entity and index keys must remain atomic. Preserve the versioned key namespace and expiry-scored indexes documented in `docs/redis-backend.md`.
- Redis transaction programs live in `internal/registry/backend/redis/scripts/`
  and are embedded by `scripts.go`. Every program documents its ordered
  `KEYS`/`ARGV` contract; update that contract and its Go call site together.
- Lists must omit expired/partially deleted records and return deterministic ID order.
- Conformance publication compares assignment generation before evaluation
  revision. Exact content retries are idempotent, same-cursor conflicts are
  rejected, and a longer cursor fence survives the short projection TTL.
- Conformance read APIs expose current state only. Keep fan-out, subscriptions,
  browser sockets, incident history, and durable replay outside Registry.
- Relay-owned agent heartbeat rollout is ordered: publish Protos, deploy Relay while the old Registry ignores the new `relay_id`, then deploy the strict Registry. Once strict Registry is live, rolling Relay back alone causes agent heartbeats to be rejected; restore Relay or roll Registry back with it.

## Development

- The module requires Go 1.24 (`toolchain go1.24.9`).
- Format with `gofmt`; run `go test ./...`, `go test -race ./...`, `go vet ./...`, and `staticcheck ./...` before handoff.
- Real Redis coverage uses the `integration` build tag. By default Testcontainers starts the pinned Redis image on a dynamic port; `REDIS_TEST_ADDR` opts into an externally managed server: `go test -tags=integration ./internal/registry/backend/redis`.
- Tests must not flush shared Redis databases. Use unique namespaces and explicit cleanup.
- Public transport changes require updating the external `aero-arc-protos` module; do not edit generated protobuf code here.
- Commits require a DCO `Signed-off-by` trailer.
- Every exported handwritten Go function or method requires a lint-valid Go doc
  comment beginning with its exact identifier. Explain parameter meaning,
  return/error behavior, TTL ownership, placement fencing, and atomicity where
  applicable; prefer readable `Parameters:` and `Returns:` sections for
  non-trivial backend and transport APIs over signature-restating prose.
- Generated protobuf documentation remains generator-owned.

`AGENT.md` contains the original architectural context and remains applicable.
