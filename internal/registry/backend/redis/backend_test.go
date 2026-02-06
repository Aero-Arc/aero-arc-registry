package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
)

var _ registry.Backend = (*Backend)(nil)

func TestNewRequiresValidConfig(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, registry.ErrRedisConfigNil) {
		t.Fatalf("expected ErrRedisConfigNil, got %v", err)
	}

	if _, err := New(&registry.RedisConfig{}); !errors.Is(err, registry.ErrRedisAddrEmpty) {
		t.Fatalf("expected ErrRedisAddrEmpty, got %v", err)
	}
}

func TestRelayAndAgentLifecycle(t *testing.T) {
	b := &Backend{cfg: &registry.RedisConfig{Address: "fake", Port: 6379}}
	b.do = newFakeRedisDoer()
	ctx := context.Background()
	now := time.Now()

	relay := registry.Relay{ID: "relay-1", Address: "127.0.0.1", GRPCPort: 50051, LastSeen: now}
	if err := b.RegisterRelay(ctx, relay); err != nil {
		t.Fatalf("register relay: %v", err)
	}

	relays, err := b.ListRelays(ctx)
	if err != nil {
		t.Fatalf("list relays: %v", err)
	}
	if len(relays) != 1 || relays[0].ID != relay.ID {
		t.Fatalf("unexpected relays: %#v", relays)
	}

	hb := now.Add(5 * time.Second)
	if err := b.HeartbeatRelay(ctx, relay.ID, hb); err != nil {
		t.Fatalf("heartbeat relay: %v", err)
	}

	agent := registry.Agent{ID: "agent-1", LastHeartbeat: now}
	if err := b.RegisterAgent(ctx, agent, relay.ID); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	if err := b.HeartbeatAgent(ctx, agent.ID, hb); err != nil {
		t.Fatalf("heartbeat agent: %v", err)
	}

	placement, err := b.GetAgentPlacement(ctx, agent.ID)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	if placement.RelayID != relay.ID {
		t.Fatalf("unexpected placement relay id: %s", placement.RelayID)
	}

	if err := b.RemoveRelay(ctx, relay.ID); err != nil {
		t.Fatalf("remove relay: %v", err)
	}

	// Backend remains persistence-only: removing a relay does not cascade into agent cleanup.
	if _, err := b.GetAgentPlacement(ctx, agent.ID); err != nil {
		t.Fatalf("expected placement to remain persisted, got %v", err)
	}
}

func TestValidationErrorsAndContext(t *testing.T) {
	b := &Backend{cfg: &registry.RedisConfig{Address: "fake", Port: 6379}}
	b.do = newFakeRedisDoer()

	ctx := context.Background()

	if err := b.RegisterRelay(ctx, registry.Relay{}); err == nil {
		t.Fatal("expected error for empty relay id")
	}

	if err := b.RegisterAgent(ctx, registry.Agent{}, "relay-1"); err == nil {
		t.Fatal("expected error for empty agent id")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.HeartbeatRelay(canceled, "relay-1", time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func newFakeRedisDoer() func(ctx context.Context, args ...string) (any, error) {
	kv := map[string]string{}
	sets := map[string]map[string]struct{}{}

	return func(ctx context.Context, args ...string) (any, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("missing command")
		}
		cmd := args[0]
		switch cmd {
		case "SET":
			kv[args[1]] = args[2]
			return "OK", nil
		case "GET":
			v, ok := kv[args[1]]
			if !ok {
				return nil, nil
			}
			return []byte(v), nil
		case "SADD":
			k := args[1]
			if _, ok := sets[k]; !ok {
				sets[k] = map[string]struct{}{}
			}
			sets[k][args[2]] = struct{}{}
			return 1, nil
		case "SMEMBERS":
			k := args[1]
			members := make([]any, 0, len(sets[k]))
			keys := make([]string, 0, len(sets[k]))
			for m := range sets[k] {
				keys = append(keys, m)
			}
			slices.Sort(keys)
			for _, m := range keys {
				members = append(members, []byte(m))
			}
			return members, nil
		case "SREM":
			if set, ok := sets[args[1]]; ok {
				delete(set, args[2])
			}
			return 1, nil
		case "EXISTS":
			if _, ok := kv[args[1]]; ok {
				return 1, nil
			}
			return 0, nil
		case "DEL":
			removed := 0
			for _, key := range args[1:] {
				if _, ok := kv[key]; ok {
					removed++
				}
				delete(kv, key)
			}
			return removed, nil
		default:
			return nil, fmt.Errorf("unsupported command: %s", cmd)
		}
	}
}

func TestRESPHelpers(t *testing.T) {
	got, err := asStringSlice([]any{[]byte("a"), "b"})
	if err != nil {
		t.Fatalf("asStringSlice err: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("unexpected string slice: %#v", got)
	}

	i, err := asInt(2)
	if err != nil || i != 2 {
		t.Fatalf("unexpected int parse: %d %v", i, err)
	}

	_, err = json.Marshal(registry.Relay{ID: "ok"})
	if err != nil {
		t.Fatalf("json marshal sanity check: %v", err)
	}
}
