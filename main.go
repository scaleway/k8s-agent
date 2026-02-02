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
	// Flags
	flagVersion := flag.Bool("version", false, "Print the version")
	flagKosmos := flag.Bool("kosmos", false, "Enable Kosmos mode (multicloud): POOL_ID, POOL_REGION and SCW_SECRET_KEY env vars must be set")
	flag.Parse()

	// Flag to print the version
	if *flagVersion {
		fmt.Println(Version)
		os.Exit(0)
	}

	// The agent must be executed as root
	if os.Getuid() != 0 {
		slog.Error("Agent must be run as root")
		os.Exit(1)
	}

	// Get node token and url to fetch the node metadata
	var userData UserData
	if *flagKosmos {
		// Kosmos mode: get userdata from env vars or local cache
		kosmosUserData, err := getKosmosUserData()
		if err != nil {
			slog.Error("Failed to get Kosmos node credentials", slog.Any("error", err))
			os.Exit(1)
		}
		userData = kosmosUserData
	} else {
		// Kapsule mode: get userdata from http://169.254.42.42/user_data/k8s
		nodeUserData, err := getNodeUserData()
		if err != nil {
			slog.Error("Failed to get Kapsule node credentials", slog.Any("error", err))
			os.Exit(1)
		}
		userData = nodeUserData
	}

	// Get the node metadata, from the PN node metadata endpoint or the external kapsule endpoint
	nodeMetadata, err := getNodeMetadata(userData.MetadataURL, userData.NodeSecretKey)
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

	// If Kosmos mode, exit after installation
	if *flagKosmos {
		slog.Info("Kosmos mode: exiting after installation")
		return
	}

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
