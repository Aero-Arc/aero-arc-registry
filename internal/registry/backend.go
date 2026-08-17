package registry

import (
	"context"
	"time"
)

// Backend defines the persistence and coordination contract
// required by the registry control plane.
type Backend interface {
	// Relay lifecycle
	RegisterRelay(ctx context.Context, relay Relay) error
	HeartbeatRelay(ctx context.Context, relayID string) error
	ListRelays(ctx context.Context) ([]Relay, error)
	// TODO(registry-ttl): add indexed stale query APIs for scale:
	// ListStaleRelays(ctx context.Context, before time.Time) ([]Relay, error)

	// Agent lifecycle
	RegisterAgent(ctx context.Context, agent Agent, relayID string) error
	HeartbeatAgent(ctx context.Context, agentID, expectedRelayID string) error
	GetAgentPlacement(ctx context.Context, agentID string) (*AgentPlacement, error)
	ListAgents(ctx context.Context) ([]Agent, error)
	// TODO(registry-ttl): add indexed stale query + batch placement APIs:
	// ListStaleAgents(ctx context.Context, before time.Time) ([]Agent, error)
	// GetAgentPlacements(ctx context.Context, agentIDs []string) (map[string]*AgentPlacement, error)

	// Control Plane Helpers
	ListRelayAgents(ctx context.Context, relayID string) ([]*Agent, error)
	RemoveAgents(ctx context.Context, agentIDs []string) error
	RemoveRelay(ctx context.Context, relayID string) error

	// Shutdown
	Close(ctx context.Context) error
}

// ConformanceBackend is the optional backend capability for fenced, TTL-scoped
// current Conformance projections. Backends that do not implement it return an
// Unimplemented transport status without weakening their existing contracts.
type ConformanceBackend interface {
	PublishConformanceSummary(ctx context.Context, summary ConformanceSummary, projectionTTL, fenceTTL time.Duration) (ConformanceProjection, PublishDisposition, error)
	GetConformanceSummary(ctx context.Context, assignmentID string) (ConformanceProjection, error)
	BatchGetConformanceSummaries(ctx context.Context, assignmentIDs []string) ([]ConformanceProjection, []string, error)
}

// TTLManagedBackend is an optional backend capability indicating that entity
// expiry is enforced atomically by the backend using its own clock. The
// registry service must not apply its local-clock TTL sweep to these backends.
type TTLManagedBackend interface {
	ManagesTTL() bool
}

// Relay represents a relay instance registered with the registry.
type Relay struct {
	ID       string
	Address  string
	GRPCPort int32
	LastSeen time.Time
}

// Agent represents an agent (e.g. drone or edge process)
type Agent struct {
	ID            string
	LastHeartbeat time.Time
}

// AgentPlacement represents the association between an agent and a relay.
type AgentPlacement struct {
	AgentID   string
	RelayID   string
	UpdatedAt time.Time
}

// PublishDisposition describes whether a Registry publication advanced the
// assignment fence or repeated the exact current evaluation.
type PublishDisposition string

const (
	PublishApplied    PublishDisposition = "applied"
	PublishIdempotent PublishDisposition = "idempotent"
)

// ConformanceSummary is one replaceable live-state projection. Assignment
// generation and evaluation revision are independent monotonic fences.
type ConformanceSummary struct {
	AssignmentID         string
	AssignmentGeneration uint64
	EvaluationRevision   uint64
	EvaluationID         string
	OperatorID           string
	AircraftID           string
	FlightID             string
	IntentID             string
	IntentVersion        uint32
	Condition            string
	MonitoringStatus     string
	RecordingStatus      string
	ObservedAt           time.Time
	FrameID              string
	Violations           []ViolationSummary
}

// ViolationSummary is current hysteresis state for one violation axis.
type ViolationSummary struct {
	ViolationType  string
	Phase          string
	OpeningFrameID string
	OpenedAt       time.Time
	LastObservedAt time.Time
	WorstDeviation float64
}

// ConformanceProjection adds Registry-owned receipt and expiry timestamps to a
// current Conformance summary.
type ConformanceProjection struct {
	Summary   ConformanceSummary
	StoredAt  time.Time
	ExpiresAt time.Time
}
