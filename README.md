# Kapsule/Kosmos agent

A Kubernetes node installer and lifecycle management tool for Scaleway's managed Kubernetes services (Kapsule and Kosmos).

## Overview

k8s-agent is a comprehensive node management solution that bootstraps Kubernetes nodes, manages component lifecycles, and provides ongoing node management through a Kubernetes controller. It's designed to work with Scaleway's infrastructure.

## Features

- **Component-based Architecture**: Install and manage Kubernetes components via YAML-defined metadata
- **Template Support**: Dynamic configuration generation with Sprig template functions
- **Version Management**: Track component versions with sub-versioning support
- **Multi-repository Support**: HTTP/HTTPS and ZIP file repositories

## Architecture

### Core Components

- **Bootstrap Mode**: Installs required components and configurations on new nodes
- **Controller Mode**: Kubernetes controller that watches for upgrade requests
- **Metadata Service**: Fetches node configuration from Scaleway infrastructure
- **Component Manager**: Handles installation, upgrades, and lifecycle management
- **Version Tracker**: Maintains component version state in `/etc/scw-k8s-versions.json`

### Component Processing

Components are defined in YAML metadata with support for:
- File operations (copy, mkdir, ...)
- Template rendering with node metadata
- Systemd service management
- Script execution

## Version Management

Component versions are tracked in `/etc/scw-k8s-versions.json`. The agent supports:
- Semantic versioning
- Sub-versioning with `~` prefix
- Component lifecycle tracking

## Build and Development

### Prerequisites

- Go 1.24+

### Building

```bash
go build -o k8s-agent .
```

### Release

Releases are automated using GoReleaser:

```bash
goreleaser release
```

This creates statically-linked binaries for Linux AMD64 and ARM64 platforms.

## Contributing

This project is developed and maintained exclusively by Scaleway. We do not accept external pull requests.

## Support

For issues and questions related to Scaleway's managed Kubernetes services, please contact Scaleway support.
