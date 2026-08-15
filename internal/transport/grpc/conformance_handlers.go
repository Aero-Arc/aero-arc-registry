package grpc

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PublishConformanceSummary validates and stores a TTL-scoped live projection
// behind Registry's assignment-generation and evaluation-revision fences.
//
// Parameters:
//   - ctx: controls cancellation and the backend mutation.
//   - req: contains the current summary published by Conformance.
//
// Returns:
//   - response: reports whether the cursor advanced or was an exact retry.
//   - error: maps invalid, stale, conflicting, and dependency failures to gRPC status.
func (s *Server) PublishConformanceSummary(ctx context.Context, req *registryv1.PublishConformanceSummaryRequest) (*registryv1.PublishConformanceSummaryResponse, error) {
	if req.GetSummary() == nil {
		return nil, status.Error(codes.InvalidArgument, "summary is required")
	}
	summary, err := conformanceSummaryFromProto(req.GetSummary())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	projection, disposition, err := s.registry.PublishConformanceSummary(ctx, summary)
	if err != nil {
		return nil, toStatusError(err)
	}
	protoDisposition := registryv1.ConformancePublishDisposition_CONFORMANCE_PUBLISH_DISPOSITION_APPLIED
	if disposition == registry.PublishIdempotent {
		protoDisposition = registryv1.ConformancePublishDisposition_CONFORMANCE_PUBLISH_DISPOSITION_IDEMPOTENT
	}
	return &registryv1.PublishConformanceSummaryResponse{
		Disposition: protoDisposition,
		Projection:  conformanceProjectionToProto(projection),
	}, nil
}

// GetConformanceSummary returns the current non-expired projection for one assignment.
//
// Parameters:
//   - ctx: controls cancellation and the backend read.
//   - req: identifies the logical assignment.
//
// Returns:
//   - response: contains current state plus Registry-owned receipt and expiry timestamps.
//   - error: maps absent, invalid, and dependency failures to gRPC status.
func (s *Server) GetConformanceSummary(ctx context.Context, req *registryv1.GetConformanceSummaryRequest) (*registryv1.GetConformanceSummaryResponse, error) {
	projection, err := s.registry.GetConformanceSummary(ctx, req.GetAssignmentId())
	if err != nil {
		return nil, toStatusError(err)
	}
	return &registryv1.GetConformanceSummaryResponse{Projection: conformanceProjectionToProto(projection)}, nil
}

// BatchGetConformanceSummaries returns current projections for up to 250
// logical assignments without converting Registry into a streaming relay.
//
// Parameters:
//   - ctx: controls cancellation and the backend batch read.
//   - req: supplies assignment IDs; duplicates are collapsed.
//
// Returns:
//   - response: separates live projections from absent or expired IDs.
//   - error: maps invalid and dependency failures to gRPC status.
func (s *Server) BatchGetConformanceSummaries(ctx context.Context, req *registryv1.BatchGetConformanceSummariesRequest) (*registryv1.BatchGetConformanceSummariesResponse, error) {
	projections, missing, err := s.registry.BatchGetConformanceSummaries(ctx, req.GetAssignmentIds())
	if err != nil {
		return nil, toStatusError(err)
	}
	response := &registryv1.BatchGetConformanceSummariesResponse{
		Projections:          make([]*registryv1.ConformanceProjection, len(projections)),
		MissingAssignmentIds: missing,
	}
	for index, projection := range projections {
		response.Projections[index] = conformanceProjectionToProto(projection)
	}
	return response, nil
}

func conformanceSummaryFromProto(summary *conformancev1.ConformanceSummary) (registry.ConformanceSummary, error) {
	condition, ok := conditionFromProto[summary.GetCondition()]
	if !ok {
		return registry.ConformanceSummary{}, fmt.Errorf("condition is invalid")
	}
	monitoring, ok := monitoringFromProto[summary.GetMonitoringStatus()]
	if !ok {
		return registry.ConformanceSummary{}, fmt.Errorf("monitoring status is invalid")
	}
	recording, ok := recordingFromProto[summary.GetRecordingStatus()]
	if !ok {
		return registry.ConformanceSummary{}, fmt.Errorf("recording status is invalid")
	}
	if summary.GetObservedAt() == nil || summary.GetObservedAt().CheckValid() != nil {
		return registry.ConformanceSummary{}, fmt.Errorf("observed_at is required and must be valid")
	}
	result := registry.ConformanceSummary{
		AssignmentID:         summary.GetAssignmentId(),
		AssignmentGeneration: summary.GetAssignmentGeneration(),
		EvaluationRevision:   summary.GetEvaluationRevision(),
		EvaluationID:         summary.GetEvaluationId(),
		OperatorID:           summary.GetOperatorId(),
		AircraftID:           summary.GetAircraftId(),
		FlightID:             summary.GetFlightId(),
		IntentID:             summary.GetIntentId(),
		IntentVersion:        summary.GetIntentVersion(),
		Condition:            condition,
		MonitoringStatus:     monitoring,
		RecordingStatus:      recording,
		ObservedAt:           summary.GetObservedAt().AsTime(),
		FrameID:              summary.GetFrameId(),
		Violations:           make([]registry.ViolationSummary, 0, len(summary.GetViolations())),
	}
	for _, violation := range summary.GetViolations() {
		if violation == nil {
			return registry.ConformanceSummary{}, fmt.Errorf("violations cannot contain nil values")
		}
		violationType, ok := violationFromProto[violation.GetViolationType()]
		if !ok {
			return registry.ConformanceSummary{}, fmt.Errorf("violation type is invalid")
		}
		phase, ok := phaseFromProto[violation.GetPhase()]
		if !ok {
			return registry.ConformanceSummary{}, fmt.Errorf("incident phase is invalid")
		}
		openedAt, err := optionalProtoTime(violation.GetOpenedAt())
		if err != nil {
			return registry.ConformanceSummary{}, fmt.Errorf("violation opened_at: %w", err)
		}
		lastObservedAt, err := optionalProtoTime(violation.GetLastObservedAt())
		if err != nil {
			return registry.ConformanceSummary{}, fmt.Errorf("violation last_observed_at: %w", err)
		}
		result.Violations = append(result.Violations, registry.ViolationSummary{
			ViolationType: violationType, Phase: phase,
			OpeningFrameID: violation.GetOpeningFrameId(), OpenedAt: openedAt,
			LastObservedAt: lastObservedAt, WorstDeviation: violation.GetWorstDeviationM(),
		})
	}
	sort.Slice(result.Violations, func(i, j int) bool { return result.Violations[i].ViolationType < result.Violations[j].ViolationType })
	return result, nil
}

func conformanceProjectionToProto(projection registry.ConformanceProjection) *registryv1.ConformanceProjection {
	return &registryv1.ConformanceProjection{
		Summary:   conformanceSummaryToProto(projection.Summary),
		StoredAt:  timestamppb.New(projection.StoredAt),
		ExpiresAt: timestamppb.New(projection.ExpiresAt),
	}
}

func conformanceSummaryToProto(summary registry.ConformanceSummary) *conformancev1.ConformanceSummary {
	result := &conformancev1.ConformanceSummary{
		AssignmentId: summary.AssignmentID, AssignmentGeneration: summary.AssignmentGeneration,
		EvaluationRevision: summary.EvaluationRevision, EvaluationId: summary.EvaluationID,
		OperatorId: summary.OperatorID, AircraftId: summary.AircraftID, FlightId: summary.FlightID,
		IntentId: summary.IntentID, IntentVersion: summary.IntentVersion,
		Condition: conditionToProto[summary.Condition], MonitoringStatus: monitoringToProto[summary.MonitoringStatus],
		RecordingStatus: recordingToProto[summary.RecordingStatus], ObservedAt: timestamppb.New(summary.ObservedAt),
		FrameId: summary.FrameID, Violations: make([]*conformancev1.ViolationSummary, len(summary.Violations)),
	}
	for index, violation := range summary.Violations {
		result.Violations[index] = &conformancev1.ViolationSummary{
			ViolationType: violationToProto[violation.ViolationType], Phase: phaseToProto[violation.Phase],
			OpeningFrameId: violation.OpeningFrameID, OpenedAt: optionalTimeProto(violation.OpenedAt),
			LastObservedAt: optionalTimeProto(violation.LastObservedAt), WorstDeviationM: violation.WorstDeviation,
		}
	}
	return result
}

func optionalProtoTime(value *timestamppb.Timestamp) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	if err := value.CheckValid(); err != nil {
		return time.Time{}, err
	}
	return value.AsTime(), nil
}

func optionalTimeProto(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}

var conditionFromProto = map[conformancev1.ConformanceCondition]string{
	conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_UNKNOWN:        "unknown",
	conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_CONFORMING:     "conforming",
	conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_SUSPECTED:      "suspected",
	conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_NON_CONFORMING: "non_conforming",
	conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_RECOVERING:     "recovering",
}
var conditionToProto = reverseConditionMap(conditionFromProto)
var monitoringFromProto = map[conformancev1.MonitoringStatus]string{
	conformancev1.MonitoringStatus_MONITORING_STATUS_RECEIVED:    "received",
	conformancev1.MonitoringStatus_MONITORING_STATUS_ARMED:       "armed",
	conformancev1.MonitoringStatus_MONITORING_STATUS_CURRENT:     "current",
	conformancev1.MonitoringStatus_MONITORING_STATUS_STALE:       "stale",
	conformancev1.MonitoringStatus_MONITORING_STATUS_UNAVAILABLE: "unavailable",
}
var monitoringToProto = reverseMonitoringMap(monitoringFromProto)
var recordingFromProto = map[conformancev1.RecordingStatus]string{
	conformancev1.RecordingStatus_RECORDING_STATUS_PENDING:   "pending",
	conformancev1.RecordingStatus_RECORDING_STATUS_CONFIRMED: "confirmed",
	conformancev1.RecordingStatus_RECORDING_STATUS_DEGRADED:  "degraded",
}
var recordingToProto = reverseRecordingMap(recordingFromProto)
var violationFromProto = map[conformancev1.ViolationType]string{
	conformancev1.ViolationType_VIOLATION_TYPE_LATERAL_DEVIATION:  "lateral_deviation",
	conformancev1.ViolationType_VIOLATION_TYPE_ALTITUDE_DEVIATION: "altitude_deviation",
	conformancev1.ViolationType_VIOLATION_TYPE_TEMPORAL_DEVIATION: "temporal_deviation",
	conformancev1.ViolationType_VIOLATION_TYPE_TELEMETRY_LOSS:     "telemetry_loss",
}
var violationToProto = reverseViolationMap(violationFromProto)
var phaseFromProto = map[conformancev1.IncidentPhase]string{
	conformancev1.IncidentPhase_INCIDENT_PHASE_CLEAR:      "clear",
	conformancev1.IncidentPhase_INCIDENT_PHASE_SUSPECTED:  "suspected",
	conformancev1.IncidentPhase_INCIDENT_PHASE_OPEN:       "open",
	conformancev1.IncidentPhase_INCIDENT_PHASE_RECOVERING: "recovering",
}
var phaseToProto = reversePhaseMap(phaseFromProto)

func reverseConditionMap(input map[conformancev1.ConformanceCondition]string) map[string]conformancev1.ConformanceCondition {
	result := make(map[string]conformancev1.ConformanceCondition, len(input))
	for key, value := range input {
		result[value] = key
	}
	return result
}
func reverseMonitoringMap(input map[conformancev1.MonitoringStatus]string) map[string]conformancev1.MonitoringStatus {
	result := make(map[string]conformancev1.MonitoringStatus, len(input))
	for key, value := range input {
		result[value] = key
	}
	return result
}
func reverseRecordingMap(input map[conformancev1.RecordingStatus]string) map[string]conformancev1.RecordingStatus {
	result := make(map[string]conformancev1.RecordingStatus, len(input))
	for key, value := range input {
		result[value] = key
	}
	return result
}
func reverseViolationMap(input map[conformancev1.ViolationType]string) map[string]conformancev1.ViolationType {
	result := make(map[string]conformancev1.ViolationType, len(input))
	for key, value := range input {
		result[value] = key
	}
	return result
}
func reversePhaseMap(input map[conformancev1.IncidentPhase]string) map[string]conformancev1.IncidentPhase {
	result := make(map[string]conformancev1.IncidentPhase, len(input))
	for key, value := range input {
		result[value] = key
	}
	return result
}
