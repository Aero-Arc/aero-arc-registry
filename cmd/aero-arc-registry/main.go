package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aero-Arc/aero-arc-registry/internal/registry"
	"github.com/Aero-Arc/aero-arc-registry/internal/transport/grpc"
	"github.com/urfave/cli/v3"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var homeDir, _ = os.UserHomeDir()

var registryCmd = cli.Command{
	Usage:  "run the aero arc registry process",
	Action: RunRegistry,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  BackendFlag,
			Value: "memory",
			Usage: "the specified backend for the registry.",
		},
		&cli.StringFlag{
			Name:  GRPCListenAddrFlag,
			Usage: "the address the grpc server should listen on",
			Value: "0.0.0.0",
		},
		&cli.IntFlag{
			Name:  GRPCListenPortFlag,
			Usage: "the port the registry's grpc server will listen on",
			Value: 50051,
		},
		&cli.BoolFlag{
			Name:  TLSEnabledFlag,
			Usage: "determine if tls is enabled in registry transport protocol",
			Value: false,
		},
		&cli.StringFlag{
			Name:  TLSKeyPathFlag,
			Usage: "path to tls key file",
			Value: fmt.Sprintf("%s/%s", homeDir, registry.DebugTLSKeyPath),
		},
		&cli.StringFlag{
			Name:  TLSCertPathFlag,
			Usage: "path to tls crt file",
			Value: fmt.Sprintf("%s/%s", homeDir, registry.DebugTLSCertPath),
		},
		&cli.DurationFlag{
			Name:  RelayTTLFlag,
			Usage: "ttl for relay health",
			Value: time.Second * 30,
		},
		&cli.DurationFlag{
			Name:  AgentTTLFlag,
			Usage: "ttl for agent health",
			Value: time.Second * 30,
		},
		&cli.DurationFlag{
			Name:  HeartbeatIntervalFlag,
			Usage: "expected relay heartbeat interval",
			Value: time.Second,
		},
		&cli.StringFlag{
			Name:  RedisAddrFlag,
			Usage: "redis instance address",
			Value: "localhost",
		},
		&cli.IntFlag{
			Name:  RedisPortFlag,
			Usage: "redis instance port",
			Value: 6379,
		},
		&cli.StringFlag{
			Name:  RedisUsernameFlag,
			Usage: "redis username",
			Value: "default",
		},
		&cli.StringFlag{
			Name:  RedisPasswordFlag,
			Usage: "redis password",
			Value: "",
		},
		&cli.IntFlag{
			Name:  RedisDBFlag,
			Usage: "specified redis db to use",
			Value: 0,
		},
		&cli.DurationFlag{
			Name:  ShutDownTimeoutFlag,
			Usage: "timeout that is enforced during a graceful shutdown",
			Value: time.Second * 30,
		},
	},
}

func RunRegistry(ctx context.Context, cmd *cli.Command) error {
	signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := buildConfigFromCLI(cmd)
	if err != nil {
		return err
	}

	backend, err := buildBackendFromConfig(cfg)
	if err != nil {
		return err
	}

	aeroRegistry, err := registry.New(cfg, backend)
	if err != nil {
		return err
	}

	var opts []gogrpc.ServerOption

	if cfg.GRPC.TLS.Enabled {
		creds, err := credentials.NewServerTLSFromFile(
			cfg.GRPC.TLS.CertPath,
			cfg.GRPC.TLS.KeyPath,
		)
		if err != nil {
			return err
		}

		opts = append(opts, gogrpc.Creds(creds))
	}

	grpcServer, err := grpc.New(aeroRegistry, opts...)
	if err != nil {
		return err
	}

	aeroRegistry.RunTTL(signalCtx)

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d",
		cfg.GRPC.ListenAddress,
		cfg.GRPC.ListenPort,
	))
	if err != nil {
		return err
	}

	go func() {
		<-signalCtx.Done()
		slog.Info("shutting down grpc server")
		grpcServer.GracefulStop()

		slog.Info("shutting down backend")
		if err := backend.Close(context.Background()); err != nil {
			slog.Error("failed to close backend", "error", err)
		}
	}()

	slog.Info("Registry gRPC server listening",
		"address", cfg.GRPC.ListenAddress,
		"port", cfg.GRPC.ListenPort,
	)
	if err := grpcServer.Serve(lis); err != nil {
		return err
	}

	return nil
}

func main() {
	if err := registryCmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
