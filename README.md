# Aero Arc Relay Registry

## Overview
The Aero Arc Relay Registry is a control-plane gRPC service for coordinating live Aero Arc Relay instances and their agent ownership. It provides a single, backend-agnostic source of current routing metadata so API servers and operator dashboards can make decisions without coupling to any specific storage system.

This solves the problem of keeping relay liveness and agent ownership visible and consistent enough for routing, while remaining replaceable and tolerant of backend failures.

## Architecture
- **Control-plane service**: stores and serves metadata only.
- **Data-plane**: relay and agent traffic flows elsewhere; the registry never forwards traffic.
- **gRPC-only**: all external interaction happens over gRPC.
- **Pluggable storage backends**: production Redis and development in-memory implementations share a Go interface. Consul and etcd are reserved backend types and are not implemented yet.

In the broader Aero Arc system, the registry sits between relays/agents and control-plane consumers. Relays and agents register and renew TTL-based ownership; control-plane consumers query the current state to drive routing and operational views.

## Design Goals
- Keep the service simple, readable, and replaceable.
- Maintain clear separation of control-plane metadata from data-plane traffic.
- Stay backend-agnostic; do not leak backend concepts into the API.
- Rely on TTL-based liveness and ownership with eventual consistency.
- Degrade gracefully when backends fail.

## Non-Goals
- Data-plane traffic forwarding or routing decisions.
- Strong consistency, global ordering, or consensus guarantees.
- Backend-specific APIs or operational dependencies exposed to clients.
- Historical analytics or long-term state retention.

## High-Level API
- Register and renew relay liveness (TTL-based).
- Register and renew agent-to-relay ownership (TTL-based).
- Query current relay and ownership state for routing and operator views.

## Status / Roadmap
- The in-memory backend is intended for local development and tests.
- The Redis backend is the production backend. It uses Redis-native expiration, atomic placement updates, and server-side timestamps; the local-clock registry sweep is disabled for Redis.
- Consul and etcd currently return `ErrNotImplemented`.
- Backward-compatible API evolution is prioritized over feature expansion.

## Run with Redis

Start Redis 8.8 or newer (integration tests pin `redis:8.8.1-alpine`), then run:

```sh
go run ./cmd/aero-arc-registry \
  --backend redis \
  --redis-addr 127.0.0.1 \
  --redis-port 6379 \
  --relay-ttl 30s \
  --agent-ttl 30s
```

Authentication is configured with `--redis-user` and `--redis-password`; select a logical database with `--redis-db`. Redis connections are established lazily, so a connection or authentication failure is returned by the first registry operation.

See [docs/redis-backend.md](docs/redis-backend.md) for the storage model, liveness behavior, and integration-test instructions.
