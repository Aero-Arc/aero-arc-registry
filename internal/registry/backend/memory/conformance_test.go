package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

func TestConformanceProjectionFencingAndExpiry(t *testing.T) {
	backend, err := New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	summary := testConformanceSummary("assignment-1", 7, 2)
	projection, disposition, err := backend.PublishConformanceSummary(ctx, summary, 20*time.Millisecond, time.Second)
	if err != nil || disposition != registry.PublishApplied || projection.ExpiresAt.Sub(projection.StoredAt) != 20*time.Millisecond {
		t.Fatalf("initial publish = %+v, %q, %v", projection, disposition, err)
	}
	if _, disposition, err = backend.PublishConformanceSummary(ctx, summary, 20*time.Millisecond, time.Second); err != nil || disposition != registry.PublishIdempotent {
		t.Fatalf("exact retry disposition = %q, error = %v", disposition, err)
	}

	conflict := summary
	conflict.EvaluationID = "changed"
	if _, _, err = backend.PublishConformanceSummary(ctx, conflict, 20*time.Millisecond, time.Second); !errors.Is(err, registry.ErrConflict) {
		t.Fatalf("same cursor conflict error = %v", err)
	}
	stale := testConformanceSummary("assignment-1", 7, 1)
	if _, _, err = backend.PublishConformanceSummary(ctx, stale, 20*time.Millisecond, time.Second); !errors.Is(err, registry.ErrStale) {
		t.Fatalf("lower revision error = %v", err)
	}

	time.Sleep(25 * time.Millisecond)
	if _, err = backend.GetConformanceSummary(ctx, "assignment-1"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("expired projection error = %v", err)
	}
	if _, _, err = backend.PublishConformanceSummary(ctx, stale, 20*time.Millisecond, time.Second); !errors.Is(err, registry.ErrStale) {
		t.Fatalf("expired projection resurrected stale cursor: %v", err)
	}

	newGeneration := testConformanceSummary("assignment-1", 8, 1)
	if _, disposition, err = backend.PublishConformanceSummary(ctx, newGeneration, time.Second, 2*time.Second); err != nil || disposition != registry.PublishApplied {
		t.Fatalf("new generation disposition = %q, error = %v", disposition, err)
	}
}

func TestBatchGetConformanceSummaries(t *testing.T) {
	backend, err := New(&registry.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []string{"assignment-b", "assignment-a"} {
		if _, _, err := backend.PublishConformanceSummary(ctx, testConformanceSummary(id, 1, 1), time.Second, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	projections, missing, err := backend.BatchGetConformanceSummaries(ctx, []string{"assignment-a", "assignment-b", "assignment-c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != 2 || projections[0].Summary.AssignmentID != "assignment-a" || projections[1].Summary.AssignmentID != "assignment-b" {
		t.Fatalf("projections = %+v", projections)
	}
	if len(missing) != 1 || missing[0] != "assignment-c" {
		t.Fatalf("missing = %v", missing)
	}
}

func testConformanceSummary(id string, generation, revision uint64) registry.ConformanceSummary {
	return registry.ConformanceSummary{
		AssignmentID: id, AssignmentGeneration: generation, EvaluationRevision: revision,
		EvaluationID: id + "-evaluation", AircraftID: "aircraft-1", FlightID: "flight-1",
		IntentID: "intent-1", IntentVersion: 1, Condition: "conforming",
		MonitoringStatus: "current", RecordingStatus: "confirmed",
		ObservedAt: time.Now().UTC(), FrameID: "frame-1",
	}
}
