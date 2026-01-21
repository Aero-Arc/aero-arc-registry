package main

import (
	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/consul"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/etcd"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/memory"
	"github.com/Aero-Arc/aero-arc-registry/internal/registry/backend/redis"
)

func buildBackendFromConfig(cfg *registry.Config) (registry.Backend, error) {
	switch cfg.Backend.Type {
	case registry.RedisRegistryBackend:
		return redis.New(cfg.Backend.Redis)
	case registry.ConsulRegistryBackend:
		return consul.New(cfg.Backend.Consul)
	case registry.EtcdRegistryBackend:
		return etcd.New(cfg.Backend.Etcd)
	case registry.MemoryRegistryBackend:
		return memory.New(cfg.Backend.Memory)
	default:
		return nil, ErrUnhandledBackend
	}
}
