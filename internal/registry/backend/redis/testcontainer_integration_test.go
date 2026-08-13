//go:build integration

package redis

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const redisIntegrationImage = "redis:8.8.1-alpine"

func startRedisTestContainer(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Logf("Starting Redis test dependency: image=%s", redisIntegrationImage)
	container, err := testcontainers.Run(ctx, redisIntegrationImage,
		testcontainers.WithExposedPorts("6379/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithOccurrence(1).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start %s: %v", redisIntegrationImage, err)
	}
	containerID := container.GetContainerID()
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}
	t.Cleanup(func() {
		if t.Failed() {
			logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer logCancel()
			if logs, logErr := container.Logs(logCtx); logErr == nil {
				defer logs.Close()
				body, _ := io.ReadAll(logs)
				t.Logf("Redis container logs:\n%s", body)
			} else {
				t.Logf("read Redis container logs: %v", logErr)
			}
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		t.Logf("Stopping Redis test dependency: container_id=%s image=%s", containerID, redisIntegrationImage)
		if err := testcontainers.TerminateContainer(container, testcontainers.StopContext(stopCtx)); err != nil {
			t.Errorf("terminate Redis container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("resolve Redis container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("resolve Redis mapped port: %v", err)
	}
	address := fmt.Sprintf("%s:%s", host, port.Port())
	t.Logf("Redis test dependency ready: container_id=%s image=%s endpoint=%s", containerID, redisIntegrationImage, address)
	return address
}
