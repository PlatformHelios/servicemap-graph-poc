package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cli"
	"github.com/PlatformHelios/servicemap-graph-poc/internal/httpapi"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		if err := httpapi.Run(ctx, args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := cli.Run(ctx, args, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
