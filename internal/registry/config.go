package registry

import (
	"fmt"
	"time"
)

const (
	defaultConformanceTTL      = 15 * time.Second
	defaultConformanceFenceTTL = 24 * time.Hour
)

// Config defines the runtime configuration for the Aero Arc Registry service.
// It captures transport configuration (gRPC), backend coordination configuration,
// and liveness semantics (TTL) in a backend-agnostic way.
type Config struct {
	// Backend defines which registry backend implementation is used
	// (e.g. memory, redis, etcd, consul) and its associated configuration.
	Backend BackendConfig

	// GRPC defines the gRPC server configuration used to expose
	// the registry control plane APIs.
	GRPC GRPCConfig

	// TTL defines liveness and expiration semantics for relays and agents.
	// These values are enforced at the registry layer, independent of backend.
	TTL TTLConfig
}

// GRPCConfig defines the gRPC server configuration for the registry service.
type GRPCConfig struct {
	// ListenAddress is the network address the gRPC server binds to.
	ListenAddress string

	// ListenPort is the TCP port the gRPC server listens on.
	ListenPort int

	// TLS defines TLS configuration for securing the gRPC transport.
	TLS TLSConfig
}

// TLSConfig defines TLS settings for securing gRPC communication.
type TLSConfig struct {
	// Enabled determines whether TLS is enabled for the gRPC server.
	Enabled bool

	// CertPath is the filesystem path to the TLS certificate.
	CertPath string

	// KeyPath is the filesystem path to the TLS private key.
	KeyPath string
}

// TTLConfig defines time-to-live and liveness expectations
// for registered relays and connected agents.
type TTLConfig struct {
	// Relay defines the maximum allowed duration since the last
	// heartbeat before a relay is considered unhealthy.
	Relay time.Duration

	// Agent defines the maximum allowed duration since the last
	// heartbeat before an agent is considered unhealthy.
	Agent time.Duration

	// Conformance defines how long a live summary remains readable without a
	// successful refresh from Conformance.
	Conformance time.Duration

	// ConformanceFence retains the latest assignment generation and evaluation
	// revision after the live summary expires, preventing stale resurrection.
	ConformanceFence time.Duration

	// TODO(registry-ttl): add optional stale grace period to support soft TTL
	// lifecycle (ACTIVE -> STALE -> DELETING) before hard removal.
	// TODO(registry-ttl): add configurable TTL sweep interval independent of TTL
	// values for adaptive/backpressure-aware scheduler evolution.
}

// WithDefaults returns a copy with backward-compatible Conformance TTL values
// filled when older callers omit the newly added fields.
//
// Returns:
//   - result: preserves explicit TTLs and supplies safe projection defaults.
func (t TTLConfig) WithDefaults() TTLConfig {
	if t.Conformance == 0 {
		t.Conformance = defaultConformanceTTL
	}
	if t.ConformanceFence == 0 {
		t.ConformanceFence = defaultConformanceFenceTTL
	}
	return t
}

// BackendConfig defines which registry backend implementation is used
// and provides backend-specific configuration.
type BackendConfig struct {
	// Type specifies the registry backend implementation.
	Type RegistryBackend

	// Redis contains Redis-specific configuration when the Redis backend is used.
	// It must be non-nil when Type is set to the Redis backend.
	Redis  *RedisConfig
	Etcd   *EtcdConfig
	Consul *ConsulConfig
	Memory *MemoryConfig
}

// RegistryBackend represents the supported registry backend implementations.
type RegistryBackend string

// RedisConfig defines configuration for the Redis-backed registry implementation.
type RedisConfig struct {
	// Address is the Redis server hostname or IP.
	Address string

	// Port is the Redis server port.
	Port int

	// Username is the Redis username used for authentication.
	Username string

	// Password is the Redis password used for authentication.
	Password string

	// DB is the Redis logical database index to use.
	DB int
}

// EtcdConfig defines configuration for the Etcd-backed registry backend.
//
// TODO:
//   - Validate endpoint formatting (host:port)
//   - Support TLS configuration (CA / cert / key)
//   - Add optional dial timeout configuration
//   - Consider lease-based TTL enforcement
type EtcdConfig struct{}

// ConsulConfig defines configuration for the Consul-backed registry backend.
//
// TODO:
//   - Validate address formatting
//   - Support ACL token authentication
//   - Support TLS configuration
//   - Decide on session vs KV-based liveness tracking
type ConsulConfig struct{}

// MemoryConfig defines configuration for the in-memory registry backend.
//
// TODO:
//   - Add optional capacity limits
//   - Add debug logging / metrics toggles
type MemoryConfig struct{}

// ParseRegistryBackend parses the supplied value into registry configuration.
//
// Parameters:
//   - backend: is the string value supplied to ParseRegistryBackend.
//
// Returns:
//   - result: is the RegistryBackend value produced by ParseRegistryBackend.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func ParseRegistryBackend(backend string) (RegistryBackend, error) {
	if registryBackend, ok := registryMap[backend]; ok {
		return registryBackend, nil
	}

	return "", fmt.Errorf("%w: %s", ErrUnsupportedBackend, backend)
}

// Validate validates Config for required fields, supported values, and safety constraints.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *Config) Validate() error {
	switch c.Backend.Type {
	case RedisRegistryBackend:
		if c.Backend.Redis == nil {
			return ErrRedisConfigNil
		}

		if err := c.Backend.Redis.Validate(); err != nil {
			return fmt.Errorf("redis config invalid: %w", err)
		}
	case MemoryRegistryBackend, EtcdRegistryBackend, ConsulRegistryBackend:
	default:
		return fmt.Errorf("unknown registry backend: %s", c.Backend.Type)
	}

	if err := c.GRPC.Validate(); err != nil {
		return fmt.Errorf("GRPC Config invalid: %w", err)
	}

	if err := c.TTL.Validate(); err != nil {
		return fmt.Errorf("TTL Config invalid: %w", err)
	}

	return nil
}

// Validate validates RedisConfig for required fields, supported values, and safety constraints.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (r *RedisConfig) Validate() error {
	if r.Address == "" {
		return ErrRedisAddrEmpty
	}

	if r.Port <= 0 {
		return ErrRedisPortInvalid
	}

	if r.DB < 0 {
		return ErrRedisDBInvalid
	}

	return nil
}

// Validate validates EtcdConfig for required fields, supported values, and safety constraints.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *EtcdConfig) Validate() error {
	// TODO: implement etcd config validation
	return nil
}

// Validate validates ConsulConfig for required fields, supported values, and safety constraints.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *ConsulConfig) Validate() error {
	// TODO: implement consul config validation
	return nil
}

// Validate validates MemoryConfig for required fields, supported values, and safety constraints.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryConfig) Validate() error {
	// TODO: implement memory config validation
	return nil
}

// Validate validates GRPCConfig for required fields, supported values, and safety constraints.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (g *GRPCConfig) Validate() error {
	if g.ListenPort <= 0 {
		return ErrGRPCPortInvalid
	}

	if g.TLS.Enabled {
		if g.TLS.CertPath == "" {
			return ErrTLSCertPathMissing
		}

		if g.TLS.KeyPath == "" {
			return ErrTLSKeyPathMissing
		}
	}

	return nil
}

// Validate validates TTLConfig for required fields, supported values, and safety constraints.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (t *TTLConfig) Validate() error {
	normalized := t.WithDefaults()
	if normalized.Agent <= 0 {
		return ErrTTLAgentInvalid
	}

	if normalized.Relay <= 0 {
		return ErrTTLRelayInvalid
	}
	if normalized.Conformance <= 0 {
		return ErrTTLConformanceInvalid
	}
	if normalized.ConformanceFence <= normalized.Conformance {
		return ErrTTLConformanceFenceInvalid
	}

	return nil
}
