package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"text/template"

	"github.com/scaleway/k8s-agent/repo"
	"gopkg.in/yaml.v3"
)

// Structs to unmarshal metadata.yaml
type ComponentSections struct {
	Install   []ComponentResources `yaml:"install,omitempty"`
	Uninstall []ComponentResources `yaml:"uninstall,omitempty"`
}

type ComponentResources struct {
	Files    []ComponentFile    `yaml:"files,omitempty"`
	Services []ComponentService `yaml:"services,omitempty"`
	Scripts  []ComponentScript  `yaml:"scripts,omitempty"`
}

type ComponentFile struct {
	State string `yaml:"state"`
	Src   string `yaml:"src,omitempty"`
	Dst   string `yaml:"dst,omitempty"`
	Mode  string `yaml:"mode,omitempty"`
	Owner string `yaml:"owner,omitempty"`
	Group string `yaml:"group,omitempty"`
}

type ComponentService struct {
	State   string `yaml:"state"`
	Name    string `yaml:"name"`
	Enabled bool   `yaml:"enabled"`
}

type ComponentScript struct {
	Cmd string `yaml:"cmd"`
}

func processComponents(ctx context.Context, nodemetadata NodeMetadata) error {
	// Open repository FS (local zip or remote http(s))
	slog.Info("Opening repositories", slog.String("uri", nodemetadata.RepoURI))
	repoFS, err := repo.NewRepoFS(nodemetadata.RepoURI)
	if err != nil {
		return err
	}

	// Get the release components for the node version
	releaseComponents, err := releaseComponents(repoFS, nodemetadata)
	if err != nil {
		return fmt.Errorf("failed to get release components: %w", err)
	}

	// Uninstall components (components are uninstalled in reverse order)
	err = uninstallComponents(ctx, repoFS, releaseComponents, nodemetadata)
	if err != nil {
		return fmt.Errorf("failed to uninstall components: %w", err)
	}

	// Install components
	err = installComponents(ctx, repoFS, releaseComponents, nodemetadata)
	if err != nil {
		return fmt.Errorf("failed to install components: %w", err)
	}

	// Cleanup the repository FS (eg: remove the zip file for local zipFS)
	err = repoFS.Cleanup()
	if err != nil {
		return fmt.Errorf("failed to cleanup repository: %w", err)
	}

	return nil
}

func uninstallComponents(ctx context.Context, repoFS fs.FS, components []Component, nodemetadata NodeMetadata) error {
	// Copy and reverse component list to uninstall
	reversedComponents := make([]Component, len(components))
	copy(reversedComponents, components)
	slices.Reverse(reversedComponents)

	// Uninstall component one by one
	for _, component := range reversedComponents {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		default:
		}

		// If the component is not installed or the version is the same, skip it
		installedVersion, err := GetComponentVersion(component.Name)
		if err != nil {
			return fmt.Errorf("failed to get component version: %w", err)
		}
		expectedVersion := expandVersion(component.Version, nodemetadata.PoolVersion)
		if installedVersion == "" || installedVersion == expectedVersion {
			continue
		}

		// Read component specific "metadata.yaml" file inside the component directory in root of the repository
		componentSections, err := componentMetada(repoFS, component.Name, installedVersion)
		if err != nil {
			return fmt.Errorf("failed to read component metadata: %w", err)
		}

		// Uninstall the component
		slog.Info("Uninstall component", slog.String("component", component.Name), slog.String("version", installedVersion))
		err = processComponentMetada(repoFS, component.Name, "uninstalled", componentSections.Uninstall, nodemetadata)
		if err != nil {
			return fmt.Errorf("failed to uninstall component %s: %w", component.Name, err)
		}
	}

	return nil
}

func installComponents(ctx context.Context, repoFS fs.FS, components []Component, nodemetadata NodeMetadata) error {
	// Install component one by one
	for _, component := range components {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		default:
		}

		// Get current installed version of the component
		installedVersion, err := GetComponentVersion(component.Name)
		if err != nil {
			return fmt.Errorf("failed to get component version: %w", err)
		}
		expectedVersion := expandVersion(component.Version, nodemetadata.PoolVersion)

		// If the component is already installed and the version is the same, skip it
		if installedVersion == expectedVersion {
			slog.Info("Component already installed", slog.String("component", component.Name), slog.String("version", expectedVersion))
			continue
		}

		// Read component specific "metadata.yaml" file inside the component directory in root of the repository
		componentSections, err := componentMetada(repoFS, component.Name, expectedVersion)
		if err != nil {
			return fmt.Errorf("failed to read component metadata: %w", err)
		}

		// Install the component
		slog.Info("Install component", slog.String("component", component.Name), slog.String("version", expectedVersion))
		err = processComponentMetada(repoFS, component.Name, expectedVersion, componentSections.Install, nodemetadata)
		if err != nil {
			return fmt.Errorf("failed to install component %s: %w", component.Name, err)
		}
	}

	return nil
}

func processComponentFiles(repoFS fs.FS, name, version string, files []ComponentFile, nodeMetadata NodeMetadata) error {
	for _, file := range files {
		// Template the source and destination paths
		src, err := templateComponentPath(file.Src, version)
		if err != nil {
			return fmt.Errorf("failed to template source path: %w", err)
		}
		dst, err := templateComponentPath(file.Dst, version)
		if err != nil {
			return fmt.Errorf("failed to template destination path: %w", err)
		}

		switch file.State {
		case "file":
			// When type is file, only copy the file from the repository to the filesystem
			filePath, err := writeFile(repoFS, name, src, dst, file.Mode, file.Owner, file.Group)
			if err != nil {
				return fmt.Errorf("failed to write file %s: %w", file.Dst, err)
			}
			slog.Info("File copied", slog.String("file", filePath))
		case "template":
			// When type is template, render the file with the node metadata and copy it to the filesystem
			filePath, err := templateFile(repoFS, name, src, dst, file.Mode, file.Owner, file.Group, nodeMetadata)
			if err != nil {
				return fmt.Errorf("failed to write file %s: %w", file.Dst, err)
			}
			slog.Info("Template rendered", slog.String("template", filePath))
		case "directory":
			// When type is dir, create the directory with the specified permissions
			// if the directory already exists, the ownership and permissions are ensured
			err := mkdir(file.Dst, file.Mode, file.Owner, file.Group)
			if err != nil {
				return fmt.Errorf("failed to make directory %s: %w", dst, err)
			}
			slog.Info("Directory created", slog.String("directory", dst))
		case "absent":
			// When type is absent, remove the file or directory
			err := os.RemoveAll(dst)
			if err != nil {
				return fmt.Errorf("failed to remove %s: %w", dst, err)
			}
			slog.Info("File/Directory removed", slog.String("path", dst))
		}
	}

	return nil
}

func processComponentScripts(scripts []ComponentScript) error {
	// Execute the scripts in bash
	for _, script := range scripts {
		// Execute the script with with the arguments via bash
		cmd := exec.Command("/bin/bash", "-c", script.Cmd)
		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("failed to execute script %s: %w", script.Cmd, err)
		}
	}

	return nil
}

func processComponentServices(services []ComponentService) error {
	// Daemon-reload to pick up the updated service files
	cmd := exec.Command("/usr/bin/systemctl", "daemon-reload")
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to daemon-reload: %w", err)
	}

	for _, service := range services {
		// Enable the service
		if service.Enabled {
			cmd = exec.Command("/usr/bin/systemctl", "enable", service.Name)
			err = cmd.Run()
			if err != nil {
				return fmt.Errorf("failed to enable service %s: %w", service.Name, err)
			}
			slog.Info("Service enabled", slog.String("service", service.Name))
		} else {
			cmd = exec.Command("/usr/bin/systemctl", "disable", service.Name)
			err = cmd.Run()
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
					// 1 is the exit code for systemctl disable when the service
					// does not exist so just ignore this error
					continue
				}

				return fmt.Errorf("failed to disable service %s: %w", service.Name, err)
			}
			slog.Info("Service disabled", slog.String("service", service.Name))
		}

		switch service.State {
		case "started":
			cmd = exec.Command("/usr/bin/systemctl", "start", service.Name)
			err = cmd.Run()
			if err != nil {
				return fmt.Errorf("failed to start service %s: %w", service.Name, err)
			}
			slog.Info("Service started", slog.String("service", service.Name))
		case "stopped":
			cmd = exec.Command("/usr/bin/systemctl", "stop", service.Name)
			err = cmd.Run()
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 5 {
					// 5 is the exit code for systemctl stop when the service
					// does not exist so just ignore this error
					continue
				}

				return fmt.Errorf("failed to stop service %s: %w", service.Name, err)
			}
			slog.Info("Service stopped", slog.String("service", service.Name))
		default:
			return fmt.Errorf("unknown service state: %s", service.State)
		}
	}

	return nil
}

// processComponentMetada processes the files and services operations defined in the component metadata
func processComponentMetada(repoFS fs.FS, name, version string, resources []ComponentResources, nodeMetadata NodeMetadata) error {
	for _, resource := range resources {
		// Process files operations
		err := processComponentFiles(repoFS, name, version, resource.Files, nodeMetadata)
		if err != nil {
			return fmt.Errorf("failed to process files: %w", err)
		}

		// Process services operations
		err = processComponentServices(resource.Services)
		if err != nil {
			return fmt.Errorf("failed to process services: %w", err)
		}

		// Process scripts operations
		err = processComponentScripts(resource.Scripts)
		if err != nil {
			return fmt.Errorf("failed to process scripts: %w", err)
		}
	}

	// Store the component version in the versions file
	err := SetComponentVersion(name, version)
	if err != nil {
		return fmt.Errorf("failed to store component version: %w", err)
	}

	return nil
}

// releaseComponents returns the list of components for the given version
func componentMetada(repoFS fs.FS, name, version string) (ComponentSections, error) {
	// Read component specific "metadata.yaml" file inside the component directory in root of the repository
	componentMetadataFile, err := fs.ReadFile(repoFS, name+"/metadata.yaml")
	if err != nil {
		return ComponentSections{}, fmt.Errorf("failed to read component file: %w", err)
	}

	// Unmarshal the metadata file
	var componentMetadata map[string]ComponentSections
	err = yaml.Unmarshal(componentMetadataFile, &componentMetadata)
	if err != nil {
		return ComponentSections{}, fmt.Errorf("failed to unmarshal component file: %w", err)
	}

	// Remove subversion suffix from the version
	version = trimVersion(version)

	// Get the metadata for the given version
	componentMetadataVersion, ok := componentMetadata[version]
	if !ok {
		return ComponentSections{}, fmt.Errorf("component version %s not found", version)
	}

	return componentMetadataVersion, nil
}

// templateComponentPath renders a component path based on the version and architecture
func templateComponentPath(path, version string) (string, error) {
	tmpl, err := template.New("path").Parse(path)
	if err != nil {
		return "", fmt.Errorf("failed to parse path: %w", err)
	}

	// Template the path with the version and architecture
	var renderedPath strings.Builder
	err = tmpl.Execute(&renderedPath, struct {
		Version string
		Arch    string
	}{
		Version: trimVersion(version),
		Arch:    runtime.GOARCH,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return renderedPath.String(), nil
}
