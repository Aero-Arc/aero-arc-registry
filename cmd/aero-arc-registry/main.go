package main

import (
	"context"
	"log"
	"os"

	"github.com/urfave/cli/v3"
)

var registryCmd = cli.Command{
	Usage:  "run the aero arc registry process",
	Action: RunRegistry,
}

func RunRegistry(ctx context.Context, cmd *cli.Command) error {
	return nil
}

func main() {
	if err := registryCmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
