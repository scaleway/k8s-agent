package main

import (
	"fmt"
	"io/fs"
	"slices"

	"gopkg.in/yaml.v3"
)

// Structs to unmarshal releases.yaml
type Releases struct {
	Versions map[string][]Component
}

type Component struct {
	Name      string
	Version   string
	Tags      []string
	DependsOn []string `yaml:"depends_on,omitempty"`
}

// releaseComponents reads the releases.yaml file and returns the components for the given node version
func releaseComponents(repoFS fs.FS, nodemetadata NodeMetadata) ([]Component, error) {
	// Read and unmarshal "releases.yaml" file at the root of the repository
	var releases Releases
	releasesFile, err := fs.ReadFile(repoFS, "releases.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read releases file: %w", err)
	}
	err = yaml.Unmarshal(releasesFile, &releases)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal releases file: %w", err)
	}

	// Get the release components for the node version
	releaseComponents, ok := releases.Versions[nodemetadata.PoolVersion]
	if !ok {
		return nil, fmt.Errorf("release %s not found", nodemetadata.PoolVersion)
	}

	// Validate the dependencies on the whole release
	err = validateDependencies(releaseComponents)
	if err != nil {
		return nil, fmt.Errorf("invalid release %s: %w", nodemetadata.PoolVersion, err)
	}

	filteredComponents := []Component{}

	// If no installer tags are specified, include all components
	if len(nodemetadata.InstallerTags) == 0 {
		filteredComponents = append(filteredComponents, releaseComponents...)
	} else {
		// Otherwise, include only components that match at least one of the installer tags
		for _, component := range releaseComponents {
			for _, tag := range nodemetadata.InstallerTags {
				if slices.Contains(component.Tags, tag) {
					filteredComponents = append(filteredComponents, component)
					break
				}
			}
		}
	}

	return filteredComponents, nil
}

// validateDependencies ensures each component is defined once and its dependencies are defined before it
func validateDependencies(components []Component) error {
	seen := make(map[string]bool, len(components))
	for _, component := range components {
		if seen[component.Name] {
			return fmt.Errorf("component %s is defined twice", component.Name)
		}

		for _, dependency := range component.DependsOn {
			if !seen[dependency] {
				return fmt.Errorf("component %s depends on %s which must be defined before it", component.Name, dependency)
			}
		}

		seen[component.Name] = true
	}

	return nil
}
