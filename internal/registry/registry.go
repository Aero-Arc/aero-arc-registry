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
// are implemented at the registry/backend boundary. Backends with a native
// clock and atomic expiry own TTL enforcement; other backends use the registry
// service's cleanup loop.
//
// The registry exposes its functionality over gRPC and is intended to be
// deployed as a standalone, horizontally scalable control plane service.
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
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

// New constructs the Registry service over a liveness/placement backend after
// validating all backend and TTL configuration.
//
// Parameters:
//   - cfg: provides the configuration values used to initialize or execute the operation.
//   - backend: persists current Relay and Agent liveness and placement.
//
// Returns:
//   - registry: is ready to serve requests; its optional TTL loop is not started.
//   - error: reports a nil or invalid configuration.
func New(cfg *Config, backend Backend) (*Registry, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}

	normalized := *cfg
	normalized.TTL = normalized.TTL.WithDefaults()
	if err := normalized.Validate(); err != nil {
		return nil, err
	}

	aeroRegistry := &Registry{
		cfg:     &normalized,
		backend: backend,
	}

	return aeroRegistry, nil
}

// RegisterRelay creates or refreshes a Relay's current liveness record.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relay: is the Relay value supplied to RegisterRelay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) RegisterRelay(ctx context.Context, relay Relay) error {
	// TODO(registry-ttl): make registry-owned timestamps authoritative by setting
	// relay.LastSeen here before persisting, instead of trusting external clocks.
	return r.backend.RegisterRelay(ctx, relay)
}

// HeartbeatRelay renews one existing Relay without changing its endpoint or incarnation.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) HeartbeatRelay(ctx context.Context, relayID string) error {
	// TODO(registry-ttl): move heartbeat timestamp source of truth to registry
	// write path (backend should persist registry-assigned time).
	return r.backend.HeartbeatRelay(ctx, relayID)
}

// ListRelays returns the currently live Relays in deterministic identifier order.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the []Relay value produced by ListRelays.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) ListRelays(ctx context.Context) ([]Relay, error) {
	return r.backend.ListRelays(ctx)
}

// RemoveRelay removes a Relay liveness record. Backends also fence or omit Agent
// placements whose owning Relay is no longer live.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) RemoveRelay(ctx context.Context, relayID string) error {
	return r.backend.RemoveRelay(ctx, relayID)
}

// RegisterAgent atomically creates or refreshes an Agent placement on a live
// Relay. Registering against another Relay is the only supported ownership move.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agent: is the Agent value supplied to RegisterAgent.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) RegisterAgent(ctx context.Context, agent Agent, relayID string) error {
	return r.backend.RegisterAgent(ctx, agent, relayID)
}

// HeartbeatAgent renews an Agent only when expectedRelayID still owns its live
// placement; a stale Relay cannot extend or move another Relay's Agent.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentID: identifies the target agent.
//   - expectedRelayID: fences renewal to the Relay that believes it owns the Agent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) HeartbeatAgent(ctx context.Context, agentID, expectedRelayID string) error {
	// TODO(registry-ttl): move heartbeat timestamp source of truth to registry
	// write path (backend should persist registry-assigned time).
	return r.backend.HeartbeatAgent(ctx, agentID, expectedRelayID)
}

// GetAgentPlacement returns the Agent's current live Relay placement.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentID: identifies the target agent.
//
// Returns:
//   - result: is the *AgentPlacement value produced by GetAgentPlacement.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) GetAgentPlacement(ctx context.Context, agentID string) (*AgentPlacement, error) {
	return r.backend.GetAgentPlacement(ctx, agentID)
}

// ListAgents returns Agents with live owning Relays in deterministic identifier order.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the []Agent value produced by ListAgents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) ListAgents(ctx context.Context) ([]Agent, error) {
	return r.backend.ListAgents(ctx)
}

// ListRelayAgents returns the live Agents currently owned by one live Relay.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - result: is the []*Agent value produced by ListRelayAgents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) ListRelayAgents(ctx context.Context, relayID string) ([]*Agent, error) {
	return r.backend.ListRelayAgents(ctx, relayID)
}

// RemoveAgents removes the selected Agent records, placements, and membership indexes.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentIDs: identifies every Agent to remove in one backend operation.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *Registry) RemoveAgents(ctx context.Context, agentIDs []string) error {
	return r.backend.RemoveAgents(ctx, agentIDs)
}

// PublishConformanceSummary validates and atomically publishes a current live
// projection behind independent assignment-generation and evaluation-revision
// fences. The backend owns receipt time and TTL enforcement.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - summary: contains the current authoritative assignment evaluation.
//
// Returns:
//   - projection: contains Registry-owned storage and expiry timestamps.
//   - disposition: distinguishes an advancing publication from an exact retry.
//   - error: reports invalid input, stale/conflicting fences, or backend failure.
func (r *Registry) PublishConformanceSummary(ctx context.Context, summary ConformanceSummary) (ConformanceProjection, PublishDisposition, error) {
	if err := validateConformanceSummary(summary); err != nil {
		return ConformanceProjection{}, "", err
	}
	backend, ok := r.backend.(ConformanceBackend)
	if !ok {
		return ConformanceProjection{}, "", ErrNotImplemented
	}
	return backend.PublishConformanceSummary(ctx, summary, r.cfg.TTL.Conformance, r.cfg.TTL.ConformanceFence)
}

// GetConformanceSummary returns one non-expired current projection.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - assignmentID: identifies the logical assignment independently of generation.
//
// Returns:
//   - projection: is the newest live summary accepted for the assignment.
//   - error: wraps ErrNotFound after expiry and reports backend failures.
func (r *Registry) GetConformanceSummary(ctx context.Context, assignmentID string) (ConformanceProjection, error) {
	if strings.TrimSpace(assignmentID) == "" {
		return ConformanceProjection{}, fmt.Errorf("assignment ID is required: %w", ErrInvalid)
	}
	backend, ok := r.backend.(ConformanceBackend)
	if !ok {
		return ConformanceProjection{}, ErrNotImplemented
	}
	return backend.GetConformanceSummary(ctx, assignmentID)
}

// BatchGetConformanceSummaries returns live projections in deterministic
// assignment-ID order and separately reports requested IDs that are absent or
// expired. Duplicate IDs are collapsed and a request is capped at 250 IDs.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - assignmentIDs: selects logical assignments independently of generation.
//
// Returns:
//   - projections: contains every live requested projection in ID order.
//   - missing: contains absent or expired IDs in deterministic order.
//   - error: reports invalid input or backend failure.
func (r *Registry) BatchGetConformanceSummaries(ctx context.Context, assignmentIDs []string) ([]ConformanceProjection, []string, error) {
	if len(assignmentIDs) == 0 || len(assignmentIDs) > 250 {
		return nil, nil, fmt.Errorf("one to 250 assignment IDs are required: %w", ErrInvalid)
	}
	unique := make(map[string]struct{}, len(assignmentIDs))
	for _, assignmentID := range assignmentIDs {
		assignmentID = strings.TrimSpace(assignmentID)
		if assignmentID == "" {
			return nil, nil, fmt.Errorf("assignment IDs cannot be empty: %w", ErrInvalid)
		}
		unique[assignmentID] = struct{}{}
	}
	ids := make([]string, 0, len(unique))
	for assignmentID := range unique {
		ids = append(ids, assignmentID)
	}
	sort.Strings(ids)
	backend, ok := r.backend.(ConformanceBackend)
	if !ok {
		return nil, nil, ErrNotImplemented
	}
	return backend.BatchGetConformanceSummaries(ctx, ids)
}

func validateConformanceSummary(summary ConformanceSummary) error {
	if strings.TrimSpace(summary.AssignmentID) == "" || summary.AssignmentGeneration == 0 || summary.EvaluationRevision == 0 || strings.TrimSpace(summary.EvaluationID) == "" || strings.TrimSpace(summary.AircraftID) == "" || strings.TrimSpace(summary.FlightID) == "" || strings.TrimSpace(summary.IntentID) == "" || summary.IntentVersion == 0 || summary.ObservedAt.IsZero() || strings.TrimSpace(summary.FrameID) == "" {
		return fmt.Errorf("conformance summary identity and cursor are required: %w", ErrInvalid)
	}
	if summary.Condition == "" || summary.MonitoringStatus == "" || summary.RecordingStatus == "" {
		return fmt.Errorf("conformance status axes are required: %w", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(summary.Violations))
	for _, violation := range summary.Violations {
		if violation.ViolationType == "" || violation.Phase == "" || math.IsNaN(violation.WorstDeviation) || math.IsInf(violation.WorstDeviation, 0) || violation.WorstDeviation < 0 {
			return fmt.Errorf("conformance violation is invalid: %w", ErrInvalid)
		}
		if _, exists := seen[violation.ViolationType]; exists {
			return fmt.Errorf("duplicate conformance violation %q: %w", violation.ViolationType, ErrInvalid)
		}
		seen[violation.ViolationType] = struct{}{}
	}
	return nil
}

// RunTTL starts the service-side expiry sweeper when the backend does not own
// native TTL enforcement. Duplicate calls are ignored, and Redis-backed
// registries return immediately because Redis server time is authoritative.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
func (r *Registry) RunTTL(ctx context.Context) {
	if r.backendManagesTTL() {
		slog.LogAttrs(ctx, slog.LevelDebug, "ttl loop disabled; backend manages expiry",
			slog.String("method", "RunTTL"),
		)
		return
	}

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
	// A native-TTL backend owns both the time authority and expiry decision.
	// Comparing its timestamps to this process's clock can delete records that
	// were freshly renewed when the two clocks are skewed.
	if r.backendManagesTTL() {
		return nil
	}

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

func (r *Registry) backendManagesTTL() bool {
	backend, ok := r.backend.(TTLManagedBackend)
	return ok && backend.ManagesTTL()
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

// Close releases backend connections owned by the Registry.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - error: reports context cancellation or backend shutdown failure.
func (r *Registry) Close(ctx context.Context) error {
	return r.backend.Close(ctx)
}
