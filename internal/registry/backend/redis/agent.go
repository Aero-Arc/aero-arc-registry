package redis

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	redisclient "github.com/redis/go-redis/v9"
)

// RegisterAgent registers the supplied Backend identity or handler.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agent: is the registry.Agent value supplied to RegisterAgent.
//   - relayID: identifies the target relay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) RegisterAgent(ctx context.Context, agent registry.Agent, relayID string) error {
	if agent.ID == "" {
		return fmt.Errorf("agent id is empty: %w", registry.ErrInvalid)
	}

	result, err := registerAgentScript.Run(ctx, b.client,
		[]string{
			b.agentKey(agent.ID),
			b.agentsKey(),
			b.relayKey(relayID),
			b.relayAgentsKey(relayID),
			b.relaysKey(),
		},
		agent.ID, relayID, b.ttl.Agent.Milliseconds(), b.relayKeyPrefix(), b.relayAgentsKeyPrefix(),
	).Int64()
	if err != nil {
		return fmt.Errorf("register agent %q: %w", agent.ID, err)
	}
	if result == 0 {
		return errRelayNotRegistered
	}
	return nil
}

// HeartbeatAgent renews liveness for the supplied Backend identity without changing ownership.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentID: identifies the target agent.
//   - expectedRelayID: identifies the target expectedrelay.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) HeartbeatAgent(ctx context.Context, agentID, expectedRelayID string) error {
	result, err := heartbeatAgentScript.Run(ctx, b.client,
		[]string{
			b.agentKey(agentID),
			b.agentsKey(),
			b.relayKey(expectedRelayID),
			b.relayAgentsKey(expectedRelayID),
			b.relaysKey(),
		},
		agentID, expectedRelayID, b.ttl.Agent.Milliseconds(), b.relayKeyPrefix(), b.relayAgentsKeyPrefix(),
	).Int64()
	if err != nil {
		return fmt.Errorf("heartbeat agent %q: %w", agentID, err)
	}
	if result == 0 {
		return errAgentNotRegistered
	}
	return nil
}

// GetAgentPlacement atomically validates and returns an Agent's live Redis placement.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentID: identifies the target agent.
//
// Returns:
//   - result: is the *registry.AgentPlacement value produced by GetAgentPlacement.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) GetAgentPlacement(ctx context.Context, agentID string) (*registry.AgentPlacement, error) {
	values, err := liveAgentScript.Run(ctx, b.client,
		[]string{b.agentKey(agentID), b.agentsKey(), b.relaysKey()}, agentID, b.relayKeyPrefix(), b.relayAgentsKeyPrefix(),
	).StringSlice()
	if errors.Is(err, redisclient.Nil) || (err == nil && len(values) == 0) {
		return nil, errAgentNotRegistered
	}
	if err != nil {
		return nil, fmt.Errorf("get agent placement %q: %w", agentID, err)
	}
	if len(values) != 3 {
		return nil, fmt.Errorf("get agent placement %q: corrupt redis response", agentID)
	}

	updatedAt, err := decodeUnixMilli(values[2])
	if err != nil {
		return nil, fmt.Errorf("decode placement %q: %w", agentID, err)
	}
	return &registry.AgentPlacement{
		AgentID: values[0], RelayID: values[1], UpdatedAt: updatedAt,
	}, nil
}

// ListAgents returns Backend records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the []registry.Agent value produced by ListAgents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) ListAgents(ctx context.Context) ([]registry.Agent, error) {
	ids, err := b.activeIndexIDs(ctx, b.agentsKey())
	if err != nil {
		return nil, fmt.Errorf("list agent index: %w", err)
	}
	if len(ids) == 0 {
		return []registry.Agent{}, nil
	}

	pipe := b.client.Pipeline()
	commands := make([]*redisclient.Cmd, len(ids))
	for i, id := range ids {
		commands[i] = readLiveAgentScript.Eval(ctx, pipe,
			[]string{b.agentKey(id), b.agentsKey(), b.relaysKey()}, id, b.relayKeyPrefix(), b.relayAgentsKeyPrefix(),
		)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("read agents: %w", err)
	}

	agents := make([]registry.Agent, 0, len(ids))
	for i := range commands {
		values, err := commands[i].StringSlice()
		if errors.Is(err, redisclient.Nil) || (err == nil && len(values) == 0) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read agent %q: %w", ids[i], err)
		}
		if len(values) != 2 {
			return nil, fmt.Errorf("read agent %q: corrupt redis response", ids[i])
		}
		lastHeartbeat, err := decodeUnixMilli(values[1])
		if err != nil {
			return nil, fmt.Errorf("decode agent %q: %w", ids[i], err)
		}
		agents = append(agents, registry.Agent{ID: values[0], LastHeartbeat: lastHeartbeat})
	}

	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents, nil
}

// ListRelayAgents returns Backend records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - relayID: identifies the target relay.
//
// Returns:
//   - result: is the []*registry.Agent value produced by ListRelayAgents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) ListRelayAgents(ctx context.Context, relayID string) ([]*registry.Agent, error) {
	relayValues, err := readLiveRelayScript.Run(ctx, b.client,
		[]string{b.relayKey(relayID), b.relaysKey(), b.relayAgentsKey(relayID)}, relayID,
	).StringSlice()
	if errors.Is(err, redisclient.Nil) || (err == nil && len(relayValues) == 0) {
		return nil, errRelayNotRegistered
	}
	if err != nil {
		return nil, fmt.Errorf("check relay %q: %w", relayID, err)
	}
	if len(relayValues) != 5 {
		return nil, fmt.Errorf("check relay %q: corrupt redis response", relayID)
	}
	relayIncarnation := relayValues[3]

	ids, err := b.activeIndexIDs(ctx, b.relayAgentsKey(relayID))
	if err != nil {
		return nil, fmt.Errorf("list agents for relay %q: %w", relayID, err)
	}

	pipe := b.client.Pipeline()
	commands := make([]*redisclient.Cmd, len(ids))
	for i, id := range ids {
		commands[i] = readRelayAgentScript.Eval(ctx, pipe,
			[]string{b.agentKey(id), b.agentsKey(), b.relayAgentsKey(relayID), b.relayKey(relayID), b.relaysKey()},
			id, relayID, relayIncarnation, b.relayKeyPrefix(), b.relayAgentsKeyPrefix(),
		)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redisclient.Nil) {
		return nil, fmt.Errorf("read agents for relay %q: %w", relayID, err)
	}

	agents := make([]*registry.Agent, 0, len(ids))
	for i := range commands {
		values, err := commands[i].StringSlice()
		if errors.Is(err, redisclient.Nil) || (err == nil && len(values) == 0) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read relay agent %q: %w", ids[i], err)
		}
		if len(values) != 2 {
			return nil, fmt.Errorf("read relay agent %q: corrupt redis response", ids[i])
		}
		lastHeartbeat, err := decodeUnixMilli(values[1])
		if err != nil {
			return nil, fmt.Errorf("decode relay agent %q: %w", ids[i], err)
		}
		agents = append(agents, &registry.Agent{ID: values[0], LastHeartbeat: lastHeartbeat})
	}

	// Do not report a placement snapshot for a relay that expired, became
	// corrupt, lost its live index entry, or was re-registered during the read.
	valid, err := validateRelayIncarnationScript.Run(ctx, b.client,
		[]string{b.relayKey(relayID), b.relaysKey(), b.relayAgentsKey(relayID)},
		relayID, relayIncarnation,
	).Int64()
	if err != nil {
		return nil, fmt.Errorf("recheck relay %q: %w", relayID, err)
	}
	if valid == 0 {
		return nil, errRelayNotRegistered
	}

	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents, nil
}

// RemoveAgents removes the selected Backend records and associated live indexes.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - agentIDs: identifies the target agent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (b *Backend) RemoveAgents(ctx context.Context, agentIDs []string) error {
	if len(agentIDs) == 0 {
		return nil
	}

	keys := make([]string, 0, len(agentIDs)+1)
	keys = append(keys, b.agentsKey())
	for _, id := range agentIDs {
		keys = append(keys, b.agentKey(id))
	}
	args := make([]interface{}, 0, len(agentIDs)+2)
	for _, id := range agentIDs {
		args = append(args, id)
	}
	args = append(args, b.relayKeyPrefix(), b.relayAgentsKeyPrefix())

	if _, err := removeAgentsScript.Run(ctx, b.client, keys, args...).Result(); err != nil {
		return fmt.Errorf("remove agents: %w", err)
	}
	return nil
}
