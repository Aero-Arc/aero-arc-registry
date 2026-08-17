package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/memory"
	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestConformanceProjectionHandlers(t *testing.T) {
	backend, err := memory.New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	server := newTransportTestServer(t, backend)
	ctx := context.Background()
	summary := &conformancev1.ConformanceSummary{
		AssignmentId: "assignment-1", AssignmentGeneration: 7, EvaluationRevision: 2,
		EvaluationId: "evaluation-2", AircraftId: "aircraft-1", FlightId: "flight-1",
		IntentId: "intent-1", IntentVersion: 1,
		Condition:        conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_CONFORMING,
		MonitoringStatus: conformancev1.MonitoringStatus_MONITORING_STATUS_CURRENT,
		RecordingStatus:  conformancev1.RecordingStatus_RECORDING_STATUS_CONFIRMED,
		ObservedAt:       timestamppb.New(time.Now()), FrameId: "frame-2",
	}
	published, err := server.PublishConformanceSummary(ctx, &registryv1.PublishConformanceSummaryRequest{Summary: summary})
	if err != nil || published.GetDisposition() != registryv1.ConformancePublishDisposition_CONFORMANCE_PUBLISH_DISPOSITION_APPLIED {
		t.Fatalf("PublishConformanceSummary() = %+v, %v", published, err)
	}
	read, err := server.GetConformanceSummary(ctx, &registryv1.GetConformanceSummaryRequest{AssignmentId: "assignment-1"})
	if err != nil || read.GetProjection().GetSummary().GetEvaluationRevision() != 2 {
		t.Fatalf("GetConformanceSummary() = %+v, %v", read, err)
	}
	batch, err := server.BatchGetConformanceSummaries(ctx, &registryv1.BatchGetConformanceSummariesRequest{AssignmentIds: []string{"missing", "assignment-1", "assignment-1"}})
	if err != nil || len(batch.GetProjections()) != 1 || len(batch.GetMissingAssignmentIds()) != 1 || batch.GetMissingAssignmentIds()[0] != "missing" {
		t.Fatalf("BatchGetConformanceSummaries() = %+v, %v", batch, err)
	}

	stale := proto.Clone(summary).(*conformancev1.ConformanceSummary)
	stale.EvaluationRevision = 1
	if _, err := server.PublishConformanceSummary(ctx, &registryv1.PublishConformanceSummaryRequest{Summary: stale}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale PublishConformanceSummary() error = %v", err)
	}
	if _, err := server.PublishConformanceSummary(ctx, &registryv1.PublishConformanceSummaryRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing summary error = %v", err)
	}
}
