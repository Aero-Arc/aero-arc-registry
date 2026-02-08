package registry

const (
	DebugTLSCertPath = ".aeroarc/local-certs/localhost.crt"
	DebugTLSKeyPath  = ".aeroarc/local-certs/localhost.key"
)

const (
	RedisRegistryBackend  RegistryBackend = "redis"
	EtcdRegistryBackend   RegistryBackend = "etcd"
	ConsulRegistryBackend RegistryBackend = "consul"
	MemoryRegistryBackend RegistryBackend = "memory"
)

var registryMap = map[string]RegistryBackend{
	"redis":  RedisRegistryBackend,
	"etcd":   EtcdRegistryBackend,
	"consul": ConsulRegistryBackend,
	"memory": MemoryRegistryBackend,
}
