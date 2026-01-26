package registry

import "errors"

var (
	ErrUnsupportedBackend        = errors.New("unsupported registry backend")
	ErrRedisConfigNil            = errors.New("redis config is nil")
	ErrRedisAddrEmpty            = errors.New("redis address is empty")
	ErrRedisPortInvalid          = errors.New("redis port must be > 0")
	ErrRedisDBInvalid            = errors.New("redis db must be > 0")
	ErrGRPCPortInvalid           = errors.New("grpc port must be > 0")
	ErrTLSCertPathMissing        = errors.New("grpc tls cert path empty")
	ErrTLSKeyPathMissing         = errors.New("grpc tls key path empty")
	ErrTTLRelayInvalid           = errors.New("relay ttl must be > 0")
	ErrTTLAgentInvalid           = errors.New("agent ttl must be > 0")
	ErrNilConfig                 = errors.New("registry config is nil")
	ErrNotImplemented            = errors.New("not implemented")
	ErrAgentNotPlaced            = errors.New("agent not placed")
	ErrHeartbeatTimestampMissing = errors.New("heartbeat timestamp is required")
)
