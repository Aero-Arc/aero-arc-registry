package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/consul"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/etcd"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/memory"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/redis"
	"github.com/urfave/cli/v3"
)

func TestBuildConfigFromCLI(t *testing.T) {
	t.Run("memory backend parses core flags", func(t *testing.T) {
		cmd := newTestCLICommand()
		_ = cmd.Set(BackendFlag, "memory")
		_ = cmd.Set(GRPCListenAddrFlag, "127.0.0.1")
		_ = cmd.Set(GRPCListenPortFlag, "50055")
		_ = cmd.Set(RelayTTLFlag, "45s")
		_ = cmd.Set(AgentTTLFlag, "15s")

		cfg, err := buildConfigFromCLI(cmd)
		if err != nil {
			t.Fatalf("buildConfigFromCLI() error = %v", err)
		}
		if cfg.Backend.Type != registry.MemoryRegistryBackend {
			t.Fatalf("expected memory backend, got %q", cfg.Backend.Type)
		}
		if cfg.GRPC.ListenAddress != "127.0.0.1" || cfg.GRPC.ListenPort != 50055 {
			t.Fatalf("unexpected grpc config: %+v", cfg.GRPC)
		}
		if cfg.TTL.Relay != 45*time.Second || cfg.TTL.Agent != 15*time.Second {
			t.Fatalf("unexpected ttl config: %+v", cfg.TTL)
		}
	})

	t.Run("redis backend maps redis config", func(t *testing.T) {
		cmd := newTestCLICommand()
		_ = cmd.Set(BackendFlag, "redis")
		_ = cmd.Set(RedisAddrFlag, "cache.internal")
		_ = cmd.Set(RedisPortFlag, "6380")
		_ = cmd.Set(RedisUsernameFlag, "svc")
		_ = cmd.Set(RedisPasswordFlag, "secret")
		_ = cmd.Set(RedisDBFlag, "2")

		cfg, err := buildConfigFromCLI(cmd)
		if err != nil {
			t.Fatalf("buildConfigFromCLI() error = %v", err)
		}
		if cfg.Backend.Redis == nil {
			t.Fatal("expected redis config to be set")
		}
		if cfg.Backend.Redis.Address != "cache.internal" || cfg.Backend.Redis.Port != 6380 || cfg.Backend.Redis.DB != 2 {
			t.Fatalf("unexpected redis config: %+v", cfg.Backend.Redis)
		}
		if cfg.Backend.Redis.Username != "svc" || cfg.Backend.Redis.Password != "secret" {
			t.Fatalf("unexpected redis auth config: %+v", cfg.Backend.Redis)
		}
	})

	t.Run("unsupported backend returns error", func(t *testing.T) {
		cmd := newTestCLICommand()
		_ = cmd.Set(BackendFlag, "unsupported")
		_, err := buildConfigFromCLI(cmd)
		if !errors.Is(err, registry.ErrUnsupportedBackend) {
			t.Fatalf("expected ErrUnsupportedBackend, got %v", err)
		}
	})
}

func TestBuildBackendFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *registry.Config
		assert func(t *testing.T, b registry.Backend)
	}{
		{
			name: "memory backend",
			cfg:  &registry.Config{Backend: registry.BackendConfig{Type: registry.MemoryRegistryBackend}},
			assert: func(t *testing.T, b registry.Backend) {
				if _, ok := b.(*memory.Backend); !ok {
					t.Fatalf("expected *memory.Backend, got %T", b)
				}
			},
		},
		{
			name: "redis backend",
			cfg: &registry.Config{
				Backend: registry.BackendConfig{
					Type:  registry.RedisRegistryBackend,
					Redis: &registry.RedisConfig{},
				},
			},
			assert: func(t *testing.T, b registry.Backend) {
				if _, ok := b.(*redis.Backend); !ok {
					t.Fatalf("expected *redis.Backend, got %T", b)
				}
			},
		},
		{
			name: "consul backend",
			cfg: &registry.Config{
				Backend: registry.BackendConfig{
					Type:   registry.ConsulRegistryBackend,
					Consul: &registry.ConsulConfig{},
				},
			},
			assert: func(t *testing.T, b registry.Backend) {
				if _, ok := b.(*consul.Backend); !ok {
					t.Fatalf("expected *consul.Backend, got %T", b)
				}
			},
		},
		{
			name: "etcd backend",
			cfg: &registry.Config{
				Backend: registry.BackendConfig{
					Type: registry.EtcdRegistryBackend,
					Etcd: &registry.EtcdConfig{},
				},
			},
			assert: func(t *testing.T, b registry.Backend) {
				if _, ok := b.(*etcd.Backend); !ok {
					t.Fatalf("expected *etcd.Backend, got %T", b)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend, err := buildBackendFromConfig(tc.cfg)
			if err != nil {
				t.Fatalf("buildBackendFromConfig() error = %v", err)
			}
			tc.assert(t, backend)
		})
	}

	t.Run("unknown backend returns unhandled error", func(t *testing.T) {
		_, err := buildBackendFromConfig(&registry.Config{
			Backend: registry.BackendConfig{Type: registry.RegistryBackend("unknown")},
		})
		if !errors.Is(err, ErrUnhandledBackend) {
			t.Fatalf("expected ErrUnhandledBackend, got %v", err)
		}
	})
}

func TestRunRegistryEarlyErrors(t *testing.T) {
	t.Run("unsupported backend returns parse error", func(t *testing.T) {
		cmd := newTestCLICommand()
		_ = cmd.Set(BackendFlag, "unsupported")
		err := RunRegistry(context.Background(), cmd)
		if !errors.Is(err, registry.ErrUnsupportedBackend) {
			t.Fatalf("expected ErrUnsupportedBackend, got %v", err)
		}
	})

	t.Run("tls cert load error returns before listen", func(t *testing.T) {
		cmd := newTestCLICommand()
		_ = cmd.Set(BackendFlag, "memory")
		_ = cmd.Set(TLSEnabledFlag, "true")
		_ = cmd.Set(TLSCertPathFlag, "/tmp/does-not-exist.crt")
		_ = cmd.Set(TLSKeyPathFlag, "/tmp/does-not-exist.key")

		err := RunRegistry(context.Background(), cmd)
		if err == nil {
			t.Fatal("expected tls load error, got nil")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "no such file") {
			t.Fatalf("expected missing file tls error, got %v", err)
		}
	})
}

func newTestCLICommand() *cli.Command {
	return &cli.Command{
		Flags: []cli.Flag{
			&cli.StringFlag{Name: BackendFlag, Value: "memory"},
			&cli.StringFlag{Name: GRPCListenAddrFlag, Value: "0.0.0.0"},
			&cli.IntFlag{Name: GRPCListenPortFlag, Value: 50051},
			&cli.BoolFlag{Name: TLSEnabledFlag, Value: false},
			&cli.StringFlag{Name: TLSKeyPathFlag, Value: "/tmp/test.key"},
			&cli.StringFlag{Name: TLSCertPathFlag, Value: "/tmp/test.crt"},
			&cli.DurationFlag{Name: RelayTTLFlag, Value: 30 * time.Second},
			&cli.DurationFlag{Name: AgentTTLFlag, Value: 30 * time.Second},
			&cli.StringFlag{Name: RedisAddrFlag, Value: "localhost"},
			&cli.IntFlag{Name: RedisPortFlag, Value: 6379},
			&cli.StringFlag{Name: RedisUsernameFlag, Value: "default"},
			&cli.StringFlag{Name: RedisPasswordFlag, Value: ""},
			&cli.IntFlag{Name: RedisDBFlag, Value: 0},
		},
	}
}
