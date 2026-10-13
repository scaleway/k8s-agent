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

// sortDependencies orders components so that each one comes after its dependencies, keeping the release order otherwise.
// It fails if a component is defined twice, depends on an unknown component or is part of a dependency cycle.
func sortDependencies(components []Component) ([]Component, error) {
	byName := make(map[string]Component, len(components))
	for _, component := range components {
		if _, ok := byName[component.Name]; ok {
			return nil, fmt.Errorf("component %s is defined twice", component.Name)
		}
		byName[component.Name] = component
	}

	sorted := make([]Component, 0, len(components))
	done := make(map[string]bool, len(components))
	visiting := make(map[string]bool)

	var visit func(name string) error
	visit = func(name string) error {
		if done[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("component %s is part of a dependency cycle", name)
		}
		visiting[name] = true

		component := byName[name]
		for _, dependency := range component.DependsOn {
			if _, ok := byName[dependency]; !ok {
				return fmt.Errorf("component %s depends on unknown component %s", name, dependency)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}

		done[name] = true
		sorted = append(sorted, component)
		return nil
	}

	for _, component := range components {
		if err := visit(component.Name); err != nil {
			return nil, err
		}
	}

	return sorted, nil
}
