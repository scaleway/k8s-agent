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
type ComponentVersions struct {
	Versions map[string]ComponentSections
}

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

	// Get the installed components versions
	installed, err := ListComponentsVersions()
	if err != nil {
		return fmt.Errorf("failed to list components versions: %w", err)
	}

	// Get the components to reinstall
	affected := affectedComponents(releaseComponents, installed, nodemetadata.PoolVersion)
	for _, component := range releaseComponents {
		if !slices.ContainsFunc(affected, func(c Component) bool { return c.Name == component.Name }) {
			slog.Info("Component already installed", slog.String("component", component.Name), slog.String("version", installed[component.Name]))
		}
	}

	// Create a plan to uninstall and install the affected components
	actions, err := planComponents(repoFS, affected, installed, nodemetadata.PoolVersion)
	if err != nil {
		return fmt.Errorf("failed to plan components: %w", err)
	}

	// Uninstall components (components are uninstalled in reverse order)
	err = uninstallComponents(ctx, repoFS, actions, nodemetadata)
	if err != nil {
		return fmt.Errorf("failed to uninstall components: %w", err)
	}

	// Install components
	err = installComponents(ctx, repoFS, actions, nodemetadata)
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

func uninstallComponents(ctx context.Context, repoFS fs.FS, actions []ComponentAction, nodemetadata NodeMetadata) error {
	// Copy and reverse actions list to uninstall
	reversedActions := make([]ComponentAction, len(actions))
	copy(reversedActions, actions)
	slices.Reverse(reversedActions)

	// Uninstall component one by one
	for _, action := range reversedActions {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		default:
		}

		// If the component is not installed, skip it
		if !isInstalled(action.InstalledVersion) {
			continue
		}

		// Uninstall the component
		slog.Info("Uninstall component", slog.String("component", action.Component.Name), slog.String("version", action.InstalledVersion), slog.String("reason", action.Reason))
		err := processComponentMetadata(repoFS, action.Component.Name, action.InstalledVersion, "uninstalled", action.Uninstall, nodemetadata)
		if err != nil {
			return fmt.Errorf("failed to uninstall component %s: %w", action.Component.Name, err)
		}
	}

	return nil
}

func installComponents(ctx context.Context, repoFS fs.FS, actions []ComponentAction, nodemetadata NodeMetadata) error {
	// Install component one by one
	for _, action := range actions {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		default:
		}

		// Install the component
		slog.Info("Install component", slog.String("component", action.Component.Name), slog.String("version", action.ExpectedVersion), slog.String("reason", action.Reason))
		err := processComponentMetadata(repoFS, action.Component.Name, action.ExpectedVersion, action.ExpectedVersion, action.Install, nodemetadata)
		if err != nil {
			return fmt.Errorf("failed to install component %s: %w", action.Component.Name, err)
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
			err := mkdir(dst, file.Mode, file.Owner, file.Group)
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
			// Restart rather than start, so an already running service picks up the files just written
			cmd = exec.Command("/usr/bin/systemctl", "restart", service.Name)
			err = cmd.Run()
			if err != nil {
				return fmt.Errorf("failed to restart service %s: %w", service.Name, err)
			}
			slog.Info("Service restarted", slog.String("service", service.Name))
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

// processComponentMetadata processes the files and services operations defined in the component metadata,
// templating paths with version and storing recordedVersion as the component version once done
func processComponentMetadata(repoFS fs.FS, name, version, recordedVersion string, resources []ComponentResources, nodeMetadata NodeMetadata) error {
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
	err := SetComponentVersion(name, recordedVersion)
	if err != nil {
		return fmt.Errorf("failed to store component version: %w", err)
	}

	return nil
}

// componentVersions reads and parses the metadata of every version of the given component
func componentVersions(repoFS fs.FS, name string) (ComponentVersions, error) {
	// Read component specific "metadata.yaml" file inside the component directory in root of the repository
	componentMetadataFile, err := fs.ReadFile(repoFS, name+"/metadata.yaml")
	if err != nil {
		return ComponentVersions{}, fmt.Errorf("failed to read component file: %w", err)
	}

	// Unmarshal the metadata file
	var componentMetadata ComponentVersions
	err = yaml.Unmarshal(componentMetadataFile, &componentMetadata)
	if err != nil {
		return ComponentVersions{}, fmt.Errorf("failed to unmarshal component file: %w", err)
	}

	return componentMetadata, nil
}

// version returns the metadata sections for the given component version
func (c ComponentVersions) version(version string) (ComponentSections, error) {
	// Remove subversion suffix from the version
	version = trimVersion(version)

	// Get the metadata for the given version
	componentMetadataVersion, ok := c.Versions[version]
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
