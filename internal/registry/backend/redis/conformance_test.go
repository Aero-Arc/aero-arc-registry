package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

func TestConformanceProjectionUsesAtomicRedisFence(t *testing.T) {
	backend, server := newTestBackend(t, time.Second, time.Second)
	ctx := context.Background()
	summary := redisTestConformanceSummary("assignment-1", 7, 2)
	projection, disposition, err := backend.PublishConformanceSummary(ctx, summary, 100*time.Millisecond, 10*time.Second)
	if err != nil || disposition != registry.PublishApplied || projection.StoredAt.IsZero() || projection.ExpiresAt.Sub(projection.StoredAt) != 100*time.Millisecond {
		t.Fatalf("initial publish = %+v, %q, %v", projection, disposition, err)
	}
	if _, disposition, err = backend.PublishConformanceSummary(ctx, summary, 100*time.Millisecond, 10*time.Second); err != nil || disposition != registry.PublishIdempotent {
		t.Fatalf("exact retry = %q, %v", disposition, err)
	}

	conflict := summary
	conflict.FrameID = "changed"
	if _, _, err = backend.PublishConformanceSummary(ctx, conflict, 100*time.Millisecond, 10*time.Second); !errors.Is(err, registry.ErrConflict) {
		t.Fatalf("same cursor conflict error = %v", err)
	}
	server.FastForward(200 * time.Millisecond)
	if _, err = backend.GetConformanceSummary(ctx, summary.AssignmentID); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("expired projection error = %v", err)
	}
	stale := redisTestConformanceSummary("assignment-1", 7, 1)
	if _, _, err = backend.PublishConformanceSummary(ctx, stale, 100*time.Millisecond, 10*time.Second); !errors.Is(err, registry.ErrStale) {
		t.Fatalf("longer fence did not reject stale revision: %v", err)
	}
	newGeneration := redisTestConformanceSummary("assignment-1", 8, 1)
	if _, disposition, err = backend.PublishConformanceSummary(ctx, newGeneration, time.Second, 10*time.Second); err != nil || disposition != registry.PublishApplied {
		t.Fatalf("new generation = %q, %v", disposition, err)
	}
}

func redisTestConformanceSummary(id string, generation, revision uint64) registry.ConformanceSummary {
	return registry.ConformanceSummary{
		AssignmentID: id, AssignmentGeneration: generation, EvaluationRevision: revision,
		EvaluationID: "evaluation-1", AircraftID: "aircraft-1", FlightID: "flight-1",
		IntentID: "intent-1", IntentVersion: 1, Condition: "non_conforming",
		MonitoringStatus: "current", RecordingStatus: "confirmed",
		ObservedAt: time.Unix(1_786_468_800, 123).UTC(), FrameID: "frame-1",
		Violations: []registry.ViolationSummary{{ViolationType: "lateral_deviation", Phase: "open", WorstDeviation: 12}},
	}
}
