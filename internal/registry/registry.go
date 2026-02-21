// Package registry implements the Aero Arc Registry control plane.
//
// The registry is responsible for tracking the liveness, identity, and
// placement of Aero Arc relays and agents in a distributed system. It acts
// as a coordination layer between stateless relay instances and higher-level
// control plane components such as APIs, operator dashboards, and fleet-wide
// management services.
//
// The registry is designed to be backend-agnostic. It defines a stable,
// backend-independent contract while allowing multiple implementations
// (e.g. in-memory, Redis, etcd, Consul) to be plugged in via configuration.
// This enables Aero Arc to integrate cleanly with existing infrastructure
// and service discovery systems without coupling core logic to a specific
// datastore or coordination mechanism.
//
// Liveness semantics such as heartbeats and time-to-live (TTL) enforcement
// are implemented at the registry layer, ensuring consistent behavior across
// all backend implementations.
//
// The registry exposes its functionality over gRPC and is intended to be
// deployed as a standalone, horizontally scalable control plane service.
package registry

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/pkg/utils"
)

type Registry struct {
	cfg     *Config
	backend Backend

	ttlLoopRunning       atomic.Bool
	ttlCleanupInProgress atomic.Bool
}

func New(cfg *Config, backend Backend) (*Registry, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	aeroRegistry := &Registry{
		cfg:     cfg,
		backend: backend,
	}

	return aeroRegistry, nil
}

func (r *Registry) RegisterRelay(ctx context.Context, relay Relay) error {
	// TODO(registry-ttl): make registry-owned timestamps authoritative by setting
	// relay.LastSeen here before persisting, instead of trusting external clocks.
	return r.backend.RegisterRelay(ctx, relay)
}

func (r *Registry) HeartbeatRelay(ctx context.Context, relayID string) error {
	// TODO(registry-ttl): move heartbeat timestamp source of truth to registry
	// write path (backend should persist registry-assigned time).
	return r.backend.HeartbeatRelay(ctx, relayID)
}

func (r *Registry) ListRelays(ctx context.Context) ([]Relay, error) {
	return r.backend.ListRelays(ctx)
}

func (r *Registry) RemoveRelay(ctx context.Context, relayID string) error {
	return r.backend.RemoveRelay(ctx, relayID)
}

func (r *Registry) RegisterAgent(ctx context.Context, agent Agent, relayID string) error {
	return r.backend.RegisterAgent(ctx, agent, relayID)
}

func (r *Registry) HeartbeatAgent(ctx context.Context, agentID string) error {
	// TODO(registry-ttl): move heartbeat timestamp source of truth to registry
	// write path (backend should persist registry-assigned time).
	return r.backend.HeartbeatAgent(ctx, agentID)
}

func (r *Registry) GetAgentPlacement(ctx context.Context, agentID string) (*AgentPlacement, error) {
	return r.backend.GetAgentPlacement(ctx, agentID)
}

func (r *Registry) ListAgents(ctx context.Context) ([]Agent, error) {
	return r.backend.ListAgents(ctx)
}

func (r *Registry) ListRelayAgents(ctx context.Context, relayID string) ([]*Agent, error) {
	return r.backend.ListRelayAgents(ctx, relayID)
}

func (r *Registry) RemoveAgents(ctx context.Context, agentIDs []string) error {
	return r.backend.RemoveAgents(ctx, agentIDs)
}

func (r *Registry) RunTTL(ctx context.Context) {
	// TODO(registry-ttl): evaluate distributed cleanup coordination for large
	// deployments (leader election, shard ownership, or backend advisory locks).
	if !r.ttlLoopRunning.CompareAndSwap(false, true) {
		slog.LogAttrs(ctx, slog.LevelWarn, "ttl loop already running; ignoring duplicate call",
			slog.String("method", "RunTTL"),
		)
		return
	}

	go func() {
		defer r.ttlLoopRunning.Store(false)

		timer := time.NewTimer(r.nextTTLCleanupInterval())
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if err := r.runTTLCleanup(ctx, time.Now()); err != nil && !errorsIsContextCancellation(err) {
					slog.LogAttrs(ctx, slog.LevelError, "ttl cleanup pass failed",
						slog.String("method", "runTTLCleanup"),
						slog.String("error", err.Error()),
					)
				}
				timer.Reset(r.nextTTLCleanupInterval())
			}
		}
	}()
}

func (r *Registry) runTTLCleanup(ctx context.Context, now time.Time) error {
	// TODO(registry-ttl): add metrics instrumentation:
	// ttl_cleanup_duration_ms, ttl_relays_removed_total, ttl_agents_removed_total,
	// ttl_skipped_runs_total, ttl_errors_total.
	// TODO(registry-ttl): evolve immediate deletion to optional soft TTL lifecycle
	// (ACTIVE -> STALE -> DELETING) with configurable grace period.
	// TODO(registry-ttl): replace multi-pass scans with a single-pass cleanup model
	// driven by stale-agent queries and ownership graph cascading.
	if !r.ttlCleanupInProgress.CompareAndSwap(false, true) {
		slog.LogAttrs(ctx, slog.LevelDebug, "ttl cleanup skipped; previous cleanup still in progress",
			slog.String("method", "runTTLCleanup"),
			slog.Bool("skipped_in_progress", true),
		)
		return nil
	}
	defer r.ttlCleanupInProgress.Store(false)

	start := time.Now()
	staleRelaysRemoved := 0
	staleAgentsRemoved := 0
	errs := &utils.ErrorRecorder{}
	defer func() {
		level := slog.LevelInfo
		if errs.HasErrors() {
			level = slog.LevelWarn
		}

		slog.LogAttrs(ctx, level, "ttl cleanup completed",
			slog.String("method", "runTTLCleanup"),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.Int("stale_relays_removed", staleRelaysRemoved),
			slog.Int("stale_agents_removed", staleAgentsRemoved),
			slog.Int("errors_count", errs.Len()),
			slog.Bool("skipped_in_progress", false),
		)
	}()

	relays, err := r.backend.ListRelays(ctx)
	// TODO(registry-ttl): replace full ListRelays/ListRelayAgents/ListAgents scans
	// with backend-indexed stale queries to avoid O(N) to O(N^2) chatter at scale.
	if err != nil {
		errs.Record(err)
		return errs.Err()
	}

	for _, relay := range relays {
		if now.Sub(relay.LastSeen) >= r.cfg.TTL.Relay {
			stillStale, err := r.isRelayStillStale(ctx, relay.ID, time.Now())
			if err != nil {
				errs.Record(err)
				continue
			}
			if !stillStale {
				continue
			}

			removedAgents, err := r.removeRelayAgents(ctx, relay.ID)
			if err != nil {
				errs.Record(err)
			} else {
				staleAgentsRemoved += removedAgents
			}

			if err := r.RemoveRelay(ctx, relay.ID); err != nil {
				errs.Record(err)
			} else {
				staleRelaysRemoved++
			}
			continue
		}

		relayAgents, err := r.backend.ListRelayAgents(ctx, relay.ID)
		if err != nil {
			errs.Record(err)
			continue
		}

		agentIDs := make([]string, 0, len(relayAgents))
		for _, agent := range relayAgents {
			if now.Sub(agent.LastHeartbeat) >= r.cfg.TTL.Agent {
				agentIDs = append(agentIDs, agent.ID)
			}
		}

		if len(agentIDs) > 0 {
			agentIDs, err = r.filterStillStaleAgents(ctx, agentIDs, time.Now())
			if err != nil {
				errs.Record(err)
				continue
			}
		}

		if len(agentIDs) > 0 {
			if err := r.backend.RemoveAgents(ctx, agentIDs); err != nil {
				errs.Record(err)
			} else {
				staleAgentsRemoved += len(agentIDs)
			}
		}
	}

	agents, err := r.backend.ListAgents(ctx)
	if err != nil {
		errs.Record(err)
		return errs.Err()
	}

	staleAgentIDs := make([]string, 0)
	for _, agent := range agents {
		if now.Sub(agent.LastHeartbeat) >= r.cfg.TTL.Agent {
			staleAgentIDs = append(staleAgentIDs, agent.ID)
		}
	}

	if len(staleAgentIDs) > 0 {
		staleAgentIDs, err = r.filterStillStaleAgents(ctx, staleAgentIDs, time.Now())
		if err != nil {
			errs.Record(err)
			staleAgentIDs = nil
		}
	}

	if len(staleAgentIDs) > 0 {
		if err := r.backend.RemoveAgents(ctx, staleAgentIDs); err != nil {
			errs.Record(err)
		} else {
			staleAgentsRemoved += len(staleAgentIDs)
		}
	}

	return errs.Err()
}

func (r *Registry) nextTTLCleanupInterval() time.Duration {
	// TODO(registry-ttl): make sweep cadence configurable and support adaptive or
	// backpressure-aware scheduling beyond static TTL + jitter timing.
	ttl := min(r.cfg.TTL.Relay, r.cfg.TTL.Agent)
	maxJitter := ttl / 10

	if maxJitter <= 0 {
		return ttl
	}

	return ttl + time.Duration(rand.Int64N(int64(maxJitter)+1))
}

func (r *Registry) removeRelayAgents(ctx context.Context, relayID string) (int, error) {
	agents, err := r.backend.ListRelayAgents(ctx, relayID)
	if err != nil {
		return 0, err
	}

	agentIDs := []string{}

	for _, agent := range agents {
		agentIDs = append(agentIDs, agent.ID)
	}

	if len(agentIDs) == 0 {
		return 0, nil
	}

	agentIDs, err = r.filterAgentsStillPlacedOnRelay(ctx, relayID, agentIDs)
	if err != nil {
		return 0, err
	}
	if len(agentIDs) == 0 {
		return 0, nil
	}

	if err := r.backend.RemoveAgents(ctx, agentIDs); err != nil {
		return 0, err
	}

	return len(agentIDs), nil
}

func (r *Registry) isRelayStillStale(ctx context.Context, relayID string, now time.Time) (bool, error) {
	relays, err := r.backend.ListRelays(ctx)
	if err != nil {
		return false, err
	}

	for _, relay := range relays {
		if relay.ID == relayID {
			return now.Sub(relay.LastSeen) >= r.cfg.TTL.Relay, nil
		}
	}

	return false, nil
}

func (r *Registry) filterStillStaleAgents(ctx context.Context, candidateIDs []string, now time.Time) ([]string, error) {
	if len(candidateIDs) == 0 {
		return nil, nil
	}

	agents, err := r.backend.ListAgents(ctx)
	if err != nil {
		return nil, err
	}

	candidates := make(map[string]struct{}, len(candidateIDs))
	for _, id := range candidateIDs {
		candidates[id] = struct{}{}
	}

	stale := make([]string, 0, len(candidateIDs))
	for _, agent := range agents {
		if _, ok := candidates[agent.ID]; !ok {
			continue
		}
		if now.Sub(agent.LastHeartbeat) >= r.cfg.TTL.Agent {
			stale = append(stale, agent.ID)
		}
	}

	return stale, nil
}

func (r *Registry) filterAgentsStillPlacedOnRelay(ctx context.Context, relayID string, candidateIDs []string) ([]string, error) {
	if len(candidateIDs) == 0 {
		return nil, nil
	}

	errs := &utils.ErrorRecorder{}
	filtered := make([]string, 0, len(candidateIDs))

	for _, agentID := range candidateIDs {
		// TODO(registry-ttl): use batch placement lookups to avoid per-agent
		// GetAgentPlacement roundtrips in large relay fan-outs.
		placement, err := r.backend.GetAgentPlacement(ctx, agentID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			errs.Record(err)
			continue
		}

		if placement.RelayID == relayID {
			filtered = append(filtered, agentID)
		}
	}

	return filtered, errs.Err()
}

func errorsIsContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (r *Registry) Close(ctx context.Context) error {
	return r.backend.Close(ctx)
}
