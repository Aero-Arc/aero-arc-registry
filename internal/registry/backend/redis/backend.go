// Package redis provides a Redis backend implementation.
package redis

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

type Backend struct {
	cfg *registry.RedisConfig

	do      func(ctx context.Context, args ...string) (any, error)
	doMulti func(ctx context.Context, cmds [][]string) ([]any, error)

	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
}

const (
	relaysSetKey = "registry:relays"
	agentsSetKey = "registry:agents"
)

func relayKey(relayID string) string {
	return "registry:relay:" + relayID
}

func agentKey(agentID string) string {
	return "registry:agent:" + agentID
}

func placementKey(agentID string) string {
	return "registry:placement:" + agentID
}

func New(cfg *registry.RedisConfig) (*Backend, error) {
	if cfg == nil {
		return nil, registry.ErrRedisConfigNil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	b := &Backend{cfg: cfg}
	b.do = b.exec
	b.doMulti = b.execMulti
	return b, nil
}

func (b *Backend) RegisterRelay(ctx context.Context, relay registry.Relay) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relay.ID == "" {
		return registry.ErrRelayIDEmpty
	}

	if relay.LastSeen.IsZero() {
		relay.LastSeen = time.Now()
	}

	payload, err := json.Marshal(relay)
	if err != nil {
		return err
	}

	_, err = b.doMulti(ctx, [][]string{
		{"SET", relayKey(relay.ID), string(payload)},
		{"SADD", relaysSetKey, relay.ID},
	})
	return err
}

func (b *Backend) HeartbeatRelay(ctx context.Context, relayID string, ts time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relayID == "" {
		return registry.ErrRelayIDEmpty
	}
	if ts.IsZero() {
		ts = time.Now()
	}

	relay, err := b.getRelay(ctx, relayID)
	if err != nil {
		return err
	}
	relay.LastSeen = ts
	return b.RegisterRelay(ctx, relay)
}

func (b *Backend) ListRelays(ctx context.Context) ([]registry.Relay, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	idsRaw, err := b.do(ctx, "SMEMBERS", relaysSetKey)
	if err != nil {
		return nil, err
	}
	ids, err := asStringSlice(idsRaw)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = relayKey(id)
	}

	args := append([]string{"MGET"}, keys...)
	raw, err := b.do(ctx, args...)
	if err != nil {
		return nil, err
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected MGET response type: %T", raw)
	}

	relays := make([]registry.Relay, 0, len(ids))
	var staleIDs []string
	for i, item := range items {
		b, ok := item.([]byte)
		if !ok || b == nil {
			staleIDs = append(staleIDs, ids[i])
			continue
		}
		var relay registry.Relay
		if err := json.Unmarshal(b, &relay); err != nil {
			return nil, err
		}
		relays = append(relays, relay)
	}

	// Clean up stale set members whose keys no longer exist.
	for _, id := range staleIDs {
		_, _ = b.do(ctx, "SREM", relaysSetKey, id)
	}

	return relays, nil
}

func (b *Backend) RemoveRelay(ctx context.Context, relayID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relayID == "" {
		return registry.ErrRelayIDEmpty
	}

	existsRaw, err := b.do(ctx, "EXISTS", relayKey(relayID))
	if err != nil {
		return err
	}
	exists, err := asInt(existsRaw)
	if err != nil {
		return err
	}
	if exists == 0 {
		return registry.ErrRelayNotRegistered
	}

	_, err = b.doMulti(ctx, [][]string{
		{"DEL", relayKey(relayID)},
		{"SREM", relaysSetKey, relayID},
	})

	// Persistence-only responsibility: remove relay record and relay index membership.
	// Any cascading invalidation of dependent agents is orchestrated by the registry layer.
	return err
}

func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if relayID == "" {
		return registry.ErrRelayIDEmpty
	}
	if agent.ID == "" {
		return registry.ErrAgentIDEmpty
	}

	if _, err := b.getRelay(ctx, relayID); err != nil {
		return err
	}
	if agent.LastHeartbeat.IsZero() {
		agent.LastHeartbeat = time.Now()
	}

	agentPayload, err := json.Marshal(agent)
	if err != nil {
		return err
	}
	placementPayload, err := json.Marshal(registry.AgentPlacement{AgentID: agent.ID, RelayID: relayID, UpdatedAt: agent.LastHeartbeat})
	if err != nil {
		return err
	}

	_, err = b.doMulti(ctx, [][]string{
		{"SET", agentKey(agent.ID), string(agentPayload)},
		{"SET", placementKey(agent.ID), string(placementPayload)},
		{"SADD", agentsSetKey, agent.ID},
	})
	return err
}

func (b *Backend) HeartbeatAgent(ctx context.Context, agentID string, ts time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if agentID == "" {
		return registry.ErrAgentIDEmpty
	}

	agent, err := b.getAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	agent.LastHeartbeat = ts
	agentPayload, err := json.Marshal(agent)
	if err != nil {
		return err
	}

	placement, err := b.GetAgentPlacement(ctx, agentID)
	if err != nil {
		return err
	}
	placement.UpdatedAt = ts
	placementPayload, err := json.Marshal(placement)
	if err != nil {
		return err
	}

	_, err = b.doMulti(ctx, [][]string{
		{"SET", agentKey(agentID), string(agentPayload)},
		{"SET", placementKey(agentID), string(placementPayload)},
	})
	return err
}

func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if agentID == "" {
		return nil, registry.ErrAgentIDEmpty
	}

	raw, err := b.do(ctx, "GET", placementKey(agentID))
	if err != nil {
		return nil, err
	}
	placementBytes, ok := raw.([]byte)
	if !ok || placementBytes == nil {
		return nil, registry.ErrAgentNotRegistered
	}

	var placement registry.AgentPlacement
	if err := json.Unmarshal(placementBytes, &placement); err != nil {
		return nil, err
	}
	return &placement, nil
}

func (b *Backend) getRelay(ctx context.Context, relayID string) (registry.Relay, error) {
	raw, err := b.do(ctx, "GET", relayKey(relayID))
	if err != nil {
		return registry.Relay{}, err
	}
	relayBytes, ok := raw.([]byte)
	if !ok || relayBytes == nil {
		return registry.Relay{}, registry.ErrRelayNotRegistered
	}

	var relay registry.Relay
	if err := json.Unmarshal(relayBytes, &relay); err != nil {
		return registry.Relay{}, err
	}
	return relay, nil
}

func (b *Backend) getAgent(ctx context.Context, agentID string) (registry.Agent, error) {
	raw, err := b.do(ctx, "GET", agentKey(agentID))
	if err != nil {
		return registry.Agent{}, err
	}
	agentBytes, ok := raw.([]byte)
	if !ok || agentBytes == nil {
		return registry.Agent{}, registry.ErrAgentNotRegistered
	}

	var agent registry.Agent
	if err := json.Unmarshal(agentBytes, &agent); err != nil {
		return registry.Agent{}, err
	}
	return agent, nil
}

func (b *Backend) Close(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		err := b.conn.Close()
		b.conn = nil
		b.reader = nil
		return err
	}
	return nil
}

// ensureConn establishes and authenticates a persistent connection if one
// does not already exist. Must be called with b.mu held.
func (b *Backend) ensureConn(ctx context.Context) error {
	if b.conn != nil {
		return nil
	}

	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", b.cfg.Address, b.cfg.Port))
	if err != nil {
		return err
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	reader := bufio.NewReader(conn)

	if b.cfg.Password != "" {
		authArgs := []string{"AUTH", b.cfg.Password}
		if b.cfg.Username != "" {
			authArgs = []string{"AUTH", b.cfg.Username, b.cfg.Password}
		}
		if _, err := writeRESP(conn, authArgs...); err != nil {
			conn.Close()
			return err
		}
		if _, err := readRESP(reader); err != nil {
			conn.Close()
			return err
		}
	}
	if b.cfg.DB > 0 {
		if _, err := writeRESP(conn, "SELECT", strconv.Itoa(b.cfg.DB)); err != nil {
			conn.Close()
			return err
		}
		if _, err := readRESP(reader); err != nil {
			conn.Close()
			return err
		}
	}

	b.conn = conn
	b.reader = reader
	return nil
}

// closeConn tears down the persistent connection. Must be called with b.mu held.
func (b *Backend) closeConn() {
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
		b.reader = nil
	}
}

// exec sends a single command over the persistent connection.
func (b *Backend) exec(ctx context.Context, args ...string) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.ensureConn(ctx); err != nil {
		return nil, err
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = b.conn.SetDeadline(deadline)
	}

	if _, err := writeRESP(b.conn, args...); err != nil {
		b.closeConn()
		return nil, err
	}
	res, err := readRESP(b.reader)
	if err != nil {
		b.closeConn()
		return nil, err
	}
	return res, nil
}

// execMulti wraps multiple commands in a MULTI/EXEC transaction for atomicity.
func (b *Backend) execMulti(ctx context.Context, cmds [][]string) ([]any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.ensureConn(ctx); err != nil {
		return nil, err
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = b.conn.SetDeadline(deadline)
	}

	// MULTI
	if _, err := writeRESP(b.conn, "MULTI"); err != nil {
		b.closeConn()
		return nil, err
	}
	if _, err := readRESP(b.reader); err != nil {
		b.closeConn()
		return nil, err
	}

	// Queue each command
	for _, cmd := range cmds {
		if _, err := writeRESP(b.conn, cmd...); err != nil {
			b.closeConn()
			return nil, err
		}
		// Read +QUEUED
		if _, err := readRESP(b.reader); err != nil {
			b.closeConn()
			return nil, err
		}
	}

	// EXEC
	if _, err := writeRESP(b.conn, "EXEC"); err != nil {
		b.closeConn()
		return nil, err
	}
	execResult, err := readRESP(b.reader)
	if err != nil {
		b.closeConn()
		return nil, err
	}

	results, ok := execResult.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected EXEC response type: %T", execResult)
	}
	return results, nil
}

func writeRESP(w io.Writer, args ...string) (int, error) {
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, arg := range args {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(arg)))
		b.WriteString("\r\n")
		b.WriteString(arg)
		b.WriteString("\r\n")
	}
	return w.Write([]byte(b.String()))
}

func readRESP(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	switch prefix {
	case '+':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		return line, nil
	case '-':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		return nil, errors.New(line)
	case ':':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		v, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		return v, nil
	case '$':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return buf[:n], nil
	case '*':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		out := make([]any, n)
		for i := 0; i < n; i++ {
			v, err := readRESP(r)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown RESP prefix: %q", prefix)
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func asStringSlice(v any) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected redis response type: %T", v)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch x := item.(type) {
		case []byte:
			out = append(out, string(x))
		case string:
			out = append(out, x)
		case nil:
			continue
		default:
			return nil, fmt.Errorf("unexpected redis array element type: %T", item)
		}
	}
	return out, nil
}

func asInt(v any) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case []byte:
		return strconv.Atoi(string(x))
	case string:
		return strconv.Atoi(x)
	default:
		return 0, fmt.Errorf("unexpected redis integer response type: %T", v)
	}
}
