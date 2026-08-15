package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	redisclient "github.com/redis/go-redis/v9"
)

// PublishConformanceSummary atomically writes one short-lived projection while
// retaining a longer independent generation/revision fence in Redis server time.
//
// Parameters:
//   - ctx: controls the Redis transaction.
//   - summary: contains one immutable evaluation cursor and current live state.
//   - projectionTTL: controls current-state visibility and must be millisecond precision.
//   - fenceTTL: retains the cursor after projection expiry and must be longer.
//
// Returns:
//   - projection: contains Redis-owned receipt and expiry timestamps.
//   - disposition: distinguishes an advancing cursor from an exact retry.
//   - error: wraps ErrStale or ErrConflict when the atomic fence rejects publication.
func (b *Backend) PublishConformanceSummary(ctx context.Context, summary registry.ConformanceSummary, projectionTTL, fenceTTL time.Duration) (registry.ConformanceProjection, registry.PublishDisposition, error) {
	if projectionTTL < time.Millisecond || fenceTTL <= projectionTTL {
		return registry.ConformanceProjection{}, "", fmt.Errorf("invalid conformance TTLs: %w", registry.ErrInvalid)
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return registry.ConformanceProjection{}, "", fmt.Errorf("encode conformance summary: %w", err)
	}
	hash := sha256.Sum256(payload)
	result, err := publishConformanceScript.Run(ctx, b.client,
		[]string{b.conformanceSummaryKey(summary.AssignmentID), b.conformanceFenceKey(summary.AssignmentID)},
		fmt.Sprintf("%020d", summary.AssignmentGeneration),
		fmt.Sprintf("%020d", summary.EvaluationRevision),
		hex.EncodeToString(hash[:]), payload, projectionTTL.Milliseconds(), fenceTTL.Milliseconds(),
	).Slice()
	if err != nil {
		return registry.ConformanceProjection{}, "", fmt.Errorf("publish conformance summary %q: %w", summary.AssignmentID, err)
	}
	if len(result) == 0 {
		return registry.ConformanceProjection{}, "", fmt.Errorf("publish conformance summary %q: corrupt redis response", summary.AssignmentID)
	}
	code, err := redisInteger(result[0])
	if err != nil {
		return registry.ConformanceProjection{}, "", fmt.Errorf("decode conformance publication %q: %w", summary.AssignmentID, err)
	}
	switch code {
	case -1:
		return registry.ConformanceProjection{}, "", fmt.Errorf("conformance cursor is older than the Registry fence: %w", registry.ErrStale)
	case 0:
		return registry.ConformanceProjection{}, "", fmt.Errorf("conformance cursor content changed: %w", registry.ErrConflict)
	case 1, 2:
	default:
		return registry.ConformanceProjection{}, "", fmt.Errorf("publish conformance summary %q: unknown redis disposition %d", summary.AssignmentID, code)
	}
	if len(result) != 3 {
		return registry.ConformanceProjection{}, "", fmt.Errorf("publish conformance summary %q: incomplete redis response", summary.AssignmentID)
	}
	storedAt, err := decodeRedisTime(result[1])
	if err != nil {
		return registry.ConformanceProjection{}, "", fmt.Errorf("decode conformance stored time: %w", err)
	}
	expiresAt, err := decodeRedisTime(result[2])
	if err != nil {
		return registry.ConformanceProjection{}, "", fmt.Errorf("decode conformance expiry time: %w", err)
	}
	disposition := registry.PublishApplied
	if code == 2 {
		disposition = registry.PublishIdempotent
	}
	return registry.ConformanceProjection{Summary: summary, StoredAt: storedAt, ExpiresAt: expiresAt}, disposition, nil
}

// GetConformanceSummary returns one live Redis projection and treats expired or
// malformed state as absent.
//
// Parameters:
//   - ctx: controls the Redis read.
//   - assignmentID: identifies the logical assignment.
//
// Returns:
//   - projection: is the current TTL-scoped value.
//   - error: wraps ErrNotFound when no complete live value exists.
func (b *Backend) GetConformanceSummary(ctx context.Context, assignmentID string) (registry.ConformanceProjection, error) {
	fields, err := readConformanceScript.Run(ctx, b.client, []string{b.conformanceSummaryKey(assignmentID)}).StringSlice()
	if errors.Is(err, redisclient.Nil) || (err == nil && len(fields) == 0) {
		return registry.ConformanceProjection{}, fmt.Errorf("conformance summary %q: %w", assignmentID, registry.ErrNotFound)
	}
	if err != nil {
		return registry.ConformanceProjection{}, fmt.Errorf("read conformance summary %q: %w", assignmentID, err)
	}
	return decodeConformanceProjection(assignmentID, fields)
}

// BatchGetConformanceSummaries pipelines atomic per-key reads and returns live
// projections plus missing IDs in the caller's deterministic order.
//
// Parameters:
//   - ctx: controls the Redis pipeline.
//   - assignmentIDs: must already be unique and deterministically ordered.
//
// Returns:
//   - projections: contains live values in input order.
//   - missing: contains absent, expired, or atomically repaired IDs.
//   - error: reports Redis or decoding failure.
func (b *Backend) BatchGetConformanceSummaries(ctx context.Context, assignmentIDs []string) ([]registry.ConformanceProjection, []string, error) {
	pipe := b.client.Pipeline()
	commands := make([]*redisclient.Cmd, len(assignmentIDs))
	for index, assignmentID := range assignmentIDs {
		commands[index] = readConformanceScript.Eval(ctx, pipe, []string{b.conformanceSummaryKey(assignmentID)})
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, nil, fmt.Errorf("batch read conformance summaries: %w", err)
	}
	projections := make([]registry.ConformanceProjection, 0, len(assignmentIDs))
	missing := make([]string, 0)
	for index, command := range commands {
		fields, err := command.StringSlice()
		if errors.Is(err, redisclient.Nil) || (err == nil && len(fields) == 0) {
			missing = append(missing, assignmentIDs[index])
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read conformance summary %q: %w", assignmentIDs[index], err)
		}
		projection, err := decodeConformanceProjection(assignmentIDs[index], fields)
		if err != nil {
			return nil, nil, err
		}
		projections = append(projections, projection)
	}
	return projections, missing, nil
}

func decodeConformanceProjection(assignmentID string, fields []string) (registry.ConformanceProjection, error) {
	if len(fields) != 3 {
		return registry.ConformanceProjection{}, fmt.Errorf("read conformance summary %q: corrupt redis response", assignmentID)
	}
	var summary registry.ConformanceSummary
	if err := json.Unmarshal([]byte(fields[0]), &summary); err != nil {
		return registry.ConformanceProjection{}, fmt.Errorf("decode conformance summary %q: %w", assignmentID, err)
	}
	if summary.AssignmentID != assignmentID {
		return registry.ConformanceProjection{}, fmt.Errorf("decode conformance summary %q: identity mismatch", assignmentID)
	}
	storedAt, err := decodeUnixMilli(fields[1])
	if err != nil {
		return registry.ConformanceProjection{}, fmt.Errorf("decode conformance stored time %q: %w", assignmentID, err)
	}
	expiresAt, err := decodeUnixMilli(fields[2])
	if err != nil {
		return registry.ConformanceProjection{}, fmt.Errorf("decode conformance expiry time %q: %w", assignmentID, err)
	}
	return registry.ConformanceProjection{Summary: summary, StoredAt: storedAt, ExpiresAt: expiresAt}, nil
}

func decodeRedisTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case string:
		return decodeUnixMilli(typed)
	case []byte:
		return decodeUnixMilli(string(typed))
	case int64:
		return time.UnixMilli(typed), nil
	default:
		return time.Time{}, fmt.Errorf("unexpected redis time type %T", value)
	}
}

func redisInteger(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected redis integer type %T", value)
	}
}
