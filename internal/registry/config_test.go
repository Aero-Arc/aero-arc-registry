package registry

import (
	"errors"
	"testing"
	"time"
)

func TestParseRegistryBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    RegistryBackend
		wantErr error
	}{
		{
			name:  "redis backend",
			input: "redis",
			want:  RedisRegistryBackend,
		},
		{
			name:  "etcd backend",
			input: "etcd",
			want:  EtcdRegistryBackend,
		},
		{
			name:  "consul backend",
			input: "consul",
			want:  ConsulRegistryBackend,
		},
		{
			name:  "memory backend",
			input: "memory",
			want:  MemoryRegistryBackend,
		},
		{
			name:    "unsupported backend",
			input:   "unknown",
			want:    "",
			wantErr: ErrUnsupportedBackend,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseRegistryBackend(test.input)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected error %v, got %v", test.wantErr, err)
			}
			if got != test.want {
				t.Fatalf("expected backend %q, got %q", test.want, got)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	validGRPC := GRPCConfig{
		ListenAddress: "127.0.0.1",
		ListenPort:    50051,
		TLS: TLSConfig{
			Enabled:  false,
			CertPath: "",
			KeyPath:  "",
		},
	}
	validTTL := TTLConfig{
		Relay: 5 * time.Second,
		Agent: 10 * time.Second,
	}
	validRedis := &RedisConfig{
		Address:  "localhost",
		Port:     6379,
		Username: "user",
		Password: "pass",
		DB:       0,
	}

	tests := []struct {
		name    string
		config  Config
		wantErr error
	}{
		{
			name: "valid memory backend config",
			config: Config{
				Backend: BackendConfig{
					Type:  MemoryRegistryBackend,
					Redis: nil,
				},
				GRPC: validGRPC,
				TTL:  validTTL,
			},
			wantErr: nil,
		},
		{
			name: "redis backend with valid redis config",
			config: Config{
				Backend: BackendConfig{
					Type:  RedisRegistryBackend,
					Redis: validRedis,
				},
				GRPC: validGRPC,
				TTL:  validTTL,
			},
			wantErr: nil,
		},
		{
			name: "redis backend with nil redis config",
			config: Config{
				Backend: BackendConfig{
					Type:  RedisRegistryBackend,
					Redis: nil,
				},
				GRPC: validGRPC,
				TTL:  validTTL,
			},
			wantErr: ErrRedisConfigNil,
		},
		{
			name: "invalid grpc listen port",
			config: Config{
				Backend: BackendConfig{
					Type: MemoryRegistryBackend,
				},
				GRPC: GRPCConfig{
					ListenAddress: "127.0.0.1",
					ListenPort:    0,
				},
				TTL: validTTL,
			},
			wantErr: ErrGRPCPortInvalid,
		},
		{
			name: "tls enabled missing cert",
			config: Config{
				Backend: BackendConfig{
					Type: MemoryRegistryBackend,
				},
				GRPC: GRPCConfig{
					ListenAddress: "127.0.0.1",
					ListenPort:    50051,
					TLS: TLSConfig{
						Enabled:  true,
						CertPath: "",
						KeyPath:  "key.pem",
					},
				},
				TTL: validTTL,
			},
			wantErr: ErrTLSCertPathMissing,
		},
		{
			name: "tls enabled missing key",
			config: Config{
				Backend: BackendConfig{
					Type: MemoryRegistryBackend,
				},
				GRPC: GRPCConfig{
					ListenAddress: "127.0.0.1",
					ListenPort:    50051,
					TLS: TLSConfig{
						Enabled:  true,
						CertPath: "cert.pem",
						KeyPath:  "",
					},
				},
				TTL: validTTL,
			},
			wantErr: ErrTLSKeyPathMissing,
		},
		{
			name: "invalid ttl agent",
			config: Config{
				Backend: BackendConfig{
					Type: MemoryRegistryBackend,
				},
				GRPC: validGRPC,
				TTL: TTLConfig{
					Relay: 5 * time.Second,
					Agent: 0,
				},
			},
			wantErr: ErrTTLAgentInvalid,
		},
		{
			name: "invalid ttl relay",
			config: Config{
				Backend: BackendConfig{
					Type: MemoryRegistryBackend,
				},
				GRPC: validGRPC,
				TTL: TTLConfig{
					Relay: 0,
					Agent: 10 * time.Second,
				},
			},
			wantErr: ErrTTLRelayInvalid,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.config.Validate()
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected error %v, got %v", test.wantErr, err)
			}
		})
	}
}

func TestRedisConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  RedisConfig
		wantErr error
	}{
		{
			name: "valid",
			config: RedisConfig{
				Address: "localhost",
				Port:    6379,
				DB:      0,
			},
			wantErr: nil,
		},
		{
			name: "empty address",
			config: RedisConfig{
				Address: "",
				Port:    6379,
				DB:      0,
			},
			wantErr: ErrRedisAddrEmpty,
		},
		{
			name: "invalid port",
			config: RedisConfig{
				Address: "localhost",
				Port:    0,
				DB:      0,
			},
			wantErr: ErrRedisPortInvalid,
		},
		{
			name: "invalid db",
			config: RedisConfig{
				Address: "localhost",
				Port:    6379,
				DB:      -1,
			},
			wantErr: ErrRedisDBInvalid,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.config.Validate()
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected error %v, got %v", test.wantErr, err)
			}
		})
	}
}

func TestGRPCConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  GRPCConfig
		wantErr error
	}{
		{
			name: "valid",
			config: GRPCConfig{
				ListenAddress: "127.0.0.1",
				ListenPort:    50051,
				TLS: TLSConfig{
					Enabled: false,
				},
			},
			wantErr: nil,
		},
		{
			name: "invalid port",
			config: GRPCConfig{
				ListenAddress: "127.0.0.1",
				ListenPort:    0,
			},
			wantErr: ErrGRPCPortInvalid,
		},
		{
			name: "tls enabled missing cert",
			config: GRPCConfig{
				ListenAddress: "127.0.0.1",
				ListenPort:    50051,
				TLS: TLSConfig{
					Enabled:  true,
					CertPath: "",
					KeyPath:  "key.pem",
				},
			},
			wantErr: ErrTLSCertPathMissing,
		},
		{
			name: "tls enabled missing key",
			config: GRPCConfig{
				ListenAddress: "127.0.0.1",
				ListenPort:    50051,
				TLS: TLSConfig{
					Enabled:  true,
					CertPath: "cert.pem",
					KeyPath:  "",
				},
			},
			wantErr: ErrTLSKeyPathMissing,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.config.Validate()
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected error %v, got %v", test.wantErr, err)
			}
		})
	}
}

func TestTTLConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  TTLConfig
		wantErr error
	}{
		{
			name: "valid",
			config: TTLConfig{
				Relay: 5 * time.Second,
				Agent: 10 * time.Second,
			},
			wantErr: nil,
		},
		{
			name: "invalid agent",
			config: TTLConfig{
				Relay: 5 * time.Second,
				Agent: 0,
			},
			wantErr: ErrTTLAgentInvalid,
		},
		{
			name: "invalid relay",
			config: TTLConfig{
				Relay: 0,
				Agent: 10 * time.Second,
			},
			wantErr: ErrTTLRelayInvalid,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.config.Validate()
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("expected error %v, got %v", test.wantErr, err)
			}
		})
	}
}
