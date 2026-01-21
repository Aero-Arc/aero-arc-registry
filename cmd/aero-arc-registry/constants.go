package main

// cli flag names
const (
	BackendFlag           = "backend"
	GRPCListenAddrFlag    = "grpc-listen-address"
	GRPCListenPortFlag    = "grpc-listen-port"
	TLSKeyPathFlag        = "tls-key-path"
	TLSCertPathFlag       = "tls-cert-path"
	RelayTTLFlag          = "relay-ttl"
	AgentTTLFlag          = "agent-ttl"
	HeartbeatIntervalFlag = "heartbeat-interval"
	RedisAddrFlag         = "redis-addr"
	RedisPortFlag         = "redis-port"
	RedisUsernameFlag     = "redis-user"
	RedisPasswordFlag     = "redis-password"
	RedisDBFlag           = "redis-db"
	ShutDownTimeoutFlag   = "shutdown-timeout"
)
