package utils

import (
	"errors"
	"strings"
	"sync"
)

// ErrorRecorder accumulates non-nil errors in insertion order.
type ErrorRecorder struct {
	mu   sync.RWMutex
	errs []error
}

// Record adds a non-nil error to the recorder.
func (r *ErrorRecorder) Record(err error) {
	if err == nil {
		return
	}

	r.mu.Lock()
	r.errs = append(r.errs, err)
	r.mu.Unlock()
}

// HasErrors returns true when at least one error has been recorded.
func (r *ErrorRecorder) HasErrors() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.errs) > 0
}

// Len returns the number of recorded errors.
func (r *ErrorRecorder) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.errs)
}

// Errors returns a copy of the recorded error slice.
func (r *ErrorRecorder) Errors() []error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]error, len(r.errs))
	copy(out, r.errs)
	return out
}

// Join returns all recorded error messages joined by newline.
func (r *ErrorRecorder) Join() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.errs) == 0 {
		return ""
	}

	parts := make([]string, 0, len(r.errs))
	for _, err := range r.errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "\n")
}

// Err joins all recorded errors into one error value.
// It returns nil when no errors have been recorded.
func (r *ErrorRecorder) Err() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.errs) == 0 {
		return nil
	}

	return errors.Join(r.errs...)
}

// Reset clears all recorded errors.
func (r *ErrorRecorder) Reset() {
	r.mu.Lock()
	r.errs = nil
	r.mu.Unlock()
}
