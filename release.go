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

// sortDependencies orders components so that each one comes after its dependencies, keeping the release order otherwise.
// It fails if a component is defined twice, depends on an unknown component or is part of a dependency cycle.
func sortDependencies(components []Component) ([]Component, error) {
	defined := make(map[string]bool, len(components))
	for _, component := range components {
		if defined[component.Name] {
			return nil, fmt.Errorf("component %s is defined twice", component.Name)
		}
		defined[component.Name] = true
	}
	for _, component := range components {
		for _, dependency := range component.DependsOn {
			if !defined[dependency] {
				return nil, fmt.Errorf("component %s depends on %s which is not defined", component.Name, dependency)
			}
		}
	}

	// Repeatedly pick the first remaining component whose dependencies are all sorted
	sorted := make([]Component, 0, len(components))
	placed := make(map[string]bool, len(components))
	remaining := slices.Clone(components)
	for len(remaining) > 0 {
		index := slices.IndexFunc(remaining, func(component Component) bool {
			return !slices.ContainsFunc(component.DependsOn, func(dependency string) bool { return !placed[dependency] })
		})
		if index == -1 {
			return nil, fmt.Errorf("dependency cycle between components %v", componentNames(remaining))
		}

		sorted = append(sorted, remaining[index])
		placed[remaining[index].Name] = true
		remaining = slices.Delete(remaining, index, index+1)
	}

	return sorted, nil
}

// componentNames returns the names of the given components
func componentNames(components []Component) []string {
	names := make([]string, 0, len(components))
	for _, component := range components {
		names = append(names, component.Name)
	}
	return names
}
