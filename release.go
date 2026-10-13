package main

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"

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

	// Validate and sort the dependencies on the whole release
	releaseComponents, err = sortDependencies(releaseComponents)
	if err != nil {
		return nil, fmt.Errorf("invalid release %s: %w", nodemetadata.PoolVersion, err)
	}

	// If no installer tags are specified, include all components
	if len(nodemetadata.InstallerTags) == 0 {
		return releaseComponents, nil
	}

	// Otherwise, include only components that match at least one of the installer tags
	filteredComponents := []Component{}
	for _, component := range releaseComponents {
		for _, tag := range nodemetadata.InstallerTags {
			if slices.Contains(component.Tags, tag) {
				filteredComponents = append(filteredComponents, component)
				break
			}
		}
	}

	return filteredComponents, nil
}

// sortDependencies orders components after their dependencies, keeping release order otherwise
func sortDependencies(components []Component) ([]Component, error) {
	names := make(map[string]bool, len(components))
	for _, component := range components {
		if names[component.Name] {
			return nil, fmt.Errorf("component %s is defined twice", component.Name)
		}
		names[component.Name] = true
	}
	for _, component := range components {
		for _, dependency := range component.DependsOn {
			if !names[dependency] {
				return nil, fmt.Errorf("component %s depends on unknown component %s", component.Name, dependency)
			}
		}
	}

	sorted := make([]Component, 0, len(components))
	done := make(map[string]bool, len(components))
	for len(sorted) < len(components) {
		// Pick the first component in release order whose dependencies are all sorted
		next := slices.IndexFunc(components, func(component Component) bool {
			return !done[component.Name] && !slices.ContainsFunc(component.DependsOn, func(dependency string) bool { return !done[dependency] })
		})
		if next == -1 {
			remaining := []string{}
			for _, component := range components {
				if !done[component.Name] {
					remaining = append(remaining, component.Name)
				}
			}
			return nil, fmt.Errorf("components %s are part of or depend on a dependency cycle", strings.Join(remaining, ", "))
		}
		done[components[next].Name] = true
		sorted = append(sorted, components[next])
	}

	return sorted, nil
}
