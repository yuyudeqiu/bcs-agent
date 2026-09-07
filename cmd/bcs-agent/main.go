package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/klog/v2"
)

func main() {
	// client-go uses klog for internal diagnostics. Keep errors visible without
	// mixing throttling and discovery INFO messages into the CLI stream.
	klog.SetSlogLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := newRootCommand(commandRunner{cli: runCLI, server: runServer}, os.Stdin, os.Stdout, os.Stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "BCS Agent 退出: %v\n", err)
		os.Exit(1)
	}
}
