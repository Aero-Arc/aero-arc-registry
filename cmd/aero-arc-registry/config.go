package main

import (
	"fmt"

	"github.com/makinje/aero-arc-registry/internal/registry"
	"github.com/urfave/cli/v3"
)

func buildConfigFromCLI(cmd *cli.Command) (*registry.Config, error) {
	backendType, err := registry.ParseRegistryBackend(cmd.String(BackendFlag))
	if err != nil {
		return nil, err
	}

	registryConfig := &registry.Config{
		Backend: registry.BackendConfig{
			Type: backendType,
		},
		GRPC: registry.GRPCConfig{
			ListenAddress: cmd.String(GRPCListenAddrFlag),
			ListenPort:    cmd.Int(GRPCListenPortFlag),
			TLS: registry.TLSConfig{
				Enabled:  true,
				CertPath: cmd.String(TLSCertPathFlag),
				KeyPath:  cmd.String(TLSKeyPathFlag),
			},
		},
		TTL: registry.TTLConfig{
			Relay: cmd.Duration(RelayTTLFlag),
			Agent: cmd.Duration(AgentTTLFlag),
		},
	}

	switch registryConfig.Backend.Type {
	case registry.RedisRegistryBackend:
		registryConfig.Backend.Redis = &registry.RedisConfig{
			Address:  cmd.String(RedisAddrFlag),
			Port:     cmd.Int(RedisPortFlag),
			Username: cmd.String(RedisUsernameFlag),
			Password: cmd.String(RedisPasswordFlag),
			DB:       cmd.Int(RedisDBFlag),
		}
	case registry.EtcdRegistryBackend:
	case registry.ConsulRegistryBackend:
	case registry.MemoryRegistryBackend:
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnhandledBackend, registryConfig.Backend.Type)
	}

	return registryConfig, nil
}
