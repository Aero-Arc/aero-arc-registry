package utils

import (
	"errors"
	"testing"
)

func TestErrorRecorderRecordAndJoin(t *testing.T) {
	var rec ErrorRecorder

	rec.Record(nil)
	rec.Record(errors.New("first error"))
	rec.Record(errors.New("second error"))

	if !rec.HasErrors() {
		t.Fatal("expected HasErrors to be true")
	}

	if got, want := rec.Len(), 2; got != want {
		t.Fatalf("unexpected length: got %d want %d", got, want)
	}

	if got, want := rec.Join(), "first error\nsecond error"; got != want {
		t.Fatalf("unexpected join output: got %q want %q", got, want)
	}
}

func TestErrorRecorderErrorsReturnsCopy(t *testing.T) {
	var rec ErrorRecorder
	rec.Record(errors.New("one"))

	errs := rec.Errors()
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}

	errs[0] = errors.New("mutated")

	if got, want := rec.Join(), "one"; got != want {
		t.Fatalf("recorder changed after external mutation: got %q want %q", got, want)
	}
}

func TestErrorRecorderErrAndReset(t *testing.T) {
	var rec ErrorRecorder

	if rec.Err() != nil {
		t.Fatal("expected nil Err when empty")
	}

	rec.Record(errors.New("one"))
	rec.Record(errors.New("two"))

	if rec.Err() == nil {
		t.Fatal("expected non-nil Err when populated")
	}

	rec.Reset()

	if rec.HasErrors() {
		t.Fatal("expected HasErrors to be false after reset")
	}
	if rec.Len() != 0 {
		t.Fatalf("expected length 0 after reset, got %d", rec.Len())
	}
	if got := rec.Join(); got != "" {
		t.Fatalf("expected empty Join after reset, got %q", got)
	}
	if rec.Err() != nil {
		t.Fatal("expected nil Err after reset")
	}
}
