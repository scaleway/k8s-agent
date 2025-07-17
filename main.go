package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var (
	Version = "dev"
)

func main() {
	// Flag to print the version
	printVersion := flag.Bool("version", false, "Print the version")
	flag.Parse()
	if *printVersion {
		fmt.Println(Version)
		os.Exit(0)
	}

	// The agent must be executed as root
	if os.Getuid() != 0 {
		slog.Error("Agent must be run as root")
		os.Exit(1)
	}

	// Get node token to fetch the node metadata
	nodeUserData, err := getNodeUserData()
	if err != nil {
		slog.Error("Failed to get credentials", slog.Any("error", err))
		os.Exit(1)
	}

	// Get the node metadata, from the PN node metadata endpoint or the external kapsule endpoint
	nodeMetadata, err := getNodeMetadata(nodeUserData.MetadataURL, nodeUserData.NodeSecretKey)
	if err != nil {
		slog.Error("Failed to get node metadata", slog.Any("error", err))
		os.Exit(1)
	}

	// // Register chan to receive system signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	ctx, sigCancel := context.WithCancel(context.Background())
	go func() {
		sig := <-sigs
		slog.Info("Received signal, shutting down", slog.String("signal", sig.String()))
		sigCancel()
	}()

	// Install the components: binaries, configuration files, and services
	err = processComponents(ctx, nodeMetadata)
	if err != nil {
		slog.Error("Failed to process components", slog.Any("error", err))
		os.Exit(1)
	}

	slog.Info("System and components processed successfully")

	// Start the node controller
	nodeController, err := NewController(ctx, nodeMetadata)
	if err != nil {
		slog.Error("Failed to create node controller", slog.Any("error", err))
		os.Exit(1)
	}
	err = nodeController.Run(ctx)
	if err != nil {
		slog.Error("Failed to run node controller", slog.Any("error", err))
		os.Exit(1)
	}
}
